# Nexspence — Deployment Guide

## Download

All releases — Docker images, `docker-compose.yml`, `config.yaml.example` — are at:

**[github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases)**

Download `docker-compose.yml` and `config.yaml.example` from the latest release, then follow the relevant section below.

---

## Docker Compose — Standard

```bash
# 1. Download files from the latest release:
#    https://github.com/nexspence/nexspence/releases/latest
#    → docker-compose.yml
#    → config.yaml.example  (rename to config.yaml and edit)

cp config.yaml.example config.yaml

# 2. Edit config.yaml — change at minimum:
#      auth.jwt_secret        (min 32 characters)
#      bootstrap.admin_password

# 3. Start PostgreSQL + Nexspence (auto-migrates schema on first run)
docker compose up -d

# 4. Verify
docker compose ps
docker compose logs -f nexspence
```

| Service | URL | Default credentials |
|---------|-----|---------------------|
| Web UI & REST API | http://localhost:8081 | `admin` / `admin123` |
| Docker registry | localhost:5000 | same credentials |
| PostgreSQL | localhost:5437 | `nexspence` / `nexspence` |

> Change the admin password immediately after first login via **Admin → Security → Users**.

---

## Docker Compose — With MinIO (S3)

MinIO is included in `docker-compose.yml` as an optional profile:

```bash
# Start with MinIO as the default blob store
NEXSPENCE_STORAGE_DEFAULT_TYPE=s3 \
  docker compose up -d

# MinIO S3 API:    http://localhost:9000
# MinIO console:   http://localhost:9001  (minioadmin / minioadmin)
# Nexspence UI:    http://localhost:8081
```

---

## Docker Compose — HA Cluster

`docker-compose.ha.yml` (included in the release) runs 2 Nexspence nodes, nginx (`least_conn` load balancer), Redis, MinIO, and PostgreSQL. All nodes are stateless — shared state lives in PostgreSQL, Redis, and S3.

```bash
# Download docker-compose.ha.yml from the latest release, then:
docker compose -f docker-compose.ha.yml up -d

# Load balancer:  http://localhost:8080
# 2 x Nexspence + nginx LB + Redis + MinIO + PostgreSQL
```

Enable Redis in `config.yaml` for each node:

```yaml
redis:
  enabled: true
  addr: "redis:6379"
  password: ""
  db: 0
```

See [docs/ha-setup.md](ha-setup.md) for the full HA guide including Kubernetes probe examples.

---

## Docker Compose — With Keycloak SSO

Starts a pre-configured Keycloak dev instance with the `nexspence` realm imported. "Sign in with Keycloak" appears on the login page automatically.

```bash
OIDC_ENABLED=true \
  docker compose --profile keycloak up -d

# Nexspence UI:    http://localhost:8081  (admin / admin123)
# Keycloak admin:  http://localhost:8180  (admin / admin)
# Test SSO user:   testuser / testpass (mapped to nx-admin role)
```

See [docs/oidc-setup.md](oidc-setup.md) for manual OIDC config and all supported providers (Keycloak, Google, Entra ID, Okta).

---

## Native Install (no Docker)

Run Nexspence directly on Linux (`.deb`/`.rpm`), macOS, or Windows with systemd /
launchd / Windows-service integration. The single binary embeds the web UI and requires
only an external PostgreSQL. See the full guide — including reverse-proxy and multi-node
load-balancer configs — in [install-local.md](install-local.md).

---

## Kubernetes (Helm)

```bash
cd deploy/helm/nexspence && helm dependency update
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

> `config.jwtSecret` is optional — when omitted, the chart auto-generates a unique random secret on first install and persists it across upgrades. Set it explicitly only to pin a known value or share it across clusters.

Five networking options (nginx, Traefik, Cilium ingress, Istio Gateway, Cilium Gateway API), external PostgreSQL, S3 storage, and HPA — see [deploy/helm/nexspence/README.md](../deploy/helm/nexspence/README.md).

---

## Image scanning (Trivy)

Image scanning is not included. Nexspence ships no scanner binary; to scan
Docker and OCI images you supply Trivy yourself — see [scanning.md](scanning.md)
for the complete per-deployment procedure (Helm: `scanning.enabled: true`;
compose: `docker compose --profile scanning up -d` with
`NEXSPENCE_SCAN_TRIVY_ENABLED=true`). Scanning of Maven/npm/PyPI/Cargo packages
uses OSV.dev and works with nothing installed.

---

## Configuration Reference

`config.yaml` is the primary configuration file. Every key can be overridden via an environment variable using the pattern `NEXSPENCE_<SECTION>_<KEY>` (uppercase, underscore-separated).

| Key | Default | Description |
|-----|---------|-------------|
| `http.addr` | `:8081` | Listen address |
| `http.base_url` | `http://localhost:8081` | Public URL used in download links |
| `http.trusted_proxies` | `[]` | Peers (IPs/CIDRs) whose `X-Forwarded-For` is believed. Empty trusts nobody, so the audit log and rate limiter see the real peer. Set it to your reverse proxy when you run behind one; `["*"]` trusts every hop. |
| `http.csp` | `""` | Content-Security-Policy for UI/API responses. Empty uses the built-in policy; `"off"` omits the header. Artifact paths are exempt. |
| `http.cors_origins` | `[]` | Origins allowed to read API responses from a browser. Empty sends no CORS header — correct when the bundled UI shares this origin. `["*"]` lets any site read responses; opt in only for a public instance. |
| `database.dsn` | `postgres://nexspence:nexspence@localhost:5437/nexspence` | PostgreSQL connection string |
| `storage.default_type` | `local` | `local` or `s3` |
| `storage.local.base_path` | `./data/blobs` | Filesystem path for local blob store |
| `storage.s3.bucket` | — | S3 bucket name (required when type=s3) |
| `storage.s3.endpoint` | — | S3 endpoint URL (e.g. `http://minio:9000`) |
| `storage.s3.force_path_style` | `true` | Required for MinIO / non-AWS S3 |
| `storage.s3.skip_tls_verify` | `false` | Accept the endpoint's TLS certificate without verifying it. For an on-prem S3 behind a private CA whose root you cannot install into the container's trust store; the connection carries the credentials and every blob, so prefer trusting the CA. Per-blob-store stores set the same thing with `skip_tls_verify` in their config (System Admin → Blob Stores → *Skip TLS certificate verification*). |
| `auth.jwt_secret` | — | JWT signing key. **From source / native install: set this (min 32 chars) before production.** The Docker image and Helm chart auto-generate a unique secret when it is unset. |
| `auth.encryption_key` | — | Optional base64 32-byte key for replication credentials (decouples them from `jwt_secret`; existing rows are re-encrypted automatically at startup). Generate: `openssl rand -base64 32` |
| `auth.jwt_expiry_hours` | `24` | JWT token lifetime |
| `auth.anonymous_enabled` | `true` | Instance-wide switch for unauthenticated reads. `false` refuses them everywhere, overriding any repository's `allow_anonymous`. |
| `auth.token_max_days` | `180` | Maximum lifetime for user API tokens (`nxs_*`) |
| `outbound.allowed_internal_cidrs` | `[]` | Internal ranges the SSRF guard may reach for proxy, webhook and replication targets. Empty refuses every loopback/private/link-local/CGNAT address. |
| `auth.rate_limit_enabled` | `true` | Token-bucket throttle per user (per client address when anonymous). Turning it off leaves `/api/v1/login` unmetered. |
| `auth.rate_limit_rps` / `auth.rate_limit_burst` | `50` / `100` | Sustained rate and burst for the above |
| `bootstrap.enabled` | `true` | Create the admin account on start. Set to `false` once real accounts exist to stop keeping admin credentials in the config — see [Removing the bootstrap admin credentials](#removing-the-bootstrap-admin-credentials). |
| `bootstrap.admin_password` | `admin123` | Auto-created admin password — **change this** |
| `scan.trivy.enabled` | `false` | Image scanning switch. Requires a Trivy binary you supply — see [scanning.md](scanning.md). |
| `scan.trivy.bin` | `trivy` | Path to the Trivy binary, or a name resolved through `PATH` |
| `cleanup.default_schedule` | `0 2 * * *` | Default cron for cleanup policies |
| `audit.retention_days` | `90` | Audit log partition retention |
| `metrics.public` | `false` | Serve `GET /metrics` without authentication. Default requires a Bearer token; see [Prometheus](#prometheus). |
| `redis.enabled` | `false` | Enable Redis (required for HA) |
| `redis.addr` | `localhost:6379` | Redis address |

---

## Removing the bootstrap admin credentials

On every start Nexspence creates the `bootstrap.admin_username` account if it is missing. That is what gives a fresh install someone to log in as — but it also means the admin username and password sit in the config file for the life of the deployment, long after real accounts exist.

To take them out, turn the bootstrap off:

```yaml
bootstrap:
  enabled: false
```

The four `admin_*` keys can then be deleted. Existing accounts — including the admin — are left exactly as they are; bootstrap never overwrites a password that has been set.

Two things to know:

- **Do this only after the admin password has been set** (first login, or any rotation in the UI / over the API). A freshly migrated database seeds the admin row with a placeholder hash that no password matches, so disabling bootstrap before that leaves nobody able to log in. The server logs a warning at startup when it detects exactly this.
- **Deleting the `bootstrap` block on its own is not enough.** The shipped defaults (`admin` / `admin123`) apply to any key you omit, and the startup security check then refuses to boot on the default password. `enabled: false` is what makes omitting the rest safe.

Under Kubernetes the same switch is `config.bootstrapEnabled: false` in the Helm values, which also lets you drop `adminPassword` from the chart's Secret. Setting it through the environment works too: `NEXSPENCE_BOOTSTRAP_ENABLED=false`.

If you would rather keep the bootstrap on but stop storing the password in the file, leave `enabled: true` and pass it as `NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD` instead.

---

## Prometheus

Nexspence exposes the Prometheus text format at `GET /metrics`, on the same listener as the API.

That shared listener is why the endpoint requires a Bearer token by default: an anonymous scrape publishes install size, artifact and download counts and the Go runtime fingerprint to anyone who can reach the instance.

**Authenticated scrape (default).** Create a user token in the UI (Profile → Tokens) and hand it to Prometheus:

```yaml
scrape_configs:
  - job_name: nexspence
    authorization:
      type: Bearer
      credentials: nxs_...
    static_configs:
      - targets: ["nexspence:8081"]
```

With a `ServiceMonitor`, put the token in a Secret instead:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: nexspence
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: nexspence
  endpoints:
    - port: http
      path: /metrics
      interval: 30s
      authorization:
        type: Bearer
        credentials:
          name: nexspence-metrics-token   # Secret with key `token` = nxs_...
          key: token
```

**Anonymous scrape.** When the listener is only reachable from a trusted network — a cluster-internal `Service`, a localhost bind, or a reverse proxy that blocks `/metrics` from outside — a scrape token is just one more secret to rotate. Turn it off:

```yaml
metrics:
  public: true
```

or `NEXSPENCE_METRICS_PUBLIC=true`, or `--set config.metricsPublic=true` for the Helm chart. The `authorization:` block then drops out of the scrape config.

The endpoint serves `nexspence_requests_total`, `nexspence_request_duration_seconds`, `nexspence_artifacts_total`, `nexspence_bytes_stored_bytes`, `nexspence_downloads_total`, `nexspence_goroutines` and `nexspence_memory_alloc_bytes`, plus the standard Go and process collectors. `/healthz` and `/readyz` are always unauthenticated and are the right targets for probes.
