package repoproxy_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// maxRewrittenMetadataBytesForTest mirrors the unexported
// repoproxy.maxRewrittenMetadataBytes (32 MiB). Kept in sync manually since
// the constant isn't exported — a mismatch would fail these tests loudly
// (an "at limit" body would either get rejected or an "oversized" body would
// get accepted), not silently.
const maxRewrittenMetadataBytesForTest = 32 << 20

// storeAndServeResponse's buffered read (cache fill) is the first of the two
// unbounded reads this codebase had on the shared path npm/PyPI/NuGet proxy
// metadata through. A hostile, compromised, or simply oversized-by-accident
// upstream must not be able to exhaust server memory before any quota/size
// check runs.
func TestServeGETRewritten_CacheFill_OversizedBody_Rejected(t *testing.T) {
	useUnguardedUpstream(t)
	oversized := bytes.Repeat([]byte("a"), maxRewrittenMetadataBytesForTest+1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(oversized)
	}))
	defer upstream.Close()

	repo := proxyRepo("rwbig1", upstream.URL)
	d := makeDeps(repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/meta.json", nil)
	err := repoproxy.ServeGETRewritten(c, d, repo, "/meta.json", "", base.Coords{}, "text/plain", 0, bytes.ToUpper)
	require.Error(t, err, "an oversized rewritten-metadata response must be rejected, not buffered without limit")

	_, getErr := d.Assets.GetByPath(context.Background(), "rwbig1", "/meta.json")
	assert.Error(t, getErr, "a rejected response must not be cached")
}

// A body exactly at the cap must still be served normally — the limit
// rejects an overflow, it does not truncate a legitimate response one byte
// short of it.
func TestServeGETRewritten_CacheFill_AtLimit_Served(t *testing.T) {
	useUnguardedUpstream(t)
	atLimit := bytes.Repeat([]byte("a"), maxRewrittenMetadataBytesForTest)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(atLimit)
	}))
	defer upstream.Close()

	repo := proxyRepo("rwbig2", upstream.URL)
	d := makeDeps(repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/meta.json", nil)
	err := repoproxy.ServeGETRewritten(c, d, repo, "/meta.json", "", base.Coords{}, "text/plain", 0, bytes.ToUpper)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, w.Body.Bytes(), maxRewrittenMetadataBytesForTest)
}

// serveCachedAsset's own re-read (rewriting a cache HIT, not just a cache
// fill) is the second of the two unbounded reads — it needs the same cap
// independently of storeAndServeResponse's, since a cache entry could in
// principle predate this fix. Seed the cache directly (bypassing the
// fetch-time guard entirely) and confirm the re-read path still rejects it.
func TestServeGETRewritten_CacheHit_OversizedCachedBody_Rejected(t *testing.T) {
	useUnguardedUpstream(t)
	repo := proxyRepo("rwbig3", "http://unused.invalid")
	d := makeDeps(repo)

	oversized := bytes.Repeat([]byte("a"), maxRewrittenMetadataBytesForTest+1)
	_, err := base.StoreArtifact(context.Background(), d, "rwbig3", "/meta.json", "text/plain",
		base.Coords{}, bytes.NewReader(oversized), int64(len(oversized)))
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/meta.json", nil)
	// maxAge 0 (immutable): served straight from cache, no upstream contact —
	// exercises serveCachedAsset's read, not storeAndServeResponse's.
	err = repoproxy.ServeGETRewritten(c, d, repo, "/meta.json", "", base.Coords{}, "text/plain", 0, bytes.ToUpper)
	require.NoError(t, err, "the error is written directly onto the response, not returned")
	assert.Equal(t, http.StatusBadGateway, w.Code)
}
