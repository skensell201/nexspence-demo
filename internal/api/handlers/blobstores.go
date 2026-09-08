package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// BlobStoreHandler handles blob store management endpoints.
type BlobStoreHandler struct {
	repo      repository.BlobStoreRepo
	repos     repository.RepositoryRepo
	assets    repository.AssetRepo
	gcSvc     *service.BlobGCService
	blobStore storage.BlobStore
	registry  *storage.Registry
}

// NewBlobStoreHandler constructs a BlobStoreHandler backed by the given blob store repository.
func NewBlobStoreHandler(repo repository.BlobStoreRepo) *BlobStoreHandler {
	return &BlobStoreHandler{repo: repo}
}

// WithGC wires the blob GC service used to release unreferenced blobs; returns the handler for chaining.
func (h *BlobStoreHandler) WithGC(svc *service.BlobGCService) *BlobStoreHandler {
	h.gcSvc = svc
	return h
}

// WithBlobStore wires the physical blob store used for connection tests; returns the handler for chaining.
func (h *BlobStoreHandler) WithBlobStore(bs storage.BlobStore) *BlobStoreHandler {
	h.blobStore = bs
	return h
}

// WithRegistry wires the blob store registry used to resolve per-store backends; returns the handler for chaining.
func (h *BlobStoreHandler) WithRegistry(r *storage.Registry) *BlobStoreHandler {
	h.registry = r
	return h
}

// WithUsageDeps wires the repository and asset repos used by the Usage endpoint.
func (h *BlobStoreHandler) WithUsageDeps(repos repository.RepositoryRepo, assets repository.AssetRepo) *BlobStoreHandler {
	h.repos = repos
	h.assets = assets
	return h
}

// List handles GET /service/rest/v1/blobstores
func (h *BlobStoreHandler) List(c *gin.Context) {
	stores, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if stores == nil {
		stores = []domain.BlobStore{}
	}
	c.JSON(http.StatusOK, domain.RedactedBlobStores(stores))
}

// Get handles GET /service/rest/v1/blobstores/:name
func (h *BlobStoreHandler) Get(c *gin.Context) {
	name := c.Param("name")
	bs, err := h.repo.Get(c.Request.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "blob store not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, domain.RedactedBlobStore(*bs))
}

// mergeBlobStoreConfig produces the config to persist from the stored one and the
// caller's replacement. The replacement wins wholesale, except for the S3 secret key:
// clients read a redacted config (see domain.RedactedBlobStore), so an omitted
// secret_key means "unchanged" rather than "clear it". Sending an explicit empty
// secret_key clears the credential. The read-only secret_key_set marker a client may
// echo back is always dropped.
func mergeBlobStoreConfig(stored, updates map[string]any) map[string]any {
	if updates == nil {
		return nil
	}
	merged := make(map[string]any, len(updates)+1)
	for k, v := range updates {
		if k == domain.SecretKeySetKey {
			continue
		}
		merged[k] = v
	}
	if secret, sent := merged[domain.SecretKeyKey]; sent {
		if s, _ := secret.(string); s == "" {
			delete(merged, domain.SecretKeyKey)
		}
		return merged
	}
	if secret, ok := stored[domain.SecretKeyKey].(string); ok && secret != "" {
		merged[domain.SecretKeyKey] = secret
	}
	return merged
}

var validFillPolicies = map[string]bool{
	"round_robin":         true,
	"write_to_first_fill": true,
}

// extractMemberIDs pulls member_ids from blob store config, handling []string and []interface{}.
func extractMemberIDs(cfg map[string]any) []string {
	if cfg == nil {
		return nil
	}
	raw := cfg["member_ids"]
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// validateGroupConfig validates config for a group blob store.
// Returns a non-empty error string on failure.
func (h *BlobStoreHandler) validateGroupConfig(ctx context.Context, cfg map[string]any) string {
	if cfg == nil {
		return "group blob store requires config with fill_policy and member_ids"
	}
	policy, _ := cfg["fill_policy"].(string)
	if !validFillPolicies[policy] {
		return "fill_policy must be 'round_robin' or 'write_to_first_fill'"
	}
	memberIDs := extractMemberIDs(cfg)
	if len(memberIDs) == 0 {
		return "group blob store must have at least one member_id"
	}
	for _, mid := range memberIDs {
		m, err := h.repo.GetByID(ctx, mid)
		if err != nil || m == nil {
			return fmt.Sprintf("member blob store %q not found", mid)
		}
		if m.Type == "group" {
			return fmt.Sprintf("member %q is itself a group — nested groups are not allowed", m.Name)
		}
	}
	return ""
}

// checkNotGroupMember returns a non-empty message if the named store is referenced as a member
// in any group blob store, to prevent orphaned groups.
func (h *BlobStoreHandler) checkNotGroupMember(ctx context.Context, name string) string {
	bs, err := h.repo.Get(ctx, name)
	if err != nil || bs == nil {
		return ""
	}
	all, err := h.repo.List(ctx)
	if err != nil {
		return ""
	}
	for _, g := range all {
		if g.Type != "group" {
			continue
		}
		for _, mid := range extractMemberIDs(g.Config) {
			if mid == bs.ID {
				return fmt.Sprintf("blob store %q is a member of group %q — remove it from the group first", name, g.Name)
			}
		}
	}
	return ""
}

// Create handles POST /service/rest/v1/blobstores/:type
func (h *BlobStoreHandler) Create(c *gin.Context) {
	blobType := c.Param("type")

	var bs domain.BlobStore
	if err := c.ShouldBindJSON(&bs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	bs.Type = blobType
	delete(bs.Config, domain.SecretKeySetKey)

	if bs.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	if bs.Type == "group" {
		if msg := h.validateGroupConfig(c.Request.Context(), bs.Config); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
	}

	if err := h.repo.Create(c.Request.Context(), &bs); err != nil {
		if conflictOnDuplicateName(c, err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, domain.RedactedBlobStore(bs))
}

// Update handles PUT /service/rest/v1/blobstores/:type/:name
func (h *BlobStoreHandler) Update(c *gin.Context) {
	name := c.Param("name")

	existing, err := h.repo.Get(c.Request.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "blob store not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var updates domain.BlobStore
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updates.Name = name
	updates.Type = existing.Type
	updates.Config = mergeBlobStoreConfig(existing.Config, updates.Config)

	if updates.Type == "group" {
		if msg := h.validateGroupConfig(c.Request.Context(), updates.Config); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
	}

	if err := h.repo.Update(c.Request.Context(), &updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.registry != nil && existing.ID != "" {
		h.registry.Invalidate(existing.ID)
	}
	c.JSON(http.StatusOK, domain.RedactedBlobStore(updates))
}

// Delete handles DELETE /service/rest/v1/blobstores/:name
func (h *BlobStoreHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	if msg := h.checkNotGroupMember(c.Request.Context(), name); msg != "" {
		c.JSON(http.StatusConflict, gin.H{"error": msg})
		return
	}
	if err := h.repo.Delete(c.Request.Context(), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// PresignGet handles GET /api/v1/blobstores/:name/presign
// Query params: key=<blobKey>, ttl=<seconds> (default 3600).
// Returns a presigned download URL (S3 stores only).
func (h *BlobStoreHandler) PresignGet(c *gin.Context) {
	// The key is caller-supplied and repository RBAC never sees it, so this is
	// an admin operation wherever it ends up mounted.
	if !requireAdmin(c) {
		return
	}
	ps, ok := h.blobStore.(storage.PresignableStore)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "presigned URLs are only supported for S3 blob stores"})
		return
	}
	key := c.Query("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key query parameter is required"})
		return
	}
	ttlSec := int64(3600)
	if v := c.Query("ttl"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ttl must be a positive integer (seconds)"})
			return
		}
		ttlSec = n
	}
	url, err := ps.PresignGetURL(c.Request.Context(), key, time.Duration(ttlSec)*time.Second)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url, "ttl_seconds": ttlSec})
}

// PresignPut handles POST /api/v1/blobstores/:name/presign
// Body JSON: {"key": "<blobKey>", "ttl": <seconds>}
// Returns a presigned upload URL (S3 stores only).
func (h *BlobStoreHandler) PresignPut(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	ps, ok := h.blobStore.(storage.PresignableStore)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "presigned URLs are only supported for S3 blob stores"})
		return
	}
	var req struct {
		Key string `json:"key"`
		TTL int64  `json:"ttl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	ttlSec := req.TTL
	if ttlSec <= 0 {
		ttlSec = 3600
	}
	url, err := ps.PresignPutURL(c.Request.Context(), req.Key, time.Duration(ttlSec)*time.Second)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url, "ttl_seconds": ttlSec})
}

// ConfigureLifecycle handles PUT /api/v1/blobstores/:name/lifecycle
// Body JSON: {"expiration_days": 30}  (0 = remove all rules)
// S3 stores only.
func (h *BlobStoreHandler) ConfigureLifecycle(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	ps, ok := h.blobStore.(storage.PresignableStore)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lifecycle configuration is only supported for S3 blob stores"})
		return
	}
	var req struct {
		ExpirationDays int32 `json:"expiration_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ExpirationDays < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expiration_days must be >= 0 (0 removes all rules)"})
		return
	}
	if err := ps.ConfigureLifecycle(c.Request.Context(), req.ExpirationDays); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"expiration_days": req.ExpirationDays})
}

// LinkedRepoInfo is one row in the Usage response: a repository that uses the blob store.
type LinkedRepoInfo struct {
	Name      string `json:"name"`
	Format    string `json:"format"`
	Type      string `json:"type"`
	BytesUsed int64  `json:"bytesUsed"`
}

// Usage handles GET /api/v1/blob-stores/:name/usage
// Returns the store details plus the repositories that use it and per-repo byte counts.
// For group blob stores, aggregates across all member stores.
func (h *BlobStoreHandler) Usage(c *gin.Context) {
	if h.repos == nil || h.assets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage deps not configured"})
		return
	}
	name := c.Param("name")
	ctx := c.Request.Context()

	bs, err := h.repo.Get(ctx, name)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "blob store not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if bs.Type == "group" {
		h.usageGroup(ctx, c, bs)
		return
	}

	linked, err := h.repos.ListByBlobStoreID(ctx, bs.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	info := make([]LinkedRepoInfo, 0, len(linked))
	var total int64
	for _, r := range linked {
		used, _ := h.assets.SumSizeByRepo(ctx, r.Name)
		info = append(info, LinkedRepoInfo{
			Name:      r.Name,
			Format:    string(r.Format),
			Type:      string(r.Type),
			BytesUsed: used,
		})
		total += used
	}

	resp := gin.H{
		"store":              bs,
		"linkedRepositories": info,
		"totalAssetBytes":    total,
	}
	if bs.QuotaBytes != nil {
		resp["quotaRemaining"] = *bs.QuotaBytes - bs.UsedBytes
	}
	c.JSON(http.StatusOK, resp)
}

type memberUsage struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UsedBytes  int64  `json:"usedBytes"`
	QuotaBytes *int64 `json:"quotaBytes,omitempty"`
}

func (h *BlobStoreHandler) usageGroup(ctx context.Context, c *gin.Context, group *domain.BlobStore) {
	memberIDs := extractMemberIDs(group.Config)
	var members []memberUsage
	var totalUsed, totalQuota int64
	hasQuota := true

	var allRepos []LinkedRepoInfo
	var totalAssetBytes int64

	for _, mid := range memberIDs {
		m, err := h.repo.GetByID(ctx, mid)
		if err != nil || m == nil {
			continue
		}
		mu := memberUsage{ID: m.ID, Name: m.Name, UsedBytes: m.UsedBytes, QuotaBytes: m.QuotaBytes}
		members = append(members, mu)
		totalUsed += m.UsedBytes
		if m.QuotaBytes == nil {
			hasQuota = false
		} else {
			totalQuota += *m.QuotaBytes
		}

		linked, err := h.repos.ListByBlobStoreID(ctx, m.ID)
		if err != nil {
			continue
		}
		for _, r := range linked {
			used, _ := h.assets.SumSizeByRepo(ctx, r.Name)
			allRepos = append(allRepos, LinkedRepoInfo{
				Name:      r.Name,
				Format:    string(r.Format),
				Type:      string(r.Type),
				BytesUsed: used,
			})
			totalAssetBytes += used
		}
	}

	resp := gin.H{
		"store":              group,
		"members":            members,
		"memberTotalUsed":    totalUsed,
		"linkedRepositories": allRepos,
		"totalAssetBytes":    totalAssetBytes,
	}
	if hasQuota {
		resp["memberTotalQuota"] = totalQuota
		resp["quotaRemaining"] = totalQuota - totalUsed
	}
	c.JSON(http.StatusOK, resp)
}

// TestConnection handles POST /api/v1/blobstores/test.
// Body: {"type": "s3"|"local", "config": {...}}
// Tries to connect and returns {"ok": true} or {"ok": false, "error": "..."}.
func (h *BlobStoreHandler) TestConnection(c *gin.Context) {
	var req struct {
		Type   string         `json:"type"`
		Config map[string]any `json:"config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	bs, err := storage.NewFromConfig(ctx, req.Type, req.Config)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Probe the store: HeadObject on a sentinel key is a cheap connectivity check.
	_, probeErr := bs.Exists(ctx, "__health__")
	if probeErr != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": probeErr.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RecomputeUsage handles POST /api/v1/blobstores/recompute-usage
// It restates every store's used_bytes from the assets that reference it — one
// size per stored object, however many assets name it — and answers with the
// repaired stores. Deployments upgraded past the per-asset accounting are
// repaired once by migration 024; this is the way back for drift found later: a
// store emptied out of band, a restored backup, a write that raced a recompute.
func (h *BlobStoreHandler) RecomputeUsage(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.repo.RecomputeUsedBytes(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stores, err := h.repo.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if stores == nil {
		stores = []domain.BlobStore{}
	}
	c.JSON(http.StatusOK, domain.RedactedBlobStores(stores))
}

// Compact handles POST /api/v1/blobstores/:name/compact
// Query params: ?dry_run=true reports orphans without deleting;
// ?min_age=24h overrides the configured grace period (0/absent → server default).
func (h *BlobStoreHandler) Compact(c *gin.Context) {
	if h.gcSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GC service not configured"})
		return
	}
	name := c.Param("name")
	opts := service.GCOptions{DryRun: c.Query("dry_run") == "true"}
	if raw := c.Query("min_age"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid min_age: " + err.Error()})
			return
		}
		opts.MinAge = d
	}
	result, err := h.gcSvc.CompactStore(c.Request.Context(), name, opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
