package handlers

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/redisclient"
	"github.com/nexspence-oss/nexspence/internal/safego"
)

// readinessTTL is how long a healthy probe result is reused. /readyz is
// unauthenticated, so without this every hit is a Postgres round trip and the
// probe becomes a cheap way to load the database. A second is short enough that
// an orchestrator still sees an outage promptly.
const readinessTTL = time.Second

// LivenessHandler returns 200 {"status":"ok"} — process is alive.
func LivenessHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// pinger is the one thing readiness needs from a dependency. Both
// *pgxpool.Pool and *redisclient.Client satisfy it.
type pinger interface {
	Ping(ctx context.Context) error
}

// ReadinessHandler checks DB and Redis connectivity in parallel.
// Either dep may be nil — it is skipped in that case.
// Returns 503 if any check fails.
func ReadinessHandler(log logger.Logger, pool *pgxpool.Pool, redis *redisclient.Client) gin.HandlerFunc {
	// Typed nils would satisfy the interface while being nil underneath, so
	// convert explicitly.
	var db, rd pinger
	if pool != nil {
		db = pool
	}
	if redis != nil {
		rd = redis
	}
	return readinessHandler(log, db, rd, readinessTTL)
}

// readinessResult is the memoized outcome of one probe round.
type readinessResult struct {
	checks map[string]string
	failed bool
	at     time.Time
}

func readinessHandler(log logger.Logger, db, redis pinger, ttl time.Duration) gin.HandlerFunc {
	var (
		mu     sync.Mutex
		cached *readinessResult
	)

	probe := func(ctx context.Context) *readinessResult {
		checks := map[string]string{}
		var cmu sync.Mutex
		var wg sync.WaitGroup
		failed := false

		// wg.Wait() below does NOT protect the process from a panic in check:
		// an unrecovered panic in any goroutine crashes the whole program
		// regardless of whether the parent is waiting on it.
		check := func(name string, p pinger) {
			defer wg.Done()
			defer safego.Recover(log, "readiness-check-"+name)()
			pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			// status starts pessimistic and is recorded from a defer registered
			// after cancel(): defers run LIFO, so this one fires before
			// safego.Recover's above, capturing whatever status was at the
			// moment of a panic instead of letting Recover swallow it silently
			// and leaving the check missing from checks (which would drop it
			// out of the response and never flip failed).
			status := "error"
			defer func() {
				cmu.Lock()
				checks[name] = status
				if status != "ok" {
					failed = true
				}
				cmu.Unlock()
			}()
			if err := p.Ping(pctx); err == nil {
				status = "ok"
			}
		}

		if db != nil {
			wg.Add(1)
			go check("db", db)
		}
		if redis != nil {
			wg.Add(1)
			go check("redis", redis)
		}
		wg.Wait()

		return &readinessResult{checks: checks, failed: failed, at: time.Now()}
	}

	return func(c *gin.Context) {
		mu.Lock()
		// Only healthy results are reused: caching a failure would keep
		// reporting an outage that has already been fixed.
		res := cached
		fresh := res != nil && !res.failed && time.Since(res.at) < ttl
		mu.Unlock()

		if !fresh {
			res = probe(c.Request.Context())
			mu.Lock()
			cached = res
			mu.Unlock()
		}

		statusStr := "ok"
		code := http.StatusOK
		if res.failed {
			statusStr = "degraded"
			code = http.StatusServiceUnavailable
		}

		resp := gin.H{"status": statusStr}
		if len(res.checks) > 0 {
			resp["checks"] = res.checks
		}
		c.JSON(code, resp)
	}
}
