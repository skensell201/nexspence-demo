package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/tracing"
)

// StoreResolver resolves a physical BlobStore from a descriptor.
// *storage.Registry satisfies this interface.
type StoreResolver interface {
	Get(ctx context.Context, desc storage.BlobStoreDescriptor) (storage.BlobStore, error)
}

// GCOptions controls a compaction run.
// MinAge <= 0 means "use the service's DefaultMinAge".
type GCOptions struct {
	DryRun bool
	MinAge time.Duration
}

// GCResult reports what a single store's compaction found and removed.
type GCResult struct {
	Store        string   `json:"store"`
	ScannedBlobs int      `json:"scannedBlobs"`
	Orphans      int      `json:"orphans"`
	FreedBytes   int64    `json:"freedBytes"`
	DryRun       bool     `json:"dryRun"`
	Errors       []string `json:"errors,omitempty"`
	// Aborted reports a pass that stopped early because the GC run reached its
	// distributed lock's TTL: the counts above are partial and the store still
	// holds orphans for the next run (#371).
	Aborted bool `json:"aborted,omitempty"`
}

// BlobGCService finds and removes blobs not referenced by any asset (orphans),
// age-gated by a grace period, across one or all blob stores.
type BlobGCService struct {
	Assets        repository.AssetRepo
	Stores        repository.BlobStoreRepo
	Resolver      StoreResolver
	Locker        distlock.Locker
	Log           logger.Logger
	DefaultMinAge time.Duration
}

const gcLockKey = "nexspence:lock:gc:run"
const gcLockTTL = 30 * time.Minute

func (s *BlobGCService) log() logger.Logger {
	if s.Log != nil {
		return s.Log
	}
	return zap.NewNop().Sugar()
}

// refIndex answers "is this key referenced in this store?".
//
// Scoping by store is what lets GC collect a copy left behind in a store an
// asset no longer lives on — a blob-store migration repoints the row without
// changing the key, so a set keyed by key alone kept calling the abandoned
// source copy "referenced" forever (#297). Rows carrying no store id (an
// implicit default) are counted as referenced in every store: they name a key
// but not a location, and guessing wrong here deletes live data.
type refIndex struct {
	byStore map[string]map[string]struct{}
	anyPos  map[string]struct{}
}

func (i refIndex) has(storeID, key string) bool {
	if _, ok := i.anyPos[key]; ok {
		return true
	}
	keys, ok := i.byStore[storeID]
	if !ok {
		return false
	}
	_, ok = keys[key]
	return ok
}

// referencedSet indexes every blob key referenced by an asset, by store.
func (s *BlobGCService) referencedSet(ctx context.Context) (refIndex, error) {
	refs, err := s.Assets.ListAllBlobRefs(ctx)
	if err != nil {
		return refIndex{}, fmt.Errorf("list db blob keys: %w", err)
	}
	idx := refIndex{byStore: make(map[string]map[string]struct{}), anyPos: make(map[string]struct{})}
	for _, r := range refs {
		if r.BlobStoreID == "" {
			idx.anyPos[r.BlobKey] = struct{}{}
			continue
		}
		keys, ok := idx.byStore[r.BlobStoreID]
		if !ok {
			keys = make(map[string]struct{})
			idx.byStore[r.BlobStoreID] = keys
		}
		keys[r.BlobKey] = struct{}{}
	}
	return idx, nil
}

// CompactStore compacts a single blob store by name.
func (s *BlobGCService) CompactStore(ctx context.Context, name string, opts GCOptions) (*GCResult, error) {
	referenced, err := s.referencedSet(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.Stores.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("get blob store %q: %w", name, err)
	}
	store, err := s.Resolver.Get(ctx, storage.BlobStoreDescriptor{
		ID: row.ID, Type: row.Type, Config: row.Config,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve blob store %q: %w", name, err)
	}
	// A single-store compaction takes no lock, so it has no TTL to outlive.
	return s.compact(ctx, name, row.ID, store, referenced, opts, time.Time{}), nil
}

// CompactAll compacts every blob store. It holds a distributed lock so only one
// node runs at a time; if another node holds it, CompactAll returns (nil, nil).
func (s *BlobGCService) CompactAll(ctx context.Context, opts GCOptions) ([]*GCResult, error) {
	// Root span: GC runs from cron with no HTTP request behind it (#302).
	ctx, span := tracing.StartRoot(ctx, "gc.compact_all")
	defer span.End()
	var deadline time.Time
	if s.Locker != nil {
		lock, err := s.Locker.Acquire(ctx, gcLockKey, gcLockTTL)
		if errors.Is(err, distlock.ErrLockHeld) {
			logger.WithTraceContext(ctx, s.log()).Info("blob gc skipped: another node is running gc")
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("blob gc: acquire lock: %w", err)
		}
		defer func() { _ = lock.Release(ctx) }()
		// Nothing renews the lock, so once gcLockTTL is up another node can
		// acquire it and start compacting the same stores alongside this run.
		// Passes stop at that moment rather than deleting unprotected (#371).
		deadline = time.Now().Add(gcLockTTL)
	}

	referenced, err := s.referencedSet(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.Stores.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("blob gc: list stores: %w", err)
	}

	results := make([]*GCResult, 0, len(rows))
	for i := range rows {
		row := rows[i]
		store, rerr := s.Resolver.Get(ctx, storage.BlobStoreDescriptor{
			ID: row.ID, Type: row.Type, Config: row.Config,
		})
		if rerr != nil {
			logger.WithTraceContext(ctx, s.log()).Error("blob gc: resolve store failed", "store", row.Name, "err", rerr)
			results = append(results, &GCResult{
				Store:  row.Name,
				DryRun: opts.DryRun,
				Errors: []string{fmt.Sprintf("resolve store: %v", rerr)},
			})
			continue
		}
		results = append(results, s.compact(ctx, row.Name, row.ID, store, referenced, opts, deadline))
	}
	return results, nil
}

// compact runs the core scan/delete for a single resolved store. deadline is
// when the run's distributed lock expires; a zero deadline means no lock is
// held and the pass runs to the end.
func (s *BlobGCService) compact(ctx context.Context, name, storeID string, store storage.BlobStore,
	referenced refIndex, opts GCOptions, deadline time.Time) *GCResult {
	minAge := opts.MinAge
	if minAge <= 0 {
		minAge = s.DefaultMinAge
	}

	result := &GCResult{Store: name, DryRun: opts.DryRun}

	entries, err := store.ListEntries(ctx)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("list entries: %v", err))
		return result
	}
	result.ScannedBlobs = len(entries)

	var removed int64
	for _, e := range entries {
		if pastDeadline(deadline) {
			s.log().Warn("blob gc: stopping — the run reached its lock TTL, another node may already hold the lock",
				"store", name, "orphans", result.Orphans, "freed_bytes", removed)
			result.Aborted = true
			break
		}
		if referenced.has(storeID, e.Key) {
			continue // still referenced in this store
		}
		// Age gate: skip blobs younger than the grace period (may be an
		// in-flight upload whose asset row is not committed yet).
		if minAge > 0 && !e.ModTime.IsZero() && time.Since(e.ModTime) < minAge {
			continue
		}
		result.Orphans++
		result.FreedBytes += e.Size
		if !opts.DryRun {
			if derr := store.Delete(ctx, e.Key); derr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("delete %s: %v", e.Key, derr))
				continue
			}
			removed += e.Size
		}
	}
	// used_bytes is how full the store is, and it just got emptier. A collected
	// store that keeps its old number reports space it does not use and starts
	// refusing writes that fit (issue #146). Only what was really deleted is
	// given back: a blob whose delete failed is still on the disk.
	if removed > 0 {
		if err := s.Stores.UpdateUsedBytes(ctx, name, -removed); err != nil {
			logger.WithTraceContext(ctx, s.log()).Error("blob gc: usage decrement failed", "store", name, "bytes", removed, "err", err)
			result.Errors = append(result.Errors, fmt.Sprintf("update used_bytes: %v", err))
		}
	}
	return result
}

// StartCronScheduler runs CompactAll on the given cron schedule until ctx is
// done. Run as a goroutine. A blank or invalid schedule disables scheduling.
func (s *BlobGCService) StartCronScheduler(ctx context.Context, schedule string, minAge time.Duration) {
	if schedule == "" {
		s.log().Info("blob gc scheduler disabled: empty schedule")
		return
	}
	// cron.Recover: without it, a panic inside CompactAll runs on cron's own
	// internal scheduler goroutine and crashes the whole process — not
	// covered by the safego.Go wrapping the outer StartCronScheduler call.
	c := cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger)))
	_, err := c.AddFunc(schedule, func() {
		if _, err := s.CompactAll(context.Background(), GCOptions{MinAge: minAge}); err != nil {
			s.log().Error("blob gc cron error", "err", err)
		}
	})
	if err != nil {
		s.log().Error("blob gc: invalid schedule, scheduler disabled", "schedule", schedule, "err", err)
		return
	}
	c.Start()
	<-ctx.Done()
	c.Stop()
}
