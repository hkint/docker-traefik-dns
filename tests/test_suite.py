import re
import unittest

# 1. Traefik Rule Extraction
# Handles Host(`domain`), Host('domain'), Host("domain"), HostSNI(...), comma separated hosts
TRAEFIK_HOST_REGEX = re.compile(
    r'Host(?:SNI)?\(\s*(?:(?:`([^`]+)`|["\']([^"\']+)["\'])\s*,?\s*)+\)',
    re.IGNORECASE
)

def extract_traefik_hosts(rule_str):
    """
    Extracts all hostnames from Traefik router rules such as:
    Host(`a.example.com`)
    Host(`b.example.com`, `c.example.com`)
    Host('d.example.com') || Host("e.example.com")
    Host(`f.example.com`) && PathPrefix(`/api`)
    HostSNI(`tls.example.com`)
    """
    hosts = []
    # Find all Host(...) or HostSNI(...) blocks
    for match in re.finditer(r'Host(?:SNI)?\(([^)]+)\)', rule_str, re.IGNORECASE):
        inner = match.group(1)
        # Find all quoted items inside
        items = re.findall(r'[`"\']([^`"\']+)[\'"`]', inner)
        for item in items:
            item = item.strip().lower()
            if item and item not in hosts:
                hosts.append(item)
    return hosts

# 2. Docker Container Label Parsing
def extract_docker_container_records(labels, default_target_ip, identifier="docker-external-dns", default_proxy=False):
    """
    Extracts DNS records from Docker container labels:
    - traefik.http.routers.<name>.rule=Host(...)
    - traefik.tcp.routers.<name>.rule=HostSNI(...)
    - <identifier>.hostname or external-dns.alpha.kubernetes.io/hostname
    - <identifier>.target or external-dns.alpha.kubernetes.io/target
    - <identifier>.proxy or external-dns.alpha.kubernetes.io/cloudflare-proxied
    """
    records = []
    
    # Check target override
    target = labels.get(f"{identifier}.target") or \
             labels.get("external-dns.alpha.kubernetes.io/target") or \
             default_target_ip
    
    # Check proxy override
    proxy_val = labels.get(f"{identifier}.proxy") or \
                labels.get("external-dns.alpha.kubernetes.io/cloudflare-proxied")
    if proxy_val is not None:
        proxy = proxy_val.lower() in ("true", "1", "yes")
    else:
        proxy = default_proxy

    discovered_hosts = set()

    # 1. Traefik router rules
    for k, v in labels.items():
        if (k.startswith("traefik.http.routers.") or k.startswith("traefik.tcp.routers.")) and k.endswith(".rule"):
            hosts = extract_traefik_hosts(v)
            for h in hosts:
                discovered_hosts.add(h)

    # 2. Direct external-dns hostname label
    direct_host = labels.get(f"{identifier}.hostname") or \
                  labels.get("external-dns.alpha.kubernetes.io/hostname")
    if direct_host:
        for h in direct_host.split(","):
            h = h.strip().lower()
            if h:
                discovered_hosts.add(h)

    for h in sorted(discovered_hosts):
        # Determine record type
        rtype = detect_record_type(target)
        records.append({
            "type": rtype,
            "name": h,
            "target": target,
            "proxy": proxy
        })
    return records

def detect_record_type(target):
    if not target:
        return "A"
    # IPv4
    if re.match(r'^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$', target):
        return "A"
    # IPv6
    if ":" in target:
        return "AAAA"
    # Hostname -> CNAME
    return "CNAME"

# 3. Zone Matcher
def find_matching_zone(domain, zones):
    """
    Finds the longest matching Cloudflare zone for a given domain.
    E.g. if zones are ['example.com', 'sub.example.com'],
    'app.sub.example.com' matches 'sub.example.com'.
    """
    matched_zone = None
    longest_len = 0
    domain = domain.lower().strip(".")
    for z in zones:
        z_clean = z.lower().strip(".")
        if domain == z_clean or domain.endswith("." + z_clean):
            if len(z_clean) > longest_len:
                matched_zone = z
                longest_len = len(z_clean)
    return matched_zone

# 4. Domain Filter
def matches_domain_filter(domain, filters):
    if not filters:
        return True
    domain = domain.lower().strip(".")
    for f in filters:
        f_clean = f.lower().strip(".")
        if domain == f_clean or domain.endswith("." + f_clean):
            return True
    return False

# 5. Diff & Reconciliation Engine
def compute_dns_diff(desired_records, existing_records, existing_txt_records, identifier="docker-external-dns"):
    """
    Computes create, update, and delete actions for Cloudflare DNS.
    Protects unmanaged records (records without valid TXT ownership record).
    """
    txt_prefix = f"TXT-ext-dns-"
    expected_txt_val = f"heritage=docker-external-dns,owner={identifier}"

    # Index existing records by (name, type)
    existing_by_key = {}
    for r in existing_records:
        existing_by_key[(r["name"].lower(), r["type"])] = r

    # Index existing TXT ownership records by record name
    txt_by_domain = {}
    for r in existing_txt_records:
        if r["name"].startswith(txt_prefix):
            target_domain = r["name"][len(txt_prefix):].lower()
            txt_by_domain[target_domain] = r

    creates = []
    updates = []
    deletes = []

    desired_keys = set()
    for d in desired_records:
        name = d["name"].lower()
        key = (name, d["type"])
        desired_keys.add(key)

        ex = existing_by_key.get(key)
        txt = txt_by_domain.get(name)

        if ex is None:
            # Need to create
            creates.append(d)
        else:
            # Exists. Check ownership!
            if txt and txt["content"] == expected_txt_val:
                # We own this record, check if update needed
                if ex["target"] != d["target"] or ex.get("proxy") != d.get("proxy"):
                    updates.append({
                        "id": ex["id"],
                        "old": ex,
                        "new": d
                    })
            else:
                # Not owned by us - DO NOT TOUCH to avoid breaking user's manual records
                print(f"[WARN] Skipping unmanaged record {name} ({d['type']})")

    # Check for deletions
    for (name, rtype), ex in existing_by_key.items():
        if (name, rtype) not in desired_keys:
            # Check if owned by us
            txt = txt_by_domain.get(name)
            if txt and txt["content"] == expected_txt_val:
                deletes.append({
                    "id": ex["id"],
                    "record": ex,
                    "txt_id": txt["id"],
                    "txt_name": txt["name"]
                })

    return creates, updates, deletes

# Unit Tests
class TestDockerExternalDNS(unittest.TestCase):
    def test_extract_traefik_hosts(self):
        cases = [
            ("Host(`app.example.com`)", ["app.example.com"]),
            ("Host(`app1.example.com`, `app2.example.com`)", ["app1.example.com", "app2.example.com"]),
            ("Host('app3.example.com') || Host(\"app4.example.com\")", ["app3.example.com", "app4.example.com"]),
            ("Host(`api.example.com`) && PathPrefix(`/v1`)", ["api.example.com"]),
            ("HostSNI(`secure.example.com`)", ["secure.example.com"]),
            ("PathPrefix(`/api`)", []),
        ]
        for rule, expected in cases:
            with self.subTest(rule=rule):
                self.assertEqual(extract_traefik_hosts(rule), expected)

    def test_extract_docker_container_records(self):
        labels = {
            "traefik.http.routers.web.rule": "Host(`web.example.com`) || Host(`web2.example.com`)",
            "traefik.http.routers.web.entrypoints": "websecure",
            "docker-external-dns.proxy": "true"
        }
        records = extract_docker_container_records(labels, "1.2.3.4", default_proxy=False)
        self.assertEqual(len(records), 2)
        self.assertEqual(records[0]["name"], "web.example.com")
        self.assertEqual(records[0]["target"], "1.2.3.4")
        self.assertTrue(records[0]["proxy"])
        self.assertEqual(records[0]["type"], "A")

    def test_cname_and_ipv6_detection(self):
        self.assertEqual(detect_record_type("192.168.1.1"), "A")
        self.assertEqual(detect_record_type("2001:db8::1"), "AAAA")
        self.assertEqual(detect_record_type("myhome.duckdns.org"), "CNAME")

    def test_zone_matching(self):
        zones = ["example.com", "sub.example.com", "other.org"]
        self.assertEqual(find_matching_zone("app.example.com", zones), "example.com")
        self.assertEqual(find_matching_zone("deep.test.sub.example.com", zones), "sub.example.com")
        self.assertEqual(find_matching_zone("example.com", zones), "example.com")
        self.assertIsNone(find_matching_zone("example.net", zones))

    def test_domain_filter(self):
        filters = ["example.com", "myhome.org"]
        self.assertTrue(matches_domain_filter("app.example.com", filters))
        self.assertTrue(matches_domain_filter("deep.sub.myhome.org", filters))
        self.assertFalse(matches_domain_filter("app.google.com", filters))

    def test_diff_engine_create_update_delete_and_safety(self):
        desired = [
            {"type": "A", "name": "app1.example.com", "target": "1.2.3.4", "proxy": True},
            {"type": "A", "name": "app2.example.com", "target": "1.2.3.5", "proxy": False},
        ]
        existing = [
            # app1 exists but old IP
            {"id": "rec_1", "type": "A", "name": "app1.example.com", "target": "1.1.1.1", "proxy": True},
            # app3 was previously synced, now gone
            {"id": "rec_3", "type": "A", "name": "app3.example.com", "target": "1.2.3.4", "proxy": True},
            # manual record without TXT owner
            {"id": "rec_manual", "type": "A", "name": "manual.example.com", "target": "8.8.8.8", "proxy": False}
        ]
        txt_records = [
            {"id": "txt_1", "name": "TXT-ext-dns-app1.example.com", "content": "heritage=docker-external-dns,owner=docker-external-dns"},
            {"id": "txt_3", "name": "TXT-ext-dns-app3.example.com", "content": "heritage=docker-external-dns,owner=docker-external-dns"}
        ]

        creates, updates, deletes = compute_dns_diff(desired, existing, txt_records)

        # app2 should be created
        self.assertEqual(len(creates), 1)
        self.assertEqual(creates[0]["name"], "app2.example.com")

        # app1 should be updated (IP changed from 1.1.1.1 to 1.2.3.4)
        self.assertEqual(len(updates), 1)
        self.assertEqual(updates[0]["new"]["target"], "1.2.3.4")

        # app3 should be deleted (owned by us, no longer in desired)
        self.assertEqual(len(deletes), 1)
        self.assertEqual(deletes[0]["record"]["name"], "app3.example.com")

if __name__ == "__main__":
    unittest.main()
