package cran_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/cran"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// useUnguardedUpstream swaps the SSRF-guarded upstream client for a plain one so
// cache-miss fetches can reach the loopback httptest server used as a fake mirror.
func useUnguardedUpstream(t *testing.T) {
	t.Helper()
	orig := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	t.Cleanup(func() { repoproxy.UpstreamClient = orig })
}

// setupProxy wires a cran proxy repo pointed at upstream and returns the engine
// plus the component/asset mocks so tests can inspect what was cached.
func setupProxy(t *testing.T, name, upstream string) (*gin.Engine, *testutil.ComponentRepo, *testutil.AssetRepo) {
	t.Helper()
	repo := testutil.SimpleRepo(name, "cran")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": upstream}

	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     assets,
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := cran.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, comps, assets
}

func getProxy(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

// A cached tarball must land on a component named after the package, with the
// version parsed out of the filename — same coordinates a hosted upload gets.
func TestCRANProxy_CachedTarball_HasPackageCoordinates(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tarball-bytes"))
	}))
	defer upstream.Close()

	r, comps, _ := setupProxy(t, "cran-proxy", upstream.URL)

	w := getProxy(t, r, "/repository/cran-proxy/src/contrib/dplyr_1.1.4.tar.gz")
	require.Equal(t, http.StatusOK, w.Code)

	page, err := comps.List(t.Context(), "cran-proxy", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "dplyr", page.Items[0].Name)
	assert.Equal(t, "1.1.4", page.Items[0].Version)
}

// The PACKAGES index is not a package: it gets its own component keyed by
// path, so it stays individually browsable and deletable.
func TestCRANProxy_CachedPackagesIndex_IsPerPathComponent(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Package: dplyr\nVersion: 1.1.4\n"))
	}))
	defer upstream.Close()

	r, comps, _ := setupProxy(t, "cran-proxy", upstream.URL)

	require.Equal(t, http.StatusOK, getProxy(t, r, "/repository/cran-proxy/src/contrib/PACKAGES").Code)

	page, err := comps.List(t.Context(), "cran-proxy", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "src/contrib/PACKAGES", page.Items[0].Name)
	assert.Equal(t, "metadata", page.Items[0].Version)
}

// Every cached asset must be reachable from the component that owns it —
// this is what the browse row needs in order to delete it.
func TestCRANProxy_CachedAsset_LinkedToComponent(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tarball-bytes"))
	}))
	defer upstream.Close()

	r, comps, assets := setupProxy(t, "cran-proxy", upstream.URL)
	require.Equal(t, http.StatusOK,
		getProxy(t, r, "/repository/cran-proxy/src/contrib/ggplot2_3.5.0.tar.gz").Code)

	page, err := comps.List(t.Context(), "cran-proxy", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	owned, err := assets.ListByComponentID(t.Context(), page.Items[0].ID)
	require.NoError(t, err)
	require.Len(t, owned, 1)
	assert.Equal(t, "/src/contrib/ggplot2_3.5.0.tar.gz", owned[0].Path)
}

// A proxy repo with no repository record at all (unresolvable) falls through
// to the hosted path, same as apt/every other format handler.
func TestCRANProxy_UnknownRepo_FallsThroughToHosted(t *testing.T) {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := cran.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	w := getProxy(t, r, "/repository/does-not-exist/src/contrib/PACKAGES")
	assert.Equal(t, http.StatusOK, w.Code, "hosted path serves an empty index for an unknown repo rather than erroring")
}
