//go:build integration

// Package integration contains end-to-end tests that boot the real router over
// a real PostgreSQL database (started automatically via dockertest, like the
// repository-layer integration tests). Run with:
//
//	go test ./internal/integration/... -tags integration -v
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

var (
	serverOnce sync.Once
	testServer *httptest.Server
)

// server boots the full application once per test binary: a migrated postgres
// from pgtest, the real router, and an HTTP listener whose address is also the
// configured BaseURL — proxy handlers rewrite upstream URLs onto it, so it
// must be the address clients actually reach.
func server(t *testing.T) *httptest.Server {
	t.Helper()
	serverOnce.Do(func() {
		pool := pgtest.Pool(t)

		// The seed migration pre-creates the admin user with a placeholder
		// hash; the configured password is normally applied by the server
		// binary's bootstrap step, which lives in package main — set it here.
		authSvc := auth.NewService("integration-test-secret-32bytes!!", 1, 4)
		hash, err := authSvc.HashPassword("admin123")
		if err != nil {
			t.Fatalf("hash admin password: %v", err)
		}
		if err := postgres.NewUserRepo(pool).UpdatePassword(context.Background(), "admin", hash); err != nil {
			t.Fatalf("set admin password: %v", err)
		}
		// UpdatePassword bumps tokens_valid_after, and a JWT's second-granular
		// iat issued within the same second reads as pre-cutoff — harmless for
		// humans, fatal for a test that logs in milliseconds later. Rewind it.
		if _, err := pool.Exec(context.Background(),
			`UPDATE users SET tokens_valid_after = now() - interval '1 minute' WHERE username = 'admin'`); err != nil {
			t.Fatalf("rewind token cutoff: %v", err)
		}

		l, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			t.Fatalf("listen: %v", lerr)
		}
		baseURL := "http://" + l.Addr().String()

		cfg := &config.Config{}
		cfg.Auth.JWTSecret = "integration-test-secret-32bytes!!"
		cfg.Auth.JWTExpiryHours = 1
		cfg.Auth.BcryptCost = 4
		cfg.Storage.Local.BasePath = t.TempDir()
		cfg.HTTP.BaseURL = baseURL
		cfg.HTTP.MaxBodyMB = 64 // a zero-value config means "0 bytes", not "unlimited"
		cfg.Log.Level = "error"
		cfg.Bootstrap.AdminUsername = "admin"
		cfg.Bootstrap.AdminPassword = "admin123"
		cfg.Bootstrap.AdminEmail = "admin@test.local"

		log := logger.New("error", "json")
		handler := api.NewRouter(context.Background(), cfg, pool, log, "integration-test")

		ts := httptest.NewUnstartedServer(handler)
		_ = ts.Listener.Close()
		ts.Listener = l
		ts.Start()
		testServer = ts
	})
	if testServer == nil {
		t.Fatal("integration server failed to start")
	}
	return testServer
}

func TestMain(m *testing.M) {
	// The proxy-format tests point repositories at loopback httptest fakes;
	// the production upstream client is SSRF-guarded and would refuse them.
	repoproxy.UpstreamClient = &http.Client{}

	code := m.Run()
	if testServer != nil {
		testServer.Close()
	}
	pgtest.Cleanup()
	os.Exit(code)
}

// login returns a Bearer token for the given credentials.
func login(t *testing.T, username, password string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	resp, err := http.Post(server(t).URL+"/api/v1/login", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "login response: %s", raw)

	var result map[string]any
	require.NoError(t, json.Unmarshal(raw, &result))
	token, ok := result["token"].(string)
	require.True(t, ok, "response missing token")
	return token
}

func authReq(t *testing.T, method, path string, body io.Reader, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, server(t).URL+path, body)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// ── Tests ─────────────────────────────────────────────────────

func TestStatusCheck(t *testing.T) {
	resp, err := http.Get(server(t).URL + "/service/rest/v1/status/check")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestLoginAndMe(t *testing.T) {
	token := login(t, "admin", "admin123")
	assert.NotEmpty(t, token)

	resp := authReq(t, http.MethodGet, "/api/v1/me", nil, token)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var me map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&me))
	assert.Equal(t, "admin", me["username"])
}

func TestRepositoryCRUD(t *testing.T) {
	token := login(t, "admin", "admin123")

	// Create hosted raw repo
	createBody := `{"name":"integration-raw","online":true,"storage":{"blobStoreName":"default","strictContentTypeValidation":false}}`
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		bytes.NewBufferString(createBody), token)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// List repos — should contain the new one
	listResp := authReq(t, http.MethodGet, "/service/rest/v1/repositories", nil, token)
	defer listResp.Body.Close()
	assert.Equal(t, http.StatusOK, listResp.StatusCode)

	// Get by name
	getResp := authReq(t, http.MethodGet, "/service/rest/v1/repositories/integration-raw", nil, token)
	defer getResp.Body.Close()
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	// Delete
	delResp := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/integration-raw", nil, token)
	defer delResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, delResp.StatusCode)
}

func TestRawArtifactPushPull(t *testing.T) {
	token := login(t, "admin", "admin123")

	// Create repo
	createBody := `{"name":"e2e-raw","online":true,"storage":{"blobStoreName":"default","strictContentTypeValidation":false}}`
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		bytes.NewBufferString(createBody), token)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// Push artifact
	artifactData := []byte("integration test artifact content")
	putReq, _ := http.NewRequest(http.MethodPut,
		server(t).URL+"/repository/e2e-raw/integration/test/artifact.bin",
		bytes.NewReader(artifactData))
	putReq.Header.Set("Authorization", "Bearer "+token)
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.ContentLength = int64(len(artifactData))
	putResp, err := http.DefaultClient.Do(putReq)
	require.NoError(t, err)
	putResp.Body.Close()
	assert.Equal(t, http.StatusCreated, putResp.StatusCode)

	// Pull artifact
	getResp := authReq(t, http.MethodGet,
		"/repository/e2e-raw/integration/test/artifact.bin", nil, token)
	defer getResp.Body.Close()
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	pulled, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	assert.Equal(t, artifactData, pulled)

	// Cleanup
	delResp := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/e2e-raw", nil, token)
	delResp.Body.Close()
}

// SearchAssets/FilterAssets filter what they return through RBAC; the direct
// GET /service/rest/v1/assets/:id endpoint didn't, letting any authenticated
// user who knew or enumerated an asset UUID from a private repository read
// its full metadata and download URL. 404, not 403, keeps the id unguessable.
func TestAssetGetByID_DeniedWithoutPrivilege(t *testing.T) {
	admin := login(t, "admin", "admin123")

	createBody := `{"name":"e2e-asset-rbac","online":true,"storage":{"blobStoreName":"default","strictContentTypeValidation":false}}`
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/raw/hosted", bytes.NewBufferString(createBody), admin)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	t.Cleanup(func() {
		d := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/e2e-asset-rbac", nil, admin)
		d.Body.Close()
	})

	putReq, _ := http.NewRequest(http.MethodPut,
		server(t).URL+"/repository/e2e-asset-rbac/secret.bin", bytes.NewReader([]byte("shh")))
	putReq.Header.Set("Authorization", "Bearer "+admin)
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putResp, err := http.DefaultClient.Do(putReq)
	require.NoError(t, err)
	putResp.Body.Close()
	require.Equal(t, http.StatusCreated, putResp.StatusCode)

	searchResp := authReq(t, http.MethodGet, "/service/rest/v1/search/assets?repository=e2e-asset-rbac", nil, admin)
	defer searchResp.Body.Close()
	require.Equal(t, http.StatusOK, searchResp.StatusCode)
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(searchResp.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	assetID := page.Items[0].ID

	userBody := `{"userId":"asset-rbac-eve","firstName":"Eve","lastName":"NoPriv","emailAddress":"asset-rbac-eve@example.com","password":"evePass123!","status":"active","roles":[]}`
	createUserResp := authReq(t, http.MethodPost, "/service/rest/v1/security/users", bytes.NewBufferString(userBody), admin)
	createUserResp.Body.Close()
	require.Equal(t, http.StatusCreated, createUserResp.StatusCode)
	t.Cleanup(func() {
		d := authReq(t, http.MethodDelete, "/service/rest/v1/security/users/asset-rbac-eve", nil, admin)
		d.Body.Close()
	})
	// Same second-granularity race as the admin setup in server(): a freshly
	// created user's tokens_valid_after defaults to now(), and logging in
	// milliseconds later can produce a JWT whose iat reads as pre-cutoff.
	_, err = pgtest.Pool(t).Exec(context.Background(),
		`UPDATE users SET tokens_valid_after = now() - interval '1 minute' WHERE username = 'asset-rbac-eve'`)
	require.NoError(t, err)

	eve := login(t, "asset-rbac-eve", "evePass123!")

	deniedResp := authReq(t, http.MethodGet, "/service/rest/v1/assets/"+assetID, nil, eve)
	defer deniedResp.Body.Close()
	assert.Equal(t, http.StatusNotFound, deniedResp.StatusCode)

	allowedResp := authReq(t, http.MethodGet, "/service/rest/v1/assets/"+assetID, nil, admin)
	defer allowedResp.Body.Close()
	assert.Equal(t, http.StatusOK, allowedResp.StatusCode)
}

func TestMetricsEndpoint(t *testing.T) {
	token := login(t, "admin", "admin123")
	resp := authReq(t, http.MethodGet, "/api/v1/metrics", nil, token)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var snap map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&snap))
	assert.Contains(t, snap, "requests_total")
	assert.Contains(t, snap, "uptime_seconds")
}
