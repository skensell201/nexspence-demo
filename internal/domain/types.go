// Package domain contains all core business types shared across layers.
// No external dependencies — only stdlib.
package domain

import (
	"time"
)

// ── Repository ───────────────────────────────────────────────

// RepoFormat identifies the artifact protocol of a repository (maven2, npm, docker, ...).
type RepoFormat string

// RepoType is a repository's role: hosted, proxy, or group.
type RepoType string

// Supported repository formats and the three repository types.
const (
	FormatMaven2 RepoFormat = "maven2"
	FormatNPM    RepoFormat = "npm"
	FormatDocker RepoFormat = "docker"
	// FormatOCI is the same OCI Distribution protocol as FormatDocker, labeled
	// for charts, ORAS artifacts and signatures rather than container images.
	FormatOCI       RepoFormat = "oci"
	FormatPyPI      RepoFormat = "pypi"
	FormatGo        RepoFormat = "go"
	FormatNuGet     RepoFormat = "nuget"
	FormatHelm      RepoFormat = "helm"
	FormatRaw       RepoFormat = "raw"
	FormatApt       RepoFormat = "apt"
	FormatYum       RepoFormat = "yum"
	FormatCargo     RepoFormat = "cargo"
	FormatConan     RepoFormat = "conan"
	FormatConda     RepoFormat = "conda"
	FormatTerraform RepoFormat = "terraform"
	FormatRubyGems  RepoFormat = "rubygems"
	FormatCRAN      RepoFormat = "cran"
	FormatAlpine    RepoFormat = "alpine"

	TypeHosted RepoType = "hosted"
	TypeProxy  RepoType = "proxy"
	TypeGroup  RepoType = "group"
)

// AllFormats is every repository format the server serves, in the order the
// documentation lists them. It is the one place to update when a format is
// added — the docs site is checked against it.
var AllFormats = []RepoFormat{
	FormatMaven2,
	FormatNPM,
	FormatPyPI,
	FormatDocker,
	FormatOCI,
	FormatGo,
	FormatNuGet,
	FormatHelm,
	FormatCargo,
	FormatApt,
	FormatYum,
	FormatConan,
	FormatRaw,
	FormatConda,
	FormatTerraform,
	FormatRubyGems,
	FormatCRAN,
	FormatAlpine,
}

// IsOCIRegistry reports whether a repository of this format speaks the OCI
// Distribution protocol — the /v2/ surface with /manifests/... and /blobs/...
// paths. One handler serves both labels: "docker" for container images, "oci"
// for charts, ORAS artifacts and signatures. They differ only in presentation,
// never in protocol behavior, so every protocol-level check uses this one method.
func (f RepoFormat) IsOCIRegistry() bool {
	return f == FormatDocker || f == FormatOCI
}

// Repository is a hosted, proxy, or group artifact repository of a given format.
type Repository struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Format           RepoFormat     `json:"format"`
	Type             RepoType       `json:"type"`
	BlobStoreID      *string        `json:"blobStoreId,omitempty"`
	Online           bool           `json:"online"`
	FormatConfig     map[string]any `json:"formatConfig,omitempty"`
	HTTPConfig       map[string]any `json:"httpConfig,omitempty"`
	ProxyConfig      map[string]any `json:"proxyConfig,omitempty"`
	CleanupPolicyIDs []string       `json:"cleanupPolicyIds,omitempty"`
	QuotaBytes       *int64         `json:"quotaBytes,omitempty"`
	RoutingRuleID    *string        `json:"routingRuleId,omitempty"`
	AllowAnonymous   bool           `json:"allowAnonymous"`
	Description      string         `json:"description,omitempty"`
	URL              string         `json:"url,omitempty"` // computed
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

// GroupMemberNames returns ordered member repository names from formatConfig["member_names"].
func GroupMemberNames(r *Repository) []string {
	if r == nil || r.FormatConfig == nil {
		return nil
	}
	raw, ok := r.FormatConfig["member_names"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

// GroupWritableMember returns the explicitly configured writable member name
// from formatConfig["writable_member"], or empty string if not set (auto-detect).
func GroupWritableMember(r *Repository) string {
	if r == nil || r.FormatConfig == nil {
		return ""
	}
	v, _ := r.FormatConfig["writable_member"].(string)
	return v
}

// ── Webhook ──────────────────────────────────────────────────

// WebhookEvent names a repository event that webhooks and the realtime feed may subscribe to.
type WebhookEvent string

// Webhook event names dispatched by the artifact and repository services.
const (
	EventArtifactPublished WebhookEvent = "artifact.published"
	EventArtifactDeleted   WebhookEvent = "artifact.deleted"
	EventRepoCreated       WebhookEvent = "repo.created"
	EventRepoUpdated       WebhookEvent = "repo.updated"
	EventRepoDeleted       WebhookEvent = "repo.deleted"
	// EventProxyError is fired when a proxy repository fails to fetch from
	// upstream — useful for the SSE realtime feed; webhooks may also subscribe.
	EventProxyError WebhookEvent = "proxy.error"
)

// Webhook is a subscription that receives HTTP POST notifications on selected events.
type Webhook struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	URL       string         `json:"url"`
	Secret    string         `json:"secret,omitempty"` // HMAC-SHA256 signing key
	Events    []WebhookEvent `json:"events"`
	Active    bool           `json:"active"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

// WebhookPayload is the JSON body sent to each registered webhook URL.
type WebhookPayload struct {
	Event      WebhookEvent   `json:"event"`
	Timestamp  time.Time      `json:"timestamp"`
	Repository string         `json:"repository,omitempty"`
	Component  map[string]any `json:"component,omitempty"`
	Asset      map[string]any `json:"asset,omitempty"`
}

// WebhookDispatcher fires webhook events asynchronously.
// Implementations must be goroutine-safe.
type WebhookDispatcher interface {
	Dispatch(payload WebhookPayload)
}

// DownloadCounter records artifact downloads for debounced persistence.
type DownloadCounter interface {
	Add(assetID string)
}

// ── Routing Rule ─────────────────────────────────────────────

// RoutingRule controls which artifact paths are allowed or blocked for a repository.
// mode=ALLOW: only paths matching at least one matcher are allowed through.
// mode=BLOCK: paths matching any matcher are blocked.
type RoutingRule struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Mode        string    `json:"mode"` // "ALLOW" | "BLOCK"
	Matchers    []string  `json:"matchers"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ── Content Selector ─────────────────────────────────────────

// ContentSelector is a CEL expression that decides whether an artifact path
// is visible for a user. Attached to one or more privileges; the auth gate
// evaluates every selector attached via the caller's effective privileges
// and denies if none returns true. CEL variables exposed to the expression:
//
//	format     string  — repository format ("maven2", "docker", ...)
//	path       string  — artifact path below the repo root ("/com/acme/...")
//	repository string  — repository name
type ContentSelector struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Expression  string    `json:"expression"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ── Blob Store ───────────────────────────────────────────────

// BlobStore is a named storage backend with an optional quota: a local
// filesystem, an S3-compatible bucket, or a group that fans writes out over
// other stores.
type BlobStore struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"` // "local" | "s3" | "group"
	Config     map[string]any `json:"config,omitempty"`
	QuotaBytes *int64         `json:"quotaBytes,omitempty"`
	UsedBytes  int64          `json:"usedBytes"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
}

// ── Vulnerability scan ───────────────────────────────────────

// ScanStatus is the outcome of a vulnerability scan run.
type ScanStatus string

// Vulnerability scan outcomes.
const (
	ScanStatusOK     ScanStatus = "ok"
	ScanStatusFailed ScanStatus = "failed"
)

// ScanResult is stored in component.Extra["scan_result"] after a Trivy scan.
type ScanResult struct {
	ScannedAt time.Time    `json:"scannedAt"`
	ImageRef  string       `json:"imageRef"`
	Status    ScanStatus   `json:"status"`
	Error     string       `json:"error,omitempty"`
	Summary   ScanSummary  `json:"summary"`
	Findings  []CVEFinding `json:"findings,omitempty"`
}

// ScanSummary holds per-severity CVE counts.
//
// Malicious is not a CVSS level: it counts malicious-package reports (OSV.dev
// `MAL-…`), which carry no score and would otherwise be indistinguishable from
// a finding the scanner could not classify. It leads the order everywhere
// severities are listed.
type ScanSummary struct {
	Malicious int `json:"malicious"`
	Critical  int `json:"critical"`
	High      int `json:"high"`
	Medium    int `json:"medium"`
	Low       int `json:"low"`
	Unknown   int `json:"unknown"`
	Total     int `json:"total"`
}

// CVEFinding is a single vulnerability entry from the scanner.
type CVEFinding struct {
	ID           string `json:"id"`
	Severity     string `json:"severity"`
	PkgName      string `json:"pkgName"`
	InstalledVer string `json:"installedVersion"`
	FixedVersion string `json:"fixedVersion,omitempty"`
	Title        string `json:"title,omitempty"`
}

// ScanResultRow is a single scan run stored in the scan_results table.
type ScanResultRow struct {
	ID          string
	ComponentID string
	Scanner     string // "trivy" | "osv"
	Status      ScanStatus
	Malicious   int
	Critical    int
	High        int
	Medium      int
	Low         int
	Unknown     int
	Total       int
	ScannedAt   time.Time
	Raw         map[string]any
	Error       string
}

// SecuritySummary is an aggregate across all scan_results rows.
type SecuritySummary struct {
	Malicious    int `json:"malicious"`
	Critical     int `json:"critical"`
	High         int `json:"high"`
	Medium       int `json:"medium"`
	Low          int `json:"low"`
	Unknown      int `json:"unknown"`
	ScannedTotal int `json:"scanned_total"` // distinct components with at least one scan
}

// VulnRow is one row in the vulnerability dashboard table, joining scan_results + components + repositories.
type VulnRow struct {
	RepoName    string    `json:"repoName"`
	Format      string    `json:"format"`
	ComponentID string    `json:"componentId"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Malicious   int       `json:"malicious"`
	Critical    int       `json:"critical"`
	High        int       `json:"high"`
	Medium      int       `json:"medium"`
	Low         int       `json:"low"`
	Unknown     int       `json:"unknown"`
	ScannedAt   time.Time `json:"scannedAt"`
}

// VulnFilter controls which rows ListVulnerabilities returns.
type VulnFilter struct {
	Repo     string // filter by repository name; empty = all
	Severity string // minimum severity: "CRITICAL" | "HIGH" | "MEDIUM" | "LOW"; empty = all
	Format   string // filter by format; empty = all
	Limit    int
	Offset   int
}

// ── Component ────────────────────────────────────────────────

// Component is a logical artifact (group/name/version) owning one or more Assets.
type Component struct {
	ID             string         `json:"id"`
	RepositoryID   string         `json:"repositoryId"`
	Repository     string         `json:"repository"` // name
	Format         string         `json:"format"`
	Group          string         `json:"group"`
	Name           string         `json:"name"`
	Version        string         `json:"version"`
	Tags           []string       `json:"tags"`
	Extra          map[string]any `json:"extra,omitempty"`
	LastDownloaded *time.Time     `json:"lastDownloaded,omitempty"`
	DownloadCount  int64          `json:"downloadCount"`
	Assets         []Asset        `json:"assets,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
}

// ── Asset ────────────────────────────────────────────────────

// Asset is a single stored file belonging to a Component, backed by a blob in a BlobStore.
type Asset struct {
	ID           string `json:"id"`
	ComponentID  string `json:"componentId"`
	RepositoryID string `json:"repositoryId"`
	Repository   string `json:"repository"` // name
	Path         string `json:"path"`
	BlobStoreID  string `json:"blobStoreId"`
	BlobKey      string `json:"blobKey,omitempty"` // storage reference (admin/browse)
	SizeBytes    int64  `json:"fileSize"`
	ContentType  string `json:"contentType"`
	SHA1         string `json:"sha1,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	MD5          string `json:"md5,omitempty"`
	DownloadURL  string `json:"downloadUrl,omitempty"` // computed
	// UploaderID is the users.id UUID when the asset was published (hosted push).
	UploaderID string `json:"uploaderId,omitempty"`
	// UploaderUsername is joined for API/browse (Nexus "Uploader" column).
	UploaderUsername string     `json:"uploader,omitempty"`
	LastModified     time.Time  `json:"lastModified"`
	LastDownloaded   *time.Time `json:"lastDownloaded,omitempty"`
	DownloadCount    int64      `json:"downloadCount"`
	CreatedAt        time.Time  `json:"createdAt"`
}

// ── User ─────────────────────────────────────────────────────

// UserStatus indicates whether a user account is active or disabled.
type UserStatus string

// UserSource identifies where a user account originated (local, ldap, oidc, saml).
type UserSource string

// User account statuses and identity sources.
const (
	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"

	UserSourceLocal UserSource = "local"
	UserSourceLDAP  UserSource = "ldap"
	UserSourceOIDC  UserSource = "oidc"
	UserSourceSAML  UserSource = "saml"
)

// User is an account that can authenticate and is granted access via roles.
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"userId"` // Nexus API uses "userId" as the identifier field
	Email        string     `json:"emailAddress"`
	PasswordHash string     `json:"-"`
	FirstName    string     `json:"firstName"`
	LastName     string     `json:"lastName"`
	Status       UserStatus `json:"status"`
	Source       UserSource `json:"source"`
	ExternalID   string     `json:"-"`
	Roles        []string   `json:"roles"` // role names
	// MustResetPassword marks an account whose password was set for it rather
	// than by it — a migrated Nexus user given a random temporary password.
	// The account logs in normally; the flag drives the prompt to change it.
	MustResetPassword bool       `json:"mustResetPassword,omitempty"`
	LastLogin         *time.Time `json:"lastLogin,omitempty"`
	// TokensValidAfter is the cutoff for JWT validity: a token whose `iat` is
	// before this instant is rejected. Bumped on disable, password change, and
	// role change so previously-issued JWTs are revoked. Zero value (distant
	// past) means all tokens are accepted.
	TokensValidAfter time.Time `json:"-"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// ── API Token ────────────────────────────────────────────────

// UserToken is a service-account API token that authenticates a specific user.
// The plaintext token value is shown to the operator exactly once at creation
// time; only the hash is persisted.
type UserToken struct {
	ID        string     `json:"id"`
	UserID    string     `json:"userId"`
	Username  string     `json:"username,omitempty"` // joined from users for list responses
	Name      string     `json:"name"`
	TokenHash string     `json:"-"`
	Scopes    []string   `json:"scopes,omitempty"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	// Token is the plaintext token value — only populated on the response of
	// a successful Create call; never loaded from the database.
	Token string `json:"token,omitempty"`
}

// ── Role ─────────────────────────────────────────────────────

// Role is a named bundle of privileges (and optionally nested roles) assignable to users.
type Role struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Privileges  []string  `json:"privileges"`
	Roles       []string  `json:"roles"` // nested roles
	ReadOnly    bool      `json:"readOnly"`
	Source      string    `json:"source,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ── Privilege ─────────────────────────────────────────────────

// PrivilegeType maps to the CHECK constraint in the privileges table.
type PrivilegeType string

// Privilege types matching the privileges-table CHECK constraint.
const (
	PrivilegeTypeWildcard                  PrivilegeType = "wildcard"
	PrivilegeTypeRepositoryView            PrivilegeType = "repository-view"
	PrivilegeTypeRepositoryAdmin           PrivilegeType = "repository-admin"
	PrivilegeTypeApplication               PrivilegeType = "application"
	PrivilegeTypeScript                    PrivilegeType = "script"
	PrivilegeTypeRepositoryContentSelector PrivilegeType = "repository-content-selector"
)

// Privilege grants a user (via a Role) access to a set of actions.
// Attrs meaning per type:
//
//	wildcard          → {"pattern": "nexus:*:read"}
//	repository-view   → {"format": "maven2", "repository": "*", "actions": ["read"]}
//	repository-admin  → {"format": "*", "repository": "*", "actions": ["read","write","delete"]}
//	application       → {"domain": "users", "actions": ["read"]}
//	script            → {"name": "my-script", "actions": ["run"]}
type Privilege struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Description       string         `json:"description,omitempty"`
	Type              PrivilegeType  `json:"type"`
	Attrs             map[string]any `json:"attrs,omitempty"`
	ContentSelectorID *string        `json:"contentSelectorId,omitempty"`
	Builtin           bool           `json:"readOnly"`
	CreatedAt         time.Time      `json:"createdAt"`
}

// ── Cleanup Policy ───────────────────────────────────────────

// CleanupScope optionally narrows a policy to a specific repository and/or path prefix.
type CleanupScope struct {
	RepositoryName string `json:"repositoryName,omitempty"`
	PathPrefix     string `json:"pathPrefix,omitempty"`
}

// CleanupPolicy defines criteria for automatically deleting stale assets from attached repositories.
type CleanupPolicy struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Description     string         `json:"description,omitempty"`
	Format          string         `json:"format"`   // "*" = all formats
	Criteria        map[string]any `json:"criteria"` // e.g. {"lastDownloadedDays":30,"artifactAgeDays":90}
	ScheduleCron    string         `json:"scheduleCron,omitempty"`
	Enabled         bool           `json:"enabled"`
	DryRun          bool           `json:"dryRun"`
	RetainNVersions int            `json:"retainNVersions"`
	Scope           CleanupScope   `json:"scope"`
	LastRunAt       *time.Time     `json:"lastRunAt,omitempty"`
	LastRunFreed    int64          `json:"lastRunFreedBytes,omitempty"`
	LastRunCount    int            `json:"lastRunCount,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

// CleanupPreviewAsset is a single asset returned by PreviewPolicy.
type CleanupPreviewAsset struct {
	Path           string     `json:"path"`
	Repository     string     `json:"repository"`
	SizeBytes      int64      `json:"sizeBytes"`
	LastDownloaded *time.Time `json:"lastDownloaded"`
	CreatedAt      time.Time  `json:"createdAt"`
	Reason         string     `json:"reason"`
}

// CleanupPreviewResult is the response of PreviewPolicy.
type CleanupPreviewResult struct {
	Assets     []CleanupPreviewAsset `json:"assets"`
	TotalCount int                   `json:"totalCount"`
	TotalBytes int64                 `json:"totalBytes"`
}

// CleanupRunResult summarizes a single cleanup-policy execution. It is returned
// to the manual-run endpoint so the UI can report what happened instead of a
// fire-and-forget acknowledgement.
type CleanupRunResult struct {
	PolicyID      string `json:"policyId"`
	Deleted       int    `json:"deleted"`
	FreedBytes    int64  `json:"freedBytes"`
	DryRun        bool   `json:"dryRun"`
	Skipped       bool   `json:"skipped"`
	SkippedReason string `json:"skippedReason,omitempty"`
	// Aborted reports a run that stopped early because it reached its
	// distributed lock's TTL: Deleted/FreedBytes are then partial, and the rest
	// of the policy's backlog is left for the next run (#371).
	Aborted bool `json:"aborted,omitempty"`
}

// ── Audit Event ──────────────────────────────────────────────

// AuditEvent is a single recorded action (who, what, when, result) in the audit log.
type AuditEvent struct {
	ID         int64          `json:"id"`
	EventTime  time.Time      `json:"eventTime"`
	UserID     *string        `json:"userId,omitempty"`
	Username   string         `json:"username"`
	RemoteIP   string         `json:"remoteIp,omitempty"`
	UserAgent  string         `json:"userAgent,omitempty"`
	Domain     string         `json:"domain"` // e.g. "REPOSITORY", "SECURITY", "USER"
	Action     string         `json:"action"` // e.g. "CREATE", "DELETE", "LOGIN"
	EntityType string         `json:"entityType,omitempty"`
	EntityID   string         `json:"entityId,omitempty"`
	EntityName string         `json:"entityName,omitempty"`
	Context    map[string]any `json:"context,omitempty"`
	Result     string         `json:"result"` // "success" | "failure" | "denied"
}

// DockerBrowseRow is one Docker component plus a sample asset path for browse-tree classification.
type DockerBrowseRow struct {
	ComponentID string `json:"componentId"`
	ImageName   string `json:"imageName"`
	Version     string `json:"version"`
	SamplePath  string `json:"samplePath"`
	// ArtifactType is the raw oci_artifact_type recorded on the component when
	// its manifest was stored, or empty for anything pushed before that metadata
	// existed. It is carried verbatim; naming it is the browse handler's job.
	ArtifactType string `json:"artifactType,omitempty"`
	// PredicateType is the in-toto predicate recorded in the manifest's
	// dev.sigstore.bundle.predicateType annotation, or empty when there is none.
	// A cosign attestation types its manifest after the DSSE envelope that wraps
	// the payload, so the envelope's type says "signed statement" and only the
	// predicate says what was stated — an SBOM, a provenance record.
	PredicateType string `json:"predicateType,omitempty"`
}

// RawBrowseAsset is a flat asset record used to build the raw browse tree.
type RawBrowseAsset struct {
	Path        string
	SizeBytes   int64
	SHA256      string
	ContentType string
	UpdatedAt   time.Time
	ComponentID string
	RepoName    string
}

// ── Pagination ───────────────────────────────────────────────

// Page is a generic paginated result with an optional continuation token.
type Page[T any] struct {
	Items             []T     `json:"items"`
	ContinuationToken *string `json:"continuationToken"`
}

// ── Blob Store Migration ─────────────────────────────────────

// MigrationAssetRow is a lightweight struct used by the migration service to
// iterate distinct blobs to copy.
type MigrationAssetRow struct {
	BlobKey           string
	SourceBlobStoreID string
	SizeBytes         int64
}

// BlobRef is one asset reference to a blob: which key, in which blob store.
// GC needs both — the same key can exist in several stores, and being
// referenced in one says nothing about the copy sitting in another.
type BlobRef struct {
	BlobKey     string
	BlobStoreID string // empty when the asset row carries no store id
}

// BlobStoreMigration tracks progress of a background blob store migration for one repository.
//
// Serialized directly by the blob-store-migration handlers, so the tags ARE
// the API contract (frontend/src/api/client.ts BlobStoreMigration). The
// nullable pointers carry no omitempty — the frontend types them `| null`,
// and a dropped key reads as undefined instead (#253, the same fix #210
// made for the replication types).
type BlobStoreMigration struct {
	ID             string     `json:"id"`
	RepositoryName string     `json:"repositoryName"`
	SourceStoreID  string     `json:"sourceStoreId"`
	TargetStoreID  string     `json:"targetStoreId"`
	Status         string     `json:"status"`
	TotalAssets    int        `json:"totalAssets"`
	DoneAssets     int        `json:"doneAssets"`
	TotalBytes     int64      `json:"totalBytes"`
	DoneBytes      int64      `json:"doneBytes"`
	ErrorMessage   *string    `json:"errorMessage"`
	StartedAt      *time.Time `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// ── Search params ────────────────────────────────────────────

// SearchParams holds the filters used to query components and assets.
type SearchParams struct {
	Repository string
	// RepositoryNames filters components/assets to any of these repository names (used when UI/API passes a group repo — expanded to members). When non-empty, Repository is ignored for SQL filtering.
	RepositoryNames []string
	Format          string
	Group           string
	Name            string
	Version         string
	SHA256          string
	Tag             string // exact match: $Tag = ANY(tags)
	// Maven
	MavenGroupID    string
	MavenArtifactID string
	MavenVersion    string
	// Docker
	DockerImageName string
	DockerImageTag  string
	// Pagination
	Offset int
	Limit  int
}

// ── Replication ──────────────────────────────────────────────────

// ReplicationRule defines a push-replication job from a local repo to a remote Nexspence instance.
type ReplicationRule struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	SourceRepo        string     `json:"source_repo"`
	TargetURL         string     `json:"target_url"`
	TargetRepo        string     `json:"target_repo"`
	TargetUsername    string     `json:"target_username"`
	TargetPasswordEnc string     `json:"-"` // AES-256-GCM encrypted, base64url; never returned in API responses
	CronExpr          string     `json:"cron_expr"`
	Enabled           bool       `json:"enabled"`
	LastRunAt         *time.Time `json:"last_run_at"`
	LastRunStatus     string     `json:"last_run_status"` // "ok", "error", "running", ""
	CreatedAt         time.Time  `json:"created_at"`
}

// ReplicationHistory records the outcome of a single replication run.
type ReplicationHistory struct {
	ID               string     `json:"id"`
	RuleID           string     `json:"rule_id"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	DurationMs       int64      `json:"duration_ms"`
	PushedCount      int        `json:"pushed_count"`
	SkippedCount     int        `json:"skipped_count"`
	FailedCount      int        `json:"failed_count"`
	TransferredBytes int64      `json:"transferred_bytes"`
	Error            string     `json:"error"`
}

// ── Promotion ────────────────────────────────────────────────

// PromotionRule defines a promotion route between two repositories.
type PromotionRule struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	FromRepo              string    `json:"from_repo"`
	ToRepo                string    `json:"to_repo"`
	PathFilter            string    `json:"path_filter,omitempty"` // CEL expression; empty = all paths
	RequireScanPass       bool      `json:"require_scan_pass"`
	RequireManualApproval bool      `json:"require_manual_approval"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// PromotionStatus is the lifecycle state of a build-promotion request.
type PromotionStatus string

// Promotion request lifecycle states.
const (
	PromotionPending   PromotionStatus = "pending"
	PromotionApproved  PromotionStatus = "approved"
	PromotionRejected  PromotionStatus = "rejected"
	PromotionCompleted PromotionStatus = "completed"
	PromotionFailed    PromotionStatus = "failed"
)

// PromotionRequest is one artifact copy task produced by a Promote action.
type PromotionRequest struct {
	ID          string          `json:"id"`
	RuleID      string          `json:"rule_id"`
	ComponentID string          `json:"component_id"`
	Status      PromotionStatus `json:"status"`
	RequestedBy string          `json:"requested_by"`
	ReviewedBy  *string         `json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time      `json:"reviewed_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}
