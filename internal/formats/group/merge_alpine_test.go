package group_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/alpine"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// buildAlpineGroupEngine wires real alpine handlers for members and a group over them.
func buildAlpineGroupEngine(t *testing.T, groupName string, memberNames ...string) *gin.Engine {
	t.Helper()

	repos := make([]*domain.Repository, 0, len(memberNames)+1)
	ms := make([]interface{}, len(memberNames))
	for i, name := range memberNames {
		repos = append(repos, testutil.SimpleRepo(name, "alpine"))
		ms[i] = name
	}
	repos = append(repos, &domain.Repository{
		ID: "repo-" + groupName, Name: groupName, Format: "alpine",
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
	alpineH := alpine.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"alpine": alpineH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		alpineH.ServeHTTP(c)
	})
	return r
}

// gzMember gzips data as one independent gzip member — real .apk files are a
// concatenation of these (control tar.gz + data tar.gz, unsigned).
func gzMember(data []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write(data)
	_ = gw.Close()
	return buf.Bytes()
}

func fakeApkBytes(seed string) []byte {
	return append(gzMember([]byte(seed+"-control")), gzMember([]byte(seed+"-data"))...)
}

func putApkPkg(t *testing.T, r *gin.Engine, repoName, arch, filename string) {
	t.Helper()
	body := fakeApkBytes(filename)
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+"/"+arch+"/"+filename, bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Contains(t, []int{http.StatusCreated, http.StatusOK}, w.Code, "upload %s: %s", filename, w.Body.String())
}

// untarApkindex extracts the plain APKINDEX text from a .tar.gz response body.
func untarApkindex(t *testing.T, body []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	require.NoError(t, err)
	require.Equal(t, "APKINDEX", hdr.Name)
	var buf bytes.Buffer
	_, err = buf.ReadFrom(tr)
	require.NoError(t, err)
	return buf.String()
}

// A group's APKINDEX must union every member's packages — first-non-404
// fan-out would shadow every member after the first one that answers.
func TestGroupMerge_AlpinePackagesUnionsMembers(t *testing.T) {
	r := buildAlpineGroupEngine(t, "ag", "ad1", "ad2")
	putApkPkg(t, r, "ad1", "x86_64", "curl-8.9.0-r0.apk")
	putApkPkg(t, r, "ad2", "x86_64", "vim-9.0-r1.apk")

	w := get(r, "/repository/ag/x86_64/APKINDEX.tar.gz")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	plain := untarApkindex(t, w.Body.Bytes())
	assert.Contains(t, plain, "P:curl")
	assert.Contains(t, plain, "P:vim")

	// Both members' packages are reachable through the group.
	curl := get(r, "/repository/ag/x86_64/curl-8.9.0-r0.apk")
	assert.Equal(t, http.StatusOK, curl.Code)
	vim := get(r, "/repository/ag/x86_64/vim-9.0-r1.apk")
	assert.Equal(t, http.StatusOK, vim.Code)
}

// Different architectures must not bleed into each other's merged index.
func TestGroupMerge_AlpineFiltersByArch(t *testing.T) {
	r := buildAlpineGroupEngine(t, "ag2", "ae1", "ae2")
	putApkPkg(t, r, "ae1", "x86_64", "curl-8.9.0-r0.apk")
	putApkPkg(t, r, "ae2", "aarch64", "vim-9.0-r1.apk")

	x86 := get(r, "/repository/ag2/x86_64/APKINDEX.tar.gz")
	require.Equal(t, http.StatusOK, x86.Code)
	x86Plain := untarApkindex(t, x86.Body.Bytes())
	assert.Contains(t, x86Plain, "P:curl")
	assert.NotContains(t, x86Plain, "P:vim")

	arm := get(r, "/repository/ag2/aarch64/APKINDEX.tar.gz")
	require.Equal(t, http.StatusOK, arm.Code)
	armPlain := untarApkindex(t, arm.Body.Bytes())
	assert.Contains(t, armPlain, "P:vim")
	assert.NotContains(t, armPlain, "P:curl")
}

// A one-member group is that member: its APKINDEX must match the member's own.
func TestGroupMerge_AlpineSingleMemberMatchesMember(t *testing.T) {
	r := buildAlpineGroupEngine(t, "ag3", "af1")
	putApkPkg(t, r, "af1", "x86_64", "curl-8.9.0-r0.apk")

	direct := get(r, "/repository/af1/x86_64/APKINDEX.tar.gz")
	viaGroup := get(r, "/repository/ag3/x86_64/APKINDEX.tar.gz")
	require.Equal(t, http.StatusOK, viaGroup.Code)
	assert.Equal(t, untarApkindex(t, direct.Body.Bytes()), untarApkindex(t, viaGroup.Body.Bytes()))
}

// The group must never relay a proxy member's real upstream index unmerged —
// same shadowing problem apt/#99 and cran solved for their own index paths.
func TestGroupMerge_AlpineNeverRelaysProxyIndexUnmerged(t *testing.T) {
	orig := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	t.Cleanup(func() { repoproxy.UpstreamClient = orig })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "APKINDEX.tar.gz") {
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(fakeApkindexTarGz(t, "P:upstreamonly\nV:1.0-r0\nA:x86_64\nS:1\nI:1\n"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	hosted := testutil.SimpleRepo("ah-hosted", "alpine")
	proxy := testutil.SimpleRepo("ah-proxy", "alpine")
	proxy.Type = domain.TypeProxy
	proxy.ProxyConfig = map[string]any{"remote_url": upstream.URL}

	groupDef := &domain.Repository{
		ID: "repo-ah", Name: "ah", Format: "alpine",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{"ah-proxy", "ah-hosted"}},
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
	alpineH := alpine.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"alpine": alpineH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		alpineH.ServeHTTP(c)
	})

	putApkPkg(t, r, "ah-hosted", "x86_64", "onlyhosted-2.0-r0.apk")

	w := get(r, "/repository/ah/x86_64/APKINDEX.tar.gz")
	require.Equal(t, http.StatusOK, w.Code)
	plain := untarApkindex(t, w.Body.Bytes())
	assert.Contains(t, plain, "P:onlyhosted")
	assert.Contains(t, plain, "P:upstreamonly", "the merge must still surface the proxy member's own packages")
}

func fakeApkindexTarGz(t *testing.T, plain string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "APKINDEX", Mode: 0o644, Size: int64(len(plain))}))
	_, err := tw.Write([]byte(plain))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}
