# docker-traefik-dns

A lightweight dynamic DNS sync service for Docker and Traefik environments.

Docker + Traefik + Cloudflare 的轻量动态 DNS 同步服务。

## Overview / 概览

This project discovers hostnames from Docker labels or Traefik router rules and reconciles them to Cloudflare DNS records.

本项目会从 Docker 标签或 Traefik 路由规则中发现域名，并同步到 Cloudflare DNS 记录中。

## Features / 功能

- Detect hostnames from Docker and Traefik / 从 Docker 和 Traefik 发现域名
- Parse `Host(...)` and `HostSNI(...)` / 解析 `Host(...)` 和 `HostSNI(...)`
- Auto-detect `A`, `AAAA`, and `CNAME` / 自动识别 `A`、`AAAA` 和 `CNAME`
- Filter by domain allow-list / 可按域名白名单过滤
- Keep ownership using TXT records / 使用 TXT 记录保存所有权
- Run on a reconciliation loop / 以定时同步循环运行
- Dry-run support / 支持 dry-run 模式

## How it works / 工作原理

1. Read config from environment variables / 从环境变量读取配置
2. Discover desired records from Docker or Traefik / 从 Docker 或 Traefik 发现目标记录
3. Normalize and deduplicate hostnames / 规范化并去重域名
4. Query Cloudflare zones and existing records / 查询 Cloudflare zone 和现有记录
5. Reconcile only managed records / 仅同步本服务管理的记录
6. Repeat on a fixed interval / 按固定间隔循环执行

## Configuration / 配置

| Variable / 变量     | Default / 默认值              | Description / 说明                                         |
| ------------------- | ----------------------------- | ---------------------------------------------------------- |
| `CF_API_TOKEN`      | required / 必填               | Cloudflare API token                                       |
| `CF_API_KEY`        | -                             | Cloudflare API key                                         |
| `CF_API_EMAIL`      | -                             | Cloudflare email for key auth                              |
| `DOMAIN_FILTER`     | -                             | Comma-separated allowed domains / 域名白名单               |
| `DEFAULT_TARGET_IP` | -                             | Default record target / 默认记录目标                       |
| `DNS_SOURCE`        | `both`                        | `docker`, `traefik`, or `both`                             |
| `CF_PROXY`          | `false`                       | Enable Cloudflare proxy / 是否开启代理                     |
| `CF_TTL`            | `1`                           | DNS TTL                                                    |
| `SYNC_INTERVAL`     | `60s`                         | Reconcile interval / 同步间隔                              |
| `DRY_RUN`           | `false`                       | Log actions without applying them / 仅打印不执行           |
| `IDENTIFIER`        | `docker-traefik-dns`          | TXT ownership identifier / TXT 所有权标识                  |
| `LOG_LEVEL`         | `info`                        | `debug`, `info`, `warn`, `error`                           |
| `DOCKER_HOST`       | `unix:///var/run/docker.sock` | Docker socket or host / Docker socket 或地址               |
| `TRAEFIK_API_URL`   | -                             | Traefik API base URL / Traefik API 地址                    |
| `TRAEFIK_TARGET_IP` | -                             | Fallback target for Traefik records / Traefik 记录回退目标 |

## Quick start / 快速开始

Create `.env` and run with Docker Compose.

创建 `.env` 并使用 Docker Compose 启动。

```bash
CF_API_TOKEN=your_cloudflare_token
DOMAIN_FILTER=example.com
DEFAULT_TARGET_IP=100.0.0.1
CF_PROXY=false
```

```yaml
services:
  docker-traefik-dns:
    build: .
    container_name: docker-traefik-dns
    restart: unless-stopped
    env_file: .env
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
```

## Examples / 示例

### Traefik rule / Traefik 路由规则

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.app.rule=Host(`app.example.com`)"
```

This is automatically picked up and converted into DNS.

这会被自动识别并转换为 DNS 记录。

### Explicit hostname override / 显式主机名覆盖

```yaml
labels:
  - "docker-traefik-dns.hostname=demo.example.com"
  - "docker-traefik-dns.target=192.168.1.50"
  - "docker-traefik-dns.proxy=true"
```

## Safety / 安全机制

The service writes ownership TXT records and only updates or deletes records it owns.

本服务会写入所有权 TXT 记录，并且只更新/删除自己创建的记录。

This prevents accidental deletion of manually managed DNS records.

这样可以避免误删手工管理的 DNS 记录。

## Notes / 说明

- Designed for self-hosted Docker and Traefik / 适用于自托管 Docker 和 Traefik
- Best used with Traefik routes / 最适合和 Traefik 路由配合使用
- `DNS_SOURCE` supports `docker`, `traefik`, or `both` / `DNS_SOURCE` 支持 `docker`、`traefik`、`both`
- `DRY_RUN=true` is useful before enabling automatic updates / 在开启自动更新前，可先用 `DRY_RUN=true` 验证

## Label compatibility / 标签兼容性

When running with Docker socket access, this project is designed to work in a standalone Docker environment, but it does not abandon standard Traefik semantics.

在使用 Docker socket 运行时，本项目设计为可独立在 Docker 环境中工作，但它并不会放弃标准 Traefik 语义。

Supported inputs / 支持的输入：

- Standard Traefik labels / 标准 Traefik 标签
  - `traefik.http.routers.*.rule`
  - `traefik.tcp.routers.*.rule`
- Custom Docker labels / 自定义 Docker 标签
  - `docker-traefik-dns.hostname`
  - `docker-traefik-dns.target`
  - `docker-traefik-dns.proxy`
- Compatibility labels / 兼容标签
  - `external-dns.alpha.kubernetes.io/hostname`
  - `external-dns.alpha.kubernetes.io/target`
  - `external-dns.alpha.kubernetes.io/cloudflare-proxied`

### Precedence / 优先级

For target and proxy values, the project prefers explicit custom labels first, then compatibility labels, then the global fallback values.

对于 `target` 和 `proxy` 值，本项目优先使用显式的自定义标签，然后是兼容标签，最后回退到全局默认值。

That means:

- `docker-traefik-dns.target` > `external-dns.alpha.kubernetes.io/target` > `DEFAULT_TARGET_IP`
- `docker-traefik-dns.proxy` > `external-dns.alpha.kubernetes.io/cloudflare-proxied` > `CF_PROXY`

For hostnames, both standard Traefik router rules and explicit hostname labels are accepted and merged together.

对于域名解析，标准 Traefik 路由规则和显式 hostname 标签都会被接受，并合并去重。

So the design is intentionally hybrid: it supports standalone Docker operation while remaining compatible with the Traefik ecosystem.

因此，这个设计是混合型的：既适合独立 Docker 运行，也兼容 Traefik 标准生态。

## License / 许可证

GPL-3.0
