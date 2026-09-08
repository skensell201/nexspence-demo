-- ============================================================
-- Nexspence — PostgreSQL 16 Database Schema
-- Migration tool: goose
-- ============================================================

-- ── Extensions ──────────────────────────────────────────────
CREATE EXTENSION IF NOT EXISTS "pgcrypto";   -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "pg_trgm";    -- trigram search index

-- ============================================================
-- BLOB STORES
-- Physical or remote storage backends for artifact data
-- ============================================================
CREATE TABLE blob_stores (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    type        TEXT NOT NULL CHECK (type IN ('local', 's3')),
    -- local: {"path": "/data/blobs"}
    -- s3:    {"bucket":"...", "region":"...", "endpoint":"...", "access_key":"...", "secret_key":"..."}
    config      JSONB NOT NULL DEFAULT '{}',
    quota_bytes BIGINT,                      -- NULL = unlimited
    used_bytes  BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- REPOSITORIES
-- ============================================================
CREATE TABLE repositories (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    format          TEXT NOT NULL CHECK (format IN (
                        'maven2', 'npm', 'docker', 'oci', 'pypi',
                        'go', 'nuget', 'helm', 'raw', 'apt', 'yum', 'cargo', 'conan', 'conda', 'terraform',
                        'rubygems', 'cran'
                    )),
    type            TEXT NOT NULL CHECK (type IN ('hosted', 'proxy', 'group')),
    blob_store_id   UUID REFERENCES blob_stores(id),
    online          BOOLEAN NOT NULL DEFAULT TRUE,

    -- Format-specific settings stored as JSONB
    -- maven2/hosted: {"version_policy":"release|snapshot|mixed", "write_policy":"allow|deny|allow_once"}
    -- maven2/proxy:  {"remote_url":"...", "negative_cache_ttl":1440}
    -- docker/hosted: {"http_port":5000, "https_port":5001, "v1_enabled":false, "force_basic_auth":true}
    -- docker/proxy:  {"remote_url":"https://registry-1.docker.io", "index_type":"hub"}
    -- npm/proxy:     {"remote_url":"https://registry.npmjs.org"}
    -- pypi/proxy:    {"remote_url":"https://pypi.org"}
    -- go/proxy:      {"remote_url":"https://proxy.golang.org", "no_proxy":""}
    -- group:         {"member_names":["repo-a","repo-b"]}  (ordered)
    format_config   JSONB NOT NULL DEFAULT '{}',

    -- HTTP options
    http_config     JSONB NOT NULL DEFAULT '{}',
    -- {"auth_type":"none|username|bearer|ntlm", "username":"", "password_enc":"", "bearer_token_enc":""}

    -- Proxy cache config
    proxy_config    JSONB NOT NULL DEFAULT '{}',
    -- {"remote_url":"...", "content_max_age":1440, "metadata_max_age":1440,
    --  "negative_cache_enabled":true, "negative_cache_ttl":1440,
    --  "blocked":false, "auto_block":true}

    -- Cleanup: linked policy IDs
    cleanup_policy_ids UUID[] NOT NULL DEFAULT '{}',

    -- Storage quota override (overrides blob store quota)
    quota_bytes     BIGINT,

    description     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_repositories_format ON repositories(format);
CREATE INDEX idx_repositories_type ON repositories(type);

-- ============================================================
-- COMPONENTS
-- Logical unit: group + name + version (e.g. a Maven GAV)
-- ============================================================
CREATE TABLE components (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_id   UUID NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    format          TEXT NOT NULL,

    -- Maven: group=groupId, name=artifactId, version=version
    -- npm:   group=scope (@org), name=package, version=version
    -- docker: group='', name=image, version=tag
    -- pypi:  group='', name=package, version=version
    -- go:    group=module_path, name='', version=version
    group_id        TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL,
    version         TEXT NOT NULL,

    -- Normalised version for sorting (semver-ish)
    version_sort    TEXT NOT NULL DEFAULT '',

    -- Format-specific extras
    -- maven2: {"packaging":"jar", "classifier":"", "base_version":"1.0.0-SNAPSHOT"}
    -- docker: {"digest":"sha256:...", "media_type":"..."}
    extra           JSONB NOT NULL DEFAULT '{}',

    last_downloaded TIMESTAMPTZ,
    download_count  BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (repository_id, format, group_id, name, version)
);

CREATE INDEX idx_components_repo ON components(repository_id);
CREATE INDEX idx_components_name ON components(name);
CREATE INDEX idx_components_version ON components(version);
CREATE INDEX idx_components_last_downloaded ON components(last_downloaded);
-- Full-text search
CREATE INDEX idx_components_fts ON components
    USING gin(to_tsvector('english', group_id || ' ' || name || ' ' || version));
-- Trigram for partial name search
CREATE INDEX idx_components_name_trgm ON components USING gin(name gin_trgm_ops);
CREATE INDEX idx_components_group_trgm ON components USING gin(group_id gin_trgm_ops);

-- ============================================================
-- ASSETS
-- Physical files that belong to a component
-- ============================================================
CREATE TABLE assets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    component_id    UUID NOT NULL REFERENCES components(id) ON DELETE CASCADE,
    repository_id   UUID NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,

    -- Path relative to repository root
    -- maven2: "com/google/guava/guava/32.1.3-jre/guava-32.1.3-jre.jar"
    -- npm:    "react/-/react-18.2.0.tgz"
    -- docker: "v2/library/nginx/blobs/sha256:abc123..."
    path            TEXT NOT NULL,

    -- BlobStore reference
    blob_store_id   UUID NOT NULL REFERENCES blob_stores(id),
    blob_key        TEXT NOT NULL,      -- key within the blob store

    -- Content metadata
    size_bytes      BIGINT NOT NULL DEFAULT 0,
    content_type    TEXT NOT NULL DEFAULT 'application/octet-stream',

    -- Checksums
    sha1            TEXT,
    sha256          TEXT,
    sha512          TEXT,
    md5             TEXT,               -- legacy Maven support

    -- Provenance
    uploader_id     UUID,               -- NULL for proxy/cached
    last_modified   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_downloaded TIMESTAMPTZ,
    download_count  BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (repository_id, path)
);

CREATE INDEX idx_assets_component ON assets(component_id);
CREATE INDEX idx_assets_repo_path ON assets(repository_id, path);
CREATE INDEX idx_assets_blob_store ON assets(blob_store_id);
CREATE INDEX idx_assets_sha256 ON assets(sha256) WHERE sha256 IS NOT NULL;
CREATE INDEX idx_assets_last_downloaded ON assets(last_downloaded);

-- ============================================================
-- USERS
-- ============================================================
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT NOT NULL UNIQUE,
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT,               -- NULL for LDAP-only users
    first_name      TEXT NOT NULL DEFAULT '',
    last_name       TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'disabled')),
    source          TEXT NOT NULL DEFAULT 'local'
                        CHECK (source IN ('local', 'ldap', 'saml')),
    -- External identity (LDAP DN, SAML NameID)
    external_id     TEXT,
    -- Set for an account whose password was chosen for it rather than by it —
    -- a migrated Nexus user given a random temporary one. Cleared on the next
    -- password change.
    must_reset_password BOOLEAN NOT NULL DEFAULT FALSE,
    last_login      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_status ON users(status);

-- ============================================================
-- ROLES
-- ============================================================
CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'local',
    -- Nexus built-in role IDs: nx-admin, nx-anonymous, nx-developer, etc.
    builtin     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Role hierarchy (roles can contain other roles)
CREATE TABLE role_roles (
    parent_role_id  UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    child_role_id   UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (parent_role_id, child_role_id),
    CHECK (parent_role_id != child_role_id)
);

-- ============================================================
-- PRIVILEGES
-- Fine-grained permissions
-- ============================================================
CREATE TABLE privileges (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    type            TEXT NOT NULL CHECK (type IN (
                        'wildcard',       -- nx:*
                        'repository-view',  -- read artifacts
                        'repository-admin', -- manage repo settings
                        'application',    -- API access
                        'script'          -- scripting (disabled by default)
                    )),
    -- wildcard:           {"pattern": "nexus:*:*"}
    -- repository-view:    {"format":"maven2", "repository":"*", "actions":["read","browse","add","edit","delete","run"]}
    -- application:        {"domain":"users", "actions":["read","create","update","delete"]}
    attrs           JSONB NOT NULL DEFAULT '{}',
    builtin         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Role ↔ Privilege mapping
CREATE TABLE role_privileges (
    role_id         UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    privilege_id    UUID NOT NULL REFERENCES privileges(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, privilege_id)
);

-- User ↔ Role mapping
CREATE TABLE user_roles (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id         UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

-- ============================================================
-- API / USER TOKENS
-- ============================================================
CREATE TABLE user_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    token_hash      TEXT NOT NULL UNIQUE,   -- bcrypt of the raw token
    -- Permissions snapshot at creation time (optional scoping)
    scopes          TEXT[] NOT NULL DEFAULT '{}',
    last_used       TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,            -- NULL = never
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_tokens_user ON user_tokens(user_id);
CREATE INDEX idx_user_tokens_hash ON user_tokens(token_hash);

-- ============================================================
-- CLEANUP POLICIES
-- ============================================================
CREATE TABLE cleanup_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    format          TEXT NOT NULL DEFAULT '*', -- '*' = all formats
    -- Criteria (all specified criteria are ANDed):
    -- {"last_downloaded_days": 30, "last_blob_updated_days": null,
    --  "asset_regex": null, "retain_versions": null,
    --  "release_type": "releases|prereleases|*"}
    criteria        JSONB NOT NULL DEFAULT '{}',
    -- Cron expression for automatic runs (Nexspence extension)
    -- Nexus only runs nightly; we allow custom schedule
    schedule_cron   TEXT NOT NULL DEFAULT '0 2 * * *',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    dry_run         BOOLEAN NOT NULL DEFAULT FALSE,
    retain_n_versions INT NOT NULL DEFAULT 0,  -- keep N newest versions per (group_id, name)
    last_run_at     TIMESTAMPTZ,
    last_run_freed  BIGINT,             -- bytes freed last run
    last_run_count  INT,                -- artifacts deleted last run
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- SCHEDULED TASKS
-- ============================================================
CREATE TABLE scheduled_tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    type            TEXT NOT NULL CHECK (type IN (
                        'cleanup.run',
                        'blobstore.compact',
                        'blobstore.rebuild-index',
                        'repository.invalidate-cache',
                        'repository.rebuild-metadata',
                        'search.reindex',
                        'security.purge-api-keys'
                    )),
    schedule_cron   TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    params          JSONB NOT NULL DEFAULT '{}',
    last_run_at     TIMESTAMPTZ,
    last_run_status TEXT,               -- 'ok', 'error', 'running'
    last_run_msg    TEXT,
    next_run_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- AUDIT LOG
-- ============================================================
CREATE TABLE audit_events (
    id              BIGSERIAL PRIMARY KEY,
    event_time      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    username        TEXT NOT NULL DEFAULT 'anonymous',
    remote_ip       INET,
    user_agent      TEXT,

    -- Category: 'security', 'repository', 'component', 'system', 'api'
    domain          TEXT NOT NULL,
    -- Action: 'create', 'update', 'delete', 'read', 'login', 'logout', etc.
    action          TEXT NOT NULL,
    -- Target entity type and ID
    entity_type     TEXT,
    entity_id       TEXT,
    entity_name     TEXT,

    -- Full change context
    context         JSONB NOT NULL DEFAULT '{}',

    -- Outcome
    result          TEXT NOT NULL DEFAULT 'success'
                        CHECK (result IN ('success', 'failure', 'denied'))
) PARTITION BY RANGE (event_time);

-- Monthly partitions (created by scheduled task or migration)
CREATE TABLE audit_events_2026_04 PARTITION OF audit_events
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE audit_events_2026_05 PARTITION OF audit_events
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');

CREATE INDEX idx_audit_time ON audit_events(event_time DESC);
CREATE INDEX idx_audit_user ON audit_events(user_id, event_time DESC);
CREATE INDEX idx_audit_domain ON audit_events(domain, action, event_time DESC);

-- ============================================================
-- LDAP CONFIGURATION
-- ============================================================
CREATE TABLE ldap_servers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    protocol        TEXT NOT NULL DEFAULT 'ldap' CHECK (protocol IN ('ldap', 'ldaps')),
    hostname        TEXT NOT NULL,
    port            INT NOT NULL DEFAULT 389,
    search_base     TEXT NOT NULL,
    auth_scheme     TEXT NOT NULL DEFAULT 'simple'
                        CHECK (auth_scheme IN ('none', 'simple', 'digest_md5', 'cram_md5')),
    bind_dn         TEXT,
    bind_password   TEXT,               -- encrypted at rest
    -- User mapping
    user_base_dn    TEXT,
    user_filter     TEXT NOT NULL DEFAULT '(objectClass=inetOrgPerson)',
    user_id_attr    TEXT NOT NULL DEFAULT 'uid',
    user_name_attr  TEXT NOT NULL DEFAULT 'cn',
    user_email_attr TEXT NOT NULL DEFAULT 'mail',
    -- Group mapping
    group_base_dn   TEXT,
    group_filter    TEXT NOT NULL DEFAULT '(objectClass=groupOfUniqueNames)',
    group_member_attr TEXT NOT NULL DEFAULT 'uniqueMember',
    group_name_attr TEXT NOT NULL DEFAULT 'cn',
    -- Nexspence maps LDAP groups → Nexspence roles by name
    connection_timeout_sec INT NOT NULL DEFAULT 30,
    retry_delay_sec INT NOT NULL DEFAULT 300,
    max_retries     INT NOT NULL DEFAULT 3,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    order_num       INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- ROUTING RULES
-- (Nexus Pro feature, we include it free)
-- Route upstream proxy requests based on path matchers
-- ============================================================
CREATE TABLE routing_rules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    mode        TEXT NOT NULL DEFAULT 'BLOCK'
                    CHECK (mode IN ('ALLOW', 'BLOCK')),
    matchers    TEXT[] NOT NULL DEFAULT '{}',   -- regex patterns
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Assign routing rule to repository (proxy)
ALTER TABLE repositories ADD COLUMN routing_rule_id UUID REFERENCES routing_rules(id) ON DELETE SET NULL;

-- ============================================================
-- SYSTEM CONFIG
-- Key-value store for runtime settings
-- ============================================================
CREATE TABLE system_config (
    key         TEXT PRIMARY KEY,
    value       JSONB NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by  UUID REFERENCES users(id) ON DELETE SET NULL
);

-- Default system values (inserted by initial migration)
INSERT INTO system_config (key, value, description) VALUES
    ('baseUrl',                 '"http://localhost:8081"',  'Base URL for HTTP links'),
    ('anonymousAccessEnabled',  'true',                     'Allow unauthenticated read access'),
    ('anonymousUserId',         '"anonymous"',              'Username for anonymous sessions'),
    ('activeRealms',            '["local","ldap"]',         'Active authentication realms'),
    ('httpRequestTimeout',      '1800',                     'HTTP request timeout seconds'),
    ('httpConnectionTimeout',   '60',                       'HTTP connection timeout seconds'),
    ('maxHttpConnections',      '20',                       'Max upstream HTTP connections per route'),
    ('smtpEnabled',             'false',                    'SMTP email notifications'),
    ('smtpConfig',              '{}',                       'SMTP server configuration');

-- ============================================================
-- MIGRATION JOBS
-- Track Nexus → Nexspence migration progress
-- ============================================================
CREATE TABLE migration_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_url      TEXT NOT NULL,
    source_user     TEXT NOT NULL,
    -- Sealed with the instance encryption key. Kept because a migration must
    -- survive a restart: the runner re-attaches to jobs left running, long
    -- after the request that supplied the credential is gone.
    source_password TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','running','paused','done','error')),
    -- Which parts to migrate. migrate_policies is the older, coarser switch and
    -- is the default the three security scopes fall back to.
    migrate_repos   BOOLEAN NOT NULL DEFAULT TRUE,
    migrate_users   BOOLEAN NOT NULL DEFAULT TRUE,
    migrate_blobs   BOOLEAN NOT NULL DEFAULT TRUE,
    migrate_policies BOOLEAN NOT NULL DEFAULT TRUE,
    migrate_privileges    BOOLEAN NOT NULL DEFAULT TRUE,
    migrate_roles         BOOLEAN NOT NULL DEFAULT TRUE,
    migrate_routing_rules BOOLEAN NOT NULL DEFAULT TRUE,
    -- Progress tracking
    total_repos     INT NOT NULL DEFAULT 0,
    done_repos      INT NOT NULL DEFAULT 0,
    total_assets    BIGINT NOT NULL DEFAULT 0,
    done_assets     BIGINT NOT NULL DEFAULT 0,
    total_bytes     BIGINT NOT NULL DEFAULT 0,
    done_bytes      BIGINT NOT NULL DEFAULT 0,
    error_count     INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    -- Continuation support: store last page token per repo
    cursor          JSONB NOT NULL DEFAULT '{}',
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Read on every startup, to re-attach to jobs a restart interrupted.
CREATE INDEX idx_migration_jobs_status ON migration_jobs(status);

-- ============================================================
-- SEED DATA — built-in roles and privileges
-- ============================================================

-- Built-in roles (mirrors Nexus defaults)
INSERT INTO roles (name, description, builtin) VALUES
    ('nx-admin',          'Administrator with all privileges', TRUE),
    ('nx-anonymous',      'Anonymous access role',             TRUE),
    ('nx-developer',      'Developer - read/write access',     TRUE),
    ('nx-deployer',       'Deployer - upload artifacts',       TRUE),
    ('nx-replication',    'Replication service account',       TRUE);

-- Default admin user (password: admin123 — must be changed on first login)
-- bcrypt hash of 'admin123'
INSERT INTO users (username, email, password_hash, first_name, last_name, status)
VALUES (
    'admin',
    'admin@example.com',
    '$2a$12$LQv3c1yqBWVHxkd0LHAkCOYz6TtxMQJqhN8/LewdBPj/VcSAg/ROS',
    'Admin',
    '',
    'active'
);

-- Anonymous user (no password)
INSERT INTO users (username, email, first_name, last_name, status)
VALUES ('anonymous', 'anonymous@system', 'Anonymous', '', 'active');
