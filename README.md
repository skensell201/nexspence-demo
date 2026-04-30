<div align="center">
  <img src="docs/assets/logo.png" alt="Nexspence" width="380">
  <br><br>
  <p><strong>Free, open-source universal artifact repository manager</strong></p>
  <p>A full-featured self-hosted alternative to Sonatype Nexus Repository OSS / Pro</p>
  <br>

  ![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go&logoColor=white)
  ![React](https://img.shields.io/badge/React-18-61DAFB?style=flat-square&logo=react&logoColor=black)
  ![TypeScript](https://img.shields.io/badge/TypeScript-5-3178C6?style=flat-square&logo=typescript&logoColor=white)
  ![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16+-4169E1?style=flat-square&logo=postgresql&logoColor=white)
  ![Docker](https://img.shields.io/badge/Docker-ready-2496ED?style=flat-square&logo=docker&logoColor=white)
  ![License](https://img.shields.io/badge/License-AGPLv3-22c55e?style=flat-square)

</div>

---

## What is Nexspence?

Nexspence is a self-hosted artifact repository manager that supports **12 package formats**, three repository types (hosted, proxy, group), fine-grained RBAC, SSO via OIDC/LDAP, audit logging, S3-compatible storage, and a modern dark-theme web UI — all in a single binary backed by PostgreSQL.

It exposes the full **Sonatype Nexus OSS v1 REST API** at `/service/rest/v1/` for drop-in compatibility with existing CI/CD pipelines, Maven/Gradle settings, and npm/pip configurations.

---
## Screenshots

### Dashboard & Repositories

<table>
  <tr>
    <td><img src="docs/assets/screenshots/repositories.PNG" alt="Repositories page" width="480"></td>
    <td><img src="docs/assets/screenshots/browse.PNG" alt="Browse" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Repositories list</em></td>
    <td align="center"><em>Browse tree view</em></td>
  </tr>
</table>

### Admin & Security

<table>
  <tr>
    <td><img src="docs/assets/screenshots/admin_blobstores.PNG" alt="Blob Stores" width="480"></td>
    <td><img src="docs/assets/screenshots/security_roles.PNG" alt="Roles & RBAC" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Blob stores — S3 + local with connection test</em></td>
    <td align="center"><em>Roles, privileges, content selectors</em></td>
  </tr>
</table>

### Cleanup & Search

<table>
  <tr>
    <td><img src="docs/assets/screenshots/cleanup.PNG" alt="Cleanup policies" width="480"></td>
    <td><img src="docs/assets/screenshots/search.PNG" alt="Search" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Cleanup policies with dry-run preview</em></td>
    <td align="center"><em>Full-text component search</em></td>
  </tr>
</table>

---

## Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) 24+
- [Docker Compose](https://docs.docker.com/compose/install/) v2 (bundled with Docker Desktop)

### 1. Clone the repository

```bash
git clone https://github.com/skensell201/nexspence
cd nexspence
```

The repository includes two ready-to-use files in the root:

| File | Purpose |
|------|---------|
| `docker-compose.yml` | Starts PostgreSQL + Nexspence (with optional Keycloak / MinIO) |
| `config.yaml` | Full application configuration — mounted read-only into the container |

### 2. Configure before first launch

Open `config.yaml` and change at minimum these two values:

```yaml
auth:
  jwt_secret: "CHANGE_ME_AT_LEAST_32_CHARACTERS_LONG"   # ← replace with a random 32+ char string

bootstrap:
  admin_password: "changeme"   # ← your initial admin password
```

Everything else works out of the box for a local setup. Environment variables in `docker-compose.yml` take precedence over `config.yaml` — so you can also override values there without editing the file:

```bash
# Example: override admin password without touching config.yaml
NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD=mysecret docker compose up -d
```

### 3. Start the stack

```bash
docker compose up -d
```

Docker Compose will:
1. Pull `postgres:16-alpine` and `nexspence/nexspence:latest`
2. Start PostgreSQL and wait for it to become healthy
3. Start Nexspence — it auto-migrates the schema and bootstraps the admin account on first run

Check that everything is up:

```bash
docker compose ps
docker compose logs -f nexspence
```

### 4. Open the web UI

| Service | URL | Default credentials |
|---------|-----|---------------------|
| Web UI & REST API | http://localhost:8081 | `admin` / `changeme` |
| Docker registry | localhost:5000 | same credentials |
| PostgreSQL | localhost:5437 | `nexspence` / `nexspence` |

> Change the default password immediately after the first login via **Administration → Security → Users**.

### 5. Stop and clean up

```bash
# Stop containers (data is preserved in Docker volumes)
docker compose down

# Stop AND remove all data volumes (full reset)
docker compose down -v
```

---

## Configuration

`config.yaml` is mounted into the container at `/app/config.yaml`. All keys can be overridden via environment variables using the pattern `NEXSPENCE_<SECTION>_<KEY>` (uppercase, underscore separator).

### HTTP server

```yaml
http:
  addr: ":8081"
  read_timeout_sec: 1800
  write_timeout_sec: 1800
  max_body_mb: 1024
  base_url: "http://localhost:8081"   # change to your public hostname in production
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
```

### Database

```yaml
database:
  dsn: "postgres://nexspence:nexspence@localhost:5437/nexspence?sslmode=disable"
  max_conns: 100
  min_conns: 5
  max_idle_sec: 300
```

> When running via Docker Compose the DSN is overridden by the `NEXSPENCE_DATABASE_DSN` environment variable — the container connects to the `postgres` service, not `localhost`.

### Storage

```yaml
storage:
  default_type: "local"   # "local" or "s3"
  local:
    base_path: "./data/blobs"
  # s3:
  #   bucket: "nexspence-blobs"
  #   region: "us-east-1"
  #   endpoint: "http://minio:9000"    # MinIO inside Docker Compose
  #   access_key_id: ""
  #   secret_access_key: ""
  #   force_path_style: true            # required for MinIO / non-AWS S3
```

### Authentication

```yaml
auth:
  jwt_secret: "CHANGE_ME_AT_LEAST_32_CHARACTERS_LONG"
  jwt_expiry_hours: 24
  anonymous_enabled: true      # allow read-only access to public repos without login
  password_min_length: 8
  bcrypt_cost: 12
  token_max_days: 180          # maximum lifetime of user API tokens (nxs_*)

bootstrap:
  admin_username: "admin"
  admin_password: "changeme"
  admin_email: "admin@example.com"
  admin_first_name: "Admin"
```

### LDAP / Active Directory (optional)

```yaml
ldap:
  enabled: false
  host: "ldap.example.com"
  port: 636                    # 636 for LDAPS, 389 for plain/STARTTLS
  use_tls: true
  bind_dn: "CN=svc-nexspence,OU=ServiceAccounts,DC=example,DC=com"
  bind_password: ""            # set via NEXSPENCE_LDAP_BIND_PASSWORD env var
  search_base: "DC=example,DC=com"
  search_filter: "(sAMAccountName={0})"   # AD; for OpenLDAP use: (uid={0})
  auto_create_users: true
  admin_group: ""              # group CN whose members get nx-admin role
```

### OIDC / OAuth2 SSO (optional)

Supports Keycloak, Google Workspace, Microsoft Entra ID, Okta.

```yaml
oidc:
  enabled: false
  display_name: "SSO"          # button label: "Sign in with {display_name}"
  issuer: ""                   # e.g. "https://kc.example.com/realms/nexspence"
  client_id: ""
  client_secret: ""            # set via NEXSPENCE_OIDC_CLIENT_SECRET env var
  redirect_url: ""             # "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: ""        # "https://nexspence.example.com"
  provisioning: "jit"          # jit | allowlist | manual
  admin_group: ""
```

---

## Optional services

The `docker-compose.yml` includes commented-out blocks for two optional services.

### Keycloak (local OIDC provider)

Uncomment the `keycloak:` block in `docker-compose.yml`:

```bash
docker compose --profile keycloak up -d
```

Admin UI: http://localhost:8180 (`admin` / `admin`)

After starting, create a realm `nexspence`, add a client `nexspence` (confidential, redirect URI: `http://localhost:8081/api/v1/auth/oidc/callback`), then enable OIDC in `config.yaml`:

```yaml
oidc:
  enabled: true
  issuer: "http://localhost:8180/realms/nexspence"
  client_id: "nexspence"
  client_secret: "<your-client-secret>"
  redirect_url: "http://localhost:8081/api/v1/auth/oidc/callback"
  frontend_base_url: "http://localhost:8081"
```

### MinIO (S3-compatible blob store)

Uncomment the `minio:` and `minio-init:` blocks in `docker-compose.yml`, then switch storage to S3 in `config.yaml`:

```yaml
storage:
  default_type: "s3"
  s3:
    bucket: "nexspence-blobs"
    region: "us-east-1"
    endpoint: "http://minio:9000"
    access_key_id: "minioadmin"
    secret_access_key: "minioadmin"
    force_path_style: true
```

MinIO console: http://localhost:9001 (`minioadmin` / `minioadmin`)

---

## Pointing your tools at Nexspence

Once the stack is running, configure your build tools to use Nexspence as the registry:

```bash
# Maven — settings.xml
<mirror>
  <id>nexspence</id>
  <url>http://localhost:8081/repository/maven-public/</url>
  <mirrorOf>central</mirrorOf>
</mirror>

# npm
npm config set registry http://localhost:8081/repository/npm-proxy/

# pip
pip install --index-url http://localhost:8081/repository/pypi-proxy/simple/ requests

# Docker
docker pull localhost:5000/my-image:latest

# Go
GOPROXY=http://localhost:8081/repository/go-proxy/,direct go get github.com/some/pkg

# Helm
helm repo add nexspence http://localhost:8081/repository/helm-hosted/

# Cargo — .cargo/config.toml
[registries.nexspence]
index = "sparse+http://localhost:8081/repository/cargo-hosted/"
```

---

## Supported Package Formats

| Format | Hosted | Proxy | Group |
|--------|:------:|:-----:|:-----:|
| Maven 2 / 3 | ✓ | ✓ | ✓ |
| npm | ✓ | ✓ | ✓ |
| PyPI | ✓ | ✓ | ✓ |
| Go modules (GOPROXY v2) | ✓ | ✓ | ✓ |
| Docker / OCI | ✓ | ✓ | ✓ |
| NuGet v2 / v3 | ✓ | ✓ | ✓ |
| Helm charts | ✓ | ✓ | ✓ |
| Cargo (Rust) | ✓ | ✓ | ✓ |
| APT (Debian/Ubuntu) | ✓ | ✓ | — |
| Yum / RPM | ✓ | ✓ | — |
| Conan (C/C++) | ✓ | ✓ | — |
| Raw files | ✓ | ✓ | ✓ |

---

## Feature Highlights

### Authentication & Access Control
- **Local accounts** — JWT bearer tokens, bcrypt passwords
- **LDAP / Active Directory** — JIT user provisioning, group → role mapping, admin-group sync
- **OIDC / OAuth 2.0 SSO** — Keycloak, Google Workspace, Microsoft Entra ID, Okta; Authorization Code + PKCE
- **User API tokens** — `nxs_…` prefixed, SHA-256 hashed in DB; usable as Bearer or HTTP Basic password
- **RBAC** — Roles, Privileges, Content Selectors (CEL expressions for path/format scoping)

### Storage
- **Local filesystem** — default, zero configuration
- **S3-compatible** — AWS S3, MinIO, Ceph; configurable per blob store; connection test endpoint
- **Per-repository blob store routing** — each repository can use a different physical blob store
- **Storage quotas** — per blob store and per repository; enforced on upload

### Repository Management
- **Hosted** — direct artifact upload and storage
- **Proxy** — transparent remote caching (cache-miss fetches from upstream, stores locally)
- **Group** — ordered union of hosted + proxy repos under a single URL
- **Cleanup policies** — by age, last-downloaded, format; retain-N-versions; cron scheduler; **dry-run preview**
- **Component tags** — free-form text tags on components, searchable via the API and UI

### Backup & Migration
- **Per-repository export** — streaming `.tar.gz` download (metadata + blobs)
- **Per-repository import** — multipart upload; skip or rename conflict resolution; deduplication by SHA-256
- **Full system backup / restore** — complete database + blob export
- **Live migration from Nexus** — import repositories, users, roles, cleanup policies from a running Nexus OSS/Pro instance

### Developer Experience
- **Nexus OSS v1 REST API** — `/service/rest/v1/` compatible; drop-in replacement
- **Full-text search** — PostgreSQL tsvector across components and assets
- **Browse UI** — tree view for raw and Docker repositories; file details with download, copy-link, and usage examples
- **Audit log** — every API action logged; filterable by date/user/path; NDJSON streaming export; 90-day retention
- **Webhooks** — `artifact.published`, `artifact.deleted`, `repo.created` events; HMAC-SHA256 signatures
- **Vulnerability scanning** — Trivy integration for Docker images; CVE results cached in component metadata
- **Dark glassmorphism UI** — sidebar collapse/expand; tabbed admin pages; wizard-style create flows

---

## API Compatibility

| Path prefix | Purpose |
|-------------|---------|
| `/service/rest/v1/` | Nexus OSS v1 REST — drop-in compatible |
| `/service/rest/beta/` | Nexus beta endpoints |
| `/api/v1/` | Nexspence-native API (migration, backup, extended admin) |
| `/repository/:name/*` | Artifact protocol endpoints |
| `/v2/` | OCI Distribution Spec v2 (Docker) |

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.22 — Gin, pgx v5, golang-migrate, go-oidc v3, zap |
| Frontend | React 18, TypeScript 5, Vite, Zustand, React Query, Axios |
| Database | PostgreSQL 16+ (pgx, goose migrations) |
| Storage | Local filesystem · S3-compatible (AWS S3, MinIO, Ceph) |
| Auth | JWT + bcrypt · LDAP/AD · OIDC + PKCE · API tokens |
| Scanning | Trivy (Docker CVE) |
| Container | Docker + Docker Compose |

---

## Interactive Architecture Diagram

Open **[`architecture.html`](architecture.html)** in any browser to explore the full request-flow diagram — click any action in the left panel to animate the request path through the system.

---

<div align="center">
  <img src="docs/assets/mini_logo.png" alt="Nexspence" width="60">
  <br>
  <sub>AGPLv3 License · Built with Go + React</sub>
</div>
