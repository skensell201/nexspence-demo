// Package base provides shared artifact storage helpers used by all format handlers.
package base

import (
	"context"
	"crypto/md5"  //nolint:gosec // md5/sha1 required for artifact-protocol checksums (Maven .md5/.sha1, npm shasum), not security
	"crypto/sha1" //nolint:gosec // md5/sha1 required for artifact-protocol checksums (Maven .md5/.sha1, npm shasum), not security
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ErrQuotaExceeded is returned by StoreArtifact when a blob store or repository quota would be exceeded.
var ErrQuotaExceeded = errors.New("storage quota exceeded")

// ErrSizeMismatch is returned by StoreArtifact when the caller declared a size
// the request body did not deliver. The declared size is what gets recorded and
// served back as Content-Length, so trusting it would leave the DB describing
// bytes the store does not hold.
var ErrSizeMismatch = errors.New("declared size does not match the bytes received")

// HTTPStatusForError maps known storage errors to appropriate HTTP status codes.
// Returns 507 Insufficient Storage for quota and out-of-space errors, 400 for a
// body that contradicts its own declared size, and 500 for everything else.
func HTTPStatusForError(err error) int {
	if errors.Is(err, ErrQuotaExceeded) || errors.Is(err, storage.ErrNoSpace) {
		return http.StatusInsufficientStorage
	}
	if errors.Is(err, ErrSizeMismatch) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// StoreResult holds checksums and metadata after a successful store.
type StoreResult struct {
	Asset  *domain.Asset
	SHA256 string
	SHA1   string
	MD5    string
	Size   int64
}

// StoreArtifact streams reader into the blob store, computes checksums,
// and upserts the component + asset records in the DB.
// coords.Version may be empty for formats that don't have versions (e.g. raw).
func StoreArtifact(ctx context.Context, d formats.Deps,
	repoName, filePath, contentType string,
	coords Coords,
	reader io.Reader, declaredSize int64,
) (*StoreResult, error) {
	repo, err := d.Repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		return nil, fmt.Errorf("repository %q not found", repoName)
	}
	if !repo.Online {
		return nil, fmt.Errorf("repository %q is offline", repoName)
	}

	// Early quota reject when declared size is known.
	if declaredSize > 0 {
		if err := checkQuota(ctx, d, repo, declaredSize); err != nil {
			return nil, err
		}
	}

	blobKey := BlobKey(repoName, filePath)

	// Resolve once — result passed to RegisterStoredBlob to avoid double-call.
	// For group stores, double-call would advance the round-robin counter twice.
	resolvedBlobStoreID, resolvedBlobStoreName, _ := resolveBlobStoreRef(ctx, d, repo)

	var physStore storage.BlobStore
	if resolvedBlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, resolvedBlobStoreID); getErr == nil {
			physStore = PhysicalStore(ctx, d, bsMeta)
		}
	}
	if physStore == nil {
		physStore = d.BlobStore
	}

	// Stream → hash writers → blob store via pipe
	sha256h := sha256.New()
	sha1h := sha1.New() //nolint:gosec // protocol checksum, not security
	md5h := md5.New()   //nolint:gosec // protocol checksum, not security

	pr, pw := io.Pipe()
	// The copier reports how many bytes it actually moved, so the size recorded
	// in the DB is never the caller's declaration taken on faith.
	copied := make(chan int64, 1)
	go func() {
		n, err := io.Copy(io.MultiWriter(pw, sha256h, sha1h, md5h), reader)
		pw.CloseWithError(err)
		copied <- n
	}()

	putErr := physStore.Put(ctx, blobKey, pr, declaredSize)
	// Unblocks the copier if the store stopped reading before EOF; a no-op once
	// the pipe has drained on its own. Receiving the count then also orders the
	// hash writers' final state against this goroutine.
	_ = pr.Close()
	written := <-copied
	if putErr != nil {
		return nil, fmt.Errorf("store blob: %w", putErr)
	}

	sha256sum := hex.EncodeToString(sha256h.Sum(nil))
	sha1sum := hex.EncodeToString(sha1h.Sum(nil))
	md5sum := hex.EncodeToString(md5h.Sum(nil))

	size := declaredSize
	if size <= 0 {
		if s, err := physStore.Size(ctx, blobKey); err == nil {
			size = s
		}
	} else if written != declaredSize {
		// A body shorter (or longer) than its own declared length: recording the
		// declaration would register a component whose stored size and checksum
		// describe different bytes, and every later download would announce a
		// Content-Length it then fails to deliver.
		_ = physStore.Delete(ctx, blobKey)
		return nil, fmt.Errorf("%w: declared %d, received %d", ErrSizeMismatch, declaredSize, written)
	}

	// Post-write quota check covers streaming uploads where size wasn't declared.
	if size > 0 && declaredSize <= 0 {
		if err := checkQuota(ctx, d, repo, size); err != nil {
			_ = physStore.Delete(ctx, blobKey)
			return nil, err
		}
	}

	asset, err := RegisterStoredBlob(ctx, d, repo, filePath, contentType, coords, blobKey, sha256sum, sha1sum, md5sum, size, resolvedBlobStoreID, resolvedBlobStoreName)
	if err != nil {
		// A registration the quota refused leaves bytes nothing references; drop
		// them unless another asset legitimately shares the key.
		if errors.Is(err, ErrQuotaExceeded) {
			if others, cerr := d.Assets.CountByBlobKey(ctx, blobKey, ""); cerr == nil && others == 0 {
				_ = physStore.Delete(ctx, blobKey)
			}
		}
		return nil, err
	}

	if d.Webhooks != nil {
		d.Webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventArtifactPublished,
			Timestamp:  asset.CreatedAt,
			Repository: repoName,
			Component: map[string]any{
				"group":   coords.Group,
				"name":    coords.Name,
				"version": coords.Version,
				"format":  string(repo.Format),
			},
			Asset: map[string]any{
				"path":        filePath,
				"contentType": contentType,
				"size":        size,
			},
		})
	}

	return &StoreResult{
		Asset:  asset,
		SHA256: sha256sum,
		SHA1:   sha1sum,
		MD5:    md5sum,
		Size:   size,
	}, nil
}

// RegisterStoredBlob upserts component + asset after a blob was written to blobKey with known checksums.
// blobStoreID and blobStoreName may be pre-resolved by the caller to avoid calling resolveBlobStoreRef
// twice (which would advance a round-robin group counter twice). Pass empty strings to resolve internally.
func RegisterStoredBlob(ctx context.Context, d formats.Deps, repo *domain.Repository,
	filePath, contentType string, coords Coords,
	blobKey string,
	sha256sum, sha1sum, md5sum string,
	size int64,
	blobStoreID, blobStoreName string,
) (*domain.Asset, error) {
	if blobStoreID == "" {
		var err error
		blobStoreID, blobStoreName, err = resolveBlobStoreRef(ctx, d, repo)
		if err != nil {
			return nil, err
		}
	}
	if blobStoreName == "" {
		// A caller that pins the store by id — the OCI digest alias, a mount —
		// pins where the bytes are, and used_bytes is keyed by name.
		if bs, err := d.Blobs.GetByID(ctx, blobStoreID); err == nil && bs != nil {
			blobStoreName = bs.Name
		}
	}

	// Read before the upsert: whether these bytes are new to the store depends on
	// what the path held a moment ago.
	prev, perr := d.Assets.GetByPath(ctx, repo.Name, filePath)
	if perr != nil {
		prev = nil
	}

	version := coords.Version
	if version == "" {
		version = "1"
	}
	name := coords.Name
	if name == "" {
		// Formats that cache proxied files verbatim (apt, yum, nuget, conan, ...)
		// have no coordinates to parse. Components are upserted on
		// (repo, format, group, name, version), so an empty name would fold every
		// cached file into one nameless component that the browse UI can't act on.
		name = strings.TrimPrefix(filePath, "/")
	}
	comp := &domain.Component{
		RepositoryID: repo.ID,
		Repository:   repo.Name,
		Format:       string(repo.Format),
		Group:        coords.Group,
		Name:         name,
		Version:      version,
	}

	asset := &domain.Asset{
		ComponentID:  comp.ID,
		RepositoryID: repo.ID,
		Repository:   repo.Name,
		Path:         filePath,
		BlobStoreID:  blobStoreID,
		BlobKey:      blobKey,
		SizeBytes:    size,
		ContentType:  contentType,
		SHA256:       sha256sum,
		SHA1:         sha1sum,
		MD5:          md5sum,
	}
	if uid := requestctx.UserID(ctx); uid != "" {
		asset.UploaderID = uid
	}

	// This is the narrow waist every write path funnels through, so the quota is
	// ENFORCED here — the callers' own checkQuota calls are fast pre-checks, not
	// the guarantee. The check, the upserts and the counter update run under
	// per-quota advisory locks (store first, then repo — a fixed order, so two
	// registrations can never deadlock): without the serialization, two
	// concurrent writers each read a usage snapshot that ignores the other and
	// jointly exceed the limit (#328). No quota configured → no locks, no cost.
	register := func(ctx context.Context) error {
		// The real increment, not the raw size: an alias or mount of an
		// already-stored blob adds no bytes and must pass even at full quota.
		probe := *asset
		if prev != nil {
			probe.ID = prev.ID
		}
		delta := usageDelta(ctx, d, &probe, prev)
		if delta > 0 {
			// Fresh reads inside the lock — the counters are exactly what a
			// concurrent registration just changed.
			if qErr := quotaHeadroom(ctx, d, repo, blobStoreID, delta); qErr != nil {
				return qErr
			}
		}
		if err := d.Components.Create(ctx, comp); err != nil {
			return fmt.Errorf("upsert component: %w", err)
		}
		asset.ComponentID = comp.ID
		if err := d.Assets.Create(ctx, asset); err != nil {
			return fmt.Errorf("upsert asset: %w", err)
		}
		if delta != 0 {
			_ = d.Blobs.UpdateUsedBytes(ctx, blobStoreName, delta)
		}
		return nil
	}

	run := register
	if repo.QuotaBytes != nil {
		inner := run
		run = func(ctx context.Context) error {
			return d.Assets.WithBlobKeyLock(ctx, "quota:repo:"+repo.Name, inner)
		}
	}
	if storeQuotaConfigured(ctx, d, repo, blobStoreID) {
		inner := run
		lockName := blobStoreName
		if lockName == "" {
			lockName = "default"
		}
		run = func(ctx context.Context) error {
			return d.Assets.WithBlobKeyLock(ctx, "quota:store:"+lockName, inner)
		}
	}
	if err := run(ctx); err != nil {
		return nil, err
	}

	queueForScanning(d, comp)
	return asset, nil
}

// storeQuotaConfigured reports whether the store this registration lands in has
// a byte quota — the signal that the registration must serialize with its
// peers. An unreadable store reads as unconfigured: the enforcement read inside
// the lock decides, and it fails closed.
func storeQuotaConfigured(ctx context.Context, d formats.Deps, repo *domain.Repository, blobStoreID string) bool {
	bs := storeForQuota(ctx, d, repo, blobStoreID)
	return bs != nil && bs.Type != "group" && bs.QuotaBytes != nil
}

// storeForQuota resolves the blob-store row whose quota governs this
// registration: the pinned store when the caller pinned one, the repository's
// own store otherwise. nil when no row can be read.
func storeForQuota(ctx context.Context, d formats.Deps, repo *domain.Repository, blobStoreID string) *domain.BlobStore {
	if blobStoreID != "" {
		if bs, err := d.Blobs.GetByID(ctx, blobStoreID); err == nil {
			return bs
		}
		return nil
	}
	bs, err := resolveBlobStoreObj(ctx, d, repo)
	if err != nil {
		return nil
	}
	return bs
}

// quotaHeadroom answers whether the store and the repository can absorb delta
// more bytes, reading both counters fresh. Callers hold the corresponding
// advisory locks, which is what makes the answer still true at commit time.
func quotaHeadroom(ctx context.Context, d formats.Deps, repo *domain.Repository, blobStoreID string, delta int64) error {
	if bs := storeForQuota(ctx, d, repo, blobStoreID); bs != nil &&
		bs.Type != "group" && bs.QuotaBytes != nil && bs.UsedBytes+delta > *bs.QuotaBytes {
		return fmt.Errorf("%w: blob store %q usage %d + %d > limit %d",
			ErrQuotaExceeded, bs.Name, bs.UsedBytes, delta, *bs.QuotaBytes)
	}
	if repo.QuotaBytes != nil {
		used, err := d.Assets.SumSizeByRepo(ctx, repo.Name)
		if err != nil {
			return fmt.Errorf("quota check: %w", err)
		}
		if used+delta > *repo.QuotaBytes {
			return fmt.Errorf("%w: repository %q usage %d + %d > limit %d",
				ErrQuotaExceeded, repo.Name, used, delta, *repo.QuotaBytes)
		}
	}
	return nil
}

// queueForScanning asks for a background scan of a component that has just been
// registered.
//
// It sits here rather than in StoreArtifact because this is the narrower waist:
// StoreArtifact goes through it, and so do the writes that skip StoreArtifact
// entirely — a proxy repository caching an upstream artifact, an OCI blob
// mount. Those are the ones most worth scanning, since their content comes from
// somewhere nobody here controls.
//
// Digest-versioned components are not queued. An OCI push registers every layer
// as one of these before it sends the manifest, so queuing them would fill a
// bounded queue with entries the scanner discards anyway — and evict the
// manifest, the only entry that describes a scannable image.
//
// The call returns immediately and cannot fail: no upload is ever held up or
// refused on account of the scanner.
func queueForScanning(d formats.Deps, comp *domain.Component) {
	if d.Scanner == nil || comp == nil || comp.ID == "" {
		return
	}
	if strings.HasPrefix(comp.Version, "sha256:") {
		return
	}
	d.Scanner.TriggerAsync(comp.ID)
}

// usageDelta reports how much the blob store's used_bytes has to move for a
// registration. The counter is how full the store is, not what its asset rows
// add up to: several assets routinely name one blob — an OCI manifest's tag and
// its digest alias, a cross-repository mount — and the store holds one copy of
// it (issue #146). prev is the asset that held the same path before the upsert,
// or nil if the path is new.
func usageDelta(ctx context.Context, d formats.Deps, asset, prev *domain.Asset) int64 {
	if prev != nil && prev.BlobKey == asset.BlobKey && prev.BlobStoreID == asset.BlobStoreID {
		// The write landed on the object the path already had: the store holds
		// the new size where it held the old one.
		return asset.SizeBytes - prev.SizeBytes
	}
	// A blob key another asset already carries is already counted. Shared keys
	// live in one store by construction: a key is derived from (repository,
	// path), and the two paths that deliberately share one — an OCI digest alias
	// and a mounted blob — are registered against the store of the asset they
	// alias. A count that cannot be read counts the bytes: overstating a store
	// costs a rejected write, understating it overfills the disk.
	if others, err := d.Assets.CountByBlobKey(ctx, asset.BlobKey, asset.ID); err == nil && others > 0 {
		return 0
	}
	// A path that moved to a different key or store leaves its old bytes behind;
	// they stay counted where they lie until the blob GC reclaims them.
	return asset.SizeBytes
}

// FetchArtifact retrieves a blob from storage and increments download count.
func FetchArtifact(ctx context.Context, d formats.Deps, repoName, filePath string,
) (io.ReadCloser, *domain.Asset, error) {
	asset, err := d.Assets.GetByPath(ctx, repoName, filePath)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil, fmt.Errorf("not found: %s/%s", repoName, filePath)
	}
	if err != nil {
		return nil, nil, err
	}

	var fetchStore storage.BlobStore
	if asset.BlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil {
			fetchStore = PhysicalStore(ctx, d, bsMeta)
		}
	}
	if fetchStore == nil {
		fetchStore = d.BlobStore
	}
	rc, _, err := fetchStore.Get(ctx, asset.BlobKey)
	if err != nil {
		return nil, nil, fmt.Errorf("blob missing: %w", err)
	}

	if d.Downloads != nil {
		d.Downloads.Add(asset.ID)
	}
	return rc, asset, nil
}

// DeleteArtifact removes a blob from storage and DB.
func DeleteArtifact(ctx context.Context, d formats.Deps, repoName, filePath string) error {
	asset, err := d.Assets.GetByPath(ctx, repoName, filePath)
	if errors.Is(err, repository.ErrNotFound) {
		return nil // idempotent
	}
	if err != nil {
		return err
	}
	var delStore storage.BlobStore
	if asset.BlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil {
			delStore = PhysicalStore(ctx, d, bsMeta)
		}
	}
	if delStore == nil {
		delStore = d.BlobStore
	}
	// The count-then-delete sequence holds the blob-key lock: two concurrent
	// deletes of the last two assets sharing one key would otherwise each see
	// the other's still-present row, both skip the physical delete, and orphan
	// the bytes with the usage counter overstated until GC.
	var rowDeleted, freed bool
	if err := d.Assets.WithBlobKeyLock(ctx, asset.BlobKey, func(ctx context.Context) error {
		// Re-read under the lock: a concurrent DeleteArtifact may have already
		// removed this row (idempotent, nothing to do), or a re-upload replaced
		// it — in which case the path now carries a different artifact this call
		// was never asked to delete.
		cur, gerr := d.Assets.GetByPath(ctx, repoName, filePath)
		if errors.Is(gerr, repository.ErrNotFound) {
			return nil
		}
		if gerr != nil {
			return gerr
		}
		// BlobKey alone doesn't catch a same-path re-upload: it's
		// sha256(repoName+":"+filePath), content-independent, so a re-upload to
		// this exact path always keeps the same BlobKey. last_modified is what
		// actually changes.
		if cur == nil || cur.ID != asset.ID || cur.BlobKey != asset.BlobKey || !cur.LastModified.Equal(asset.LastModified) {
			return nil
		}
		// One blob can carry several assets: an OCI manifest push registers the tag
		// path and the digest-alias path on the same blob key, and a client that
		// deletes one still pulls the other. Deleting the bytes here would leave the
		// survivor — and the referrers index built from it — advertising content that
		// is gone. A count that cannot be read keeps the blob: an orphan is reclaimed
		// by the blob GC, whereas bytes deleted under a live asset are lost.
		others, cerr := d.Assets.CountByBlobKey(ctx, asset.BlobKey, asset.ID)

		// The row goes first, before the bytes. Whichever half fails, the leftover
		// has to be one the system can recover from: a blob with no row is reclaimed
		// by the blob GC, while a row whose bytes are already gone is not
		// self-healing — every later fetch finds the row and then fails to read the
		// blob instead of answering a clean 404, and the store keeps accounting for
		// bytes that no longer exist, since the usage decrement below is never
		// reached. Everything after this point reasons from the asset struct already
		// in hand, not from a fresh read, so the order does not change what is
		// counted or decremented.
		if err := d.Assets.Delete(ctx, asset.ID); err != nil {
			return err
		}
		rowDeleted = true
		if cerr == nil && others == 0 {
			// Both blob store backends report a missing object as a successful
			// delete, so a nil error means the bytes are not there any more.
			freed = delStore.Delete(ctx, asset.BlobKey) == nil
		}
		return nil
	}); err != nil {
		return err
	}
	if !rowDeleted {
		return nil // a concurrent caller already deleted or replaced it
	}
	metrics.ArtifactsDeleted.Add(1)
	// Decremented only when the bytes actually left the store, mirroring the
	// registration side, which counts a blob once however many assets name it.
	// Decrementing for a blob a surviving asset still reads would take a size off
	// the store that is still on the disk.
	if freed {
		_ = DecrementBlobStoreUsage(ctx, d.Blobs, asset)
	}
	if d.Webhooks != nil {
		d.Webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventArtifactDeleted,
			Timestamp:  asset.LastModified,
			Repository: repoName,
			Asset: map[string]any{
				"path":        filePath,
				"contentType": asset.ContentType,
				"size":        asset.SizeBytes,
			},
		})
	}
	return nil
}

// DecrementBlobStoreUsage reduces the owning blob store's used_bytes by asset.SizeBytes.
// Call it only once the object itself has left the store: the counter is how full the
// store is, and a registration adds a blob's size once however many assets name it.
// Best-effort — callers typically ignore the error.
func DecrementBlobStoreUsage(ctx context.Context, blobs repository.BlobStoreRepo, asset *domain.Asset) error {
	if asset == nil || asset.SizeBytes <= 0 {
		return nil
	}
	name := ""
	if asset.BlobStoreID != "" {
		bs, err := blobs.GetByID(ctx, asset.BlobStoreID)
		if err != nil {
			return err
		}
		if bs != nil {
			name = bs.Name
		}
	}
	if name == "" {
		name = "default"
	}
	return blobs.UpdateUsedBytes(ctx, name, -asset.SizeBytes)
}

// BlobKey returns a deterministic content-addressed storage key for a path.
func BlobKey(repoName, filePath string) string {
	h := sha256.Sum256([]byte(repoName + ":" + filePath))
	return hex.EncodeToString(h[:])
}

// BlobKeyByDigest returns a key directly from a sha256 digest string (e.g. "sha256:abc123").
func BlobKeyByDigest(digest string) string {
	return "digest/" + digest
}

// Coords holds the parsed artifact coordinates used for component records.
type Coords struct {
	Group   string // e.g. Maven groupId, npm scope, Go module path
	Name    string // package/artifact/chart name
	Version string // semantic version
}

// CheckQuota verifies that writing `size` bytes for repo won't exceed the repository or
// blob-store quota. Exported for callers that write blobs outside StoreArtifact —
// the proxy cache-fill path — so every write path shares one quota gate.
func CheckQuota(ctx context.Context, d formats.Deps, repo *domain.Repository, size int64) error {
	return checkQuota(ctx, d, repo, size)
}

// checkQuota verifies that writing `size` bytes won't exceed either the blob store quota or the
// repository-level quota. Returns ErrQuotaExceeded if either is breached.
// A group store has no bytes of its own, so its check is deferred to
// resolveBlobStoreRef: PickMember skips members at capacity under every fill
// policy and returns "" once none are left, which surfaces as ErrQuotaExceeded.
func checkQuota(ctx context.Context, d formats.Deps, repo *domain.Repository, size int64) error {
	bs, err := resolveBlobStoreObj(ctx, d, repo)
	if err != nil {
		return err
	}
	if bs.Type != "group" && bs.QuotaBytes != nil && bs.UsedBytes+size > *bs.QuotaBytes {
		return fmt.Errorf("%w: blob store %q usage %d + %d > limit %d",
			ErrQuotaExceeded, bs.Name, bs.UsedBytes, size, *bs.QuotaBytes)
	}
	if repo.QuotaBytes != nil {
		used, err := d.Assets.SumSizeByRepo(ctx, repo.Name)
		if err != nil {
			return fmt.Errorf("quota check: %w", err)
		}
		if used+size > *repo.QuotaBytes {
			return fmt.Errorf("%w: repository %q usage %d + %d > limit %d",
				ErrQuotaExceeded, repo.Name, used, size, *repo.QuotaBytes)
		}
	}
	return nil
}

// resolveBlobStoreObj returns the full BlobStore record for a repository.
func resolveBlobStoreObj(ctx context.Context, d formats.Deps, repo *domain.Repository) (*domain.BlobStore, error) {
	if repo.BlobStoreID != nil {
		ref := strings.TrimSpace(*repo.BlobStoreID)
		if ref != "" {
			bs, err := d.Blobs.GetByID(ctx, ref)
			if err != nil {
				return nil, fmt.Errorf("blob store: %w", err)
			}
			if bs != nil {
				return bs, nil
			}
			return nil, fmt.Errorf("blob store id %q not found", ref)
		}
	}
	bs, err := d.Blobs.Get(ctx, "default")
	if errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("default blob store not found")
	}
	if err != nil {
		return nil, fmt.Errorf("blob store: %w", err)
	}
	return bs, nil
}

// ResolveBlobStore returns the physical BlobStore, its DB id, and its DB name for repo.
// It mirrors the blob store resolution used inside StoreArtifact, so callers that need to
// write blobs directly (e.g. proxy cache) use the same store that RegisterStoredBlob records.
func ResolveBlobStore(ctx context.Context, d formats.Deps, repo *domain.Repository) (id, name string, store storage.BlobStore) {
	id, name, _ = resolveBlobStoreRef(ctx, d, repo)
	if id != "" {
		if bsMeta, err := d.Blobs.GetByID(ctx, id); err == nil {
			store = PhysicalStore(ctx, d, bsMeta)
		}
	}
	if store == nil {
		store = d.BlobStore
	}
	return id, name, store
}

// PhysicalStore returns the physical BlobStore for the given domain blob store.
// If the registry is set and the descriptor is valid, it returns the cached/created instance.
// Falls back to d.BlobStore (the global default) on any error or missing registry.
func PhysicalStore(ctx context.Context, d formats.Deps, bs *domain.BlobStore) storage.BlobStore {
	if d.Registry == nil || bs == nil {
		return d.BlobStore
	}
	store, err := d.Registry.Get(ctx, storage.BlobStoreDescriptor{
		ID:     bs.ID,
		Type:   bs.Type,
		Config: bs.Config,
	})
	if err != nil {
		return d.BlobStore
	}
	return store
}

// groupMemberIDs extracts member_ids from a group blob store config.
// Handles []string (from Go) and []interface{} (from JSON unmarshal).
func groupMemberIDs(bs *domain.BlobStore) []string {
	if bs.Config == nil {
		return nil
	}
	raw := bs.Config["member_ids"]
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

// groupFillPolicy returns the fill_policy from a group blob store config, defaulting to "round_robin".
func groupFillPolicy(bs *domain.BlobStore) string {
	if bs.Config == nil {
		return "round_robin"
	}
	if p, ok := bs.Config["fill_policy"].(string); ok && p != "" {
		return p
	}
	return "round_robin"
}

// resolveBlobStoreRef returns the blob store UUID for assets.blob_store_id (FK)
// and the store name for BlobStoreRepo.UpdateUsedBytes (keyed by name).
// For group stores, it picks a physical member using the configured fill policy.
func resolveBlobStoreRef(ctx context.Context, d formats.Deps, repo *domain.Repository) (id string, name string, err error) {
	var bs *domain.BlobStore
	if repo.BlobStoreID != nil {
		ref := strings.TrimSpace(*repo.BlobStoreID)
		if ref != "" {
			bs, err = d.Blobs.GetByID(ctx, ref)
			if err != nil {
				return "", "", fmt.Errorf("blob store: %w", err)
			}
			if bs == nil {
				return "", "", fmt.Errorf("blob store id %q not found", ref)
			}
		}
	}
	if bs == nil {
		bs, err = d.Blobs.Get(ctx, "default")
		if err != nil {
			return "", "", fmt.Errorf("blob store: %w", err)
		}
		if bs == nil {
			return "", "", fmt.Errorf("default blob store not found (seed blob_stores or assign repository.blobStoreId)")
		}
	}

	if bs.Type != "group" {
		return bs.ID, bs.Name, nil
	}

	// Group store: pick a physical member via fill policy.
	memberIDs := groupMemberIDs(bs)
	if len(memberIDs) == 0 {
		return "", "", fmt.Errorf("group blob store %q has no members", bs.Name)
	}
	if d.Registry == nil {
		return "", "", fmt.Errorf("group blob store %q requires Registry to be configured", bs.Name)
	}

	memberMap := make(map[string]domain.BlobStore, len(memberIDs))
	var members []storage.MemberInfo
	for _, mid := range memberIDs {
		m, getErr := d.Blobs.GetByID(ctx, mid)
		if getErr != nil || m == nil {
			continue
		}
		members = append(members, storage.MemberInfo{
			ID:         m.ID,
			QuotaBytes: m.QuotaBytes,
			UsedBytes:  m.UsedBytes,
		})
		memberMap[m.ID] = *m
	}
	if len(members) == 0 {
		return "", "", fmt.Errorf("group blob store %q: no valid members found", bs.Name)
	}

	policy := groupFillPolicy(bs)
	memberID := d.Registry.PickMember(bs.ID, policy, members)
	if memberID == "" {
		return "", "", fmt.Errorf("%w: all members of group blob store %q are at capacity", ErrQuotaExceeded, bs.Name)
	}

	m := memberMap[memberID]
	return m.ID, m.Name, nil
}
