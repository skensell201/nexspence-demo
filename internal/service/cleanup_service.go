package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel/attribute"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/tracing"
)

// CleanupService runs cleanup policies — finds stale assets and removes them.
type CleanupService struct {
	policies   repository.CleanupPolicyRepo
	repos      repository.RepositoryRepo
	assets     repository.AssetRepo
	components repository.ComponentRepo
	blobs      repository.BlobStoreRepo
	blobStore  storage.BlobStore
	resolver   StoreResolver
	log        logger.Logger
	locker     distlock.Locker

	mu              sync.Mutex
	cronScheduler   *cron.Cron
	entryIDs        map[string]cron.EntryID
	defaultSchedule string
}

// NewCleanupService constructs a service that runs cleanup policies and schedules them via cron.
func NewCleanupService(
	policies repository.CleanupPolicyRepo,
	repos repository.RepositoryRepo,
	assets repository.AssetRepo,
	blobs repository.BlobStoreRepo,
	blobStore storage.BlobStore,
	log logger.Logger,
) *CleanupService {
	return &CleanupService{
		policies:  policies,
		repos:     repos,
		assets:    assets,
		blobs:     blobs,
		blobStore: blobStore,
		log:       log,
		entryIDs:  make(map[string]cron.EntryID),
	}
}

// WithLocker sets the distributed locker used to prevent concurrent cleanup runs across nodes.
func (s *CleanupService) WithLocker(l distlock.Locker) *CleanupService {
	s.locker = l
	return s
}

// WithResolver sets the store resolver so blobs are deleted from the physical
// blob store each asset actually lives on (S3, a group member, ...) rather than
// the global default. Without a resolver the service falls back to blobStore.
func (s *CleanupService) WithResolver(r StoreResolver) *CleanupService {
	s.resolver = r
	return s
}

// WithComponents sets the component repo so components left without any assets
// after a cleanup run are pruned. Without it, deleted assets leave empty
// component rows that make the repository still look populated in the UI.
func (s *CleanupService) WithComponents(c repository.ComponentRepo) *CleanupService {
	s.components = c
	return s
}

// storeForAsset resolves the physical blob store an asset lives on. Falls back to
// the global default store when no resolver is configured or resolution fails.
func (s *CleanupService) storeForAsset(ctx context.Context, a *domain.Asset) storage.BlobStore {
	if s.resolver == nil || a.BlobStoreID == "" {
		return s.blobStore
	}
	bs, err := s.blobs.GetByID(ctx, a.BlobStoreID)
	if err != nil || bs == nil {
		return s.blobStore
	}
	store, err := s.resolver.Get(ctx, storage.BlobStoreDescriptor{ID: bs.ID, Type: bs.Type, Config: bs.Config})
	if err != nil || store == nil {
		return s.blobStore
	}
	return store
}

// StartCronScheduler starts cron-based per-policy scheduling. Run as a goroutine.
// Policies with a non-empty schedule_cron field use that expression; others use defaultSchedule.
func (s *CleanupService) StartCronScheduler(ctx context.Context, defaultSchedule string) {
	s.mu.Lock()
	s.defaultSchedule = defaultSchedule
	s.cronScheduler = cron.New()
	s.mu.Unlock()

	policies, err := s.policies.List(ctx)
	if err != nil {
		s.log.Error("cleanup: failed to load policies for scheduler", "err", err)
	} else {
		s.mu.Lock()
		for _, p := range policies {
			if p.Enabled {
				s.addEntryLocked(p)
			}
		}
		s.mu.Unlock()
	}

	s.cronScheduler.Start()
	<-ctx.Done()
	s.cronScheduler.Stop()
}

// ReloadPolicy updates the cron schedule for a single policy (call after Create/Update/Delete).
// If the policy is not found or disabled, its cron entry is removed.
func (s *CleanupService) ReloadPolicy(ctx context.Context, policyID string) {
	// Fetch from DB outside the lock to avoid holding it during I/O.
	p, _ := s.policies.Get(ctx, policyID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cronScheduler == nil {
		return // scheduler not started yet
	}

	// Remove existing entry if present.
	if eid, ok := s.entryIDs[policyID]; ok {
		s.cronScheduler.Remove(eid)
		delete(s.entryIDs, policyID)
	}

	if p == nil || !p.Enabled {
		return
	}
	s.addEntryLocked(*p)
}

// addEntryLocked registers a cron job for policy p. Caller must hold s.mu.
func (s *CleanupService) addEntryLocked(p domain.CleanupPolicy) {
	schedule := p.ScheduleCron
	if schedule == "" {
		schedule = s.defaultSchedule
	}

	job := func() {
		if _, err := s.runPolicyLocked(context.Background(), p); err != nil {
			s.log.Error("cleanup cron error", "policy", p.Name, "err", err)
		}
	}

	id, err := s.cronScheduler.AddFunc(schedule, job)
	if err != nil {
		s.log.Warn("cleanup: invalid schedule_cron, falling back to default",
			"policy", p.Name, "schedule", schedule, "err", err)
		id, _ = s.cronScheduler.AddFunc(s.defaultSchedule, job)
	}
	s.entryIDs[p.ID] = id
}

const cleanupPolicyLockPrefix = "nexspence:lock:cleanup:policy:"
const cleanupLockTTL = 30 * time.Minute

// errAcquireLock marks a failure to reach the lock backend, as opposed to a
// policy that simply failed to run. Without the lock there is no exclusion, so
// callers stop instead of running unprotected.
var errAcquireLock = errors.New("cleanup: acquire lock")

// runPolicyLocked runs p while holding its own distributed lock. Every path that
// runs a policy — the cron schedule, a manual single-policy run, RunAll — goes
// through here, so nodes in an HA deployment never run the same policy at once.
// The lock is per policy, so unrelated policies still run in parallel.
func (s *CleanupService) runPolicyLocked(ctx context.Context, p domain.CleanupPolicy) (*domain.CleanupRunResult, error) {
	if s.locker == nil {
		// No locker, no exclusivity to lose: a single-node deployment has no TTL
		// to run out of.
		return s.runPolicy(ctx, p, time.Time{})
	}

	lock, err := s.locker.Acquire(ctx, cleanupPolicyLockPrefix+p.ID, cleanupLockTTL)
	if errors.Is(err, distlock.ErrLockHeld) {
		const reason = "another node is already running this policy"
		logger.WithTraceContext(ctx, s.log).Info("cleanup skipped: "+reason, "policy", p.Name)
		return &domain.CleanupRunResult{
			PolicyID: p.ID, DryRun: p.DryRun, Skipped: true, SkippedReason: reason,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w for policy %q: %w", errAcquireLock, p.Name, err)
	}
	defer func() { _ = lock.Release(ctx) }()

	// The lock expires on its own after cleanupLockTTL whether or not this run
	// has finished, and nothing renews it — so past that moment another node can
	// acquire the same lock and start the same policy alongside this one (#371).
	// A run that reaches its deadline stops instead of continuing unprotected;
	// what it already deleted is kept and reported, and the next run picks up
	// the rest.
	return s.runPolicy(ctx, p, time.Now().Add(cleanupLockTTL))
}

// RunAll executes all enabled cleanup policies once and returns a summary.
//
// The root span exists because cleanup runs with no HTTP request behind it
// (cron, or a fire-and-forget goroutine off the manual trigger): without an
// explicit root its DB and blob-store spans have nothing to attach to and the
// whole job is invisible in traces (#302).
func (s *CleanupService) RunAll(ctx context.Context) error {
	ctx, span := tracing.StartRoot(ctx, "cleanup.run_all")
	defer span.End()
	policies, err := s.policies.List(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: list policies: %w", err)
	}
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		if _, err := s.runPolicyLocked(ctx, p); err != nil {
			if errors.Is(err, errAcquireLock) {
				return err
			}
			logger.WithTraceContext(ctx, s.log).Error("cleanup policy failed", "policy", p.Name, "err", err)
		}
	}
	return nil
}

// RunPolicy executes a single policy by ID, discarding the run summary.
// Retained for callers (task scheduler) that only need success/failure.
func (s *CleanupService) RunPolicy(ctx context.Context, id string) error {
	_, err := s.RunPolicyResult(ctx, id)
	return err
}

// RunPolicyResult executes a single policy by ID and returns a summary of what
// happened (deleted count, freed bytes, or a skip reason). The manual-run
// endpoint uses this to report the outcome instead of a fire-and-forget ack.
func (s *CleanupService) RunPolicyResult(ctx context.Context, id string) (*domain.CleanupRunResult, error) {
	ctx, span := tracing.StartRoot(ctx, "cleanup.run_policy", attribute.String("cleanup.policy_id", id))
	defer span.End()
	p, err := s.policies.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("cleanup policy %q not found", id)
	}
	return s.runPolicyLocked(ctx, *p)
}

// runPolicy executes p. deadline is the moment this run's distributed lock
// expires; a zero deadline means the run holds no lock and has nothing to
// outlive.
func (s *CleanupService) runPolicy(ctx context.Context, p domain.CleanupPolicy, deadline time.Time) (*domain.CleanupRunResult, error) {
	// Every line this run logs carries the run's trace_id, so a warning like
	// "blob delete failed" can be jumped to the exact cleanup.run trace it
	// happened in (#321). With tracing disabled this is s.log unchanged.
	log := logger.WithTraceContext(ctx, s.log)
	res := &domain.CleanupRunResult{PolicyID: p.ID, DryRun: p.DryRun}

	lastDownloadedDays := intCriteria(p.Criteria, "lastDownloadedDays")
	artifactAgeDays := intCriteria(p.Criteria, "artifactAgeDays")
	pathPrefix := strCriteria(p.Criteria, "pathPrefix")
	nameGlob := strCriteria(p.Criteria, "nameGlob")

	var repoNames []string
	var err error
	if p.Scope.RepositoryName != "" {
		repoNames = []string{p.Scope.RepositoryName}
		if p.Scope.PathPrefix != "" {
			pathPrefix = p.Scope.PathPrefix
		}
	} else {
		repoNames, err = s.repos.ListNamesByCleanupPolicyID(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("cleanup: list repos for policy: %w", err)
		}
	}
	if len(repoNames) == 0 {
		reason := "policy is not attached to any repository — attach it to a repository or set a scope"
		log.Info("cleanup: "+reason, "policy", p.Name)
		res.Skipped = true
		res.SkippedReason = reason
		return res, nil
	}

	// No age/download criteria means "delete everything matching the scope"
	// (path/name filters and retainNVersions still apply). This is intentional
	// for a clear-all policy; the repository targeting above is the safety gate.
	if lastDownloadedDays == 0 && artifactAgeDays == 0 {
		log.Info("cleanup: no age criteria — removing all artifacts matching scope",
			"policy", p.Name, "repos", repoNames, "path_prefix", pathPrefix,
			"name_glob", nameGlob, "retain_versions", p.RetainNVersions, "dry_run", p.DryRun)
	}

	const batchLimit = 500
	// A pass can legitimately delete nothing: every row it scanned changed under
	// it, or every delete failed. The next scan re-reads fresh snapshots and
	// usually succeeds — but a row perturbed on every pass would otherwise keep
	// the loop spinning forever, so a few fruitless passes end the run.
	const maxNoProgressPasses = 3
	var freed int64
	var deleted int
	noProgress := 0
	aborted := false
	for {
		if pastDeadline(deadline) {
			log.Warn("cleanup: stopping — the run reached its lock TTL, another node may already hold the lock",
				"policy", p.Name, "deleted", deleted, "freed_bytes", freed)
			aborted = true
			break
		}
		stale, err := s.assets.ListStale(ctx, p.Format, repoNames, lastDownloadedDays, artifactAgeDays, pathPrefix, nameGlob, p.RetainNVersions, batchLimit)
		if err != nil {
			return nil, fmt.Errorf("cleanup: list stale assets: %w", err)
		}
		if len(stale) == 0 {
			break
		}
		deletedBefore := deleted
		for _, a := range stale {
			if p.DryRun {
				log.Info("cleanup dry-run: would delete", "policy", p.Name,
					"asset", a.Path, "repo", a.Repository, "size", a.SizeBytes)
				freed += a.SizeBytes
				deleted++
				continue
			}
			asset := a
			// The lock serializes this delete against every other reader-then-actor
			// on the same blob key — a concurrent mount, a DeleteArtifact call,
			// another cleanup — and the conditional delete refuses a row that was
			// downloaded or re-uploaded after the staleness scan: deleting by the
			// captured ID would erase a live artifact and the next pull would 404.
			lockErr := s.assets.WithBlobKeyLock(ctx, a.BlobKey, func(ctx context.Context) error {
				rowDeleted, derr := s.assets.DeleteIfUnchanged(ctx, a.ID, a.BlobKey, a.LastDownloaded, a.LastModified)
				if derr != nil {
					log.Warn("cleanup: asset delete failed", "id", a.ID, "err", derr)
					return nil
				}
				if !rowDeleted {
					log.Info("cleanup: skipped — asset changed after the staleness scan",
						"id", a.ID, "path", a.Path, "repo", a.Repository)
					return nil
				}
				deleted++
				// One object can carry several assets — an OCI manifest's tag and its
				// digest alias, a mounted layer — and expiring one of them says
				// nothing about the others. Deleting the bytes under a surviving
				// asset would leave it advertising content that is gone (#144); an
				// unreadable count keeps them, since an orphan is reclaimed by the
				// blob GC while bytes deleted under a live asset are lost.
				others, cerr := s.assets.CountByBlobKey(ctx, a.BlobKey, a.ID)
				if cerr != nil {
					log.Warn("cleanup: blob reference count failed, keeping blob",
						"key", a.BlobKey, "err", cerr)
					return nil
				}
				if others == 0 {
					if err := s.storeForAsset(ctx, &asset).Delete(ctx, a.BlobKey); err != nil {
						log.Warn("cleanup: blob delete failed", "key", a.BlobKey, "err", err)
					} else {
						// Bytes count as freed on exactly the same condition as
						// used_bytes moves: the physical delete succeeded. A row
						// whose blob is shared with a surviving asset, or whose
						// delete failed, leaves the bytes on disk — reporting them
						// as freed tells the operator storage was reclaimed that
						// never was (#368).
						freed += a.SizeBytes
						// used_bytes is how full the store is, so it only moves when
						// bytes actually left it (#146).
						_ = base.DecrementBlobStoreUsage(ctx, s.blobs, &asset)
					}
				}
				return nil
			})
			if lockErr != nil {
				log.Warn("cleanup: blob key lock failed", "key", a.BlobKey, "err", lockErr)
			}
		}
		if !p.DryRun {
			if deleted == deletedBefore {
				noProgress++
				if noProgress >= maxNoProgressPasses {
					log.Warn("cleanup: no progress after repeated passes — stopping this run",
						"policy", p.Name, "passes", noProgress)
					break
				}
			} else {
				noProgress = 0
			}
		}
		// A real run advances the cursor by deleting the batch, so the next query
		// returns the following rows. A dry run deletes nothing, so re-querying
		// would return the same rows forever — report the first batch and stop.
		if p.DryRun {
			if len(stale) == batchLimit {
				log.Info("cleanup dry-run: results capped at one batch — actual run would delete more",
					"policy", p.Name, "batch", batchLimit)
			}
			break
		}
	}

	// Prune components left without any assets so the repository no longer shows
	// empty rows in the browse UI. Skipped on dry runs (nothing was deleted).
	if s.components != nil && !p.DryRun && deleted > 0 {
		for _, rn := range repoNames {
			if err := s.components.DeleteOrphans(ctx, rn); err != nil {
				log.Warn("cleanup: failed to prune orphan components", "repo", rn, "err", err)
			}
		}
	}

	now := time.Now()
	if err := s.policies.RecordRun(ctx, p.ID, now, deleted, freed); err != nil {
		log.Warn("cleanup: failed to record run stats", "policy", p.Name, "err", err)
	}

	log.Info("cleanup policy complete",
		"policy", p.Name,
		"deleted", deleted,
		"freed_bytes", freed,
		"dry_run", p.DryRun)

	res.Deleted = deleted
	res.FreedBytes = freed
	res.Aborted = aborted
	return res, nil
}

// PreviewPolicy loads stale assets for a policy (limit 200) without deleting them.
func (s *CleanupService) PreviewPolicy(ctx context.Context, id string) (*domain.CleanupPreviewResult, error) {
	p, err := s.policies.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("cleanup policy %q not found", id)
	}

	lastDownloadedDays := intCriteria(p.Criteria, "lastDownloadedDays")
	artifactAgeDays := intCriteria(p.Criteria, "artifactAgeDays")
	pathPrefix := strCriteria(p.Criteria, "pathPrefix")
	nameGlob := strCriteria(p.Criteria, "nameGlob")

	var repoNames []string
	if p.Scope.RepositoryName != "" {
		repoNames = []string{p.Scope.RepositoryName}
		if p.Scope.PathPrefix != "" {
			pathPrefix = p.Scope.PathPrefix
		}
	} else {
		repoNames, err = s.repos.ListNamesByCleanupPolicyID(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("cleanup: list repos for policy: %w", err)
		}
	}

	var reason string
	switch {
	case lastDownloadedDays > 0:
		reason = fmt.Sprintf("not dl %dd", lastDownloadedDays)
	case artifactAgeDays > 0:
		reason = fmt.Sprintf("age %dd", artifactAgeDays)
	default:
		reason = "stale"
	}

	const previewLimit = 200
	stale, err := s.assets.ListStale(ctx, p.Format, repoNames, lastDownloadedDays, artifactAgeDays, pathPrefix, nameGlob, p.RetainNVersions, previewLimit)
	if err != nil {
		return nil, fmt.Errorf("cleanup: list stale assets: %w", err)
	}

	result := &domain.CleanupPreviewResult{
		Assets: make([]domain.CleanupPreviewAsset, 0, len(stale)),
	}
	for _, a := range stale {
		result.Assets = append(result.Assets, domain.CleanupPreviewAsset{
			Path:           a.Path,
			Repository:     a.Repository,
			SizeBytes:      a.SizeBytes,
			LastDownloaded: a.LastDownloaded,
			CreatedAt:      a.CreatedAt,
			Reason:         reason,
		})
		result.TotalBytes += a.SizeBytes
	}
	result.TotalCount = len(result.Assets)
	return result, nil
}

func strCriteria(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func intCriteria(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return 0
}
