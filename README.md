<div align="center">
  <img src="assets/logo.png" alt="Nexspence" width="380">
  <br><br>
  <p><strong>Free, open-source universal artifact repository manager</strong></p>
  <p>A full-featured self-hosted alternative to Sonatype Nexus Repository OSS / Pro</p>
  <br>

  ![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go&logoColor=white)
  ![React](https://img.shields.io/badge/React-18-61DAFB?style=flat-square&logo=react&logoColor=black)
  ![TypeScript](https://img.shields.io/badge/TypeScript-5-3178C6?style=flat-square&logo=typescript&logoColor=white)
  ![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-4169E1?style=flat-square&logo=postgresql&logoColor=white)
  ![Docker](https://img.shields.io/badge/Docker-ready-2496ED?style=flat-square&logo=docker&logoColor=white)
  ![License](https://img.shields.io/badge/License-MIT-22c55e?style=flat-square)

</div>

---

## What is Nexspence?

Nexspence is a self-hosted artifact repository manager that supports **12 package formats**, three repository types (hosted, proxy, group), fine-grained RBAC, SSO via OIDC/LDAP, audit logging, S3-compatible storage, and a modern dark-theme web UI — all in a single binary backed by PostgreSQL.

It exposes the full **Sonatype Nexus OSS v1 REST API** at `/service/rest/v1/` for drop-in compatibility with existing CI/CD pipelines, Maven/Gradle settings, and npm/pip configurations.

---

## Status

> Active development. Core functionality is production-ready. See the [interactive architecture diagram](architecture.html) for a full picture of implemented subsystems.

Phase milestones completed: Authentication (local/LDAP/OIDC), all 12 format handlers, RBAC + content selectors, webhooks, audit log, vulnerability scanning, cleanup policies (with dry-run + retain-N-versions), per-repository blob store routing (local + S3), full backup/restore, per-repository export/import, and live migration from Nexus OSS/Pro.

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
- **OIDC / OAuth 2.0 SSO** — Keycloak, Google Workspace, Microsoft Entra ID, Okta; Authorization Code + PKCE; fragment-based JWT delivery
- **User API tokens** — `nxs_…` prefixed, SHA-256 hashed in DB; usable as Bearer or HTTP Basic password
- **RBAC** — Roles, Privileges, Content Selectors (CEL expressions for path/format scoping)

### Storage
- **Local filesystem** — default, zero configuration
- **S3-compatible** — AWS S3, MinIO, Ceph; configurable per blob store; connection test endpoint; health-checked in system dashboard
- **Per-repository blob store routing** — each repository can use a different physical blob store (local or S3)
- **Storage quotas** — per blob store and per repository; enforced on upload

### Repository Management
- **Hosted** — direct artifact upload and storage
- **Proxy** — transparent remote caching (cache-miss fetches from upstream, stores locally)
- **Group** — ordered union of hosted + proxy repos under a single URL; content served from first matching member
- **Cleanup policies** — by age, last-downloaded, format; retain-N-versions; scoped by repository; cron scheduler; **dry-run preview** before destructive run
- **Component tags** — free-form text tags on components, searchable via the API and UI

### Backup & Migration
- **Per-repository export** — streaming `.tar.gz` download (metadata + blobs); no intermediate disk usage
- **Per-repository import** — multipart upload; skip or rename conflict resolution; deduplication by SHA-256
- **Full system backup / restore** — complete database + blob export
- **Live migration from Nexus** — import repositories, users, roles, cleanup policies, and all artifacts from a running Nexus OSS/Pro instance via its REST API

### Developer Experience
- **Nexus OSS v1 REST API** — `/service/rest/v1/` compatible; drop-in replacement without reconfiguring clients
- **Full-text search** — PostgreSQL tsvector across components and assets
- **Browse UI** — tree view for raw and Docker repositories; file details panel with download, copy-link, and usage examples
- **Audit log** — every API action logged; filterable by date/user/path; NDJSON streaming export; 90-day retention with automatic partition rotation
- **Webhooks** — `artifact.published`, `artifact.deleted`, `repo.created` events; HMAC-SHA256 signatures; async delivery with test endpoint
- **Vulnerability scanning** — Trivy integration for Docker images; CVE results cached in component metadata
- **Dark glassmorphism UI** — sidebar collapse/expand (persisted to localStorage); tabbed admin pages; wizard-style create flows; transfer-list role assignment

---

## Screenshots

> _Screenshots of the running application. Open `architecture.html` in a browser for an interactive request-flow diagram._

### Dashboard & Repositories

<table>
  <tr>
    <td><img src="assets/screenshots/repositories.PNG" alt="Repositories page" width="480"></td>
    <td><img src="assets/screenshots/browse.PNG" alt="Browse" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Repositories list</em></td>
    <td align="center"><em>Browse tree view</em></td>
  </tr>
</table>

### Admin & Security

<table>
  <tr>
    <td><img src="assets/screenshots/admin_blobstores.PNG" alt="Blob Stores" width="480"></td>
    <td><img src="assets/screenshots/security_roles.PNG" alt="Roles & RBAC" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Blob stores — S3 + local with connection test</em></td>
    <td align="center"><em>Roles, privileges, content selectors</em></td>
  </tr>
</table>

### Cleanup & Search

<table>
  <tr>
    <td><img src="assets/screenshots/cleanup.PNG" alt="Cleanup policies" width="480"></td>
    <td><img src="assets/screenshots/search.PNG" alt="Search" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Cleanup policies with dry-run preview</em></td>
    <td align="center"><em>Full-text component search</em></td>
  </tr>
</table>

---

## Interactive Architecture Diagram

Open **[`architecture.html`](architecture.html)** in any browser to explore the full request-flow diagram:

- Click any action in the left panel to animate the request path through the system
- Toggle services (PostgreSQL, BlobStore, LDAP, S3, etc.) to see failure scenarios
- Hover nodes for implementation details

---

## Quick Start

```bash
# Clone, configure, and run the full stack
git clone https://github.com/skensell201/nexspence
cd nexspence

# (Optional) edit config.yaml — set admin password, DB credentials, storage backend
cp config.yaml config.local.yaml

# Start PostgreSQL + Nexspence + frontend dev server
docker compose up --build
```

The server auto-migrates the database and bootstraps the admin account on first start.

| Service | Default URL |
|---------|-------------|
| Web UI | http://localhost:3000 |
| API | http://localhost:8081 |
| Default admin | `admin` / `admin123` |

### Point your tools at Nexspence

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
docker pull localhost:8081/my-image:latest

# Go
GOPROXY=http://localhost:8081/repository/go-proxy/,direct go get github.com/some/pkg

# Helm
helm repo add nexspence http://localhost:8081/repository/helm-hosted/
```

---

## Configuration Overview

`config.yaml` controls every aspect of the server:

```yaml
server:
  port: 8081

database:
  dsn: "postgres://nexspence:nexspence@localhost:5432/nexspence"

storage:
  default_path: "./data/blobs"   # local blob store root

jwt:
  secret: "change-me"
  expiry: 24h

bootstrap:
  admin_username: "admin"
  admin_password: "admin123"     # override via NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD

# Optional: LDAP
ldap:
  enabled: false
  url: "ldap://dc.example.com:389"
  bind_dn: "cn=svc,dc=example,dc=com"
  bind_password: "secret"
  user_base_dn: "ou=Users,dc=example,dc=com"
  admin_group: "cn=nexspence-admins,ou=Groups,dc=example,dc=com"

# Optional: OIDC SSO
oidc:
  enabled: false
  provider_url: "https://accounts.google.com"
  client_id: "..."
  client_secret: "..."
  provisioning: "jit"            # jit | allowlist | manual
```

---

## API Compatibility

Nexspence exposes two API surfaces:

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
| Database | PostgreSQL 15+ (pgx, goose migrations) |
| Storage | Local filesystem · S3-compatible (AWS S3, MinIO, Ceph) |
| Auth | JWT + bcrypt · LDAP/AD · OIDC + PKCE · API tokens |
| Scanning | Trivy (Docker CVE) |
| Container | Docker + Docker Compose |

---

<div align="center">
  <img src="assets/mini_logo.png" alt="Nexspence" width="60">
  <br>
  <sub>AGPLv3 License · Built with Go + React</sub>
</div>
