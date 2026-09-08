package api

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func headerFor(t *testing.T, policy, path string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(securityHeaders(policy))
	r.GET("/*any", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Header().Get("Content-Security-Policy")
}

// The SPA keeps its JWT in localStorage, which is normal for a bearer-token
// client but means any future XSS is a full token theft. A CSP is the defense
// in depth that was missing.
func TestCSP_ServedOnTheUI(t *testing.T) {
	got := headerFor(t, DefaultCSP, "/repositories")
	require.NotEmpty(t, got)

	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	} {
		assert.Contains(t, got, want)
	}
	assert.NotContains(t, got, "script-src 'self' 'unsafe-inline'",
		"inline script is the thing being prevented")
}

// Artifact paths serve content users uploaded — including raw repositories used
// to host documentation sites, which legitimately carry their own scripts and
// styles. Applying the UI policy there would break a shipped feature.
func TestCSP_NotAppliedToArtifactPaths(t *testing.T) {
	for _, path := range []string{
		"/repository/raw-docs/index.html",
		"/v2/myimage/manifests/latest",
	} {
		assert.Empty(t, headerFor(t, DefaultCSP, path), "path %s", path)
	}
}

// An operator hosting the UI behind something with its own policy, or needing a
// CDN, can replace or disable it.
func TestCSP_Configurable(t *testing.T) {
	custom := "default-src 'none'"
	assert.Equal(t, custom, headerFor(t, custom, "/"))
	assert.Empty(t, headerFor(t, "", "/"), "empty policy disables the header")
}

// The other hardening headers keep applying everywhere, artifacts included.
func TestSecurityHeaders_AppliedEverywhere(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(securityHeaders(DefaultCSP))
	r.GET("/*any", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/raw/x.html", nil))

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
}

// The policy has to actually admit the bundled UI: Vite emits a module script
// and a stylesheet, both same-origin, plus data: URIs for small assets.
func TestDefaultCSP_AllowsTheBundledUI(t *testing.T) {
	for _, want := range []string{
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src 'self'",
	} {
		assert.Contains(t, DefaultCSP, want)
	}
	// Vite injects the stylesheet via a <style> tag in dev and a link in prod;
	// component libraries commonly inject <style> at runtime.
	assert.True(t, strings.Contains(DefaultCSP, "style-src 'self' 'unsafe-inline'"),
		"style-src needs unsafe-inline for runtime-injected styles: %s", DefaultCSP)
}

// The UI used to load its webfonts from Google, which cost the policy two
// allowlist entries and the app its only third-party request. The Geist
// families ship with the bundle now (#431), so the policy names no external
// origin at all — and an entry added back would have to justify itself here
// first.
func TestDefaultCSP_NamesNoThirdPartyOrigin(t *testing.T) {
	assert.NotContains(t, DefaultCSP, "https://",
		"the UI is entirely same-origin; an external origin here needs a reason: %s", DefaultCSP)
}

// Guards against the UI gaining a new external dependency that the policy does
// not know about — in the document itself, and in the stylesheets, which is
// where a webfont would come back from. Skipped when the sources are not
// available.
func TestDefaultCSP_CoversEveryExternalOriginTheUIReferences(t *testing.T) {
	frontend := filepath.Join("..", "..", "frontend")
	if _, err := os.Stat(frontend); err != nil {
		t.Skip("frontend sources not available")
	}

	sources := []string{filepath.Join(frontend, "index.html")}
	err := filepath.WalkDir(filepath.Join(frontend, "src"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".css") {
			sources = append(sources, path)
		}
		return nil
	})
	require.NoError(t, err)

	external := regexp.MustCompile(`https://[a-zA-Z0-9.-]+`)
	for _, src := range sources {
		raw, err := os.ReadFile(src)
		require.NoError(t, err)
		for _, o := range external.FindAllString(string(raw), -1) {
			assert.Contains(t, DefaultCSP, o,
				"%s references %s but the CSP does not allow it", src, o)
		}
	}
}
