import json
import re

from test_suite import (
    extract_traefik_hosts,
    extract_docker_container_records,
    find_matching_zone,
    matches_domain_filter,
    compute_dns_diff
)

class MockCloudflareAPI:
    def __init__(self, zones):
        self.zones = zones # name -> zone_id
        self.records = {}  # id -> record dict
        self._next_id = 1

    def list_records(self, zone_id):
        return [r for r in self.records.values() if r["zone_id"] == zone_id]

    def create_record(self, zone_id, record):
        rec_id = f"cf_rec_{self._next_id}"
        self._next_id += 1
        stored = dict(record)
        stored["id"] = rec_id
        stored["zone_id"] = zone_id
        self.records[rec_id] = stored
        return stored

    def update_record(self, zone_id, rec_id, record):
        if rec_id in self.records:
            self.records[rec_id].update(record)
            return self.records[rec_id]
        raise ValueError(f"Record {rec_id} not found")

    def delete_record(self, zone_id, rec_id):
        if rec_id in self.records:
            del self.records[rec_id]
            return True
        raise ValueError(f"Record {rec_id} not found")

def run_simulation():
    print("=== Starting End-to-End Simulation ===")
    
    # 1. Initialize Mock Cloudflare with zones
    cf = MockCloudflareAPI({"example.com": "zone_123", "homelab.org": "zone_456"})
    
    # Existing manual record in Cloudflare (unmanaged, no TXT owner)
    cf.create_record("zone_123", {
        "type": "A",
        "name": "blog.example.com",
        "target": "9.9.9.9",
        "proxy": False
    })
    
    # 2. Simulate running Docker containers
    container_1_labels = {
        "traefik.enable": "true",
        "traefik.http.routers.whoami.rule": "Host(`whoami.example.com`) || Host(`whoami-alt.example.com`)",
        "docker-external-dns.proxy": "true"
    }
    container_2_labels = {
        "traefik.enable": "true",
        "traefik.http.routers.grafana.rule": "Host(`grafana.homelab.org`) && PathPrefix(`/`)",
        "docker-external-dns.target": "10.0.0.5"
    }
    
    default_ip = "1.2.3.4"
    desired = []
    desired.extend(extract_docker_container_records(container_1_labels, default_ip))
    desired.extend(extract_docker_container_records(container_2_labels, default_ip))
    
    print(f"Discovered {len(desired)} desired records from containers:")
    for d in desired:
        print(f"  - {d['name']} ({d['type']}) -> {d['target']} (proxied: {d['proxy']})")
        
    # 3. Simulate Syncer Cycle 1: First sync (Creation)
    existing_records = [r for r in cf.records.values() if r["type"] != "TXT"]
    existing_txt = [r for r in cf.records.values() if r["type"] == "TXT"]
    
    creates, updates, deletes = compute_dns_diff(desired, existing_records, existing_txt)
    print(f"\nCycle 1 Diff: {len(creates)} creates, {len(updates)} updates, {len(deletes)} deletes")
    assert len(creates) == 3, f"Expected 3 creates, got {len(creates)}"
    assert len(updates) == 0
    assert len(deletes) == 0
    
    # Apply creates
    for c in creates:
        zone_id = cf.zones[find_matching_zone(c["name"], cf.zones.keys())]
        rec = cf.create_record(zone_id, c)
        # Create TXT ownership
        cf.create_record(zone_id, {
            "type": "TXT",
            "name": f"TXT-ext-dns-{c['name']}",
            "content": "heritage=docker-external-dns,owner=docker-external-dns"
        })
    
    print(f"Cloudflare record count after Cycle 1: {len(cf.records)} (3 A-records + 3 TXT + 1 manual)")
    assert len(cf.records) == 7
    
    # 4. Simulate Syncer Cycle 2: No changes (Idempotency test)
    existing_records = [r for r in cf.records.values() if r["type"] != "TXT"]
    existing_txt = [r for r in cf.records.values() if r["type"] == "TXT"]
    creates, updates, deletes = compute_dns_diff(desired, existing_records, existing_txt)
    print(f"Cycle 2 (Idempotency) Diff: {len(creates)} creates, {len(updates)} updates, {len(deletes)} deletes")
    assert len(creates) == 0
    assert len(updates) == 0
    assert len(deletes) == 0
    
    # 5. Simulate Syncer Cycle 3: IP Changed & Container Removed
    # Container 2 (Grafana) is stopped/removed.
    # Container 1 changes default IP from 1.2.3.4 to 1.2.3.99
    new_default_ip = "1.2.3.99"
    desired_cycle_3 = extract_docker_container_records(container_1_labels, new_default_ip)
    
    existing_records = [r for r in cf.records.values() if r["type"] != "TXT"]
    existing_txt = [r for r in cf.records.values() if r["type"] == "TXT"]
    creates, updates, deletes = compute_dns_diff(desired_cycle_3, existing_records, existing_txt)
    print(f"\nCycle 3 Diff: {len(creates)} creates, {len(updates)} updates, {len(deletes)} deletes")
    assert len(creates) == 0
    assert len(updates) == 2, f"Expected 2 updates for container 1 hosts, got {len(updates)}"
    assert len(deletes) == 1, f"Expected 1 delete for grafana, got {len(deletes)}"
    assert deletes[0]["record"]["name"] == "grafana.homelab.org"
    
    # Apply updates & deletes
    for u in updates:
        zone_id = cf.zones[find_matching_zone(u["new"]["name"], cf.zones.keys())]
        cf.update_record(zone_id, u["id"], u["new"])
        
    for d in deletes:
        zone_id = cf.zones[find_matching_zone(d["record"]["name"], cf.zones.keys())]
        cf.delete_record(zone_id, d["id"])
        cf.delete_record(zone_id, d["txt_id"])
        
    print(f"Cloudflare record count after Cycle 3: {len(cf.records)} (2 updated A-records + 2 TXT + 1 manual)")
    assert len(cf.records) == 5
    
    # Verify manual record was never touched
    manual_rec = [r for r in cf.records.values() if r["name"] == "blog.example.com"][0]
    assert manual_rec["target"] == "9.9.9.9", "Manual unmanaged record should NOT be modified!"
    
    print("\nAll End-to-End Simulation Tests PASSED successfully!")

if __name__ == "__main__":
    run_simulation()
