// Package testutil provides shared test helpers and in-memory mock implementations
// of repository and storage interfaces. It must only be imported from _test.go files.
package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ── compile-time interface assertions ────────────────────────
var (
	_ repository.RepositoryRepo         = (*RepoRepo)(nil)
	_ repository.BlobStoreRepo          = (*BlobStoreRepo)(nil)
	_ repository.ComponentRepo          = (*ComponentRepo)(nil)
	_ repository.AssetRepo              = (*AssetRepo)(nil)
	_ repository.CleanupPolicyRepo      = (*CleanupPolicyRepo)(nil)
	_ repository.AuditRepo              = (*AuditRepo)(nil)
	_ repository.UserRepo               = (*UserRepo)(nil)
	_ repository.RoleRepo               = (*RoleRepo)(nil)
	_ repository.ContentSelectorRepo    = (*ContentSelectorRepo)(nil)
	_ repository.UserTokenRepo          = (*UserTokenRepo)(nil)
	_ repository.WebhookRepo            = (*WebhookRepo)(nil)
	_ repository.RoutingRuleRepo        = (*RoutingRuleRepo)(nil)
	_ repository.PrivilegeRepo          = (*PrivilegeRepo)(nil)
	_ repository.BlobStoreMigrationRepo = (*BlobStoreMigrationRepo)(nil)
	_ repository.ScanResultRepo         = (*ScanResultRepo)(nil)
	_ repository.ReplicationRepo        = (*ReplicationRepo)(nil)
	_ repository.PromotionRepo          = (*PromotionRepo)(nil)
	_ repository.MigrationRepo          = (*MigrationRepo)(nil)
	_ storage.BlobStore                 = (*BlobStore)(nil)
)

// ── RepositoryRepo ────────────────────────────────────────────

type RepoRepo struct {
	mu    sync.Mutex
	repos map[string]*domain.Repository
	Err   error // when set, List/Get/Create/Update/Delete return it (500-branch seam)
}

func NewRepoRepo(repos ...*domain.Repository) *RepoRepo {
	r := &RepoRepo{repos: make(map[string]*domain.Repository)}
	for _, repo := range repos {
		r.repos[repo.Name] = repo
	}
	return r
}

func (r *RepoRepo) List(_ context.Context, _, _ string) ([]domain.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	out := make([]domain.Repository, 0, len(r.repos))
	for _, v := range r.repos {
		out = append(out, *v)
	}
	return out, nil
}
func (r *RepoRepo) Get(_ context.Context, name string) (*domain.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	v, ok := r.repos[name]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (r *RepoRepo) GetByID(_ context.Context, id string) (*domain.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.repos {
		if v.ID == id {
			return v, nil
		}
	}
	return nil, repository.ErrNotFound
}
func (r *RepoRepo) Create(_ context.Context, repo *domain.Repository) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.repos[repo.Name] = repo
	return nil
}
func (r *RepoRepo) Update(_ context.Context, repo *domain.Repository) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.repos[repo.Name] = repo
	return nil
}
func (r *RepoRepo) Delete(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	delete(r.repos, name)
	return nil
}

func (r *RepoRepo) ListNamesByCleanupPolicyID(_ context.Context, policyID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for _, v := range r.repos {
		for _, id := range v.CleanupPolicyIDs {
			if id == policyID {
				names = append(names, v.Name)
				break
			}
		}
	}
	return names, nil
}

func (r *RepoRepo) ListByBlobStoreID(_ context.Context, blobStoreID string) ([]domain.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Repository
	for _, v := range r.repos {
		if v.BlobStoreID != nil && *v.BlobStoreID == blobStoreID {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (r *RepoRepo) DetachCleanupPolicyID(_ context.Context, policyID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	for _, v := range r.repos {
		var next []string
		for _, id := range v.CleanupPolicyIDs {
			if id != policyID {
				next = append(next, id)
			}
		}
		v.CleanupPolicyIDs = next
	}
	return nil
}

// ── BlobStoreRepo ─────────────────────────────────────────────

type BlobStoreRepo struct {
	mu     sync.Mutex
	stores map[string]*domain.BlobStore
	// assets backs RecomputeUsedBytes, which in postgres reads the assets table
	// this repo shares a database with. Without it a recompute finds no assets
	// and zeroes every store, exactly as the SQL would against an empty table.
	assets *AssetRepo
}

func NewBlobStoreRepo(stores ...*domain.BlobStore) *BlobStoreRepo {
	b := &BlobStoreRepo{stores: make(map[string]*domain.BlobStore)}
	for _, s := range stores {
		b.stores[s.Name] = s
	}
	if len(b.stores) == 0 {
		b.stores["default"] = &domain.BlobStore{
			ID:   "00000000-0000-0000-0000-000000000001",
			Name: "default",
			Type: "local",
		}
	}
	return b
}

func (b *BlobStoreRepo) List(_ context.Context) ([]domain.BlobStore, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]domain.BlobStore, 0, len(b.stores))
	for _, v := range b.stores {
		out = append(out, *v)
	}
	return out, nil
}
func (b *BlobStoreRepo) Get(_ context.Context, name string) (*domain.BlobStore, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.stores[name]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}

func (b *BlobStoreRepo) GetByID(_ context.Context, id string) (*domain.BlobStore, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.stores {
		if s.ID == id {
			cp := *s
			return &cp, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (b *BlobStoreRepo) Create(_ context.Context, s *domain.BlobStore) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stores[s.Name] = s
	return nil
}
func (b *BlobStoreRepo) Update(_ context.Context, s *domain.BlobStore) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stores[s.Name] = s
	return nil
}
func (b *BlobStoreRepo) Delete(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.stores, name)
	return nil
}
func (b *BlobStoreRepo) UpdateUsedBytes(_ context.Context, name string, delta int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.stores[name]; ok {
		s.UsedBytes += delta
	}
	return nil
}

// WithAssets wires the asset repo a recompute reads, mirroring the single
// database the postgres implementations share. Returns the repo for chaining.
func (b *BlobStoreRepo) WithAssets(a *AssetRepo) *BlobStoreRepo {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.assets = a
	return b
}

// RecomputeUsedBytes mirrors the SQL: every store's used_bytes becomes the sum
// of its assets' DISTINCT blob keys, largest size per key, with an unset
// blob_store_id read as the store named "default". A stub answer would leave the
// repair — the only thing that fixes a counter inflated by the old per-asset
// increment — untestable.
func (b *BlobStoreRepo) RecomputeUsedBytes(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var assets []domain.Asset
	if b.assets != nil {
		assets = b.assets.Snapshot()
	}
	defaultID := ""
	if def, ok := b.stores["default"]; ok {
		defaultID = def.ID
	}

	// storeID → blobKey → largest size seen for that key.
	perStore := make(map[string]map[string]int64)
	for _, a := range assets {
		if strings.TrimSpace(a.BlobKey) == "" {
			continue
		}
		storeID := a.BlobStoreID
		if storeID == "" {
			storeID = defaultID
		}
		if storeID == "" {
			continue
		}
		keys, ok := perStore[storeID]
		if !ok {
			keys = make(map[string]int64)
			perStore[storeID] = keys
		}
		if a.SizeBytes > keys[a.BlobKey] {
			keys[a.BlobKey] = a.SizeBytes
		}
	}

	for _, s := range b.stores {
		var total int64
		for _, size := range perStore[s.ID] {
			total += size
		}
		s.UsedBytes = total
	}
	return nil
}

// ── ComponentRepo ─────────────────────────────────────────────

type ComponentRepo struct {
	mu         sync.Mutex
	components map[string]*domain.Component
	nextID     int
	Err        error // when non-nil, ListByRepoNames/Get/Search/Delete/SetTags return it (500-branch seam)
	// UpdateExtraErr is returned by UpdateExtra when set — the seam for a write
	// that fails after the work producing it already succeeded.
	UpdateExtraErr error
	// DockerRowsByRepo maps repoName→browse rows; ListDockerBrowseRows returns the
	// union of rows for the requested repo names (mirrors the SQL WHERE rep.name IN (...)).
	// A row's ArtifactType stands for the SQL's extra->>'oci_artifact_type': the
	// query projects it verbatim and reports the absent key as the empty string,
	// so a row here is set the same way.
	DockerRowsByRepo map[string][]domain.DockerBrowseRow
	DockerBrowseErr  error // when non-nil, ListDockerBrowseRows returns it (500-branch seam)
	// LastListLimit/LastListOffset record the paging arguments of the most recent
	// ListByRepoNames call so handler tests can assert the offset was decoded.
	LastListLimit  int
	LastListOffset int
	// DeleteOrphansCalls records the repo names passed to DeleteOrphans so cleanup
	// tests can assert orphan components are pruned after their assets are deleted.
	DeleteOrphansCalls []string
}

func NewComponentRepo() *ComponentRepo {
	return &ComponentRepo{components: make(map[string]*domain.Component)}
}

func (c *ComponentRepo) List(ctx context.Context, repoName string, limit, offset int) (*domain.Page[domain.Component], error) {
	return c.ListByRepoNames(ctx, []string{repoName}, limit, offset)
}

func (c *ComponentRepo) ListByRepoNames(_ context.Context, names []string, limit, offset int) (*domain.Page[domain.Component], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LastListLimit, c.LastListOffset = limit, offset
	if c.Err != nil {
		return nil, c.Err
	}
	allow := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n != "" {
			allow[n] = struct{}{}
		}
	}
	items := make([]domain.Component, 0, len(c.components))
	for _, v := range c.components {
		if len(allow) == 0 { // empty/"" names → match all (List with no repo filter)
			items = append(items, *v)
			continue
		}
		if _, ok := allow[v.Repository]; ok {
			items = append(items, *v)
		}
	}
	// Stable order (map iteration is random) so paging is deterministic, mirroring
	// the SQL "ORDER BY c.name, c.version".
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].Version < items[j].Version
	})

	if limit <= 0 {
		return &domain.Page[domain.Component]{Items: items}, nil
	}
	if offset >= len(items) {
		return &domain.Page[domain.Component]{Items: []domain.Component{}}, nil
	}
	items = items[offset:]
	var token *string
	if len(items) > limit {
		items = items[:limit]
		next := strconv.Itoa(offset + limit)
		token = &next
	}
	return &domain.Page[domain.Component]{Items: items, ContinuationToken: token}, nil
}
func (c *ComponentRepo) Get(_ context.Context, id string) (*domain.Component, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return nil, c.Err
	}
	v, ok := c.components[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (c *ComponentRepo) Search(_ context.Context, params domain.SearchParams) (*domain.Page[domain.Component], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return nil, c.Err
	}
	allow := make(map[string]struct{}, len(params.RepositoryNames))
	for _, n := range params.RepositoryNames {
		allow[n] = struct{}{}
	}
	items := make([]domain.Component, 0, len(c.components))
	for _, v := range c.components {
		if params.Repository != "" && v.Repository != params.Repository {
			continue
		}
		if len(allow) > 0 {
			if _, ok := allow[v.Repository]; !ok {
				continue
			}
		}
		items = append(items, *v)
	}
	// Mirror the SQL layer's ordering, limit clamp and continuation token
	// (component_repo.go Search): a mock that returns everything regardless of
	// Limit hides exactly the class of bug where a caller asks for more rows
	// than the repository layer will ever hand out.
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].Version < items[j].Version
	})
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := params.Offset
	if offset >= len(items) {
		return &domain.Page[domain.Component]{Items: []domain.Component{}}, nil
	}
	items = items[offset:]
	var token *string
	if len(items) > limit {
		items = items[:limit]
		next := strconv.Itoa(offset + limit)
		token = &next
	}
	return &domain.Page[domain.Component]{Items: items, ContinuationToken: token}, nil
}

func (c *ComponentRepo) ListDockerBrowseRows(_ context.Context, names []string, _ int) ([]domain.DockerBrowseRow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.DockerBrowseErr != nil {
		return nil, c.DockerBrowseErr
	}
	var out []domain.DockerBrowseRow
	for _, n := range names {
		out = append(out, c.DockerRowsByRepo[n]...)
	}
	return out, nil
}

// ListOCIReferrers mirrors the SQL: filter by repository name (IN repoNames),
// by extra["oci_subject"] == subjectDigest, and by name == imageName when
// imageName is non-empty.
func (c *ComponentRepo) ListOCIReferrers(_ context.Context, repoNames []string, imageName, subjectDigest string) ([]domain.Component, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return nil, c.Err
	}
	if len(repoNames) == 0 {
		return nil, nil
	}
	allow := make(map[string]struct{}, len(repoNames))
	for _, n := range repoNames {
		allow[n] = struct{}{}
	}
	var items []domain.Component
	for _, v := range c.components {
		if _, ok := allow[v.Repository]; !ok {
			continue
		}
		subj, _ := v.Extra["oci_subject"].(string)
		if subj != subjectDigest {
			continue
		}
		if imageName != "" && v.Name != imageName {
			continue
		}
		items = append(items, *v)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].Version < items[j].Version
	})
	return items, nil
}

func (c *ComponentRepo) Create(_ context.Context, comp *domain.Component) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Mirrors the SQL upsert key ON CONFLICT (repository_id, format, group_id,
	// name, version): re-creating the same coordinates reuses the existing row
	// rather than adding a duplicate.
	key := strings.Join([]string{comp.Repository, comp.RepositoryID, comp.Format, comp.Group, comp.Name, comp.Version}, "\x00")
	for _, existing := range c.components {
		ek := strings.Join([]string{existing.Repository, existing.RepositoryID, existing.Format, existing.Group, existing.Name, existing.Version}, "\x00")
		if ek == key {
			comp.ID = existing.ID
			comp.CreatedAt = existing.CreatedAt
			c.components[existing.ID] = comp
			return nil
		}
	}
	c.nextID++
	comp.ID = fmt.Sprintf("comp-%d", c.nextID)
	c.components[comp.ID] = comp
	return nil
}
func (c *ComponentRepo) Delete(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return c.Err
	}
	delete(c.components, id)
	return nil
}

func (c *ComponentRepo) DeleteOrphans(_ context.Context, repoName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DeleteOrphansCalls = append(c.DeleteOrphansCalls, repoName)
	return nil
}

func (c *ComponentRepo) UpdateExtra(_ context.Context, id string, extra map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.UpdateExtraErr != nil {
		return c.UpdateExtraErr
	}
	comp := c.components[id]
	if comp == nil {
		return nil
	}
	if comp.Extra == nil {
		comp.Extra = make(map[string]any)
	}
	for k, v := range extra {
		comp.Extra[k] = v
	}
	return nil
}

func (c *ComponentRepo) SetTags(_ context.Context, id string, tags []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return c.Err
	}
	comp, ok := c.components[id]
	if !ok {
		return fmt.Errorf("component not found: %s", id)
	}
	comp.Tags = tags
	return nil
}

func (c *ComponentRepo) AddComponent(comp *domain.Component) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.components[comp.ID] = comp
}

// ── AssetRepo ─────────────────────────────────────────────────

type AssetRepo struct {
	mu          sync.Mutex
	assets      map[string]*domain.Asset // key: "repo:path"
	byID        map[string]*domain.Asset
	nextID      int
	Stale       []domain.Asset // populated by tests to control ListStale output
	LastRetainN int
	// StaleRepeat, when true, makes ListStale return the full Stale slice on every
	// call without consuming it — simulating a dry run where nothing is deleted, so
	// the same rows keep matching. Used to prove the dry-run loop terminates.
	StaleRepeat bool
	// ListStaleCalls counts ListStale invocations so tests can assert the dry-run
	// path does a single pass instead of looping.
	ListStaleCalls int
	MigrationRows  []domain.MigrationAssetRow
	Err            error // when non-nil, ListByComponentID/ListByComponentIDs/SearchAssets/SumSizeByRepo return it (500-branch seam)
	// DeleteErr, when non-nil, makes Delete fail while leaving the row in place —
	// the seam for "the DB write failed after the bytes were already touched".
	DeleteErr error
	// CreateErr, when non-nil, makes Create fail without writing — the seam for
	// "the row insert failed after the blob bytes were already stored".
	CreateErr error
	// ListByComponentIDsCalls counts how many times ListByComponentIDs has been called.
	ListByComponentIDsCalls int
	// ListByComponentIDCalls counts how many times the singular ListByComponentID has been called.
	ListByComponentIDCalls int
	// RawRowsByRepo maps repoName→raw browse assets; ListRawBrowseAssets returns the
	// union for the requested repo names (mirrors the SQL WHERE rep.name IN (...)).
	RawRowsByRepo map[string][]domain.RawBrowseAsset
	RawBrowseErr  error // when non-nil, ListRawBrowseAssets returns it (500-branch seam)
	BrowseErr     error // when non-nil, ListByRepoAndPath/ListPathsByRepo/ListRawAssetPaths return it (500-branch seam)
	// GetByPathErr, when non-nil, makes GetByPath return it. Separate from Err so
	// a test can tell a lookup that failed from a path that does not exist —
	// callers that must not treat the two alike need exactly that distinction.
	GetByPathErr error
	// DownloadIncrements records aggregated counts passed to IncrementDownloads.
	DownloadIncrements map[string]int64
	// createdPaths records the order Create was called in; read via CreatedPaths.
	createdPaths []string
	// blobKeyLocks holds one mutex per blob key, mirroring the postgres advisory
	// lock: WithBlobKeyLock callers for the same key serialize, different keys
	// run concurrently.
	blobKeyLocks map[string]*sync.Mutex
	// BlobKeyLockKeys records the keys WithBlobKeyLock was called with, in
	// acquisition order, so tests can assert a call site takes the lock at all.
	BlobKeyLockKeys []string
	// CountByBlobKeyHook, when set, runs during CountByBlobKey after the count is
	// taken but before it is returned (outside the repo's own mutex) — the seam
	// race tests use to pause a caller exactly between its read and its act.
	CountByBlobKeyHook func(blobKey, excludeID string)
	// SumSizeByRepoHook, when set, runs during SumSizeByRepo after the sum is
	// taken but before it is returned (outside the repo's own mutex) — the seam
	// quota race tests use to line up concurrent uploads on one usage snapshot.
	SumSizeByRepoHook func(repoName string)
}

func NewAssetRepo() *AssetRepo {
	return &AssetRepo{
		assets: make(map[string]*domain.Asset),
		byID:   make(map[string]*domain.Asset),
	}
}

// Snapshot returns a copy of every stored asset row — the whole assets table,
// as a recompute over it would see.
func (a *AssetRepo) Snapshot() []domain.Asset {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]domain.Asset, 0, len(a.byID))
	for _, v := range a.byID {
		out = append(out, *v)
	}
	return out
}

// List returns the repository's assets. An empty repoName lists everything,
// so tests that never set Asset.Repository keep working; naming a repository
// filters to it, which is what a group of several members needs to behave like
// several members.
func (a *AssetRepo) List(_ context.Context, repoName string, _, _ int) (*domain.Page[domain.Asset], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := make([]domain.Asset, 0, len(a.byID))
	for _, v := range a.byID {
		if repoName != "" && v.Repository != "" && v.Repository != repoName {
			continue
		}
		items = append(items, *v)
	}
	return &domain.Page[domain.Asset]{Items: items}, nil
}
func (a *AssetRepo) Get(_ context.Context, id string) (*domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.byID[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (a *AssetRepo) GetByPath(_ context.Context, repoName, path string) (*domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.GetByPathErr != nil {
		return nil, a.GetByPathErr
	}
	v, ok := a.assets[repoName+":"+path]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (a *AssetRepo) SearchAssets(_ context.Context, params domain.SearchParams) (*domain.Page[domain.Asset], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Err != nil {
		return nil, a.Err
	}
	allow := make(map[string]struct{}, len(params.RepositoryNames))
	for _, n := range params.RepositoryNames {
		allow[n] = struct{}{}
	}
	items := make([]domain.Asset, 0, len(a.byID))
	for _, v := range a.byID {
		if params.Repository != "" && v.Repository != params.Repository {
			continue
		}
		if len(allow) > 0 {
			if _, ok := allow[v.Repository]; !ok {
				continue
			}
		}
		items = append(items, *v)
	}
	return &domain.Page[domain.Asset]{Items: items}, nil
}
func (a *AssetRepo) ListStale(_ context.Context, _ string, _ []string, _, _ int, _, _ string, retainNVersions int, limit int) ([]domain.Asset, error) {
	a.mu.Lock()
	a.LastRetainN = retainNVersions
	a.ListStaleCalls++
	defer a.mu.Unlock()
	if len(a.Stale) == 0 {
		return nil, nil
	}
	// A row the SQL scan returns exists in the table at scan time. Tests populate
	// Stale directly, so register each returned row as a live asset too — unless
	// the test already seeded a (possibly diverged) live row under that ID, which
	// is exactly how a changed-since-scan race is modeled.
	for i := range a.Stale {
		row := a.Stale[i]
		if _, ok := a.byID[row.ID]; !ok && row.ID != "" {
			live := row
			a.byID[row.ID] = &live
			a.assets[live.Repository+":"+live.Path] = &live
		}
	}
	// Non-consuming mode: return the same rows each call (dry-run has no deletes).
	if a.StaleRepeat {
		n := limit
		if n <= 0 || n > len(a.Stale) {
			n = len(a.Stale)
		}
		return append([]domain.Asset(nil), a.Stale[:n]...), nil
	}
	n := limit
	if n <= 0 {
		n = 500
	}
	if len(a.Stale) > n {
		batch := append([]domain.Asset(nil), a.Stale[:n]...)
		a.Stale = a.Stale[n:]
		return batch, nil
	}
	out := append([]domain.Asset(nil), a.Stale...)
	a.Stale = nil
	return out, nil
}
func (a *AssetRepo) Create(_ context.Context, asset *domain.Asset) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.CreateErr != nil {
		return a.CreateErr
	}
	// Mirror the postgres upsert: an existing (repo,path) keeps its ID and refreshes
	// last_modified, rather than creating a duplicate row.
	key := asset.Repository + ":" + asset.Path
	if existing, ok := a.assets[key]; ok {
		asset.ID = existing.ID
		if asset.CreatedAt.IsZero() {
			asset.CreatedAt = existing.CreatedAt
		}
	} else {
		a.nextID++
		asset.ID = fmt.Sprintf("asset-%d", a.nextID)
	}
	// The postgres impl stamps last_modified = NOW() on insert/upsert and created_at
	// on insert; the proxy cache relies on last_modified for freshness.
	now := time.Now()
	asset.LastModified = now
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = now
	}
	// Index by repo name (for GetByPath) — matches what postgres impl does via JOIN
	a.assets[key] = asset
	a.byID[asset.ID] = asset
	a.createdPaths = append(a.createdPaths, asset.Path)
	return nil
}

// SeedAsset inserts an asset preserving the ID the test chose — unlike Create,
// which mirrors the postgres upsert and always assigns its own. Tests use it to
// stage a live row that diverges from a scan snapshot carrying the same ID.
func (a *AssetRepo) SeedAsset(asset *domain.Asset) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.byID[asset.ID] = asset
	a.assets[asset.Repository+":"+asset.Path] = asset
}

// CreatedPaths returns the asset paths passed to Create, in call order. Tests
// that care about write ordering — a manifest must not be registered before the
// blobs it names — read it.
func (a *AssetRepo) CreatedPaths() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.createdPaths...)
}

// TouchLastModified sets the asset's LastModified to now (mirrors the postgres
// UPDATE used to extend proxy metadata freshness after a 304 revalidation).
func (a *AssetRepo) TouchLastModified(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := a.byID[id]; ok {
		v.LastModified = time.Now()
	}
	return nil
}
func (a *AssetRepo) Delete(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.DeleteErr != nil {
		return a.DeleteErr
	}
	asset, ok := a.byID[id]
	if !ok {
		return nil
	}
	delete(a.byID, id)
	// Keyed the same way Create keys it — by repository NAME. Keying the removal
	// off RepositoryID left the path index pointing at a deleted asset, so
	// GetByPath kept resolving it.
	delete(a.assets, asset.Repository+":"+asset.Path)
	return nil
}
func (a *AssetRepo) IncrementDownloads(_ context.Context, counts map[string]int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.DownloadIncrements == nil {
		a.DownloadIncrements = map[string]int64{}
	}
	for id, n := range counts {
		a.DownloadIncrements[id] += n
	}
	return nil
}

func (a *AssetRepo) ListByComponentID(_ context.Context, componentID string) ([]domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ListByComponentIDCalls++
	if a.Err != nil {
		return nil, a.Err
	}
	var out []domain.Asset
	for _, v := range a.byID {
		if v.ComponentID == componentID {
			cp := *v
			out = append(out, cp)
		}
	}
	return out, nil
}

func (a *AssetRepo) ListByComponentIDs(_ context.Context, componentIDs []string) (map[string][]domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ListByComponentIDsCalls++
	if a.Err != nil {
		return nil, a.Err
	}
	want := make(map[string]struct{}, len(componentIDs))
	for _, id := range componentIDs {
		want[id] = struct{}{}
	}
	out := make(map[string][]domain.Asset)
	for _, v := range a.byID {
		if _, ok := want[v.ComponentID]; ok {
			cp := *v
			out[v.ComponentID] = append(out[v.ComponentID], cp)
		}
	}
	// Sort each slice by path to match the postgres ORDER BY path behavior.
	for k := range out {
		slice := out[k]
		sort.Slice(slice, func(i, j int) bool { return slice[i].Path < slice[j].Path })
		out[k] = slice
	}
	return out, nil
}

func (a *AssetRepo) ListAllBlobRefs(_ context.Context) ([]domain.BlobRef, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := make(map[domain.BlobRef]struct{})
	for _, v := range a.byID {
		if v.BlobKey != "" {
			seen[domain.BlobRef{BlobKey: v.BlobKey, BlobStoreID: v.BlobStoreID}] = struct{}{}
		}
	}
	refs := make([]domain.BlobRef, 0, len(seen))
	for r := range seen {
		refs = append(refs, r)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].BlobKey != refs[j].BlobKey {
			return refs[i].BlobKey < refs[j].BlobKey
		}
		return refs[i].BlobStoreID < refs[j].BlobStoreID
	})
	return refs, nil
}

func (a *AssetRepo) SumSizeByRepo(_ context.Context, repoName string) (int64, error) {
	a.mu.Lock()
	if a.Err != nil {
		a.mu.Unlock()
		return 0, a.Err
	}
	// Mirrors the SQL: one size per distinct blob key, largest wins. Several
	// assets can name one stored object, and the repository holds it once.
	perKey := make(map[string]int64)
	for _, v := range a.byID {
		if v.Repository != repoName {
			continue
		}
		if v.SizeBytes > perKey[v.BlobKey] {
			perKey[v.BlobKey] = v.SizeBytes
		}
	}
	var total int64
	for _, size := range perKey {
		total += size
	}
	// The hook runs outside the repo mutex — it exists to hold a caller between
	// its usage read and its next act, and holding the mutex there would wedge
	// every other repo call instead of just this caller.
	hook := a.SumSizeByRepoHook
	a.mu.Unlock()
	if hook != nil {
		hook(repoName)
	}
	return total, nil
}

func (a *AssetRepo) ListPathsByRepo(_ context.Context, repoName, q string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.BrowseErr != nil {
		return nil, a.BrowseErr
	}
	seen := make(map[string]struct{})
	for _, asset := range a.byID {
		if asset.Repository != repoName {
			continue
		}
		// extract all directory prefixes from path
		p := asset.Path
		for {
			idx := strings.LastIndex(p, "/")
			if idx <= 0 {
				break
			}
			p = p[:idx+1]
			if q == "" || strings.Contains(strings.ToLower(p), strings.ToLower(q)) {
				seen[p] = struct{}{}
			}
			p = p[:idx]
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (a *AssetRepo) ListByRepoAndPath(_ context.Context, repoName, pathPrefix string) ([]domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.BrowseErr != nil {
		return nil, a.BrowseErr
	}
	var out []domain.Asset
	for _, asset := range a.byID {
		if asset.Repository == repoName && strings.HasPrefix(asset.Path, pathPrefix) {
			out = append(out, *asset)
		}
	}
	return out, nil
}

func (a *AssetRepo) ListRawBrowseAssets(_ context.Context, names []string) ([]domain.RawBrowseAsset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.RawBrowseErr != nil {
		return nil, a.RawBrowseErr
	}
	var out []domain.RawBrowseAsset
	for _, n := range names {
		out = append(out, a.RawRowsByRepo[n]...)
	}
	return out, nil
}

// ListOCIImageNames mirrors the SQL: the distinct <name> of every
// /manifests/<name>/<reference> asset in the named repositories, unordered.
// Assets stored under any other prefix — every blob — name no image.
func (a *AssetRepo) ListOCIImageNames(_ context.Context, repoNames []string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.BrowseErr != nil {
		return nil, a.BrowseErr
	}
	if len(repoNames) == 0 {
		return nil, nil
	}
	allow := make(map[string]struct{}, len(repoNames))
	for _, n := range repoNames {
		allow[n] = struct{}{}
	}
	seen := make(map[string]struct{})
	var out []string
	for _, asset := range a.byID {
		if _, ok := allow[asset.Repository]; !ok {
			continue
		}
		rest, ok := strings.CutPrefix(asset.Path, "/manifests/")
		if !ok {
			continue
		}
		// The reference is the last segment, exactly as the SQL regexp's greedy
		// group leaves it: a path with no reference names no image.
		idx := strings.LastIndex(rest, "/")
		if idx <= 0 {
			continue
		}
		name := rest[:idx]
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

// CountByBlobKey mirrors the postgres query: how many assets reference blobKey,
// not counting the one being excluded. DeleteArtifact reads it to decide whether
// the physical blob is still needed, so a stub answer would make that decision
// untestable.
func (a *AssetRepo) CountByBlobKey(_ context.Context, blobKey, excludeID string) (int, error) {
	a.mu.Lock()
	if a.Err != nil {
		a.mu.Unlock()
		return 0, a.Err
	}
	n := 0
	for _, v := range a.byID {
		if v.BlobKey == blobKey && v.ID != excludeID {
			n++
		}
	}
	// The hook runs outside the repo mutex: it exists to block a caller between
	// its count and its next act, and holding the mutex there would wedge every
	// other repo call instead of just this caller.
	hook := a.CountByBlobKeyHook
	a.mu.Unlock()
	if hook != nil {
		hook(blobKey, excludeID)
	}
	return n, nil
}

// DeleteIfUnchanged mirrors the postgres conditional delete: the row goes only
// if it still carries the scanned blob key, last_downloaded (NULL-safe) and
// last_modified.
func (a *AssetRepo) DeleteIfUnchanged(_ context.Context, id, blobKey string, lastDownloaded *time.Time, lastModified time.Time) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.DeleteErr != nil {
		return false, a.DeleteErr
	}
	v, ok := a.byID[id]
	if !ok || v.BlobKey != blobKey || !v.LastModified.Equal(lastModified) {
		return false, nil
	}
	switch {
	case v.LastDownloaded == nil && lastDownloaded == nil:
	case v.LastDownloaded != nil && lastDownloaded != nil && v.LastDownloaded.Equal(*lastDownloaded):
	default:
		return false, nil
	}
	delete(a.byID, id)
	delete(a.assets, v.Repository+":"+v.Path)
	return true, nil
}

// WithBlobKeyLock mirrors the postgres advisory lock with one in-process mutex
// per key: same-key callers serialize, different keys do not block each other.
func (a *AssetRepo) WithBlobKeyLock(ctx context.Context, blobKey string, fn func(ctx context.Context) error) error {
	a.mu.Lock()
	if a.blobKeyLocks == nil {
		a.blobKeyLocks = make(map[string]*sync.Mutex)
	}
	l, ok := a.blobKeyLocks[blobKey]
	if !ok {
		l = &sync.Mutex{}
		a.blobKeyLocks[blobKey] = l
	}
	a.mu.Unlock()
	l.Lock()
	defer l.Unlock()
	a.mu.Lock()
	a.BlobKeyLockKeys = append(a.BlobKeyLockKeys, blobKey)
	a.mu.Unlock()
	return fn(ctx)
}

// CountByBlobKeyInStore mirrors the postgres query: how many assets reference
// blobKey and still live on blobStoreID. The blob-store migration reads it
// before deleting the source copy, so a stub answer would make that untestable.
func (a *AssetRepo) CountByBlobKeyInStore(_ context.Context, blobKey, blobStoreID string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Err != nil {
		return 0, a.Err
	}
	n := 0
	for _, v := range a.byID {
		if v.BlobKey == blobKey && v.BlobStoreID == blobStoreID {
			n++
		}
	}
	return n, nil
}

func (a *AssetRepo) ListRawAssetPaths(_ context.Context, repoName string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.BrowseErr != nil {
		return nil, a.BrowseErr
	}
	seen := make(map[string]struct{})
	for _, asset := range a.byID {
		if asset.Repository == repoName {
			seen[asset.Path] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (a *AssetRepo) ListForBlobStoreMigration(_ context.Context, _, _ string) ([]domain.MigrationAssetRow, error) {
	return a.MigrationRows, nil
}

// UpdateBlobStoreForBlobKey mirrors the postgres UPDATE: every asset of that
// repository sharing the key is repointed at the new store. The migration
// decides whether the source copy may be deleted from what this leaves behind,
// so the old no-op made that decision untestable.
func (a *AssetRepo) UpdateBlobStoreForBlobKey(_ context.Context, blobKey, repoName, newBlobStoreID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Err != nil {
		return a.Err
	}
	for _, v := range a.byID {
		if v.BlobKey == blobKey && v.Repository == repoName {
			v.BlobStoreID = newBlobStoreID
		}
	}
	return nil
}

// ── CleanupPolicyRepo ─────────────────────────────────────────

type CleanupPolicyRepo struct {
	mu       sync.Mutex
	policies map[string]*domain.CleanupPolicy
	nextID   int
	Updates  []*domain.CleanupPolicy // records Update calls
	// RunRecords records RecordRun calls (run stats persisted after a policy run).
	RunRecords []CleanupRunRecord
	Err        error // when set, List/Get/Create/Update/Delete return it (500-branch seam)
}

// CleanupRunRecord captures the arguments of a RecordRun call.
type CleanupRunRecord struct {
	ID    string
	At    time.Time
	Count int
	Freed int64
}

func NewCleanupPolicyRepo(policies ...*domain.CleanupPolicy) *CleanupPolicyRepo {
	r := &CleanupPolicyRepo{policies: make(map[string]*domain.CleanupPolicy)}
	for _, p := range policies {
		r.policies[p.ID] = p
	}
	return r
}

func (r *CleanupPolicyRepo) List(_ context.Context) ([]domain.CleanupPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	out := make([]domain.CleanupPolicy, 0, len(r.policies))
	for _, v := range r.policies {
		out = append(out, *v)
	}
	return out, nil
}
func (r *CleanupPolicyRepo) Get(_ context.Context, id string) (*domain.CleanupPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	return r.policies[id], nil
}
func (r *CleanupPolicyRepo) Create(_ context.Context, p *domain.CleanupPolicy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.nextID++
	p.ID = fmt.Sprintf("policy-%d", r.nextID)
	r.policies[p.ID] = p
	return nil
}
func (r *CleanupPolicyRepo) Update(_ context.Context, p *domain.CleanupPolicy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.policies[p.ID] = p
	r.Updates = append(r.Updates, p)
	return nil
}
func (r *CleanupPolicyRepo) RecordRun(_ context.Context, id string, at time.Time, count int, freed int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.RunRecords = append(r.RunRecords, CleanupRunRecord{ID: id, At: at, Count: count, Freed: freed})
	if p, ok := r.policies[id]; ok {
		p.LastRunAt = &at
		p.LastRunCount = count
		p.LastRunFreed = freed
	}
	return nil
}
func (r *CleanupPolicyRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	delete(r.policies, id)
	return nil
}

// ── BlobStore (storage) ───────────────────────────────────────

type BlobStore struct {
	mu      sync.Mutex
	blobs   map[string][]byte
	mtimes  map[string]time.Time
	Deleted []string // records Delete calls
}

func NewBlobStore() *BlobStore {
	return &BlobStore{blobs: make(map[string][]byte), mtimes: make(map[string]time.Time)}
}

func (b *BlobStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blobs[key] = data
	b.mtimes[key] = time.Now()
	return nil
}
func (b *BlobStore) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.blobs[key]
	if !ok {
		return nil, 0, fmt.Errorf("blob not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}
func (b *BlobStore) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.blobs, key)
	b.Deleted = append(b.Deleted, key)
	return nil
}
func (b *BlobStore) Exists(_ context.Context, key string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.blobs[key]
	return ok, nil
}
func (b *BlobStore) Size(_ context.Context, key string) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.blobs[key]
	if !ok {
		return 0, fmt.Errorf("blob not found: %s", key)
	}
	return int64(len(data)), nil
}
func (b *BlobStore) UsedBytes(_ context.Context) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var total int64
	for _, data := range b.blobs {
		total += int64(len(data))
	}
	return total, nil
}
func (b *BlobStore) ListKeys(_ context.Context) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := make([]string, 0, len(b.blobs))
	for k := range b.blobs {
		keys = append(keys, k)
	}
	return keys, nil
}

// SetMTime overrides the recorded modification time for a key (test helper).
func (b *BlobStore) SetMTime(key string, t time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mtimes[key] = t
}

func (b *BlobStore) ListEntries(_ context.Context) ([]storage.BlobEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entries := make([]storage.BlobEntry, 0, len(b.blobs))
	for k, data := range b.blobs {
		entries = append(entries, storage.BlobEntry{
			Key:     k,
			Size:    int64(len(data)),
			ModTime: b.mtimes[k],
		})
	}
	return entries, nil
}
func (b *BlobStore) Has(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.blobs[key]
	return ok
}
func (b *BlobStore) Read(key string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.blobs[key]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return string(data), nil
}

// ── FakeResolver ──────────────────────────────────────────────

// FakeResolver returns a fixed BlobStore for any descriptor. Implements
// service.StoreResolver for GC tests.
type FakeResolver struct{ Store storage.BlobStore }

func NewFakeResolver(s storage.BlobStore) FakeResolver { return FakeResolver{Store: s} }

func (f FakeResolver) Get(_ context.Context, _ storage.BlobStoreDescriptor) (storage.BlobStore, error) {
	return f.Store, nil
}

// ── HeldLocker ────────────────────────────────────────────────

// HeldLocker always reports the lock as held by another caller.
type HeldLocker struct{}

func NewHeldLocker() HeldLocker { return HeldLocker{} }

func (HeldLocker) Acquire(_ context.Context, _ string, _ time.Duration) (distlock.Lock, error) {
	return nil, distlock.ErrLockHeld
}

func (HeldLocker) ForceRelease(_ context.Context, _ string) error { return nil }

// ── Helpers ───────────────────────────────────────────────────

// SimpleRepo returns a hosted repository ready for use in tests.
func SimpleRepo(name, format string) *domain.Repository {
	return &domain.Repository{
		ID:     "repo-" + name,
		Name:   name,
		Format: domain.RepoFormat(format),
		Type:   domain.TypeHosted,
		Online: true,
	}
}

// MakeReader returns an io.Reader wrapping the given string.
func MakeReader(s string) io.Reader { return strings.NewReader(s) }

// ── AuditRepo ─────────────────────────────────────────────────

type AuditRepo struct {
	mu     sync.Mutex
	Events []domain.AuditEvent
}

func NewAuditRepo() *AuditRepo { return &AuditRepo{} }

func (a *AuditRepo) Write(_ context.Context, e *domain.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Events = append(a.Events, *e)
	return nil
}

// Snapshot returns a copy of the current events slice under the mutex,
// safe to read from test goroutines concurrently with Write calls.
func (a *AuditRepo) Snapshot() []domain.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]domain.AuditEvent, len(a.Events))
	copy(out, a.Events)
	return out
}

func (a *AuditRepo) match(e domain.AuditEvent, q repository.AuditQuery) bool {
	if q.Domain != "" && e.Domain != q.Domain {
		return false
	}
	if q.Action != "" && e.Action != q.Action {
		return false
	}
	if q.Username != "" && e.Username != q.Username {
		return false
	}
	if q.From != nil && e.EventTime.Before(*q.From) {
		return false
	}
	if q.To != nil && !e.EventTime.Before(*q.To) {
		return false
	}
	return true
}

func (a *AuditRepo) List(_ context.Context, q repository.AuditQuery) ([]domain.AuditEvent, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var matched []domain.AuditEvent
	for _, e := range a.Events {
		if a.match(e, q) {
			matched = append(matched, e)
		}
	}
	total := len(matched)
	if q.Offset >= total {
		return nil, total, nil
	}
	matched = matched[q.Offset:]
	if q.Limit > 0 && len(matched) > q.Limit {
		matched = matched[:q.Limit]
	}
	return matched, total, nil
}

func (a *AuditRepo) Stream(_ context.Context, q repository.AuditQuery, fn func(domain.AuditEvent) error) error {
	a.mu.Lock()
	snapshot := append([]domain.AuditEvent(nil), a.Events...)
	a.mu.Unlock()
	// Mirror the SQL: limit/offset apply when the caller set them; Limit 0
	// means "not specified" and streams everything.
	skipped, sent := 0, 0
	for _, e := range snapshot {
		if !a.match(e, q) {
			continue
		}
		if skipped < q.Offset {
			skipped++
			continue
		}
		if q.Limit > 0 && sent >= q.Limit {
			break
		}
		if err := fn(e); err != nil {
			return err
		}
		sent++
	}
	return nil
}

// ── UserRepo ──────────────────────────────────────────────────

type UserRepo struct {
	mu         sync.Mutex
	users      map[string]*domain.User // key: username
	byID       map[string]*domain.User
	nextID     int
	oidcTokens map[string]string // userID → id_token
	Err        error             // when non-nil, mutating methods return this error
}

func NewUserRepo(users ...*domain.User) *UserRepo {
	r := &UserRepo{
		users:      make(map[string]*domain.User),
		byID:       make(map[string]*domain.User),
		oidcTokens: make(map[string]string),
	}
	for _, u := range users {
		r.users[u.Username] = u
		r.byID[u.ID] = u
	}
	return r
}

func (r *UserRepo) List(_ context.Context, _ string) ([]domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	out := make([]domain.User, 0, len(r.users))
	for _, u := range r.users {
		out = append(out, *u)
	}
	return out, nil
}
func (r *UserRepo) Get(_ context.Context, username string) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	v, ok := r.users[username]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (r *UserRepo) GetByID(_ context.Context, id string) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	v, ok := r.byID[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (r *UserRepo) Create(_ context.Context, u *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	// Mirror the real table's constraints: username is unique, and email is
	// unique only when non-empty (users_email_nonempty_key is a partial index,
	// so any number of email-less users is fine).
	if _, exists := r.users[u.Username]; exists {
		return &repository.UniqueViolationError{Field: "username"}
	}
	if err := r.checkEmailFreeLocked(u.Email, u.Username); err != nil {
		return err
	}
	r.nextID++
	if u.ID == "" {
		u.ID = fmt.Sprintf("user-%d", r.nextID)
	}
	r.users[u.Username] = u
	r.byID[u.ID] = u
	return nil
}

// checkEmailFreeLocked reports the users_email_nonempty_key violation that the
// real table would raise. Caller must hold r.mu.
func (r *UserRepo) checkEmailFreeLocked(email, ownUsername string) error {
	if email == "" {
		return nil
	}
	for name, existing := range r.users {
		if name != ownUsername && existing.Email == email {
			return &repository.UniqueViolationError{Field: "email"}
		}
	}
	return nil
}

func (r *UserRepo) Update(_ context.Context, u *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	if err := r.checkEmailFreeLocked(u.Email, u.Username); err != nil {
		return err
	}
	r.users[u.Username] = u
	r.byID[u.ID] = u
	return nil
}
func (r *UserRepo) UpdatePassword(_ context.Context, username, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.users[username]; ok {
		u.PasswordHash = hash
	}
	return nil
}
func (r *UserRepo) Delete(_ context.Context, username string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	if u, ok := r.users[username]; ok {
		delete(r.byID, u.ID)
		delete(r.users, username)
	}
	return nil
}
func (r *UserRepo) UpdateLastLogin(_ context.Context, _ string) error { return nil }

func (r *UserRepo) BumpTokensValidAfter(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	if u, ok := r.byID[userID]; ok {
		u.TokensValidAfter = time.Now()
	}
	return nil
}

func (r *UserRepo) SetOIDCTokens(_ context.Context, userID, idToken, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if idToken == "" {
		delete(r.oidcTokens, userID)
	} else {
		r.oidcTokens[userID] = idToken
	}
	return nil
}

func (r *UserRepo) GetOIDCIDToken(_ context.Context, userID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.oidcTokens[userID], nil
}

// ── RoleRepo ──────────────────────────────────────────────────

type RoleRepo struct {
	mu               sync.Mutex
	roles            map[string]*domain.Role
	userRoles        map[string][]string // userID → roleIDs
	rolePrivileges   map[string][]string // roleID → privilegeIDs
	nextID           int
	Err              error // when non-nil, mutating/listing methods return this error
	SetPrivilegesErr error // when non-nil, SetPrivileges returns this error (independent of Err)
}

func NewRoleRepo(roles ...*domain.Role) *RoleRepo {
	r := &RoleRepo{
		roles:          make(map[string]*domain.Role),
		userRoles:      make(map[string][]string),
		rolePrivileges: make(map[string][]string),
	}
	for _, role := range roles {
		r.roles[role.ID] = role
	}
	return r
}

func (r *RoleRepo) List(_ context.Context) ([]domain.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	out := make([]domain.Role, 0, len(r.roles))
	for _, v := range r.roles {
		out = append(out, *v)
	}
	return out, nil
}
func (r *RoleRepo) Get(_ context.Context, id string) (*domain.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.roles[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (r *RoleRepo) GetByName(_ context.Context, name string) (*domain.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.roles {
		if v.Name == name {
			return v, nil
		}
	}
	return nil, repository.ErrNotFound
}
func (r *RoleRepo) Create(_ context.Context, role *domain.Role) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.nextID++
	if role.ID == "" {
		role.ID = fmt.Sprintf("role-%d", r.nextID)
	}
	r.roles[role.ID] = role
	return nil
}
func (r *RoleRepo) Update(_ context.Context, role *domain.Role) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.roles[role.ID] = role
	return nil
}
func (r *RoleRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	delete(r.roles, id)
	return nil
}
func (r *RoleRepo) GetUserRoles(_ context.Context, userID string) ([]domain.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	var out []domain.Role
	for _, rid := range r.userRoles[userID] {
		if role, ok := r.roles[rid]; ok {
			out = append(out, *role)
		}
	}
	return out, nil
}
func (r *RoleRepo) SetUserRoles(_ context.Context, userID string, roleIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.userRoles[userID] = append([]string(nil), roleIDs...)
	return nil
}

func (r *RoleRepo) SetPrivileges(_ context.Context, roleID string, privilegeIDs []string) error {
	if r.SetPrivilegesErr != nil {
		return r.SetPrivilegesErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.rolePrivileges[roleID] = append([]string(nil), privilegeIDs...)
	return nil
}

func (r *RoleRepo) ListPrivilegeIDsByRole(_ context.Context, roleID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.rolePrivileges[roleID]...), nil
}

// PrivilegesOf returns the privilege IDs last assigned to the role.
func (r *RoleRepo) PrivilegesOf(roleID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.rolePrivileges[roleID]...)
}

// UserRoleIDs returns the role IDs last assigned to the user.
func (r *RoleRepo) UserRoleIDs(userID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.userRoles[userID]...)
}

// ── ContentSelectorRepo ───────────────────────────────────────

type ContentSelectorRepo struct {
	mu                sync.Mutex
	selectors         map[string]*domain.ContentSelector
	nextID            int
	PrivilegeSelector map[string]string   // privilegeName → selectorID
	UserSelectors     map[string][]string // userID → []selectorID
}

func NewContentSelectorRepo() *ContentSelectorRepo {
	return &ContentSelectorRepo{
		selectors:         make(map[string]*domain.ContentSelector),
		PrivilegeSelector: make(map[string]string),
		UserSelectors:     make(map[string][]string),
	}
}

func (r *ContentSelectorRepo) List(_ context.Context) ([]domain.ContentSelector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.ContentSelector, 0, len(r.selectors))
	for _, v := range r.selectors {
		out = append(out, *v)
	}
	return out, nil
}
func (r *ContentSelectorRepo) Get(_ context.Context, id string) (*domain.ContentSelector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.selectors[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (r *ContentSelectorRepo) GetByName(_ context.Context, name string) (*domain.ContentSelector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.selectors {
		if v.Name == name {
			cp := *v
			return &cp, nil
		}
	}
	return nil, repository.ErrNotFound
}
func (r *ContentSelectorRepo) Create(_ context.Context, s *domain.ContentSelector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	s.ID = fmt.Sprintf("cs-%d", r.nextID)
	cp := *s
	r.selectors[s.ID] = &cp
	return nil
}
func (r *ContentSelectorRepo) Update(_ context.Context, s *domain.ContentSelector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.selectors[s.ID] = &cp
	return nil
}
func (r *ContentSelectorRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.selectors, id)
	return nil
}
func (r *ContentSelectorRepo) ListForUser(_ context.Context, userID string) ([]domain.ContentSelector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.ContentSelector
	for _, id := range r.UserSelectors[userID] {
		if s, ok := r.selectors[id]; ok {
			out = append(out, *s)
		}
	}
	return out, nil
}
func (r *ContentSelectorRepo) AttachToPrivilege(_ context.Context, privilegeName, selectorID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.PrivilegeSelector[privilegeName] = selectorID
	return nil
}
func (r *ContentSelectorRepo) DetachFromPrivilege(_ context.Context, privilegeName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.PrivilegeSelector, privilegeName)
	return nil
}

// ── UserTokenRepo ─────────────────────────────────────────────

type UserTokenRepo struct {
	mu     sync.Mutex
	tokens map[string]*domain.UserToken // key: ID
	byHash map[string]*domain.UserToken // key: TokenHash
	nextID int
}

func NewUserTokenRepo() *UserTokenRepo {
	return &UserTokenRepo{
		tokens: make(map[string]*domain.UserToken),
		byHash: make(map[string]*domain.UserToken),
	}
}

func (r *UserTokenRepo) ListByUser(_ context.Context, userID string) ([]domain.UserToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.UserToken
	for _, t := range r.tokens {
		if t.UserID == userID {
			out = append(out, *t)
		}
	}
	return out, nil
}
func (r *UserTokenRepo) Get(_ context.Context, id string) (*domain.UserToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.tokens[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (r *UserTokenRepo) GetByHash(_ context.Context, hash string) (*domain.UserToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.byHash[hash]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (r *UserTokenRepo) Create(_ context.Context, t *domain.UserToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	if t.ID == "" {
		t.ID = fmt.Sprintf("tok-%d", r.nextID)
	}
	cp := *t
	r.tokens[t.ID] = &cp
	if t.TokenHash != "" {
		r.byHash[t.TokenHash] = &cp
	}
	return nil
}
func (r *UserTokenRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tokens[id]; ok {
		delete(r.byHash, t.TokenHash)
		delete(r.tokens, id)
	}
	return nil
}
func (r *UserTokenRepo) TouchLastUsed(_ context.Context, _ string) error { return nil }

// ── WebhookRepo ───────────────────────────────────────────────

type WebhookRepo struct {
	mu       sync.Mutex
	webhooks map[string]*domain.Webhook
	nextID   int
}

func NewWebhookRepo() *WebhookRepo {
	return &WebhookRepo{webhooks: make(map[string]*domain.Webhook)}
}

func (r *WebhookRepo) List(_ context.Context) ([]domain.Webhook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Webhook, 0, len(r.webhooks))
	for _, v := range r.webhooks {
		out = append(out, *v)
	}
	return out, nil
}
func (r *WebhookRepo) Get(_ context.Context, id string) (*domain.Webhook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.webhooks[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (r *WebhookRepo) ListByEvent(_ context.Context, event domain.WebhookEvent) ([]domain.Webhook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Webhook
	for _, v := range r.webhooks {
		for _, e := range v.Events {
			if e == event {
				out = append(out, *v)
				break
			}
		}
	}
	return out, nil
}
func (r *WebhookRepo) Create(_ context.Context, w *domain.Webhook) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	if w.ID == "" {
		w.ID = fmt.Sprintf("wh-%d", r.nextID)
	}
	cp := *w
	r.webhooks[w.ID] = &cp
	return nil
}
func (r *WebhookRepo) Update(_ context.Context, w *domain.Webhook) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *w
	r.webhooks[w.ID] = &cp
	return nil
}
func (r *WebhookRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.webhooks, id)
	return nil
}

// ── RoutingRuleRepo ───────────────────────────────────────────

type RoutingRuleRepo struct {
	mu     sync.Mutex
	rules  map[string]*domain.RoutingRule
	nextID int
	Err    error // when non-nil, all methods return this error
}

func NewRoutingRuleRepo() *RoutingRuleRepo {
	return &RoutingRuleRepo{rules: make(map[string]*domain.RoutingRule)}
}

func (r *RoutingRuleRepo) List(_ context.Context) ([]domain.RoutingRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	out := make([]domain.RoutingRule, 0, len(r.rules))
	for _, v := range r.rules {
		out = append(out, *v)
	}
	return out, nil
}
func (r *RoutingRuleRepo) Get(_ context.Context, id string) (*domain.RoutingRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	v, ok := r.rules[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return v, nil
}
func (r *RoutingRuleRepo) GetByName(_ context.Context, name string) (*domain.RoutingRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	for _, v := range r.rules {
		if v.Name == name {
			cp := *v
			return &cp, nil
		}
	}
	return nil, repository.ErrNotFound
}
func (r *RoutingRuleRepo) Create(_ context.Context, rr *domain.RoutingRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.nextID++
	if rr.ID == "" {
		rr.ID = fmt.Sprintf("rr-%d", r.nextID)
	}
	cp := *rr
	r.rules[rr.ID] = &cp
	return nil
}
func (r *RoutingRuleRepo) Update(_ context.Context, rr *domain.RoutingRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	cp := *rr
	r.rules[rr.ID] = &cp
	return nil
}
func (r *RoutingRuleRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	delete(r.rules, id)
	return nil
}

// ── PrivilegeRepo ─────────────────────────────────────────────

type PrivilegeRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Privilege
	Err  error // when non-nil, all methods return this error
}

func NewPrivilegeRepo(items ...*domain.Privilege) *PrivilegeRepo {
	r := &PrivilegeRepo{data: make(map[string]*domain.Privilege)}
	for _, p := range items {
		r.data[p.ID] = p
	}
	return r
}

func (r *PrivilegeRepo) List(_ context.Context) ([]domain.Privilege, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	out := make([]domain.Privilege, 0, len(r.data))
	for _, p := range r.data {
		out = append(out, *p)
	}
	return out, nil
}

func (r *PrivilegeRepo) Get(_ context.Context, id string) (*domain.Privilege, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	p, ok := r.data[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *PrivilegeRepo) GetByName(_ context.Context, name string) (*domain.Privilege, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	for _, p := range r.data {
		if p.Name == name {
			cp := *p
			return &cp, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (r *PrivilegeRepo) Create(_ context.Context, p *domain.Privilege) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	// Mirror migration 007's check constraint: a repository-content-selector
	// privilege must name its selector. A mock that accepts the row hides
	// exactly the failure every such privilege hits against real Postgres.
	if p.Type == domain.PrivilegeTypeRepositoryContentSelector && (p.ContentSelectorID == nil || *p.ContentSelectorID == "") {
		return fmt.Errorf(`new row for relation "privileges" violates check constraint "privileges_selector_required"`)
	}
	if p.ID == "" {
		p.ID = fmt.Sprintf("priv-%d", len(r.data)+1)
	}
	cp := *p
	r.data[p.ID] = &cp
	return nil
}

func (r *PrivilegeRepo) Update(_ context.Context, p *domain.Privilege) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	if _, ok := r.data[p.ID]; !ok {
		return fmt.Errorf("privilege not found: %s", p.ID)
	}
	cp := *p
	r.data[p.ID] = &cp
	return nil
}

func (r *PrivilegeRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	delete(r.data, id)
	return nil
}

func (r *PrivilegeRepo) ListByRole(_ context.Context, _ string) ([]domain.Privilege, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	return []domain.Privilege{}, nil
}

func (r *PrivilegeRepo) PrivilegeRoleMap(_ context.Context) (map[string][]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	return map[string][]string{}, nil
}

// ── BlobStoreMigrationRepo ────────────────────────────────────

type BlobStoreMigrationRepo struct {
	mu         sync.Mutex
	migrations map[string]*domain.BlobStoreMigration

	// CreateErr, when set, makes Create fail — for exercising what a caller
	// leaves behind when the migration row cannot be written.
	CreateErr error
}

func NewBlobStoreMigrationRepo(ms ...*domain.BlobStoreMigration) *BlobStoreMigrationRepo {
	r := &BlobStoreMigrationRepo{migrations: make(map[string]*domain.BlobStoreMigration)}
	for _, m := range ms {
		r.migrations[m.ID] = m
	}
	return r
}

func (r *BlobStoreMigrationRepo) Create(_ context.Context, m *domain.BlobStoreMigration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.CreateErr != nil {
		return r.CreateErr
	}
	// Mirror migration 027's partial unique index: at most one pending/running
	// migration per repository, with finished rows unconstrained.
	if m.Status == "pending" || m.Status == "running" {
		for _, existing := range r.migrations {
			if existing.RepositoryName == m.RepositoryName &&
				(existing.Status == "pending" || existing.Status == "running") {
				return &repository.UniqueViolationError{Field: "repository_name"}
			}
		}
	}
	if m.ID == "" {
		m.ID = fmt.Sprintf("mig-%d", len(r.migrations)+1)
	}
	cp := *m
	r.migrations[m.ID] = &cp
	return nil
}

func (r *BlobStoreMigrationRepo) Get(_ context.Context, id string) (*domain.BlobStoreMigration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.migrations[id]
	if m == nil {
		return nil, repository.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (r *BlobStoreMigrationRepo) GetActiveByRepo(_ context.Context, repoName string) (*domain.BlobStoreMigration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.migrations {
		if m.RepositoryName == repoName && (m.Status == "pending" || m.Status == "running") {
			cp := *m
			return &cp, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (r *BlobStoreMigrationRepo) GetLatestByRepo(_ context.Context, repoName string) (*domain.BlobStoreMigration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest *domain.BlobStoreMigration
	for _, m := range r.migrations {
		if m.RepositoryName == repoName {
			if latest == nil || m.CreatedAt.After(latest.CreatedAt) {
				latest = m
			}
		}
	}
	if latest == nil {
		return nil, repository.ErrNotFound
	}
	cp := *latest
	return &cp, nil
}

func (r *BlobStoreMigrationRepo) SetTotals(_ context.Context, id string, total int, totalBytes int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := r.migrations[id]; m != nil {
		m.TotalAssets = total
		m.TotalBytes = totalBytes
	}
	return nil
}

func (r *BlobStoreMigrationRepo) UpdateProgress(_ context.Context, id string, done int, doneBytes int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := r.migrations[id]; m != nil {
		m.DoneAssets = done
		m.DoneBytes = doneBytes
	}
	return nil
}

func (r *BlobStoreMigrationRepo) UpdateStatus(_ context.Context, id string, status string, errMsg *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := r.migrations[id]; m != nil {
		m.Status = status
		m.ErrorMessage = errMsg
	}
	return nil
}

func (r *BlobStoreMigrationRepo) FinishMigration(_ context.Context, id string, status string, errMsg *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := r.migrations[id]; m != nil {
		m.Status = status
		m.ErrorMessage = errMsg
	}
	return nil
}

func (r *BlobStoreMigrationRepo) ListActive(_ context.Context) ([]domain.BlobStoreMigration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.BlobStoreMigration
	for _, m := range r.migrations {
		if m.Status == "pending" || m.Status == "running" {
			out = append(out, *m)
		}
	}
	return out, nil
}

// ── ScanResultRepo ─────────────────────────────────────────────

type ScanResultRepo struct {
	mu   sync.Mutex
	rows []*domain.ScanResultRow
	Err  error // when set, Aggregate/List return it (500-branch seam)
	// InsertErr is returned by Insert when set — the seam for a scan whose history
	// row cannot be written.
	InsertErr error
	// VulnRows is returned by List (with len as total) when non-nil; lets tests assert
	// the Vulnerabilities handler's success body without a live DB.
	VulnRows []*domain.VulnRow
	// LastFilter records the filter of the most recent List call, so a test can
	// assert what the handler asked for — paging in particular, which the mock
	// does not itself apply.
	LastFilter domain.VulnFilter
}

func NewScanResultRepo() *ScanResultRepo { return &ScanResultRepo{} }

func (r *ScanResultRepo) Insert(_ context.Context, row *domain.ScanResultRow) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.InsertErr != nil {
		return r.InsertErr
	}
	cp := *row
	r.rows = append(r.rows, &cp)
	return nil
}

func (r *ScanResultRepo) GetLatestByComponent(_ context.Context, componentID string) (*domain.ScanResultRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest *domain.ScanResultRow
	for _, row := range r.rows {
		if row.ComponentID == componentID {
			if latest == nil || row.ScannedAt.After(latest.ScannedAt) {
				cp := *row
				latest = &cp
			}
		}
	}
	if latest == nil {
		return nil, repository.ErrNotFound
	}
	return latest, nil
}

func (r *ScanResultRepo) Aggregate(_ context.Context) (*domain.SecuritySummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	// latest scan per component
	latest := map[string]*domain.ScanResultRow{}
	for _, row := range r.rows {
		prev, ok := latest[row.ComponentID]
		if !ok || row.ScannedAt.After(prev.ScannedAt) {
			cp := *row
			latest[row.ComponentID] = &cp
		}
	}
	s := &domain.SecuritySummary{ScannedTotal: len(latest)}
	for _, row := range latest {
		s.Malicious += row.Malicious
		s.Critical += row.Critical
		s.High += row.High
		s.Medium += row.Medium
		s.Low += row.Low
		s.Unknown += row.Unknown
	}
	return s, nil
}

func (r *ScanResultRepo) List(_ context.Context, f domain.VulnFilter) ([]*domain.VulnRow, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.LastFilter = f
	if r.Err != nil {
		return nil, 0, r.Err
	}
	if r.VulnRows != nil {
		return r.VulnRows, len(r.VulnRows), nil
	}
	return nil, 0, nil
}

// Rows returns all inserted scan rows (for assertions in tests).
func (r *ScanResultRepo) Rows() []*domain.ScanResultRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]*domain.ScanResultRow, len(r.rows))
	copy(cp, r.rows)
	return cp
}

// ── ReplicationRepo ──────────────────────────────────────────────

type ReplicationRepo struct {
	mu      sync.Mutex
	rules   map[string]*domain.ReplicationRule
	history []domain.ReplicationHistory
}

func NewReplicationRepo() *ReplicationRepo {
	return &ReplicationRepo{rules: make(map[string]*domain.ReplicationRule)}
}

func (r *ReplicationRepo) ListRules(_ context.Context) ([]domain.ReplicationRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.ReplicationRule, 0, len(r.rules))
	for _, v := range r.rules {
		out = append(out, *v)
	}
	return out, nil
}

func (r *ReplicationRepo) GetRule(_ context.Context, id string) (*domain.ReplicationRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.rules[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	cp := *v
	return &cp, nil
}

func (r *ReplicationRepo) CreateRule(_ context.Context, rule *domain.ReplicationRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule.ID = fmt.Sprintf("rule-%d", len(r.rules)+1)
	rule.CreatedAt = time.Now()
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}

func (r *ReplicationRepo) UpdateRule(_ context.Context, rule *domain.ReplicationRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rules[rule.ID]; !ok {
		return fmt.Errorf("rule not found: %s", rule.ID)
	}
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}

func (r *ReplicationRepo) DeleteRule(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rules, id)
	return nil
}

func (r *ReplicationRepo) UpdateRuleStatus(_ context.Context, id, status string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.rules[id]; ok {
		v.LastRunStatus = status
		v.LastRunAt = &at
	}
	return nil
}

func (r *ReplicationRepo) AddHistory(_ context.Context, h *domain.ReplicationHistory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	h.ID = fmt.Sprintf("hist-%d", len(r.history)+1)
	r.history = append(r.history, *h)
	return nil
}

func (r *ReplicationRepo) ListHistory(_ context.Context, ruleID string, limit int) ([]domain.ReplicationHistory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.ReplicationHistory
	for i := len(r.history) - 1; i >= 0 && len(out) < limit; i-- {
		if r.history[i].RuleID == ruleID {
			out = append(out, r.history[i])
		}
	}
	return out, nil
}

// ── PromotionRepo mock ────────────────────────────────────────

type PromotionRepo struct {
	mu       sync.Mutex
	Rules    map[string]*domain.PromotionRule
	Requests map[string]*domain.PromotionRequest
	nextID   int
	// GetRequestHook, when set, runs during GetRequest after the row is read but
	// before it is returned (outside the repo mutex) — the seam race tests use to
	// line up several callers on the same pending snapshot.
	GetRequestHook func(id string)
	// requestLocks holds one mutex per request id, mirroring the postgres
	// SELECT ... FOR UPDATE row lock in WithPendingRequestLock.
	requestLocks map[string]*sync.Mutex
}

func NewPromotionRepo() *PromotionRepo {
	return &PromotionRepo{
		Rules:    make(map[string]*domain.PromotionRule),
		Requests: make(map[string]*domain.PromotionRequest),
	}
}

func (r *PromotionRepo) genID() string {
	r.nextID++
	return fmt.Sprintf("promo-%d", r.nextID)
}

func (r *PromotionRepo) ListRules(_ context.Context) ([]domain.PromotionRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.PromotionRule, 0, len(r.Rules))
	for _, v := range r.Rules {
		out = append(out, *v)
	}
	return out, nil
}

func (r *PromotionRepo) GetRule(_ context.Context, id string) (*domain.PromotionRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.Rules[id]; ok {
		cp := *v
		return &cp, nil
	}
	return nil, repository.ErrNotFound
}

func (r *PromotionRepo) ListRulesByFromRepo(_ context.Context, fromRepo string) ([]domain.PromotionRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.PromotionRule
	for _, v := range r.Rules {
		if v.FromRepo == fromRepo {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (r *PromotionRepo) CreateRule(_ context.Context, rule *domain.PromotionRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rule.ID == "" {
		rule.ID = r.genID()
	}
	rule.CreatedAt = time.Now()
	rule.UpdatedAt = rule.CreatedAt
	cp := *rule
	r.Rules[rule.ID] = &cp
	return nil
}

func (r *PromotionRepo) UpdateRule(_ context.Context, rule *domain.PromotionRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule.UpdatedAt = time.Now()
	cp := *rule
	r.Rules[rule.ID] = &cp
	return nil
}

func (r *PromotionRepo) DeleteRule(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.Rules, id)
	return nil
}

func (r *PromotionRepo) CreateRequest(_ context.Context, req *domain.PromotionRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.ID == "" {
		req.ID = r.genID()
	}
	req.CreatedAt = time.Now()
	cp := *req
	r.Requests[req.ID] = &cp
	return nil
}

func (r *PromotionRepo) GetRequest(_ context.Context, id string) (*domain.PromotionRequest, error) {
	r.mu.Lock()
	var req *domain.PromotionRequest
	if v, ok := r.Requests[id]; ok {
		cp := *v
		req = &cp
	}
	// The hook runs outside the repo mutex; it exists to hold callers between
	// their status read and their next act, and holding the mutex there would
	// wedge every other repo call.
	hook := r.GetRequestHook
	r.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	if req == nil {
		return nil, repository.ErrNotFound
	}
	return req, nil
}

func (r *PromotionRepo) ListRequests(_ context.Context, status string) ([]domain.PromotionRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.PromotionRequest
	for _, v := range r.Requests {
		if status == "" || string(v.Status) == status {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (r *PromotionRepo) UpdateRequestStatus(_ context.Context, id string, status domain.PromotionStatus,
	reviewedBy *string, reviewedAt, completedAt *time.Time, errMsg string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	req, ok := r.Requests[id]
	if !ok {
		return fmt.Errorf("not found: %s", id)
	}
	req.Status = status
	req.ReviewedBy = reviewedBy
	req.ReviewedAt = reviewedAt
	req.CompletedAt = completedAt
	req.Error = errMsg
	return nil
}

// WithPendingRequestLock mirrors the postgres SELECT ... FOR UPDATE flow: one
// mutex per request id serializes reviewers; the second caller enters only
// after the first's outcome is applied and so sees the non-pending status.
func (r *PromotionRepo) WithPendingRequestLock(ctx context.Context, id string,
	fn func(ctx context.Context, req *domain.PromotionRequest) repository.PromotionOutcome,
) error {
	r.mu.Lock()
	if r.requestLocks == nil {
		r.requestLocks = make(map[string]*sync.Mutex)
	}
	l, ok := r.requestLocks[id]
	if !ok {
		l = &sync.Mutex{}
		r.requestLocks[id] = l
	}
	r.mu.Unlock()
	l.Lock()
	defer l.Unlock()

	r.mu.Lock()
	stored, ok := r.Requests[id]
	if !ok {
		r.mu.Unlock()
		return repository.ErrNotFound
	}
	if stored.Status != domain.PromotionPending {
		status := string(stored.Status)
		r.mu.Unlock()
		return &repository.RequestNotPendingError{Status: status}
	}
	cp := *stored
	r.mu.Unlock()

	outcome := fn(ctx, &cp)
	if outcome.Status == domain.PromotionPending {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	req, ok := r.Requests[id]
	if !ok {
		return repository.ErrNotFound
	}
	req.Status = outcome.Status
	req.ReviewedBy = outcome.ReviewedBy
	req.ReviewedAt = outcome.ReviewedAt
	req.CompletedAt = outcome.CompletedAt
	req.Error = outcome.Error
	return nil
}

// PutBytes is a test helper that stores raw bytes under key in the BlobStore mock.
func (b *BlobStore) PutBytes(ctx context.Context, key string, data []byte) error {
	return b.Put(ctx, key, bytes.NewReader(data), int64(len(data)))
}

// ── MigrationRepo ─────────────────────────────────────────────

// MigrationRepo is an in-memory repository.MigrationRepo. Every job is stored
// by value and handed back as a copy, so a test reading progress never races
// with the runner goroutine writing it.
type MigrationRepo struct {
	mu     sync.Mutex
	jobs   map[string]*domain.MigrationJob
	order  []string
	nextID int

	ListErr   error
	GetErr    error
	CreateErr error
	UpdateErr error
	DeleteErr error
}

func NewMigrationRepo(jobs ...*domain.MigrationJob) *MigrationRepo {
	m := &MigrationRepo{jobs: make(map[string]*domain.MigrationJob)}
	for _, j := range jobs {
		m.jobs[j.ID] = j
		m.order = append(m.order, j.ID)
	}
	return m
}

func (m *MigrationRepo) List(_ context.Context) ([]domain.MigrationJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ListErr != nil {
		return nil, m.ListErr
	}
	out := make([]domain.MigrationJob, 0, len(m.order))
	for _, id := range m.order {
		if j, ok := m.jobs[id]; ok {
			out = append(out, *j)
		}
	}
	return out, nil
}

func (m *MigrationRepo) Get(_ context.Context, id string) (*domain.MigrationJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.GetErr != nil {
		return nil, m.GetErr
	}
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("migration job not found: %s", id)
	}
	cp := *j
	return &cp, nil
}

func (m *MigrationRepo) Create(_ context.Context, job *domain.MigrationJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CreateErr != nil {
		return m.CreateErr
	}
	if job.ID == "" {
		m.nextID++
		job.ID = fmt.Sprintf("mig-%d", m.nextID)
	}
	if job.Status == "" {
		job.Status = domain.MigrationPending
	}
	now := time.Now()
	job.CreatedAt, job.UpdatedAt = now, now
	stored := *job
	m.jobs[job.ID] = &stored
	m.order = append(m.order, job.ID)
	return nil
}

func (m *MigrationRepo) UpdateStatus(_ context.Context, id string, status domain.MigrationJobStatus) error {
	return m.mutate(id, m.UpdateErr, func(j *domain.MigrationJob) { j.Status = status })
}

func (m *MigrationRepo) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.DeleteErr != nil {
		return m.DeleteErr
	}
	if _, ok := m.jobs[id]; !ok {
		return fmt.Errorf("migration job not found: %s", id)
	}
	delete(m.jobs, id)
	return nil
}

func (m *MigrationRepo) ListActive(_ context.Context) ([]domain.MigrationJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ListErr != nil {
		return nil, m.ListErr
	}
	var out []domain.MigrationJob
	for _, id := range m.order {
		if j, ok := m.jobs[id]; ok && j.IsActive() {
			out = append(out, *j)
		}
	}
	return out, nil
}

func (m *MigrationRepo) SetSourcePassword(_ context.Context, id, sealed string) error {
	return m.mutate(id, m.UpdateErr, func(j *domain.MigrationJob) { j.SourcePassword = sealed })
}

func (m *MigrationRepo) SetStarted(_ context.Context, id string, at time.Time) error {
	return m.mutate(id, m.UpdateErr, func(j *domain.MigrationJob) {
		j.Status = domain.MigrationRunning
		if j.StartedAt == nil {
			stamp := at
			j.StartedAt = &stamp
		}
		j.FinishedAt = nil
	})
}

func (m *MigrationRepo) SetTotals(_ context.Context, id string, totalRepos int, totalAssets int64) error {
	return m.mutate(id, m.UpdateErr, func(j *domain.MigrationJob) {
		j.TotalRepos, j.TotalAssets = totalRepos, totalAssets
	})
}

func (m *MigrationRepo) UpdateProgress(_ context.Context, id string, doneRepos int, doneAssets int64,
	errorCount int, lastError *string,
) error {
	return m.mutate(id, m.UpdateErr, func(j *domain.MigrationJob) {
		j.DoneRepos, j.DoneAssets, j.ErrorCount = doneRepos, doneAssets, errorCount
		if lastError != nil {
			msg := *lastError
			j.LastError = &msg
		}
	})
}

func (m *MigrationRepo) FinishJob(_ context.Context, id string,
	status domain.MigrationJobStatus, errMsg *string,
) error {
	return m.mutate(id, m.UpdateErr, func(j *domain.MigrationJob) {
		j.Status = status
		now := time.Now()
		j.FinishedAt = &now
		if errMsg != nil {
			msg := *errMsg
			j.LastError = &msg
		}
	})
}

func (m *MigrationRepo) mutate(id string, forced error, fn func(*domain.MigrationJob)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if forced != nil {
		return forced
	}
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("migration job not found: %s", id)
	}
	fn(j)
	j.UpdatedAt = time.Now()
	return nil
}
