package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/tracing"
)

// ErrMigrationAlreadyRunning is returned by Start when the repository already
// has a pending or running migration — whether the pre-check saw it or the
// database's partial unique index refused the insert.
var ErrMigrationAlreadyRunning = errors.New("a migration is already running for this repository")

// BlobStoreMigrationService manages background migrations of repository blobs
// from one blob store to another.
type BlobStoreMigrationService struct {
	migrations repository.BlobStoreMigrationRepo
	assets     repository.AssetRepo
	repos      repository.RepositoryRepo
	blobs      repository.BlobStoreRepo
	registry   *storage.Registry
	locker     distlock.Locker

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewBlobStoreMigrationService constructs a service that migrates assets between blob stores.
func NewBlobStoreMigrationService(
	migrations repository.BlobStoreMigrationRepo,
	assets repository.AssetRepo,
	repos repository.RepositoryRepo,
	blobs repository.BlobStoreRepo,
	registry *storage.Registry,
) *BlobStoreMigrationService {
	return &BlobStoreMigrationService{
		migrations: migrations,
		assets:     assets,
		repos:      repos,
		blobs:      blobs,
		registry:   registry,
		cancels:    make(map[string]context.CancelFunc),
	}
}

// WithLocker sets the distributed locker used to prevent concurrent migrations on the same repo across nodes.
func (s *BlobStoreMigrationService) WithLocker(l distlock.Locker) *BlobStoreMigrationService {
	s.locker = l
	return s
}

// Start validates inputs, creates a migration record, and launches the background goroutine.
func (s *BlobStoreMigrationService) Start(ctx context.Context, repoName, targetStoreID string) (*domain.BlobStoreMigration, error) {
	repo, err := s.repos.Get(ctx, repoName)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("repository %q not found", repoName)
	}
	if err != nil {
		return nil, fmt.Errorf("get repo: %w", err)
	}

	// Validate target store exists.
	_, err = s.blobs.GetByID(ctx, targetStoreID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("target blob store not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get target store: %w", err)
	}

	// Validate: not the same as current.
	if repo.BlobStoreID != nil && *repo.BlobStoreID == targetStoreID {
		return nil, fmt.Errorf("target blob store is the same as the repository's current store")
	}

	// Enforce single active migration per repo. This pre-check answers the
	// common case with a clear error; it is not the guarantee. Two simultaneous
	// requests both pass it, and the distributed lock below cannot stop them
	// either when Redis is absent — the default deployment, where the locker is
	// a no-op. The database's partial unique index is what actually decides,
	// and Create translates its violation into this same error.
	active, err := s.migrations.GetActiveByRepo(ctx, repoName)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if active != nil {
		return nil, ErrMigrationAlreadyRunning
	}

	// Capture source store ID for the history record.
	sourceStoreID := ""
	if repo.BlobStoreID != nil {
		sourceStoreID = *repo.BlobStoreID
	}

	// Take the lock before anything is recorded. runMigration is the only path
	// that clears the migration row and the cancel func, and it never runs if
	// the lock is lost — so a row created first would stay "pending" forever and
	// wedge every later Start for this repo as "already running".
	var migLock distlock.Lock
	var deadline time.Time
	if s.locker != nil {
		var lockErr error
		migLock, lockErr = s.locker.Acquire(ctx, blobMigLockKey(repoName), blobMigLockTTL)
		if errors.Is(lockErr, distlock.ErrLockHeld) {
			return nil, fmt.Errorf("blob store migration for %q is already running on another node", repoName)
		}
		if lockErr != nil {
			return nil, fmt.Errorf("acquire migration lock: %w", lockErr)
		}
		// Nothing renews the lock, so at blobMigLockTTL another node can start a
		// second migration of this repo. Two runMigration goroutines interleaving
		// their UpdateBlobStoreForBlobKey and FinishMigration calls is the one
		// case here that is not merely duplicate work, so the run stops at its
		// deadline instead (#371).
		deadline = time.Now().Add(blobMigLockTTL)
	}

	m := &domain.BlobStoreMigration{
		RepositoryName: repoName,
		SourceStoreID:  sourceStoreID,
		TargetStoreID:  targetStoreID,
		Status:         "pending",
	}
	if err := s.migrations.Create(ctx, m); err != nil {
		// Nothing will run, so hand the lock back now rather than leave the repo
		// blocked for the rest of its TTL.
		if migLock != nil {
			_ = migLock.Release(ctx)
		}
		// The request that lost the insert race: the row another request just
		// wrote is exactly what the pre-check above looks for, so it gets the
		// same answer it would have got a moment later.
		if errors.Is(err, repository.ErrAlreadyExists) {
			return nil, ErrMigrationAlreadyRunning
		}
		return nil, fmt.Errorf("create migration record: %w", err)
	}

	migCtx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel stored in s.cancels and invoked via Cancel() or runMigration's defer on every exit path (no leak)
	s.mu.Lock()
	s.cancels[m.ID] = cancel
	s.mu.Unlock()

	go s.runMigration(migCtx, m, migLock, deadline) //nolint:gosec // detached context is intentional: background migration must outlive the request
	return m, nil
}

const blobMigLockTTL = 2 * time.Hour

func blobMigLockKey(repoName string) string { return "nexspence:lock:blobmig:" + repoName }

// Cancel signals the running migration goroutine to stop.
func (s *BlobStoreMigrationService) Cancel(_ context.Context, migrationID string) error {
	s.mu.Lock()
	cancel, ok := s.cancels[migrationID]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

// GetLatestByRepo returns the most recent migration for a repo regardless of status.
func (s *BlobStoreMigrationService) GetLatestByRepo(ctx context.Context, repoName string) (*domain.BlobStoreMigration, error) {
	return s.migrations.GetLatestByRepo(ctx, repoName)
}

// ResumeAll is called on server startup to mark interrupted migrations as canceled
// so users can restart them. Goroutines cannot be safely resumed across process restarts.
func (s *BlobStoreMigrationService) ResumeAll(ctx context.Context) error {
	active, err := s.migrations.ListActive(ctx)
	if err != nil {
		return err
	}
	interrupted := "interrupted by server restart"
	for _, m := range active {
		_ = s.migrations.FinishMigration(ctx, m.ID, "cancelled", &interrupted) //nolint:misspell // API/DB status value consumed by frontend (status === 'cancelled')
		// The process that took this lock died without releasing it, and only
		// runMigration's deferred Release ever would. Clearing it here is what
		// makes the migration restartable now instead of two hours from now.
		if s.locker != nil {
			_ = s.locker.ForceRelease(ctx, blobMigLockKey(m.RepositoryName))
		}
	}
	return nil
}

// runMigration copies every asset of the repo to the target store. deadline is
// when the migration's distributed lock expires; a zero deadline means no lock
// is held and the run has nothing to outlive.
func (s *BlobStoreMigrationService) runMigration(ctx context.Context, m *domain.BlobStoreMigration,
	lock distlock.Lock, deadline time.Time) {
	defer func() {
		if lock != nil {
			_ = lock.Release(context.Background())
		}
	}()
	defer func() {
		s.mu.Lock()
		if cancel, ok := s.cancels[m.ID]; ok {
			cancel() // release the context resources held by WithCancel on all exit paths
		}
		delete(s.cancels, m.ID)
		s.mu.Unlock()
	}()

	// Root span: the migration goroutine runs on context.Background(), cut
	// loose from the HTTP request that started it (#302). The span hangs off
	// bgCtx so every DB and blob-store call below joins one trace.
	bgCtx, span := tracing.StartRoot(context.Background(), "blob_store_migration.run",
		attribute.String("repository.name", m.RepositoryName),
		attribute.String("blob_store.target_id", m.TargetStoreID))
	defer span.End()

	if err := s.migrations.UpdateStatus(bgCtx, m.ID, "running", nil); err != nil {
		return
	}

	rows, err := s.assets.ListForBlobStoreMigration(bgCtx, m.RepositoryName, m.TargetStoreID)
	if err != nil {
		errMsg := err.Error()
		_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
		return
	}

	var totalBytes int64
	for _, r := range rows {
		totalBytes += r.SizeBytes
	}
	_ = s.migrations.SetTotals(bgCtx, m.ID, len(rows), totalBytes)

	// Load target store descriptor once.
	targetStoreMeta, err := s.blobs.GetByID(bgCtx, m.TargetStoreID)
	if err != nil || targetStoreMeta == nil {
		errMsg := fmt.Sprintf("cannot load target store: %v", err)
		_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
		return
	}
	targetStore, err := s.registry.Get(bgCtx, storage.BlobStoreDescriptor{
		ID:     targetStoreMeta.ID,
		Type:   targetStoreMeta.Type,
		Config: targetStoreMeta.Config,
	})
	if err != nil {
		errMsg := fmt.Sprintf("cannot open target store: %v", err)
		_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
		return
	}

	doneAssets := 0
	var doneBytes int64

	for _, row := range rows {
		if pastDeadline(deadline) {
			msg := fmt.Sprintf("aborted: exceeded the lock's TTL after migrating %d of %d assets", doneAssets, len(rows))
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "cancelled", &msg) //nolint:misspell // API/DB status value consumed by frontend (status === 'cancelled')
			return
		}
		select {
		case <-ctx.Done():
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "cancelled", nil) //nolint:misspell // API/DB status value consumed by frontend (status === 'cancelled')
			return
		default:
		}

		// Load source store for this blob.
		sourceMeta, err := s.blobs.GetByID(bgCtx, row.SourceBlobStoreID)
		if err != nil || sourceMeta == nil {
			errMsg := fmt.Sprintf("cannot load source store %s: %v", row.SourceBlobStoreID, err)
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
			return
		}
		sourceStore, err := s.registry.Get(bgCtx, storage.BlobStoreDescriptor{
			ID:     sourceMeta.ID,
			Type:   sourceMeta.Type,
			Config: sourceMeta.Config,
		})
		if err != nil {
			errMsg := fmt.Sprintf("cannot open source store: %v", err)
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
			return
		}

		// Copy blob if not already in target (resume support).
		exists, err := targetStore.Exists(bgCtx, row.BlobKey)
		if err != nil {
			errMsg := fmt.Sprintf("checking target for %s: %v", row.BlobKey, err)
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
			return
		}
		// expectedSize is what the flip below re-verifies against right before
		// it runs: copied's own re-check when this iteration actually copied
		// bytes, or the size already on record when resuming a run that had
		// already gotten this key to target on a prior pass.
		expectedSize := row.SizeBytes
		if !exists {
			copied, err := s.copyBlob(bgCtx, sourceStore, targetStore, row.BlobKey)
			if err != nil {
				errMsg := err.Error()
				_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
				return
			}
			_ = s.blobs.UpdateUsedBytes(bgCtx, targetStoreMeta.Name, copied)
			expectedSize = copied
		}

		// The repository stays pointed at the source store for the whole
		// migration (it only flips once, after every row is done), so an
		// ordinary write to this exact key can still land on source in the gap
		// between the copy above and the flip+drop below. Both steps run under
		// the same blob-key lock other same-key actors already respect
		// (DeleteArtifact, CleanupService, oci.mountBlob), and the source
		// object is re-measured immediately before the flip — a size mismatch
		// means a write landed here since the copy, so this row's migration
		// fails loudly (same philosophy as copyBlob's own re-check, #298)
		// rather than repointing the asset at target while different bytes
		// than the ones just copied sit on source.
		flipErr := s.assets.WithBlobKeyLock(bgCtx, row.BlobKey, func(ctx context.Context) error {
			cur, sizeErr := sourceStore.Size(ctx, row.BlobKey)
			if sizeErr != nil {
				return fmt.Errorf("re-checking source blob %s before flip: %w", row.BlobKey, sizeErr)
			}
			if cur != expectedSize {
				return fmt.Errorf("blob %s changed on source before flip (%d -> %d bytes); re-run the migration", row.BlobKey, expectedSize, cur)
			}
			if err := s.assets.UpdateBlobStoreForBlobKey(ctx, row.BlobKey, m.RepositoryName, m.TargetStoreID); err != nil {
				return fmt.Errorf("updating asset pointers for %s: %w", row.BlobKey, err)
			}
			// The bytes now live in the target and no row points at the source
			// copy any more: drop it and give the space back. Without this the
			// source keeps both the object and its used_bytes forever — GC
			// cannot help, the key is still referenced, just in another store
			// (#297).
			s.dropSourceCopy(ctx, sourceStore, sourceMeta.Name, row)
			return nil
		})
		if flipErr != nil {
			errMsg := flipErr.Error()
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
			return
		}

		doneAssets++
		doneBytes += row.SizeBytes
		_ = s.migrations.UpdateProgress(bgCtx, m.ID, doneAssets, doneBytes)
	}

	// Update repository's blob_store_id to target.
	repo, err := s.repos.Get(bgCtx, m.RepositoryName)
	if err == nil && repo != nil {
		repo.BlobStoreID = &m.TargetStoreID
		_ = s.repos.Update(bgCtx, repo)
	}

	_ = s.migrations.FinishMigration(bgCtx, m.ID, "done", nil)
}

// copyBlob streams one blob from source to target and returns the bytes written.
//
// The source is re-measured after the copy, because a client can overwrite the
// blob in place while it streams: the repository is still pointed at the source
// store for the whole migration, so ordinary uploads keep landing there. Writing
// the bytes we read before that upload and then repointing the row would leave
// the asset row (new size) disagreeing with the target's physical bytes (old
// content) — silent corruption on every later read (#298). A changed size means
// we lost the race, so the staged copy is dropped and the whole migration fails
// loudly; a re-run copies the current bytes.
func (s *BlobStoreMigrationService) copyBlob(ctx context.Context, sourceStore, targetStore storage.BlobStore, blobKey string) (int64, error) {
	rc, size, err := sourceStore.Get(ctx, blobKey)
	if err != nil {
		return 0, fmt.Errorf("reading blob %s: %w", blobKey, err)
	}
	putErr := targetStore.Put(ctx, blobKey, rc, size)
	_ = rc.Close()
	if putErr != nil {
		return 0, fmt.Errorf("writing blob %s: %w", blobKey, putErr)
	}

	after, sizeErr := sourceStore.Size(ctx, blobKey)
	if sizeErr != nil || after != size {
		_ = targetStore.Delete(ctx, blobKey)
		if sizeErr != nil {
			return 0, fmt.Errorf("re-checking source blob %s: %w", blobKey, sizeErr)
		}
		return 0, fmt.Errorf("blob %s changed during migration (%d -> %d bytes); re-run the migration", blobKey, size, after)
	}
	return size, nil
}

// dropSourceCopy removes the migrated blob from the source store and decrements
// that store's used_bytes, but only once nothing on the source references the
// key any more — the same key can legitimately still be in use there by another
// repository's asset or an OCI digest alias.
func (s *BlobStoreMigrationService) dropSourceCopy(ctx context.Context, sourceStore storage.BlobStore, sourceName string, row domain.MigrationAssetRow) {
	remaining, err := s.assets.CountByBlobKeyInStore(ctx, row.BlobKey, row.SourceBlobStoreID)
	if err != nil || remaining > 0 {
		return
	}
	size, err := sourceStore.Size(ctx, row.BlobKey)
	if err != nil || size <= 0 {
		size = row.SizeBytes
	}
	if err := sourceStore.Delete(ctx, row.BlobKey); err != nil {
		return
	}
	if size > 0 {
		_ = s.blobs.UpdateUsedBytes(ctx, sourceName, -size)
	}
}
