package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
)

func buildHealthRouter(liveness, readiness gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.GET("/healthz", liveness)
	r.GET("/readyz", readiness)
	return r
}

func TestHealthz_AlwaysOK(t *testing.T) {
	r := buildHealthRouter(handlers.LivenessHandler(), handlers.ReadinessHandler(nil, nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("want status=ok got %q", body["status"])
	}
}

func TestReadyz_NilDeps_OK(t *testing.T) {
	r := buildHealthRouter(handlers.LivenessHandler(), handlers.ReadinessHandler(nil, nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("want status=ok got %v", body["status"])
	}
	checks, _ := body["checks"].(map[string]any)
	if len(checks) != 0 {
		t.Fatalf("expected empty checks, got %v", checks)
	}
}
