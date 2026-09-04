package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountScan wires the real ScanService (over mocks) as router.go does.
// The OSV client is pointed at a local httptest server so the non-Docker scan path
// is exercised without a live network call. The Trivy binary is forced to a
// nonexistent path so the Docker path resolves to ScannerMissing (503).
func mountScan(t *testing.T) (*gin.Engine, *testutil.ComponentRepo, *testutil.ScanResultRepo, *httptest.Server) {
	t.Helper()
	comps := testutil.NewComponentRepo()
	scanRepo := testutil.NewScanResultRepo()

	// Local OSV stub: returns one HIGH vuln for any query.
	osvSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"vulns":[{"id":"GHSA-xxxx","summary":"test vuln","database_specific":{"severity":"HIGH"}}]}`))
	}))
	t.Cleanup(osvSrv.Close)

	svc := service.NewScanService(comps, "http://localhost").WithScanResults(scanRepo)
	svc.OSVClient = &service.OSVClient{BaseURL: osvSrv.URL, HTTPClient: osvSrv.Client()}
	svc = svc.WithTrivy(service.TrivyOptions{Enabled: true, Bin: "/nonexistent/trivy-binary-xyz"}) // force ScannerMissing on Docker path
	svc.TrivyTimeout = 5 * time.Second

	h := handlers.NewScanHandler(svc)
	r := gin.New()
	r.POST("/api/v1/components/:id/scan", h.Scan)
	r.GET("/api/v1/components/:id/scan", h.GetScanResult)
	r.GET("/api/v1/security/summary", h.Summary)
	r.GET("/api/v1/security/vulnerabilities", h.Vulnerabilities)
	r.POST("/api/v1/security/scan/bulk", h.BulkScanHandler)
	return r, comps, scanRepo, osvSrv
}

func seedComponent(t *testing.T, comps *testutil.ComponentRepo, format, name, version string) *domain.Component {
	t.Helper()
	c := &domain.Component{Format: format, Name: name, Version: version, Repository: "repo1"}
	require.NoError(t, comps.Create(testContext(), c))
	return c
}

// ── Scan (single) ──────────────────────────────────────────────────────────────

func TestScanHandler_Scan_OSV_Success(t *testing.T) {
	r, comps, scanRepo, _ := mountScan(t)
	c := seedComponent(t, comps, "maven", "log4j-core", "2.14.0")

	rec := do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var res domain.ScanResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, domain.ScanStatusOK, res.Status)
	assert.Equal(t, 1, res.Summary.High)
	assert.Equal(t, 1, res.Summary.Total)
	require.Len(t, res.Findings, 1)
	assert.Equal(t, "GHSA-xxxx", res.Findings[0].ID)
	// A scan row should have been persisted.
	assert.NotEmpty(t, scanRepo.Rows())
}

func TestScanHandler_Scan_ComponentNotFound_400(t *testing.T) {
	// Scan returns "component not found" error → handler maps non-Trivy errors to 400.
	r, _, _, _ := mountScan(t)
	rec := do(t, r, http.MethodPost, "/api/v1/components/ghost/scan", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestScanHandler_Scan_UnsupportedFormat_400(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "raw", "file", "1")
	rec := do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestScanHandler_Scan_Docker_TrivyMissing_503(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "docker", "myimage", "latest")
	rec := do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan",
		map[string]any{"imageRef": "localhost/myimage:latest"})
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestScanHandler_Scan_RepoError_400(t *testing.T) {
	// Components.Get errors → propagated through Scan → handler maps to 400 (non-Trivy).
	r, comps, _, _ := mountScan(t)
	comps.Err = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/api/v1/components/any/scan", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── GetScanResult ──────────────────────────────────────────────────────────────

func TestScanHandler_GetScanResult_NoCache_204(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "docker", "img", "v1")
	rec := do(t, r, http.MethodGet, "/api/v1/components/"+c.ID+"/scan", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestScanHandler_GetScanResult_Cached_200(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "maven", "pkg", "1.0")
	// Run an OSV scan first so a result is cached in component.Extra.
	rec := do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodGet, "/api/v1/components/"+c.ID+"/scan", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var res domain.ScanResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, domain.ScanStatusOK, res.Status)
}

func TestScanHandler_GetScanResult_RepoError_500(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	comps.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/components/any/scan", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// Unlike ComponentHandler.Get (#291), this sibling endpoint never checked
// repository visibility — any authenticated user who knew or enumerated a
// component UUID from a private repository could read its full vulnerability
// report. 404, not 403, keeps the id unguessable.
func TestScanHandler_GetScanResult_DeniedWithoutPrivilege(t *testing.T) {
	comps := testutil.NewComponentRepo()
	scanRepo := testutil.NewScanResultRepo()
	repos := testutil.NewRepoRepo()

	require.NoError(t, repos.Create(testContext(), &domain.Repository{
		ID: "repo1", Name: "repo1", Format: domain.FormatMaven2, Type: domain.TypeHosted, AllowAnonymous: false,
	}))
	svc := service.NewScanService(comps, "http://localhost").WithScanResults(scanRepo)
	rbacSvc := service.NewRBACService(emptyRBACRepo{}, repos, zap.NewNop().Sugar(), true)
	h := handlers.NewScanHandler(svc).WithRBAC(comps, repos, rbacSvc)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "eve")
		c.Set("roles", []string{"nx-viewer"})
		c.Next()
	})
	r.GET("/api/v1/components/:id/scan", h.GetScanResult)

	c := seedComponent(t, comps, "maven", "pkg", "1.0")
	rec := do(t, r, http.MethodGet, "/api/v1/components/"+c.ID+"/scan", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ── Summary ────────────────────────────────────────────────────────────────────

func TestScanHandler_Summary_OK(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "maven", "pkg", "1.0")
	// Produce a persisted scan row via OSV scan (1 HIGH).
	require.Equal(t, http.StatusOK,
		do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil).Code)

	rec := do(t, r, http.MethodGet, "/api/v1/security/summary", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var s domain.SecuritySummary
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
	assert.Equal(t, 1, s.High)
	assert.Equal(t, 1, s.ScannedTotal)
}

// The full chain for a malicious-package report, over the wire: OSV response →
// service → persisted row → aggregate → JSON body. It is the one seam where a
// wrong struct tag would go unnoticed by every other test.
func TestScanHandler_Summary_ReportsMaliciousCount(t *testing.T) {
	comps := testutil.NewComponentRepo()
	scanRepo := testutil.NewScanResultRepo()

	osvSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"vulns":[{"id":"MAL-2025-46974","summary":"Malicious code in debug"}]}`))
	}))
	t.Cleanup(osvSrv.Close)

	svc := service.NewScanService(comps, "http://localhost").WithScanResults(scanRepo)
	svc.OSVClient = &service.OSVClient{BaseURL: osvSrv.URL, HTTPClient: osvSrv.Client()}

	h := handlers.NewScanHandler(svc)
	r := gin.New()
	r.POST("/api/v1/components/:id/scan", h.Scan)
	r.GET("/api/v1/security/summary", h.Summary)

	c := seedComponent(t, comps, "npm", "debug", "4.4.2")

	rec := do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var res domain.ScanResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, 1, res.Summary.Malicious)
	assert.Equal(t, 0, res.Summary.Unknown)

	// And the field survives under the name the frontend reads it by.
	rec = do(t, r, http.MethodGet, "/api/v1/security/summary", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, float64(1), body["malicious"])
}

func TestScanHandler_Summary_RepoError_500(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.Err = errors.New("agg down")
	rec := do(t, r, http.MethodGet, "/api/v1/security/summary", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Vulnerabilities ──────────────────────────────────────────────────────────────

func TestScanHandler_Vulnerabilities_Empty(t *testing.T) {
	r, _, _, _ := mountScan(t)
	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Items []*domain.VulnRow `json:"items"`
		Total int               `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotNil(t, body.Items)
	assert.Empty(t, body.Items)
	assert.Equal(t, 0, body.Total)
}

func TestScanHandler_Vulnerabilities_WithRows_AndFilters(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.VulnRows = []*domain.VulnRow{
		{RepoName: "repo1", Format: "maven", Name: "pkg", Version: "1.0", High: 2},
	}
	// Exercise the limit/offset/filter query-param parsing branches.
	rec := do(t, r, http.MethodGet,
		"/api/v1/security/vulnerabilities?repo=repo1&severity=HIGH&format=maven&limit=10&offset=5", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Items []*domain.VulnRow `json:"items"`
		Total int               `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	assert.Equal(t, 1, body.Total)
	assert.Equal(t, "pkg", body.Items[0].Name)
}

func TestScanHandler_Vulnerabilities_BadLimitOffset_UsesDefaults(t *testing.T) {
	// Non-numeric limit/offset are ignored (defaults kept) — still 200.
	r, _, _, _ := mountScan(t)
	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities?limit=abc&offset=-3", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// A client-set limit is capped before it reaches the SQL LIMIT clause, so one
// request cannot pull every scanned component in the registry.
func TestScanHandler_Vulnerabilities_LimitClamped(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities?limit=5000000", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1000, scanRepo.LastFilter.Limit)

	rec = do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities?limit=10", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 10, scanRepo.LastFilter.Limit, "a normal page size is untouched")
}

// The export path carries its own, much larger row cap and must not be pulled
// down to the paging ceiling.
func TestScanHandler_VulnerabilitiesExport_NotClampedToPageSize(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities?export=csv&limit=5000000", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Greater(t, scanRepo.LastFilter.Limit, 1000)
}

func TestScanHandler_Vulnerabilities_RepoError_500(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.Err = errors.New("list down")
	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── BulkScanHandler ──────────────────────────────────────────────────────────────

func TestScanHandler_BulkScan_OK(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	seedComponent(t, comps, "maven", "a", "1.0")
	seedComponent(t, comps, "npm", "b", "2.0")
	// sha256 alias should be skipped by BulkScan.
	seedComponent(t, comps, "docker", "img", "sha256:deadbeef")

	rec := do(t, r, http.MethodPost, "/api/v1/security/scan/bulk", map[string]any{"repo": "repo1"})
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Scanned int `json:"scanned"`
		Failed  int `json:"failed"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	// Two OSV-scannable components succeed; the sha256 docker alias is skipped.
	assert.Equal(t, 2, body.Scanned)
	assert.Equal(t, 0, body.Failed)
}

func TestScanHandler_BulkScan_RepoError_500(t *testing.T) {
	// Components.Search errors → BulkScan returns err → 500.
	r, comps, _, _ := mountScan(t)
	comps.Err = errors.New("search down")
	rec := do(t, r, http.MethodPost, "/api/v1/security/scan/bulk", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── ScannerStatus ──────────────────────────────────────────────────────────────

func TestScannerStatus_ReportsDisabled(t *testing.T) {
	svc := service.NewScanService(nil, "http://localhost:8081").WithTrivy(service.TrivyOptions{Enabled: false})
	r := buildScanRouter(svc)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/scanner", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["state"] != "disabled" {
		t.Errorf("state = %v, want disabled", body["state"])
	}
	if _, ok := body["path"]; ok {
		t.Error("a disabled scanner must not report a filesystem path")
	}
	if msg, ok := body["message"].(string); !ok || msg == "" {
		t.Error("message must be a complete sentence the UI can render as-is")
	}
}

func TestScannerStatus_ReportsReadyWithVersionAndPath(t *testing.T) {
	bin := fakeTrivyBin(t, "")
	svc := service.NewScanService(nil, "http://localhost:8081").WithTrivy(service.TrivyOptions{Enabled: true, Bin: bin})
	r := buildScanRouter(svc)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/scanner", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["state"] != "ready" {
		t.Errorf("state = %v, want ready", body["state"])
	}
	if body["path"] != bin {
		t.Errorf("path = %v, want %q", body["path"], bin)
	}
	if body["version"] != "0.70.0" {
		t.Errorf("version = %v, want 0.70.0", body["version"])
	}
}
