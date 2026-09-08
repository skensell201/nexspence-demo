package cran_test

import (
	"bytes"
	"mime/multipart"
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
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

func setup(repo *domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := cran.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func putPkg(r *gin.Engine, repoName, path, content string) int {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+path,
		strings.NewReader(content))
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestCRAN_UploadAndDownload(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs", "cran")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putPkg(r, "rpkgs", "/src/contrib/dplyr_1.1.4.tar.gz", "pkg-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpkgs/src/contrib/dplyr_1.1.4.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pkg-bytes", w.Body.String())
}

func TestCRAN_RootUpload_NormalizesToSrcContrib(t *testing.T) {
	// Regression for the apt #46 pattern: a root-level PUT must not 405.
	repo := testutil.SimpleRepo("rpkgs-root", "cran")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putPkg(r, "rpkgs-root", "/ggplot2_3.5.0.tar.gz", "gg-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpkgs-root/src/contrib/ggplot2_3.5.0.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "gg-bytes", w.Body.String())

	req = httptest.NewRequest(http.MethodGet,
		"/repository/rpkgs-root/src/contrib/PACKAGES", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "ggplot2")
}

func TestCRAN_PackagesIndex_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs2", "cran")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpkgs2/src/contrib/PACKAGES", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "", w.Body.String())
}

func TestCRAN_PackagesIndex_ShowsPackage(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs3", "cran")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putPkg(r, "rpkgs3", "/src/contrib/jsonlite_1.8.8.tar.gz", "json-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpkgs3/src/contrib/PACKAGES", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Package: jsonlite")
	assert.Contains(t, w.Body.String(), "Version: 1.8.8")
}

func TestCRAN_PackagesIndexGz(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs-gz", "cran")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putPkg(r, "rpkgs-gz", "/src/contrib/xml2_1.3.6.tar.gz", "xml-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpkgs-gz/src/contrib/PACKAGES.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/x-gzip", w.Header().Get("Content-Type"))
}

func TestCRAN_Delete(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs4", "cran")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putPkg(r, "rpkgs4", "/src/contrib/stringr_1.5.1.tar.gz", "str-bytes"))

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/rpkgs4/src/contrib/stringr_1.5.1.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestCRAN_GetNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs5", "cran")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpkgs5/src/contrib/missing_1.0.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCRAN_ProxyRejectMutation(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs6", "cran")
	repo.Type = domain.TypeProxy

	r := setup(repo)
	code := putPkg(r, "rpkgs6", "/src/contrib/pkg_1.0.tar.gz", "data")
	assert.Equal(t, http.StatusMethodNotAllowed, code)
}

// postPkg uploads a tarball via POST to an explicit path (body = raw bytes).
func postPkg(r *gin.Engine, repoName, path, content string) int {
	req := httptest.NewRequest(http.MethodPost, "/repository/"+repoName+path,
		strings.NewReader(content))
	req.Header.Set("Content-Type", "application/x-gzip")
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// postPkgMultipart uploads a tarball via Nexus-style POST to the repo root
// (multipart/form-data with a "file" field carrying the filename).
func postPkgMultipart(r *gin.Engine, repoName, filename, content string) int {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", filename)
	_, _ = part.Write([]byte(content))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/repository/"+repoName+"/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestCRAN_PostPath_Upload(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs-post", "cran")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		postPkg(r, "rpkgs-post", "/purrr_1.0.2.tar.gz", "purrr-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpkgs-post/src/contrib/purrr_1.0.2.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "purrr-bytes", w.Body.String())
}

func TestCRAN_PostRootMultipart_Upload(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs-mp", "cran")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		postPkgMultipart(r, "rpkgs-mp", "tibble_3.2.1.tar.gz", "tibble-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpkgs-mp/src/contrib/tibble_3.2.1.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "tibble-bytes", w.Body.String())
}

func TestCRAN_Name(t *testing.T) {
	h := cran.New(formats.Deps{})
	assert.Equal(t, "cran", h.Name())
}

func TestCRAN_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("rpkgs7", "cran")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPatch, "/repository/rpkgs7/src/contrib/PACKAGES", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
