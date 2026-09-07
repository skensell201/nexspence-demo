package metrics

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/safego"
)

const ringSize = 360 // 1 hour at 10-second intervals

// DataPoint is a single timestamped metrics snapshot in the ring buffer.
type DataPoint struct {
	Timestamp       int64 `json:"timestamp"`
	RequestsTotal   int64 `json:"requests_total"`
	RequestErrors   int64 `json:"request_errors"`
	ArtifactsStored int64 `json:"artifacts_stored"`
	BytesStored     int64 `json:"bytes_stored"`
	DownloadsTotal  int64 `json:"downloads_total"`
	Goroutines      int   `json:"goroutines"`
}

// RingBuffer is a fixed-size circular buffer of DataPoints.
type RingBuffer struct {
	mu   sync.RWMutex
	data [ringSize]DataPoint
	head int
	size int
}

// Add appends a DataPoint, overwriting the oldest entry when the buffer is full.
func (r *RingBuffer) Add(p DataPoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[r.head] = p
	r.head = (r.head + 1) % ringSize
	if r.size < ringSize {
		r.size++
	}
}

// Snapshot returns all stored DataPoints in chronological order (oldest first).
func (r *RingBuffer) Snapshot() []DataPoint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return nil
	}
	result := make([]DataPoint, r.size)
	start := (r.head - r.size + ringSize) % ringSize
	for i := 0; i < r.size; i++ {
		result[i] = r.data[(start+i)%ringSize]
	}
	return result
}

// History is the global ring buffer populated by StartSampler.
var History = &RingBuffer{}

// StartSampler starts a background goroutine that samples metrics every 10s
// and stops when ctx is canceled.
func StartSampler(ctx context.Context, log logger.Logger, pool *pgxpool.Pool) {
	safego.Go(log, "metrics-sampler", func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				func() {
					defer safego.Recover(log, "metrics-sampler-tick")()
					takeSample(ctx, pool)
				}()
			case <-ctx.Done():
				return
			}
		}
	})
}

func takeSample(ctx context.Context, pool *pgxpool.Pool) {
	var artifacts, bytes, downloads int64
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size_bytes),0), COALESCE(SUM(download_count),0) FROM assets`,
	).Scan(&artifacts, &bytes, &downloads); err != nil {
		return // skip sample on DB error; keep last-known values in gauges
	}

	UpdateGauges(artifacts, bytes, downloads)

	History.Add(DataPoint{
		Timestamp:       time.Now().Unix(),
		RequestsTotal:   RequestsTotal.Load(),
		RequestErrors:   RequestErrors.Load(),
		ArtifactsStored: artifacts,
		BytesStored:     bytes,
		DownloadsTotal:  downloads,
		Goroutines:      runtime.NumGoroutine(),
	})
}
