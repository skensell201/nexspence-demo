package yum_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/formats/yum"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A proxied RPM must be registered under its real NVR (name-version-release):
// the OSV/Trivy scan queries by comp.Name/comp.Version, so a path-fallback
// name and placeholder version make every RPM pulled through a Yum proxy
// invisible to vulnerability scanning — the same root cause #336 closed for
// Cargo.

func setupYumProxy(t *testing.T, upstreamBody string) (*gin.Engine, *testutil.ComponentRepo) {
	t.Helper()
	orig := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	t.Cleanup(func() { repoproxy.UpstreamClient = orig })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(upstreamBody))
	}))
	t.Cleanup(srv.Close)

	repo := &domain.Repository{
		ID: "repo-yum-proxy", Name: "yum-proxy", Format: domain.RepoFormat("yum"),
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": srv.URL},
	}
	comps := testutil.NewComponentRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := yum.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, comps
}

func TestYum_ProxyDownload_RegistersRealRPMCoords(t *testing.T) {
	r, comps := setupYumProxy(t, "rpm-bytes")

	req := httptest.NewRequest(http.MethodGet,
		"/repository/yum-proxy/openssl-1.1.1k-1.el8.x86_64.rpm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	page, err := comps.Search(context.Background(), domain.SearchParams{Repository: "yum-proxy", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "the cached RPM must be registered as one component")
	assert.Equal(t, "openssl", page.Items[0].Name,
		"the component carries the RPM's real name — the OSV query is built from it")
	assert.Equal(t, "1.1.1k", page.Items[0].Version,
		"the component carries the RPM's real version — a placeholder never matches an advisory range")
}

// repodata/ holds mutable index files, not versioned packages, and keeps the
// generic path-fallback metadata treatment.
func TestYum_ProxyRepomd_KeepsMetadataFallback(t *testing.T) {
	r, comps := setupYumProxy(t, `<?xml version="1.0"?><repomd/>`)

	req := httptest.NewRequest(http.MethodGet, "/repository/yum-proxy/repodata/repomd.xml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	page, err := comps.Search(context.Background(), domain.SearchParams{Repository: "yum-proxy", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.NotEqual(t, "openssl", page.Items[0].Name, "an index file must not register with RPM-style coords")
}
