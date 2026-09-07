package nuget_test

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
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A proxied package must be registered under its real name and version: the
// OSV/Trivy scan queries by comp.Name/comp.Version, so a path-fallback name
// and placeholder version make every package pulled through a NuGet proxy
// invisible to vulnerability scanning — the same root cause #336 closed for
// Cargo. UpstreamClient is unguarded package-wide via TestMain
// (proxy_testmain_test.go).
func setupNuGetProxy(t *testing.T, upstreamBody string) (*gin.Engine, *testutil.ComponentRepo) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(upstreamBody))
	}))
	t.Cleanup(srv.Close)

	repo := &domain.Repository{
		ID: "repo-nuget-proxy", Name: "nuget-proxy", Format: domain.RepoFormat("nuget"),
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
	h := nuget.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, comps
}

// A proxy repo forwards whatever local path the client requested straight
// onto upstream — a real client (nuget.exe, dotnet) requests packages at
// "/v3-flatcontainer/" (hyphenated, the shape nuget.org and most real feeds
// advertise via config.json/index.json), not the "/v3/flatcontainer/"
// (slash) convention this handler's own hosted-repo routes use locally.
func TestNuGet_ProxyDownload_RegistersRealPackageCoords(t *testing.T) {
	r, comps := setupNuGetProxy(t, "nupkg-bytes")

	req := httptest.NewRequest(http.MethodGet,
		"/repository/nuget-proxy/v3-flatcontainer/newtonsoft.json/13.0.1/newtonsoft.json.13.0.1.nupkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	page, err := comps.Search(context.Background(), domain.SearchParams{Repository: "nuget-proxy", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "the cached package must be registered as one component")
	assert.Equal(t, "newtonsoft.json", page.Items[0].Name,
		"the component carries the package's real name — the OSV query is built from it")
	assert.Equal(t, "13.0.1", page.Items[0].Version,
		"the component carries the package's real version — a placeholder never matches an advisory range")
}

// The slash convention this handler's own hosted routes use locally must
// keep working too — proxyCoords matches by .nupkg suffix, not a fixed
// prefix, so either shape resolves.
func TestNuGet_ProxyDownload_SlashConventionAlsoRegistersRealCoords(t *testing.T) {
	r, comps := setupNuGetProxy(t, "nupkg-bytes")

	req := httptest.NewRequest(http.MethodGet,
		"/repository/nuget-proxy/v3/flatcontainer/newtonsoft.json/13.0.1/newtonsoft.json.13.0.1.nupkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	page, err := comps.Search(context.Background(), domain.SearchParams{Repository: "nuget-proxy", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "newtonsoft.json", page.Items[0].Name)
	assert.Equal(t, "13.0.1", page.Items[0].Version)
}

// The registration-index page is versionless metadata and keeps the generic
// path-fallback treatment.
func TestNuGet_ProxyRegistrationIndex_KeepsMetadataFallback(t *testing.T) {
	r, comps := setupNuGetProxy(t, `{"count":0,"items":[]}`)

	req := httptest.NewRequest(http.MethodGet, "/repository/nuget-proxy/v3/registration/newtonsoft.json/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	page, err := comps.Search(context.Background(), domain.SearchParams{Repository: "nuget-proxy", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.NotEqual(t, "13.0.1", page.Items[0].Version, "a registration index page must not register with .nupkg-style coords")
}
