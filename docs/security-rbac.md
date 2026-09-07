# Nexspence — Security: Content Selectors, Privileges, Roles

## RBAC Model

```
Content Selector  ──►  Privilege  ──►  Role  ──►  User
  (CEL filter)       (permission)   (permission set)
```

1. **Content Selector** — a CEL expression that describes *which* artifacts the rule applies to (by format, path, or repository name).
2. **Privilege** — a `repository-content-selector` permission always bound to a Content Selector. This is the **only way to create privileges** in Nexspence (unlike Nexus, which has wildcard/application/script types).
3. **Role** — a set of privileges assigned to users.

---

## Privilege Types

Nexspence supports a single privilege type for user-created permissions:

| Type | Description |
|------|-------------|
| `repository-content-selector` | Privilege whose scope is defined by a Content Selector CEL expression |

Built-in privileges may have legacy types (`wildcard`, `repository-view`, etc.) but new privileges of those types cannot be created through the UI.

> **Why?** Nexspence simplifies the access model: instead of two steps (create privilege + attach Content Selector), there is one step — choose a Content Selector when creating the privilege.

---

## CEL Expressions for Content Selectors

Content Selectors use [CEL (Common Expression Language)](https://github.com/google/cel-spec).

### Available variables

| Variable | Type | Description |
|----------|------|-------------|
| `format` | string | Repository format: `"maven2"`, `"npm"`, `"docker"`, `"pypi"`, `"raw"`, `"helm"`, `"cargo"`, `"go"`, `"nuget"`, `"apt"`, `"yum"`, `"conan"`, `"conda"`, `"terraform"` |
| `path` | string | Artifact path (starts with `/`) |
| `repository` | string | Repository name |

### Expression examples

```cel
# Maven only
format == "maven2"

# npm only
format == "npm"

# Docker only
format == "docker"

# PyPI or npm (monorepo style)
format == "pypi" || format == "npm"

# Specific repository
repository == "releases"

# Maven from a specific repository
format == "maven2" && repository == "maven-releases"

# Maven SNAPSHOT artifacts only
format == "maven2" && path.contains("-SNAPSHOT")

# Helm charts in the production repository
format == "helm" && repository == "helm-prod"

# Maven artifacts under org.example group
format == "maven2" && path.startsWith("/org/example/")

# Docker or OCI images under a specific team namespace.
# Registry paths are normalised before matching: the /v2/ prefix is stripped and
# the path is cut at the /manifests/, /blobs/ or /tags/ keyword, so a request for
# /v2/myteam/app/manifests/1.0 is matched as /myteam/app/. Write the selector
# against that shape — a selector starting with /v2/ never matches.
format == "docker" && path.startsWith("/myteam/")

# All artifacts (no restriction)
true

# Maven release artifacts only (no SNAPSHOTs, no beta)
format == "maven2" && !path.contains("SNAPSHOT") && !path.contains("-beta")

# Conda packages for a specific channel
format == "conda" && repository == "conda-hosted"

# Terraform providers only
format == "terraform"
```

---

## Step-by-Step Setup

### Step 1 — Create a Content Selector

**UI:** Security → Content Selectors → New Content Selector

**API:**
```http
POST /service/rest/v1/security/content-selectors
Content-Type: application/json

{
  "name": "maven-releases-only",
  "description": "Maven release artifacts (no snapshots)",
  "expression": "format == \"maven2\" && !path.contains(\"SNAPSHOT\")"
}
```

The response contains `id` — needed in Step 2.

---

### Step 2 — Create a Privilege

**UI:** Security → Privileges → New Privilege → select Content Selector from the dropdown.
The CEL expression of the selected selector is shown immediately below the dropdown.

**API:**
```http
POST /service/rest/v1/security/privileges
Content-Type: application/json

{
  "name": "view-maven-releases",
  "description": "Read Maven release artifacts",
  "type": "repository-content-selector",
  "contentSelectorId": "<selector-uuid>",
  "attrs": { "actions": ["browse", "read"] }
}
```

The response contains `id` — needed for Step 3.

---

### Step 3 — Create a Role and assign the Privilege

**UI:** Security → Roles → Edit → add privileges via checkboxes.

**API — create role:**
```http
POST /service/rest/v1/security/roles
Content-Type: application/json

{
  "name": "maven-reader",
  "description": "Read-only access to Maven releases",
  "privileges": ["<privilege-id>"]
}
```

---

### Step 4 — Assign the Role to a User

**UI:** Users & Roles → Users → Assign Roles button

**API:**
```http
PUT /service/rest/v1/security/users/{userId}/roles
Content-Type: application/json

{
  "roleIds": ["<role-id>"]
}
```

---

## Common Scenarios by Format

### Maven — Read-only for a specific repository

```json
// 1. Content Selector
{ "name": "maven-releases", "expression": "format == \"maven2\" && repository == \"maven-releases\"" }

// 2. Privilege
{ "name": "nx-maven-releases-read", "type": "repository-content-selector",
  "contentSelectorId": "<uuid>", "attrs": { "actions": ["browse", "read"] } }

// 3. Role
{ "name": "maven-developer", "description": "Read Maven releases" }
```

---

### npm — Package publishing (CI/CD)

```json
// 1. Content Selector
{ "name": "npm-all", "expression": "format == \"npm\"" }

// 2. Privilege
{ "name": "nx-npm-all-write", "type": "repository-content-selector",
  "contentSelectorId": "<uuid>", "attrs": { "actions": ["browse", "read", "write"] } }

// 3. Role
{ "name": "npm-publisher", "description": "Publish npm packages" }
```

---

### Docker — Read-only for a team namespace

```json
// 1. Content Selector
{ "name": "docker-myteam", "expression": "format == \"docker\" && path.startsWith(\"/v2/myteam/\")" }

// 2. Privilege
{ "name": "nx-docker-myteam-read", "type": "repository-content-selector",
  "contentSelectorId": "<uuid>", "attrs": { "actions": ["browse", "read"] } }

// 3. Role
{ "name": "docker-consumer-myteam" }
```

---

### PyPI — Upload access for poetry/twine

```json
// 1. Content Selector
{ "name": "pypi-hosted", "expression": "format == \"pypi\" && repository == \"pypi-hosted\"" }

// 2. Privilege
{ "name": "nx-pypi-write", "type": "repository-content-selector",
  "contentSelectorId": "<uuid>", "attrs": { "actions": ["browse", "read", "write"] } }

// 3. Role
{ "name": "pypi-publisher" }
```

---

### Helm — Read-only for deployments

```json
// 1. Content Selector
{ "name": "helm-all", "expression": "format == \"helm\"" }

// 2. Privilege
{ "name": "nx-helm-read", "type": "repository-content-selector",
  "contentSelectorId": "<uuid>", "attrs": { "actions": ["browse", "read"] } }

// 3. Role
{ "name": "helm-deployer" }
```

---

### Conda — Channel read access

```json
// 1. Content Selector
{ "name": "conda-all", "expression": "format == \"conda\"" }

// 2. Privilege
{ "name": "nx-conda-read", "type": "repository-content-selector",
  "contentSelectorId": "<uuid>", "attrs": { "actions": ["browse", "read"] } }

// 3. Role
{ "name": "conda-consumer" }
```

---

### Terraform — Provider registry read access

```json
// 1. Content Selector
{ "name": "terraform-all", "expression": "format == \"terraform\"" }

// 2. Privilege
{ "name": "nx-terraform-read", "type": "repository-content-selector",
  "contentSelectorId": "<uuid>", "attrs": { "actions": ["browse", "read"] } }

// 3. Role
{ "name": "terraform-consumer" }
```

---

### Universal read-only role (all formats)

```json
// 1. Content Selector
{ "name": "all-artifacts", "expression": "true" }

// 2. Privilege
{ "name": "nx-all-read", "type": "repository-content-selector",
  "contentSelectorId": "<uuid>", "attrs": { "actions": ["browse", "read"] } }

// 3. Role
{ "name": "anonymous-reader", "description": "Read all public repos" }
```

---

### Maven — Block SNAPSHOT artifacts

```json
// 1. Content Selector (releases only, no SNAPSHOT)
{ "name": "maven-releases-only", "expression": "format == \"maven2\" && !path.contains(\"SNAPSHOT\")" }

// 2. Privilege
{ "name": "nx-maven-no-snapshot", "type": "repository-content-selector",
  "contentSelectorId": "<uuid>", "attrs": { "actions": ["browse", "read"] } }

// 3. Role
{ "name": "maven-release-reader" }
```

---

## Built-in Roles

| Role | Description |
|------|-------------|
| `nx-admin` | Full access to everything |
| `nx-anonymous` | Anonymous user (read-only public repos) |
| `nx-developer` | Read + publish artifacts |

Built-in roles cannot be deleted or modified.

---

## Access Check Order

When a request is made for an artifact:

```
1. Authentication (JWT Bearer / API Token / Basic Auth)
2. User roles → list of privileges
3. For each privilege of type repository-content-selector:
   a. Fetch associated Content Selector → CEL expression
   b. Evaluate expression against variables (format, path, repository)
   c. If true → access granted
4. At least one privilege grants access → 200 OK
   No matching privilege → 403 Forbidden
```

---

## Brute-force Protection

Two independent throttles sit in front of every password check.

**Per-account failed-login backoff.** Five consecutive failures for an account
are free, so a mistyped password never costs anything. After that each further
failure doubles the wait — 1s, 2s, 4s … capped at 15 minutes — and the account's
next attempt is refused with `429` and a `Retry-After` header *before* the
password is hashed, so the attack stops paying for bcrypt. A successful login
clears the count, and a quiet account forgets it after 30 minutes.

The budget is keyed on the account name rather than the client address,
deliberately: an address is only as trustworthy as `http.trusted_proxies`, and
an attacker rotating addresses still spends the same budget. It applies at every
place credentials are accepted — `/api/v1/login`, Basic auth on `/repository/…`
and `/v2/…`, and the API middleware — so moving the guessing to another endpoint
does not buy a fresh allowance. When Redis is enabled the count is shared across
HA nodes, so spreading attempts over servers does not multiply it.

Failed credentials are logged wherever they occur, including on the paths that
fall through to anonymous access; previously those left no trace at all.

**Request rate limit.** `auth.rate_limit_enabled` (on by default) additionally
buckets requests per authenticated user, or per client address for anonymous
calls. It is coarser and complements, rather than replaces, the per-account
backoff above.

---

## Anonymous Access

Two switches must agree before an unauthenticated request is served:

| Switch | Scope | Default |
|---|---|---|
| `auth.anonymous_enabled` | whole instance | `true` |
| repository `allow_anonymous` | one repository | `false` |

Setting `auth.anonymous_enabled: false` refuses every unauthenticated read
regardless of how individual repositories are configured — repository listings,
browse trees, component and asset listings, artifact downloads, and anonymous
tokens from the Docker token endpoint. Anonymous writes are never allowed by
either switch.

### Browsing without signing in

The web UI follows the same two switches. A visitor with no session lands on
Repositories, Browse and Search rather than a login wall, and sees exactly the
repositories both switches agree on — an empty list when the instance-wide
switch is off, or when no repository sets `allow_anonymous`. The sidebar offers
a Sign in link in place of the user pill, and every other page (Users, Cleanup
Policies, System Admin, Security, Audit Log) still redirects to `/login`.

The endpoints behind those three pages accept an unauthenticated caller and let
RBAC decide: repository list and get, the browse trees, component and asset
reads, and search. A repository the caller may not see answers `404`, not
`401` or `403`, so its name stays unguessable. Everything else — writes,
deletes, `/api/v1/me`, tokens, scan results, and the whole admin surface —
still requires a token.

The UI asks for that signed-in-only data (blob-store names, repository quota,
the caller's privileges, scan results) only while a session exists, and offers
promotion and bulk selection only then. A `401` is treated as an expired
session — clearing it and returning to `/login` — only when the request
carried a token; a visitor who never had one is left on the page.

The Docker `/v2/` handshake itself is unconditional: an unauthenticated ping
always answers `401` with a `Bearer` challenge pointing at `/v2/token`.
Clients with credentials trade them there for a user token (this is the
request `docker login` verifies); without credentials the endpoint issues an
anonymous token — carrying no identity, so per-repository RBAC still decides
every read — or refuses it entirely when `auth.anonymous_enabled` is off.

---

## LDAP External Role Mapping

On every LDAP login, Nexspence automatically syncs the user's roles from their LDAP group memberships (REPLACE semantics — existing roles are fully replaced each time).

### Mapping strategies (all three applied in order)

| Priority | Strategy | Config |
|----------|----------|--------|
| 1 | `admin_group` → `nx-admin` | `ldap.admin_group` |
| 2 | Explicit group → role mapping | `ldap.role_mappings` |
| 3 | Group name equals role name | automatic |

### Configuration

```yaml
ldap:
  enabled: true
  admin_group: "nexus-administrators"   # plain CN or full DN
  role_mappings:
    "dev-team":  "developers"   # LDAP group → Nexspence role name
    "qa-team":   "testers"
    "ops-group": "operators"
```

### Behavior

- Roles are assigned via `SetUserRoles` (REPLACE): on each login, the user's role list is fully recomputed from their current LDAP groups.
- If an LDAP group is not matched by any strategy, it is silently ignored (no new roles are created).
- `admin_group` accepts both plain CN (`"nexus-administrators"`) and full DN (`"CN=nexus-administrators,OU=Groups,DC=example,DC=com"`) — the first RDN is compared case-insensitively.
- Role sync failure does not block login (best-effort, error is logged).

---

## Troubleshooting

| Problem | Cause | Solution |
|---------|-------|----------|
| 403 when reading an artifact | No privilege whose Content Selector matches the request | Check CEL expression — verify `format`, `repository`, `path` values match |
| Content Selector has no effect | Incorrect CEL expression | `GET /service/rest/v1/security/content-selectors` — verify the `expression` field |
| Role assigned but no access | Privilege not attached to the Role | `PUT /roles/{id}/privileges` with the correct privilege IDs |
| Docker pull denied | Docker uses `/v2/...` paths | Use Content Selector with `format == "docker"` |
| "No content selectors defined" in UI | A Content Selector must exist before creating a Privilege | Security → Content Selectors → New Selector |
| Conda install fails with 403 | Channel path not matching CEL expression | Verify `format == "conda"` and correct `repository` or `path` in selector |
| Terraform init fails with 403 | Provider source path not matching selector | Use `format == "terraform"` or narrow by `repository` |
