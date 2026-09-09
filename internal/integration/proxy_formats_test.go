//go:build integration

// Real-shape proxy tests (#349): one test per proxy-capable format, each
// against an httptest fake shaped like that format's REAL public registry —
// the URL scheme the actual upstream serves, not the shape the handler's own
// code assumes. Both #347's Cargo bug and #349's NuGet bug shipped because
// unit-test mocks mirrored the handler's wrong assumption; every fake here is
// modeled on a live audit of the real registry (see #349's table).
package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createProxyRepo creates a proxy repository of the given format through the
// real API and registers cleanup.
func createProxyRepo(t *testing.T, format, name, remoteURL string) {
	t.Helper()
	token := login(t, "admin", "admin123")
	body := fmt.Sprintf(`{"name":%q,"online":true,"proxyConfig":{"remote_url":%q}}`, name, remoteURL)
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/"+format+"/proxy",
		strings.NewReader(body), token)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create %s proxy: %s", format, raw)
	t.Cleanup(func() {
		del := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/"+name, nil, token)
		del.Body.Close()
	})
}

// get fetches a path from the running server with admin auth and returns the
// response; the caller owns the body.
func get(t *testing.T, p string) *http.Response {
	t.Helper()
	token := login(t, "admin", "admin123")
	return authReq(t, http.MethodGet, p, nil, token)
}

func fetchOK(t *testing.T, p string) string {
	t.Helper()
	resp := get(t, p)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s: %s", p, raw)
	return string(raw)
}

// ── apt (archive.ubuntu.com shape) ───────────────────────────────

func TestProxyApt_RealShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dists/jammy/InRelease":
			fmt.Fprint(w, "-----BEGIN PGP SIGNED MESSAGE-----\nSuite: jammy\n")
		case "/dists/jammy/main/binary-amd64/Packages":
			fmt.Fprint(w, "Package: hello\nVersion: 2.10-3\nFilename: pool/main/h/hello/hello_2.10-3_amd64.deb\n")
		case "/pool/main/h/hello/hello_2.10-3_amd64.deb":
			fmt.Fprint(w, "deb-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "apt", "apt-real", up.URL)

	assert.Contains(t, fetchOK(t, "/repository/apt-real/dists/jammy/InRelease"), "Suite: jammy")
	assert.Contains(t, fetchOK(t, "/repository/apt-real/dists/jammy/main/binary-amd64/Packages"), "Package: hello")
	assert.Equal(t, "deb-bytes", fetchOK(t, "/repository/apt-real/pool/main/h/hello/hello_2.10-3_amd64.deb"))
}

// ── alpine (dl-cdn.alpinelinux.org shape) ────────────────────────

func TestProxyAlpine_RealShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x86_64/APKINDEX.tar.gz":
			w.Header().Set("Content-Type", "application/gzip")
			fmt.Fprint(w, "apkindex-bytes")
		case "/x86_64/curl-8.9.0-r0.apk":
			fmt.Fprint(w, "apk-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "alpine", "alpine-real", up.URL)

	assert.Contains(t, fetchOK(t, "/repository/alpine-real/x86_64/APKINDEX.tar.gz"), "apkindex-bytes")
	assert.Equal(t, "apk-bytes", fetchOK(t, "/repository/alpine-real/x86_64/curl-8.9.0-r0.apk"))
}

// ── cran (cran.r-project.org shape) ──────────────────────────────

func TestProxyCRAN_RealShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/src/contrib/PACKAGES":
			fmt.Fprint(w, "Package: dplyr\nVersion: 1.1.4\n")
		case "/src/contrib/dplyr_1.1.4.tar.gz":
			fmt.Fprint(w, "cran-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "cran", "cran-real", up.URL)

	assert.Contains(t, fetchOK(t, "/repository/cran-real/src/contrib/PACKAGES"), "Package: dplyr")
	assert.Equal(t, "cran-bytes", fetchOK(t, "/repository/cran-real/src/contrib/dplyr_1.1.4.tar.gz"))
}

// ── yum (EPEL shape) ─────────────────────────────────────────────

func TestProxyYum_RealShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repodata/repomd.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0"?><repomd><data type="primary"><location href="repodata/primary.xml.gz"/></data></repomd>`)
		case "/Packages/h/hello-2.10-1.el9.x86_64.rpm":
			fmt.Fprint(w, "rpm-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "yum", "yum-real", up.URL)

	assert.Contains(t, fetchOK(t, "/repository/yum-real/repodata/repomd.xml"), "repomd")
	assert.Equal(t, "rpm-bytes", fetchOK(t, "/repository/yum-real/Packages/h/hello-2.10-1.el9.x86_64.rpm"))
}

// ── conda (conda-forge channel shape) ────────────────────────────

func TestProxyConda_RealShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/noarch/repodata.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"info":{"subdir":"noarch"},"packages":{"tzdata-2024a-h0c530f3_0.tar.bz2":{"name":"tzdata","version":"2024a"}},"packages.conda":{}}`)
		case "/noarch/tzdata-2024a-h0c530f3_0.tar.bz2":
			fmt.Fprint(w, "conda-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "conda", "conda-real", up.URL)

	assert.Contains(t, fetchOK(t, "/repository/conda-real/noarch/repodata.json"), "tzdata-2024a")
	assert.Equal(t, "conda-bytes", fetchOK(t, "/repository/conda-real/noarch/tzdata-2024a-h0c530f3_0.tar.bz2"))
}

// ── conan (center2.conan.io v2 shape) ────────────────────────────

func TestProxyConan_RealShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/conans/zlib/1.2.11/_/_/latest":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"revision":"a1b2c3","time":"2024-01-01T00:00:00.000+0000"}`)
		case "/v2/conans/zlib/1.2.11/_/_/revisions/a1b2c3/files/conanfile.py":
			fmt.Fprint(w, "conanfile-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "conan", "conan-real", up.URL)

	assert.Contains(t, fetchOK(t, "/repository/conan-real/v2/conans/zlib/1.2.11/_/_/latest"), "a1b2c3")
	assert.Equal(t, "conanfile-bytes",
		fetchOK(t, "/repository/conan-real/v2/conans/zlib/1.2.11/_/_/revisions/a1b2c3/files/conanfile.py"))
}

// ── go modules (proxy.golang.org shape) ──────────────────────────

func TestProxyGoModules_RealShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rsc.io/quote/@v/list":
			fmt.Fprint(w, "v1.5.2\nv1.5.1\n")
		case "/rsc.io/quote/@v/v1.5.2.zip":
			w.Header().Set("Content-Type", "application/zip")
			fmt.Fprint(w, "zip-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	// The format segment is "go" (domain.FormatGo), not the package name.
	createProxyRepo(t, "go", "go-real", up.URL)

	assert.Contains(t, fetchOK(t, "/repository/go-real/rsc.io/quote/@v/list"), "v1.5.2")
	assert.Equal(t, "zip-bytes", fetchOK(t, "/repository/go-real/rsc.io/quote/@v/v1.5.2.zip"))
}

// ── helm (chart repo shape, absolute chart URLs in index.yaml) ───

func TestProxyHelm_RealShape(t *testing.T) {
	var up *httptest.Server
	up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			fmt.Fprintf(w, "apiVersion: v1\nentries:\n  demo:\n    - name: demo\n      version: 1.0.0\n      urls:\n        - %s/demo-1.0.0.tgz\n", up.URL)
		case "/demo-1.0.0.tgz":
			fmt.Fprint(w, "chart-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "helm", "helm-real", up.URL)

	idx := fetchOK(t, "/repository/helm-real/index.yaml")
	assert.Contains(t, idx, "/repository/helm-real/demo-1.0.0.tgz",
		"chart URLs must be rewritten onto the proxy")
	assert.NotContains(t, idx, up.URL, "upstream host must not leak into the rewritten index")
	assert.Equal(t, "chart-bytes", fetchOK(t, "/repository/helm-real/demo-1.0.0.tgz"))
}

// ── terraform (registry.terraform.io shape) ──────────────────────

func TestProxyTerraform_RealShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/providers/hashicorp/null/versions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"hashicorp/null","versions":[{"version":"3.2.2","platforms":[{"os":"linux","arch":"amd64"}]}]}`)
		case "/v1/modules/hashicorp/consul/aws/0.11.0.tar.gz":
			fmt.Fprint(w, "module-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "terraform", "tf-real", up.URL)

	assert.Contains(t, fetchOK(t, "/repository/tf-real/v1/providers/hashicorp/null/versions"), "3.2.2")
	assert.Equal(t, "module-bytes", fetchOK(t, "/repository/tf-real/v1/modules/hashicorp/consul/aws/0.11.0.tar.gz"))
}

// ── npm (registry.npmjs.org shape, scoped package) ───────────────

func TestProxyNpmScoped_RealShape(t *testing.T) {
	// registry.npmjs.org answers 405 to /@scope/name — the slash must reach it
	// percent-encoded exactly once (found live; the double-escape regression
	// was fixed alongside #347's investigation).
	var up *httptest.Server
	up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.RequestURI {
		case "/@types%2Fnode":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"name":"@types/node","versions":{"20.0.0":{"name":"@types/node","version":"20.0.0","dist":{"tarball":"%s/@types/node/-/node-20.0.0.tgz"}}}}`, up.URL)
		case "/@types/node/-/node-20.0.0.tgz":
			fmt.Fprint(w, "tgz-bytes")
		default:
			// The real registry 405s the decoded form of the packument path.
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "npm", "npm-real", up.URL)

	pack := fetchOK(t, "/repository/npm-real/@types/node")
	assert.Contains(t, pack, "/repository/npm-real/@types/node/-/node-20.0.0.tgz",
		"tarball URLs must be rewritten onto the proxy")
	assert.NotContains(t, pack, up.URL)
	assert.Equal(t, "tgz-bytes", fetchOK(t, "/repository/npm-real/@types/node/-/node-20.0.0.tgz"))
}

// ── pypi (pypi.org shape: /simple pages, files on a second host) ─

func TestProxyPyPI_RealShape(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/packages/aa/bb/requests-2.31.0.tar.gz" {
			fmt.Fprint(w, "sdist-bytes")
			return
		}
		http.NotFound(w, r)
	}))
	defer files.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// The handler pins Accept to text/html (#191); the real pypi.org would
		// otherwise serve PEP 691 JSON, which the rewriter can't process.
		case r.URL.Path == "/simple/requests" || r.URL.Path == "/simple/requests/":
			require.Contains(t, r.Header.Get("Accept"), "text/html",
				"simple-page fetch must force the HTML representation")
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><body><a href="%s/packages/aa/bb/requests-2.31.0.tar.gz#sha256=deadbeef">requests-2.31.0.tar.gz</a></body></html>`, files.URL)
		// pypi.org itself answers /packages/ with a redirect to
		// files.pythonhosted.org — a different host, which the proxy's
		// upstream client must follow.
		case strings.HasPrefix(r.URL.Path, "/packages/"):
			http.Redirect(w, r, files.URL+r.URL.Path, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "pypi", "pypi-real", up.URL)

	page := fetchOK(t, "/repository/pypi-real/simple/requests")
	assert.Contains(t, page, "/repository/pypi-real/packages/aa/bb/requests-2.31.0.tar.gz#sha256=deadbeef",
		"file hrefs must be rewritten onto the proxy, fragment preserved")
	assert.NotContains(t, page, files.URL)
	assert.Equal(t, "sdist-bytes", fetchOK(t, "/repository/pypi-real/packages/aa/bb/requests-2.31.0.tar.gz"),
		"file downloads follow the real registry's redirect to the file host")
}

// ── cargo (crates.io shape: sparse index + separate dl host) ─────

func TestProxyCargo_RealShape(t *testing.T) {
	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/crates/serde/1.0.0/download" {
			fmt.Fprint(w, "crate-bytes")
			return
		}
		http.NotFound(w, r)
	}))
	defer dl.Close()
	// index.crates.io: config.json + prefix-sharded lines at the ROOT — no
	// /index/ segment exists upstream (#347's bug: the local route prefix
	// leaked into upstream requests).
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"dl":"%s/api/v1/crates","api":"%s"}`, dl.URL, dl.URL)
		case "/se/rd/serde":
			fmt.Fprint(w, `{"name":"serde","vers":"1.0.0","deps":[],"cksum":"abc","features":{},"yanked":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer idx.Close()
	createProxyRepo(t, "cargo", "cargo-real", idx.URL)

	line := fetchOK(t, "/repository/cargo-real/index/se/rd/serde")
	assert.Contains(t, line, `"name":"serde"`)
	assert.Equal(t, "crate-bytes", fetchOK(t, "/repository/cargo-real/api/v1/crates/serde/1.0.0/download"),
		"downloads must follow the dl base from the upstream's own config.json (a different host on real crates.io)")
}

// ── nuget (api.nuget.org shape: /v3/index.json, /v3-flatcontainer) ─

func TestProxyNuGet_RealShape(t *testing.T) {
	var up *httptest.Server
	up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/index.json":
			fmt.Fprintf(w, `{"version":"3.0.0","resources":[{"@id":"%s/v3-flatcontainer/","@type":"PackageBaseAddress/3.0.0"}]}`, up.URL)
		case "/v3-flatcontainer/newtonsoft.json/index.json":
			fmt.Fprint(w, `{"versions":["13.0.3"]}`)
		case "/v3-flatcontainer/newtonsoft.json/13.0.3/newtonsoft.json.13.0.3.nupkg":
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "nupkg-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	createProxyRepo(t, "nuget", "nuget-real", up.URL)

	svcIdx := fetchOK(t, "/repository/nuget-real/index.json")
	assert.Contains(t, svcIdx, "/repository/nuget-real/v3-flatcontainer/",
		"service index resources keep the real hyphenated path, re-rooted locally")
	assert.NotContains(t, svcIdx, up.URL)
	assert.Contains(t, fetchOK(t, "/repository/nuget-real/v3-flatcontainer/newtonsoft.json/index.json"), "13.0.3")
	assert.Equal(t, "nupkg-bytes",
		fetchOK(t, "/repository/nuget-real/v3-flatcontainer/newtonsoft.json/13.0.3/newtonsoft.json.13.0.3.nupkg"))
}
