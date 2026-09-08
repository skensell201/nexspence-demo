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

// A one-member group is that member: its PACKAGES must match the member's own.
func TestGroupMerge_CranSingleMemberMatchesMember(t *testing.T) {
	r := buildCranGroupEngine(t, "rg3", "ri1")
	putPkg(t, r, "ri1", "dplyr_1.1.4.tar.gz")

	direct := get(r, "/repository/ri1/src/contrib/PACKAGES")
	viaGroup := get(r, "/repository/rg3/src/contrib/PACKAGES")
	require.Equal(t, http.StatusOK, viaGroup.Code)
	assert.Equal(t, direct.Body.String(), viaGroup.Body.String())
}
