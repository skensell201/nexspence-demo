package audit

import (
	"context"
	"time"

	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/nexspence-oss/nexspence/internal/safego"
)

// Rotator runs partition lifecycle for audit_events on a ticker.
type Rotator struct {
	store PartitionStore
	cfg   config.AuditConfig
	log   logger.Logger
	now   func() time.Time // injectable for tests; defaults to time.Now in NewRotator
}

// NewRotator creates a Rotator that manages audit_events partitions per cfg.
func NewRotator(store PartitionStore, cfg config.AuditConfig, log logger.Logger) *Rotator {
	return &Rotator{store: store, cfg: cfg, log: log, now: time.Now}
}

// Run blocks until ctx is canceled, ticking every cfg.RotationInterval.
// Run does NOT execute the first tick — call RunOnce(ctx) before Run if you
// want the start-up partitions guaranteed before the server accepts traffic.
func (r *Rotator) Run(ctx context.Context) {
	interval := r.cfg.RotationInterval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			func() {
				defer safego.Recover(r.log, "audit-rotator-tick")()
				r.tick(ctx)
			}()
		}
	}
}

// RunOnce executes one full pass synchronously.
func (r *Rotator) RunOnce(ctx context.Context) {
	r.tick(ctx)
}

func (r *Rotator) tick(ctx context.Context) {
	if err := r.ensureFuturePartitions(ctx); err != nil {
		r.log.Errorw("audit rotator: ensure partitions", "err", err)
	}
	if err := r.dropOldPartitions(ctx); err != nil {
		r.log.Errorw("audit rotator: drop old partitions", "err", err)
	}
	r.checkSoftCap(ctx)
}

func (r *Rotator) ensureFuturePartitions(ctx context.Context) error {
	months := r.cfg.LookaheadMonths
	if months < 0 {
		months = 0
	}
	base := r.now()
	for i := 0; i <= months; i++ {
		target := base.AddDate(0, i, 0)
		from, to := MonthBounds(target)
		name := PartitionName(target)
		if err := r.store.CreatePartition(ctx, name, from, to); err != nil {
			return err
		}
	}
	return nil
}

func (r *Rotator) dropOldPartitions(ctx context.Context) error {
	parts, err := r.store.ListPartitions(ctx)
	if err != nil {
		return err
	}
	cutoff := r.now().UTC().AddDate(0, 0, -r.cfg.RetentionDays)
	for _, p := range parts {
		if !p.To.After(cutoff) {
			if err := r.store.DropPartition(ctx, p.Name); err != nil {
				r.log.Warnw("audit rotator: drop failed",
					"partition", p.Name, "err", err)
				continue
			}
			r.log.Infow("audit rotator: dropped partition",
				"partition", p.Name, "to", p.To.Format("2006-01-02"))
		}
	}
	return nil
}

func (r *Rotator) checkSoftCap(ctx context.Context) {
	n, err := r.store.CountRows(ctx)
	if err != nil {
		r.log.Warnw("audit rotator: count rows", "err", err)
		return
	}
	metrics.AuditEventsCount.Store(n)
	if r.cfg.SoftCap > 0 && n > r.cfg.SoftCap {
		r.log.Warnw("audit_events soft cap exceeded",
			"count", n, "cap", r.cfg.SoftCap)
	}
}
