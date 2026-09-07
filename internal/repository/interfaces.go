// Package repository defines interfaces for all database access.
// All implementations live in repository/postgres/.
package repository

import (
	"context"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// RepositoryRepo manages repository definitions.
type RepositoryRepo interface {
	List(ctx context.Context, format, repoType string) ([]domain.Repository, error)
	Get(ctx context.Context, name string) (*domain.Repository, error)
	GetByID(ctx context.Context, id string) (*domain.Repository, error)
	Create(ctx context.Context, r *domain.Repository) error
	Update(ctx context.Context, r *domain.Repository) error
	Delete(ctx context.Context, name string) error
	// ListNamesByCleanupPolicyID returns repository names that reference the policy.
	ListNamesByCleanupPolicyID(ctx context.Context, policyID string) ([]string, error)
	// DetachCleanupPolicyID removes policyID from every repositories.cleanup_policy_ids array.
	DetachCleanupPolicyID(ctx context.Context, policyID string) error
	// ListByBlobStoreID returns repositories that use the given blob_store_id.
	ListByBlobStoreID(ctx context.Context, blobStoreID string) ([]domain.Repository, error)
}

// ComponentRepo manages component metadata.
type ComponentRepo interface {
	List(ctx context.Context, repoName string, limit int, offset int) (*domain.Page[domain.Component], error)
	// ListByRepoNames returns components from any of the given repositories (used for group repo browse/search).
	ListByRepoNames(ctx context.Context, repoNames []string, limit int, offset int) (*domain.Page[domain.Component], error)
	Get(ctx context.Context, id string) (*domain.Component, error)
	Search(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Component], error)
	// ListDockerBrowseRows returns components of both OCI Distribution formats — docker
	// and oci — with one asset path per row (for Tags vs Manifests vs Blobs).
	ListDockerBrowseRows(ctx context.Context, repoNames []string, maxRows int) ([]domain.DockerBrowseRow, error)
	Create(ctx context.Context, c *domain.Component) error
	Delete(ctx context.Context, id string) error
	// UpdateExtra merges JSON into components.extra (e.g. scan_result).
	UpdateExtra(ctx context.Context, id string, extra map[string]any) error
	// SetTags replaces the full tag list for a component.
	SetTags(ctx context.Context, id string, tags []string) error
	// DeleteOrphans removes components in repoName that have no remaining assets.
	DeleteOrphans(ctx context.Context, repoName string) error
	// ListOCIReferrers returns components whose stored manifest metadata names
	// subjectDigest as its subject — the referrers of that manifest. imageName
	// restricts the search to one repository path; empty means any.
	ListOCIReferrers(ctx context.Context, repoNames []string, imageName, subjectDigest string) ([]domain.Component, error)
}

// AssetRepo manages artifact file records.
type AssetRepo interface {
	List(ctx context.Context, repoName string, limit int, offset int) (*domain.Page[domain.Asset], error)
	Get(ctx context.Context, id string) (*domain.Asset, error)
	GetByPath(ctx context.Context, repoName, path string) (*domain.Asset, error)
	SearchAssets(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Asset], error)
	// ListStale returns assets matching cleanup criteria:
	//   format "*" matches all; lastDownloadedDays/artifactAgeDays 0 = no filter.
	//   repoNames non-empty restricts to those repositories; empty = any repository (use with care).
	//   pathPrefix filters assets whose path starts with that prefix (empty = no filter).
	//   nameGlob is a glob pattern matched against the full asset path (* = any chars, ? = one char).
	// retainNVersions — when > 0, the N newest versions of each (group_id, name) are excluded from results.
	ListStale(ctx context.Context, format string, repoNames []string, lastDownloadedDays, artifactAgeDays int, pathPrefix, nameGlob string, retainNVersions int, limit int) ([]domain.Asset, error)
	Create(ctx context.Context, a *domain.Asset) error
	Delete(ctx context.Context, id string) error
	// DeleteIfUnchanged deletes the row only if it is still exactly what an
	// earlier scan read — same blob key, same last_downloaded, same
	// last_modified. It reports whether the row was deleted; a mismatch (the
	// asset was downloaded or re-uploaded since the scan) leaves the row alone.
	DeleteIfUnchanged(ctx context.Context, id, blobKey string, lastDownloaded *time.Time, lastModified time.Time) (bool, error)
	// WithBlobKeyLock serializes every caller that wants to read-then-act on a
	// given blob key (count references, physically delete, register a new
	// reference). Any other WithBlobKeyLock caller for the same key — same
	// process or not — blocks until fn returns. fn's own reads and writes go
	// through the repo's normal connections: the guarantee is mutual exclusion,
	// not that fn's effects share one transaction.
	WithBlobKeyLock(ctx context.Context, blobKey string, fn func(ctx context.Context) error) error
	// TouchLastModified sets last_modified = NOW() for the asset. The proxy cache
	// uses last_modified as the "last validated" timestamp for metadata freshness:
	// on a successful upstream 304 revalidation the cached copy is confirmed current,
	// so its freshness window is extended without rewriting the blob.
	TouchLastModified(ctx context.Context, id string) error
	// IncrementDownloads applies batched download-count increments (asset ID → count)
	// to assets and their parent components in one transaction.
	IncrementDownloads(ctx context.Context, counts map[string]int64) error
	// ListByComponentID returns all assets for a component (ordered by path).
	ListByComponentID(ctx context.Context, componentID string) ([]domain.Asset, error)
	// ListByComponentIDs returns assets for many components in one query,
	// grouped by component ID (each slice ordered by path, like ListByComponentID).
	ListByComponentIDs(ctx context.Context, componentIDs []string) (map[string][]domain.Asset, error)
	// ListAllBlobRefs returns the distinct (blob_key, blob_store_id) pairs
	// referenced by assets (for GC). The store id is part of the answer
	// because a key referenced in one store says nothing about the copy of
	// that key sitting in another.
	ListAllBlobRefs(ctx context.Context) ([]domain.BlobRef, error)
	// SumSizeByRepo returns total size_bytes of all assets in the repository.
	SumSizeByRepo(ctx context.Context, repoName string) (int64, error)
	// ListForBlobStoreMigration returns distinct (blob_key, source_blob_store_id, size_bytes)
	// for all assets in repoName whose blob_store_id differs from targetStoreID.
	ListForBlobStoreMigration(ctx context.Context, repoName, targetStoreID string) ([]domain.MigrationAssetRow, error)
	// UpdateBlobStoreForBlobKey sets blob_store_id = newBlobStoreID for all assets
	// in repoName that have the given blob_key.
	UpdateBlobStoreForBlobKey(ctx context.Context, blobKey, repoName, newBlobStoreID string) error
	// ListPathsByRepo returns unique directory-level path prefixes from assets
	// in the given repository. If q is non-empty, only paths containing q
	// (case-insensitive) are returned. Fetches up to 5000 raw paths from the DB
	// then expands directory prefixes in Go.
	ListPathsByRepo(ctx context.Context, repoName, q string) ([]string, error)
	// ListRawAssetPaths returns raw asset paths (not directory-expanded) for
	// the given repository. Used by format-specific path transformations (e.g. Docker).
	ListRawAssetPaths(ctx context.Context, repoName string) ([]string, error)
	// ListByRepoAndPath returns all assets in repoName whose path starts with pathPrefix.
	// Use pathPrefix="" to list all assets in the repo.
	ListByRepoAndPath(ctx context.Context, repoName, pathPrefix string) ([]domain.Asset, error)
	// CountByBlobKey returns the number of assets that reference blobKey, excluding the asset with excludeID.
	// Used to decide whether the physical blob file can be deleted.
	CountByBlobKey(ctx context.Context, blobKey, excludeID string) (int, error)
	// CountByBlobKeyInStore returns the number of assets that reference blobKey
	// and still live on blobStoreID. Used to decide whether a physical blob may
	// be removed from one specific store.
	CountByBlobKeyInStore(ctx context.Context, blobKey, blobStoreID string) (int, error)
	// ListRawBrowseAssets returns all assets for the given raw-format repos with metadata for tree building.
	ListRawBrowseAssets(ctx context.Context, repoNames []string) ([]domain.RawBrowseAsset, error)
	// ListOCIImageNames returns the distinct image names held by the given OCI
	// Distribution repositories — the <name> of a stored
	// /manifests/<name>/<reference> asset. Only images that have a manifest are
	// named: a blob carries no image the client can pull. The order is
	// unspecified; the caller sorts.
	ListOCIImageNames(ctx context.Context, repoNames []string) ([]string, error)
}

// ContentSelectorRepo manages content selector definitions (privilege-scoped paths).
type ContentSelectorRepo interface {
	List(ctx context.Context) ([]domain.ContentSelector, error)
	Get(ctx context.Context, id string) (*domain.ContentSelector, error)
	GetByName(ctx context.Context, name string) (*domain.ContentSelector, error)
	Create(ctx context.Context, s *domain.ContentSelector) error
	Update(ctx context.Context, s *domain.ContentSelector) error
	Delete(ctx context.Context, id string) error
	ListForUser(ctx context.Context, userID string) ([]domain.ContentSelector, error)
	AttachToPrivilege(ctx context.Context, privilegeName, selectorID string) error
	DetachFromPrivilege(ctx context.Context, privilegeName string) error
}

// PrivilegeRepo manages privilege definitions.
type PrivilegeRepo interface {
	List(ctx context.Context) ([]domain.Privilege, error)
	Get(ctx context.Context, id string) (*domain.Privilege, error)
	GetByName(ctx context.Context, name string) (*domain.Privilege, error)
	Create(ctx context.Context, p *domain.Privilege) error
	Update(ctx context.Context, p *domain.Privilege) error
	Delete(ctx context.Context, id string) error
	// ListByRole returns privileges assigned to a role via role_privileges.
	ListByRole(ctx context.Context, roleID string) ([]domain.Privilege, error)
	// PrivilegeRoleMap returns a map of privilege ID → role names that include it.
	// Used by the UI to display "Used in Roles" for each privilege.
	PrivilegeRoleMap(ctx context.Context) (map[string][]string, error)
}

// RoutingRuleRepo manages request routing rules.
type RoutingRuleRepo interface {
	List(ctx context.Context) ([]domain.RoutingRule, error)
	Get(ctx context.Context, id string) (*domain.RoutingRule, error)
	GetByName(ctx context.Context, name string) (*domain.RoutingRule, error)
	Create(ctx context.Context, rr *domain.RoutingRule) error
	Update(ctx context.Context, rr *domain.RoutingRule) error
	Delete(ctx context.Context, id string) error
}

// UserTokenRepo manages per-user API tokens.
type UserTokenRepo interface {
	ListByUser(ctx context.Context, userID string) ([]domain.UserToken, error)
	Get(ctx context.Context, id string) (*domain.UserToken, error)
	GetByHash(ctx context.Context, tokenHash string) (*domain.UserToken, error)
	Create(ctx context.Context, t *domain.UserToken) error
	Delete(ctx context.Context, id string) error
	TouchLastUsed(ctx context.Context, id string) error
}

// WebhookRepo manages outbound webhooks.
type WebhookRepo interface {
	List(ctx context.Context) ([]domain.Webhook, error)
	Get(ctx context.Context, id string) (*domain.Webhook, error)
	ListByEvent(ctx context.Context, event domain.WebhookEvent) ([]domain.Webhook, error)
	Create(ctx context.Context, w *domain.Webhook) error
	Update(ctx context.Context, w *domain.Webhook) error
	Delete(ctx context.Context, id string) error
}

// UserRepo manages user accounts.
type UserRepo interface {
	List(ctx context.Context, source string) ([]domain.User, error)
	Get(ctx context.Context, username string) (*domain.User, error)
	GetByID(ctx context.Context, id string) (*domain.User, error)
	Create(ctx context.Context, u *domain.User) error
	Update(ctx context.Context, u *domain.User) error
	UpdatePassword(ctx context.Context, username, hash string) error
	Delete(ctx context.Context, username string) error
	UpdateLastLogin(ctx context.Context, username string) error
	// BumpTokensValidAfter sets tokens_valid_after to now for the user,
	// invalidating any JWT issued before this call (used on disable, password
	// change, and role change).
	BumpTokensValidAfter(ctx context.Context, userID string) error
	// SetOIDCTokens stores id_token and refresh_token for an OIDC user.
	// Pass empty strings to clear both columns (e.g. on logout).
	SetOIDCTokens(ctx context.Context, userID string, idToken, refreshToken string) error
	// GetOIDCIDToken returns the stored id_token for userID, or "" if unset.
	GetOIDCIDToken(ctx context.Context, userID string) (string, error)
}

// RoleRepo manages roles and privileges.
type RoleRepo interface {
	List(ctx context.Context) ([]domain.Role, error)
	Get(ctx context.Context, id string) (*domain.Role, error)
	// GetByName resolves a role by its unique name.
	GetByName(ctx context.Context, name string) (*domain.Role, error)
	Create(ctx context.Context, r *domain.Role) error
	Update(ctx context.Context, r *domain.Role) error
	Delete(ctx context.Context, id string) error
	GetUserRoles(ctx context.Context, userID string) ([]domain.Role, error)
	SetUserRoles(ctx context.Context, userID string, roleIDs []string) error
	// SetPrivileges replaces all role_privileges rows for the role.
	SetPrivileges(ctx context.Context, roleID string, privilegeIDs []string) error
	// ListPrivilegeIDsByRole returns privilege IDs for a role (lightweight, for JWT building).
	ListPrivilegeIDsByRole(ctx context.Context, roleID string) ([]string, error)
}

// CleanupPolicyRepo manages cleanup policies.
type CleanupPolicyRepo interface {
	List(ctx context.Context) ([]domain.CleanupPolicy, error)
	Get(ctx context.Context, id string) (*domain.CleanupPolicy, error)
	Create(ctx context.Context, p *domain.CleanupPolicy) error
	Update(ctx context.Context, p *domain.CleanupPolicy) error
	// RecordRun persists the outcome of a cleanup run (last run time, deleted
	// count, freed bytes). Kept separate from Update so editing a policy through
	// the form — which carries no run stats — does not wipe them.
	RecordRun(ctx context.Context, id string, at time.Time, count int, freed int64) error
	Delete(ctx context.Context, id string) error
}

// AuditQuery holds filter and pagination parameters for AuditRepo.List/Stream.
type AuditQuery struct {
	Domain   string     // empty = any
	Action   string     // empty = any
	Username string     // empty = any (exact match)
	From     *time.Time // inclusive lower bound; nil = no lower bound
	To       *time.Time // exclusive upper bound; nil = no upper bound
	Limit    int        // ignored by Stream. List impl applies its own default and cap.
	Offset   int        // ignored by Stream
}

// AuditRepo writes and reads audit log events.
type AuditRepo interface {
	Write(ctx context.Context, e *domain.AuditEvent) error
	List(ctx context.Context, q AuditQuery) (items []domain.AuditEvent, total int, err error)
	Stream(ctx context.Context, q AuditQuery, fn func(domain.AuditEvent) error) error
}

// PrivilegeWithSelector is returned by RBACRepo — one row per privilege attached to the user.
type PrivilegeWithSelector struct {
	Actions    []string // from privilege attrs.actions; empty = all actions
	Expression string   // CEL expression from content_selector
}

// RBACRepo resolves a user's effective privileges for access checks.
type RBACRepo interface {
	GetUserPrivilegesWithSelectors(ctx context.Context, userID string) ([]PrivilegeWithSelector, error)
}

// MigrationRepo manages migration job records.
type MigrationRepo interface {
	List(ctx context.Context) ([]domain.MigrationJob, error)
	Get(ctx context.Context, id string) (*domain.MigrationJob, error)
	Create(ctx context.Context, job *domain.MigrationJob) error
	UpdateStatus(ctx context.Context, id string, status domain.MigrationJobStatus) error
	Delete(ctx context.Context, id string) error
	// ListActive returns pending|running jobs — the ones the runner re-attaches
	// to on startup after a process restart.
	ListActive(ctx context.Context) ([]domain.MigrationJob, error)
	// SetSourcePassword stores the sealed Nexus credential for the job.
	SetSourcePassword(ctx context.Context, id, sealed string) error
	// SetStarted marks the job running and stamps started_at (once — a resumed
	// job keeps the timestamp of its first run).
	SetStarted(ctx context.Context, id string, at time.Time) error
	// SetTotals records how much work the run discovered.
	SetTotals(ctx context.Context, id string, totalRepos int, totalAssets int64) error
	// UpdateProgress records how much of that work is done, along with the
	// running error tally and the most recent per-item failure.
	UpdateProgress(ctx context.Context, id string, doneRepos int, doneAssets int64,
		errorCount int, lastError *string) error
	// FinishJob stamps finished_at and sets the terminal status.
	FinishJob(ctx context.Context, id string, status domain.MigrationJobStatus, errMsg *string) error
}

// BlobStoreRepo manages blob store configuration.
type BlobStoreRepo interface {
	List(ctx context.Context) ([]domain.BlobStore, error)
	Get(ctx context.Context, name string) (*domain.BlobStore, error)
	GetByID(ctx context.Context, id string) (*domain.BlobStore, error)
	Create(ctx context.Context, b *domain.BlobStore) error
	Update(ctx context.Context, b *domain.BlobStore) error
	Delete(ctx context.Context, name string) error
	UpdateUsedBytes(ctx context.Context, name string, delta int64) error
	// RecomputeUsedBytes restates every store's used_bytes as the bytes it holds:
	// one size per distinct blob key, since several assets can name one stored
	// object. Repairs a counter that drifted from what is on the disk.
	RecomputeUsedBytes(ctx context.Context) error
}

// BlobStoreMigrationRepo persists blob store migration job records.
type BlobStoreMigrationRepo interface {
	Create(ctx context.Context, m *domain.BlobStoreMigration) error
	Get(ctx context.Context, id string) (*domain.BlobStoreMigration, error)
	// GetActiveByRepo returns a pending|running migration for the repo, or nil if none.
	GetActiveByRepo(ctx context.Context, repoName string) (*domain.BlobStoreMigration, error)
	// GetLatestByRepo returns the most recent migration regardless of status, or nil.
	GetLatestByRepo(ctx context.Context, repoName string) (*domain.BlobStoreMigration, error)
	// ListActive returns all pending|running migrations (used by ResumeAll on startup).
	ListActive(ctx context.Context) ([]domain.BlobStoreMigration, error)
	SetTotals(ctx context.Context, id string, totalAssets int, totalBytes int64) error
	UpdateProgress(ctx context.Context, id string, doneAssets int, doneBytes int64) error
	UpdateStatus(ctx context.Context, id string, status string, errMsg *string) error
	FinishMigration(ctx context.Context, id string, status string, errMsg *string) error
}

// ScanResultRepo manages rows in the scan_results table.
type ScanResultRepo interface {
	Insert(ctx context.Context, r *domain.ScanResultRow) error
	GetLatestByComponent(ctx context.Context, componentID string) (*domain.ScanResultRow, error)
	// Aggregate returns summed severity counts across the latest scan per component.
	Aggregate(ctx context.Context) (*domain.SecuritySummary, error)
	// List returns vulnerability rows with join to components+repositories, filtered by f.
	// Returns (rows, totalCount, error).
	List(ctx context.Context, f domain.VulnFilter) ([]*domain.VulnRow, int, error)
}

// ReplicationRepo manages replication rules and their run history.
type ReplicationRepo interface {
	ListRules(ctx context.Context) ([]domain.ReplicationRule, error)
	GetRule(ctx context.Context, id string) (*domain.ReplicationRule, error)
	CreateRule(ctx context.Context, r *domain.ReplicationRule) error
	UpdateRule(ctx context.Context, r *domain.ReplicationRule) error
	DeleteRule(ctx context.Context, id string) error
	UpdateRuleStatus(ctx context.Context, id, status string, at time.Time) error
	AddHistory(ctx context.Context, h *domain.ReplicationHistory) error
	ListHistory(ctx context.Context, ruleID string, limit int) ([]domain.ReplicationHistory, error)
}

// PromotionRepo manages promotion rules and requests.
type PromotionRepo interface {
	// Rules
	ListRules(ctx context.Context) ([]domain.PromotionRule, error)
	GetRule(ctx context.Context, id string) (*domain.PromotionRule, error)
	// ListRulesByFromRepo returns rules where from_repo matches the given name.
	ListRulesByFromRepo(ctx context.Context, fromRepo string) ([]domain.PromotionRule, error)
	CreateRule(ctx context.Context, r *domain.PromotionRule) error
	UpdateRule(ctx context.Context, r *domain.PromotionRule) error
	DeleteRule(ctx context.Context, id string) error
	// Requests
	CreateRequest(ctx context.Context, r *domain.PromotionRequest) error
	GetRequest(ctx context.Context, id string) (*domain.PromotionRequest, error)
	// ListRequests returns requests filtered by status ("" = all).
	ListRequests(ctx context.Context, status string) ([]domain.PromotionRequest, error)
	// UpdateRequestStatus sets status and optional review/completion metadata.
	UpdateRequestStatus(ctx context.Context, id string, status domain.PromotionStatus,
		reviewedBy *string, reviewedAt, completedAt *time.Time, errMsg string) error
	// WithPendingRequestLock locks the request row, verifies it is still
	// pending, runs fn with the row held, and persists fn's outcome in the same
	// transaction. A concurrent caller for the same request blocks until this
	// one commits, then sees the non-pending status — so fn (the copy) can never
	// run twice for one request. Returns ErrNotFound for an unknown id and
	// *RequestNotPendingError when the row already left pending, in both cases
	// without calling fn. An outcome whose Status is still PromotionPending
	// leaves the row untouched.
	WithPendingRequestLock(ctx context.Context, id string,
		fn func(ctx context.Context, req *domain.PromotionRequest) PromotionOutcome) error
}

// PromotionOutcome is what a WithPendingRequestLock callback decides about the
// locked request: the final status plus the review/completion metadata that
// UpdateRequestStatus would take.
type PromotionOutcome struct {
	Status      domain.PromotionStatus
	ReviewedBy  *string
	ReviewedAt  *time.Time
	CompletedAt *time.Time
	Error       string
}
