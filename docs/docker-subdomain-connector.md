# Docker Subdomain Connector

Access Docker repositories via subdomain without specifying a port or `/repository/` path prefix.

## Overview

With the connector enabled, Docker clients can use:

```
docker pull myrepo.nexspence.example.com/alpine:latest
```

instead of:

```
docker pull nexspence.example.com:8081/repository/myrepo/alpine:latest
```

## Setup

### 1. Enable in config.yaml

```yaml
docker:
  subdomain_connector:
    enabled: true
    base_domain: "nexspence.example.com"
```

### Hostname aliases (optional)

By default the subdomain must equal the repository name. When migrating from
Nexus — where connector ports and DNS names are chosen independently of repo
names — that forces repo renames or Host-rewriting proxy rules. Aliases map any
client-facing hostname to the repository that should answer it:

```yaml
docker:
  subdomain_connector:
    enabled: true
    base_domain: "nexspence.example.com"   # optional when only aliases are used
    aliases:
      # legacy DNS name (any domain) → repository name
      docker-hub-proxy.domain.example.com: dockerhub-proxy
      # an alias under base_domain overrides the implicit subdomain repo
      hub.nexspence.example.com: dockerhub-proxy
```

Alias hostnames are matched case-insensitively, ignoring any port. Point the
DNS record (or Istio/Ingress route) at Nexspence and pass the original `Host`
header through, exactly as below — no repository rename needed.

### 1b. Or with the Helm chart

```yaml
config:
  docker:
    subdomainConnector:
      enabled: true
      baseDomain: "nexspence.example.com"
      aliases: {}   # optional, same meaning as above
```

The chart passes `enabled` and `baseDomain` as environment variables; setting
any alias makes it mount a small config file, because a YAML map has no
environment-variable spelling. Remember to add a wildcard host to
`ingress.hosts` — see the chart README's *Docker Subdomain Connector* section.

### 2. Wildcard DNS

Add a wildcard DNS A record pointing to your Nexspence server:

```
*.nexspence.example.com  →  <your-server-ip>
```

### 3. Reverse Proxy — nginx

```nginx
server {
    listen 443 ssl;
    server_name *.nexspence.example.com;

    ssl_certificate     /etc/ssl/certs/nexspence.crt;
    ssl_certificate_key /etc/ssl/private/nexspence.key;

    location / {
        proxy_pass         http://localhost:8081;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 1800s;
        client_max_body_size 0;
    }
}
```

> **Important:** Pass the original `Host` header through (`proxy_set_header Host $host`). The subdomain connector reads the `Host` header to extract the repository name.

### 4. Reverse Proxy — Traefik

```yaml
# Add these labels to the Nexspence service in docker-compose.yml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.nexspence-subdomain.rule=HostRegexp(`{subdomain:[a-z0-9-]+}.nexspence.example.com`)"
  - "traefik.http.routers.nexspence-subdomain.entrypoints=websecure"
  - "traefik.http.routers.nexspence-subdomain.tls=true"
  - "traefik.http.services.nexspence-subdomain.loadbalancer.server.port=8081"
```

## How It Works

`SubdomainRewriter` is an `http.Handler` wrapper that sits in front of the Gin engine. When it detects a request with `Host: <repo>.<baseDomain>`, it rewrites the URL path before routing:

```
GET /v2/alpine/manifests/latest  (Host: myrepo.nexspence.example.com)
→
GET /v2/myrepo/alpine/manifests/latest
```

The OCI version check (`/v2/`) is not rewritten — Docker clients use it to detect the registry endpoint. Direct access via the base domain continues to work unchanged.

## Limitations

- Only single-level subdomains are supported (`myrepo.example.com`, not `a.b.example.com`).
- HTTPS + wildcard TLS certificate required for production (`*.nexspence.example.com`).
- Authentication is still enforced per repository — `docker login` is required for private repos.
- `docker login` target must match the subdomain: `docker login myrepo.nexspence.example.com`.
