package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/safego"
)

// tokenBucket tracks request allowance for a single identity using a token
// bucket algorithm. Each identity starts with burstSize tokens; tokens refill
// at refillRate per second up to the burst cap.
type tokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

func (b *tokenBucket) allow(rate, burst float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.lastRefill = now
	b.tokens += elapsed * rate
	if b.tokens > burst {
		b.tokens = burst
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RateLimitMiddleware returns a Gin middleware that limits requests per
// authenticated user (keyed by userID, or by remote IP for anonymous calls).
// rate is tokens/second; burst is the maximum burst size.
func RateLimitMiddleware(log logger.Logger, rate, burst float64) gin.HandlerFunc {
	type entry struct {
		bucket   *tokenBucket
		lastSeen time.Time
	}
	var (
		mu      sync.Mutex
		buckets = make(map[string]*entry)
	)

	// Evict stale entries every 5 minutes to prevent unbounded growth.
	safego.Go(log, "ratelimit-bucket-eviction", func() {
		for range time.Tick(5 * time.Minute) {
			func() {
				defer safego.Recover(log, "ratelimit-bucket-eviction-tick")()
				threshold := time.Now().Add(-10 * time.Minute)
				mu.Lock()
				defer mu.Unlock()
				for k, e := range buckets {
					if e.lastSeen.Before(threshold) {
						delete(buckets, k)
					}
				}
			}()
		}
	})

	return func(c *gin.Context) {
		key := ""
		if uid, ok := c.Get("userID"); ok {
			if s, ok2 := uid.(string); ok2 && s != "" {
				key = "u:" + s
			}
		}
		if key == "" {
			key = "ip:" + c.ClientIP()
		}

		mu.Lock()
		e, exists := buckets[key]
		if !exists {
			e = &entry{bucket: &tokenBucket{tokens: burst, lastRefill: time.Now()}}
			buckets[key] = e
		}
		e.lastSeen = time.Now()
		mu.Unlock()

		if !e.bucket.allow(rate, burst) {
			// Coarse retry hint: a 1s floor, not an exact per-token backoff.
			retryAfter := int(1.0 / rate)
			if retryAfter < 1 {
				retryAfter = 1
			}
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		c.Next()
	}
}
