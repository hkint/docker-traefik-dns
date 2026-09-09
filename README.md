# docker-traefik-dns

A lightweight, dedicated dynamic DNS automation service tailored for self-hosted Docker stacks and homelabs. It automatically detects `Host` domain rules configured on **Traefik** routers or **Docker container labels**, and creates / updates corresponding DNS records on **Cloudflare**.

---

## 🚀 Key Features & Modifications

1. **Lightweight & Self-Hosted Focused**:
   - Stripped away unnecessary providers (e.g. Pi-hole), leaving a clean, hyper-focused, low-resource Cloudflare DNS engine.
   - Memory footprint < 15MB RAM; Docker image size ~15MB.
2. **Dual Host Discovery**:
   - **Direct Docker Socket Inspection**: Automatically reads Traefik labels (`traefik.http.routers.<name>.rule=Host(...)`) and standard external-dns labels directly from `/var/run/docker.sock`. You do not even need Traefik API exposed!
   - **Traefik API Provider**: Can also connect directly to local or remote Traefik endpoints (`/api/http/routers` and `/api/tcp/routers`).
   - Supports robust rule parsing: backticks, single/double quotes, multiple comma-separated hosts, `HostSNI`, and compound expressions (`||`, `&&`).
3. **Safe Cloudflare Synchronization**:
   - Uses TXT ownership records (`heritage=docker-traefik-dns,owner=<identifier>`) to guarantee it **never overwrites or deletes existing manual DNS records** in your Cloudflare account.
   - Auto-detects DNS record types (`A` for IPv4, `AAAA` for IPv6, `CNAME` for hostnames).
   - Zone auto-discovery with longest-suffix matching.
   - Supports Cloudflare Proxy toggle (`CF_PROXY`) globally and per-container override.

---

## 🛠️ Configuration Options

| Environment Variable | Default                       | Description                                                                  |
| :------------------- | :---------------------------- | :--------------------------------------------------------------------------- |
| `CF_API_TOKEN`       | _Required_                    | Cloudflare API Token (requires `Zone.DNS` edit permissions)                  |
| `DEFAULT_TARGET_IP`  | -                             | Default IP address (WAN or LAN) to point domain records to                   |
| `DOMAIN_FILTER`      | -                             | Comma-separated list of domains to manage (e.g. `example.com,homelab.org`)   |
| `DNS_SOURCE`         | `both`                        | Source of hostnames: `both` (default), `docker`, or `traefik`                |
| `CF_PROXY`           | `false`                       | Enable Cloudflare CDN proxy (orange cloud) by default                        |
| `CF_TTL`             | `1`                           | DNS TTL in seconds (`1` for Cloudflare Automatic)                            |
| `SYNC_INTERVAL`      | `60s`                         | Polling and reconciliation interval                                          |
| `DRY_RUN`            | `false`                       | If `true`, logs planned operations without making API calls                  |
| `IDENTIFIER`         | `docker-traefik-dns`          | Unique owner ID used in TXT ownership verification                           |
| `LOG_LEVEL`          | `info`                        | Logging verbosity (`debug`, `info`, `warn`, `error`)                         |
| `DOCKER_HOST`        | `unix:///var/run/docker.sock` | Docker socket path or TCP address                                            |
| `TRAEFIK_API_URL`    | -                             | Traefik API base URL (e.g. `http://traefik:8080`)                            |
| `TRAEFIK_TARGET_IP`  | -                             | Target IP specific to Traefik API routes (falls back to `DEFAULT_TARGET_IP`) |

---

## 📦 Quick Start with Docker Compose

### 1. Create `.env` file

```bash
CF_API_TOKEN=your_cloudflare_api_token_here
DOMAIN_FILTER=example.com
DEFAULT_TARGET_IP=1.2.3.4
CF_PROXY=false
```

### 2. Run with Docker Compose

```yaml
services:
  external-dns:
    image: docker-traefik-dns:latest
    build: .
    container_name: docker-traefik-dns
    restart: unless-stopped
    env_file: .env
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks:
      - traefik-net

networks:
  traefik-net
    driver: bridge
```

### 3. Container Label Examples

#### Any Service with Traefik:

```yaml
services:
  whoami:
    image: traefik/whoami
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.whoami.rule=Host(`whoami.example.com`)"
```

`docker-traefik-dns` automatically detects `whoami.example.com` and creates an A record pointing to `DEFAULT_TARGET_IP` on Cloudflare!

#### Overriding IP or Cloudflare Proxy per-container:

```yaml
services:
  my-app:
    image: my-app:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.app.rule=Host(`app.example.com`)"
      - "docker-traefik-dns.target=192.168.1.50"
      - "docker-traefik-dns.proxy=true"
```
