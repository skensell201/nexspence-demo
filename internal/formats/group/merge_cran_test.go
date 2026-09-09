package group_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/cran"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// buildCranGroupEngine wires real cran handlers for members and a group over them.
func buildCranGroupEngine(t *testing.T, groupName string, memberNames ...string) *gin.Engine {
	t.Helper()

	repos := make([]*domain.Repository, 0, len(memberNames)+1)
	ms := make([]interface{}, len(memberNames))
	for i, name := range memberNames {
		repos = append(repos, testutil.SimpleRepo(name, "cran"))
		ms[i] = name
	}
	repos = append(repos, &domain.Repository{
		ID: "repo-" + groupName, Name: groupName, Format: "cran",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": ms},
	})

	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	cranH := cran.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"cran": cranH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		cranH.ServeHTTP(c)
	})
	return r
}

func putPkg(t *testing.T, r *gin.Engine, repoName, filename string) {
	t.Helper()
	body := "pkg-payload-" + filename
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+"/src/contrib/"+filename, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Contains(t, []int{http.StatusCreated, http.StatusOK}, w.Code, "upload %s: %s", filename, w.Body.String())
}

// A group's PACKAGES must union every member's packages — first-non-404
// fan-out would shadow every member after the first one that answers.
func TestGroupMerge_CranPackagesUnionsMembers(t *testing.T) {
	r := buildCranGroupEngine(t, "rg", "rd1", "rd2")
	putPkg(t, r, "rd1", "dplyr_1.1.4.tar.gz")
	putPkg(t, r, "rd2", "ggplot2_3.5.0.tar.gz")

	w := get(r, "/repository/rg/src/contrib/PACKAGES")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, "Package: dplyr")
	assert.Contains(t, body, "Package: ggplot2")

	// Both members' packages are reachable through the group.
	dplyr := get(r, "/repository/rg/src/contrib/dplyr_1.1.4.tar.gz")
	assert.Equal(t, http.StatusOK, dplyr.Code)
	ggplot2 := get(r, "/repository/rg/src/contrib/ggplot2_3.5.0.tar.gz")
	assert.Equal(t, http.StatusOK, ggplot2.Code)
}

// The gzip flavor has to be the same document as the plain one, not a
// separately merged one.
func TestGroupMerge_CranPackagesGzUnzipsToPlain(t *testing.T) {
	r := buildCranGroupEngine(t, "rg2", "rh1", "rh2")
	putPkg(t, r, "rh1", "dplyr_1.1.4.tar.gz")
	putPkg(t, r, "rh2", "stringr_1.5.1.tar.gz")

	plain := get(r, "/repository/rg2/src/contrib/PACKAGES").Body.Bytes()
	gz := get(r, "/repository/rg2/src/contrib/PACKAGES.gz").Body.Bytes()

	zr, err := gzip.NewReader(bytes.NewReader(gz))
	require.NoError(t, err)
	unzipped, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, string(plain), string(unzipped))
}

// A group with a proxy member must never relay the proxy's real upstream
// PACKAGES.rds: an R client reads that document before PACKAGES.gz/PACKAGES
// (utils::available.packages), and a real, parseable .rds from upstream would
// let R stop right there — silently hiding every hosted-only package forever,
// not just until the next request. Regression for the review on PR #430.
func TestGroupMerge_CranNeverRelaysProxyRDS(t *testing.T) {
	orig := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	t.Cleanup(func() { repoproxy.UpstreamClient = orig })

	const rdsMarker = "REAL-UPSTREAM-RDS-BINARY"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/PACKAGES.rds") {
			_, _ = w.Write([]byte(rdsMarker))
			return
		}
		_, _ = w.Write([]byte("Package: upstreamonly\nVersion: 1.0\n"))
	}))
	defer upstream.Close()

	hosted := testutil.SimpleRepo("rj-hosted", "cran")
	proxy := testutil.SimpleRepo("rj-proxy", "cran")
	proxy.Type = domain.TypeProxy
	proxy.ProxyConfig = map[string]any{"remote_url": upstream.URL}

	// Proxy listed ahead of hosted: this is the actual failure mode, not just
	// any order — before this fix, a hosted member answering first with its own
	// 405 would (by accident) stop the old first-non-404 fan-out before proxy
	// ever got a turn. With proxy first, its 200 wins the old fan-out outright.
	groupDef := &domain.Repository{
		ID: "repo-rj", Name: "rj", Format: "cran",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{"rj-proxy", "rj-hosted"}},
	}

	repoRepo := testutil.NewRepoRepo(hosted, proxy, groupDef)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	cranH := cran.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"cran": cranH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		cranH.ServeHTTP(c)
	})

	putPkg(t, r, "rj-hosted", "onlyhosted_2.0.tar.gz")

	rds := get(r, "/repository/rj/src/contrib/PACKAGES.rds")
	assert.NotContains(t, rds.Body.String(), rdsMarker,
		"the group must never answer with the proxy's real upstream .rds — R would parse it successfully and never fall back to the merged PACKAGES.gz")
	// #436: relaying a member's document here also meant transferring it for
	// nothing (a real proxy's plain PACKAGES can be tens of MB) before R even
	// discards it — cran.Handler implements GroupIndexStrictMerger so this
	// answers a short 502 instead of the proxy's document.
	assert.Equal(t, http.StatusBadGateway, rds.Code, "the refusal must fail the group honestly, not degrade to a member's document")
	assert.NotContains(t, rds.Body.String(), "upstreamonly", "the 502 body must be an error, not the proxy member's own document")
	assert.NotContains(t, rds.Body.String(), "onlyhosted", "the 502 body must be an error, not the hosted member's own document")

	// The properly mergeable flavors are unaffected: both members still show up.
	plain := get(r, "/repository/rj/src/contrib/PACKAGES")
	require.Equal(t, http.StatusOK, plain.Code)
	assert.Contains(t, plain.Body.String(), "Package: onlyhosted")
	assert.Contains(t, plain.Body.String(), "Package: upstreamonly")
}

// A one-member group is that member: its PACKAGES must match the member's own.
func TestGroupMerge_CranSingleMemberMatchesMember(t *testing.T) {
	r := buildCranGroupEngine(t, "rg3", "ri1")
	putPkg(t, r, "ri1", "dplyr_1.1.4.tar.gz")

	direct := get(r, "/repository/ri1/src/contrib/PACKAGES")
	viaGroup := get(r, "/repository/rg3/src/contrib/PACKAGES")
	require.Equal(t, http.StatusOK, viaGroup.Code)
	assert.Equal(t, direct.Body.String(), viaGroup.Body.String())
}
