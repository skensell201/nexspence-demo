package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingPinger struct {
	calls atomic.Int32
	err   error
}

func (p *countingPinger) Ping(context.Context) error {
	p.calls.Add(1)
	return p.err
}

func serveReady(h gin.HandlerFunc) *httptest.ResponseRecorder {
	r := gin.New()
	r.GET("/readyz", h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return w
}

// /readyz is unauthenticated. Without a cache every hit is a Postgres round
// trip, which turns the liveness surface into a cheap way to load the DB.
func TestReadiness_CachesTheProbeResult(t *testing.T) {
	db := &countingPinger{}
	h := readinessHandler(nil, db, nil, time.Minute)

	for range 20 {
		require.Equal(t, http.StatusOK, serveReady(h).Code)
	}
	assert.Equal(t, int32(1), db.calls.Load(), "20 requests must not be 20 pings")
}

func TestReadiness_CacheExpires(t *testing.T) {
	db := &countingPinger{}
	h := readinessHandler(nil, db, nil, time.Nanosecond)

	serveReady(h)
	time.Sleep(time.Millisecond)
	serveReady(h)

	assert.Equal(t, int32(2), db.calls.Load(), "a stale result must be refreshed")
}

func TestReadiness_ReportsFailure(t *testing.T) {
	db := &countingPinger{err: errors.New("connection refused")}
	h := readinessHandler(nil, db, nil, time.Minute)

	w := serveReady(h)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "degraded")
}

// A failing dependency must not be remembered for the full TTL, or the probe
// keeps reporting an outage that is already over.
func TestReadiness_FailureIsNotCached(t *testing.T) {
	db := &countingPinger{err: errors.New("connection refused")}
	h := readinessHandler(nil, db, nil, time.Minute)

	serveReady(h)
	db.err = nil
	w := serveReady(h)

	assert.Equal(t, int32(2), db.calls.Load())
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestReadiness_ChecksBothDependencies(t *testing.T) {
	db := &countingPinger{}
	redis := &countingPinger{}
	h := readinessHandler(nil, db, redis, time.Minute)

	w := serveReady(h)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"db":"ok"`)
	assert.Contains(t, w.Body.String(), `"redis":"ok"`)
}

func TestReadiness_NoDependencies_IsOK(t *testing.T) {
	w := serveReady(readinessHandler(nil, nil, nil, time.Minute))
	assert.Equal(t, http.StatusOK, w.Code)
}

type panickingPinger struct{}

func (panickingPinger) Ping(context.Context) error { panic("boom") }

// safego.Recover stops a panicking Ping from crashing the process, but the
// check's own result must still land in checks as "error" (and flip failed),
// not be silently dropped — a dropped check would leave /readyz reporting 200
// "ok" for a dependency that just panicked.
func TestReadiness_PanickingDependencyReportsFailure(t *testing.T) {
	h := readinessHandler(nil, panickingPinger{}, nil, time.Minute)

	w := serveReady(h)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"db":"error"`)
}
