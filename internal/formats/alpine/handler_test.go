package alpine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
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
	h := New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func putApk(r *gin.Engine, repoName, path string, content []byte) int {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+path,
		strings.NewReader(string(content)))
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestAlpine_UploadAndDownload(t *testing.T) {
	repo := testutil.SimpleRepo("apks", "alpine")
	r := setup(repo)
	apk := fakeApk([]byte("curl-control"), []byte("curl-data"))

	require.Equal(t, http.StatusCreated,
		putApk(r, "apks", "/x86_64/curl-8.9.0-r0.apk", apk))

	req := httptest.NewRequest(http.MethodGet, "/repository/apks/x86_64/curl-8.9.0-r0.apk", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, apk, w.Body.Bytes())
}

func TestAlpine_Upload_RejectsNonGzipContent(t *testing.T) {
	repo := testutil.SimpleRepo("apks-bad", "alpine")
	r := setup(repo)

	code := putApk(r, "apks-bad", "/x86_64/broken-1.0-r0.apk", []byte("not gzip at all"))
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestAlpine_Index_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("apks2", "alpine")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/apks2/x86_64/APKINDEX.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	plain, err := unpackIndexTarGz(w.Body.Bytes())
	require.NoError(t, err)
	assert.Equal(t, "", plain)
}

func TestAlpine_Index_ShowsUploadedPackage(t *testing.T) {
	repo := testutil.SimpleRepo("apks3", "alpine")
	r := setup(repo)
	apk := fakeApk([]byte("vim-control"), []byte("vim-data"))

	require.Equal(t, http.StatusCreated,
		putApk(r, "apks3", "/x86_64/vim-9.0-r1.apk", apk))

	req := httptest.NewRequest(http.MethodGet, "/repository/apks3/x86_64/APKINDEX.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	plain, err := unpackIndexTarGz(w.Body.Bytes())
	require.NoError(t, err)
	assert.Contains(t, plain, "P:vim")
	assert.Contains(t, plain, "V:9.0-r1")
	assert.Contains(t, plain, "A:x86_64")
	assert.Contains(t, plain, "C:Q1")
}

// TestAlpine_Upload_ParsesRealPKGInfo verifies the full path a real client
// depends on: the index generated from an uploaded .apk carries D: (depends),
// so a real apk client can resolve shared-library dependencies through this
// repository — the exact gap live verification against a real Alpine
// container caught (installing curl "succeeded" with no D: line, but the
// binary failed to run because libcurl was never pulled in).
func TestAlpine_Upload_ParsesRealPKGInfo(t *testing.T) {
	repo := testutil.SimpleRepo("apks-pkginfo", "alpine")
	r := setup(repo)

	control := tarWith(t, ".PKGINFO", []byte(realPKGInfo))
	data := tarWith(t, "usr/bin/curl", []byte("binary-content"))
	apk := append(gzipMember(control), gzipMember(data)...)

	// Filename intentionally does NOT match .PKGINFO's pkgname/pkgver — real
	// .PKGINFO must win, same as the real `apk index` tool.
	require.Equal(t, http.StatusCreated,
		putApk(r, "apks-pkginfo", "/x86_64/whatever-name-0.0.1.apk", apk))

	req := httptest.NewRequest(http.MethodGet, "/repository/apks-pkginfo/x86_64/APKINDEX.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	plain, err := unpackIndexTarGz(w.Body.Bytes())
	require.NoError(t, err)
	assert.Contains(t, plain, "P:curl")
	assert.Contains(t, plain, "V:8.22.0-r0")
	assert.Contains(t, plain, "T:URL retrieval utility and library")
	assert.Contains(t, plain, "L:curl")
	assert.Contains(t, plain, "I:285023")
	assert.Contains(t, plain, "D:")
	assert.Contains(t, plain, "so:libcurl.so.4")
	assert.Contains(t, plain, "p:")
	assert.Contains(t, plain, "cmd:curl=8.22.0-r0")
}

func TestAlpine_Index_FiltersByArch(t *testing.T) {
	repo := testutil.SimpleRepo("apks4", "alpine")
	r := setup(repo)
	apk := fakeApk([]byte("c1"), []byte("d1"))
	require.Equal(t, http.StatusCreated, putApk(r, "apks4", "/aarch64/curl-8.9.0-r0.apk", apk))

	req := httptest.NewRequest(http.MethodGet, "/repository/apks4/x86_64/APKINDEX.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	plain, err := unpackIndexTarGz(w.Body.Bytes())
	require.NoError(t, err)
	assert.Equal(t, "", plain, "aarch64 package must not appear in the x86_64 index")
}

func TestAlpine_Delete(t *testing.T) {
	repo := testutil.SimpleRepo("apks5", "alpine")
	r := setup(repo)
	apk := fakeApk([]byte("c"), []byte("d"))
	require.Equal(t, http.StatusCreated, putApk(r, "apks5", "/x86_64/vim-9.0-r1.apk", apk))

	req := httptest.NewRequest(http.MethodDelete, "/repository/apks5/x86_64/vim-9.0-r1.apk", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAlpine_GetNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("apks6", "alpine")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/apks6/x86_64/missing-1.0-r0.apk", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAlpine_ProxyRejectMutation(t *testing.T) {
	repo := testutil.SimpleRepo("apks7", "alpine")
	repo.Type = domain.TypeProxy
	r := setup(repo)

	code := putApk(r, "apks7", "/x86_64/pkg-1.0-r0.apk", fakeApk([]byte("c"), []byte("d")))
	assert.Equal(t, http.StatusMethodNotAllowed, code)
}

func TestApkCoords(t *testing.T) {
	name, version := apkCoords("curl-8.9.0-r0.apk")
	assert.Equal(t, "curl", name)
	assert.Equal(t, "8.9.0-r0", version)

	// A hyphenated package name must not be split at the wrong point.
	name, version = apkCoords("libxml2-dev-2.12.6-r0.apk")
	assert.Equal(t, "libxml2-dev", name)
	assert.Equal(t, "2.12.6-r0", version)

	// Non-conforming name: whole filename kept, same fallback as apt's debCoords.
	name, version = apkCoords("weird.apk")
	assert.Equal(t, "weird", name)
	assert.Equal(t, "0.0.0", version)
}
