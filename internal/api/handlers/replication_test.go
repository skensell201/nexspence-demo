package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountReplication wires the real ReplicationService (over mocks) onto a gin engine,
// mirroring router.go. The cron scheduler is nil, so the go-routine ReloadRule calls
// spawned by Create/Update are harmless no-ops.
func mountReplication(t *testing.T) *gin.Engine {
	t.Helper()
	repRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(repRepo, testutil.NewAssetRepo(), testutil.NewBlobStore(), "test-secret", nil, cleanupNopLog())
	h := handlers.NewReplicationHandler(svc, nil)

	r := gin.New()
	r.GET("/api/v1/replication/rules", h.List)
	r.POST("/api/v1/replication/rules", h.Create)
	r.PUT("/api/v1/replication/rules/:id", h.Update)
	r.DELETE("/api/v1/replication/rules/:id", h.Delete)
	r.POST("/api/v1/replication/rules/:id/run", h.ManualRun)
	r.POST("/api/v1/replication/rules/:id/test", h.TestConnection)
	r.GET("/api/v1/replication/rules/:id/history", h.ListHistory)
	return r
}

// replicationCreate posts a valid rule and returns its server-assigned ID.
func replicationCreate(t *testing.T, r *gin.Engine, name string) string {
	t.Helper()
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules", map[string]any{
		"name":        name,
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
	})
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())
	var rule domain.ReplicationRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))
	require.NotEmpty(t, rule.ID)
	return rule.ID
}

// ── List ────────────────────────────────────────────────────────────────────

func TestReplicationHandler_List_Empty(t *testing.T) {
	r := mountReplication(t)
	rec := do(t, r, http.MethodGet, "/api/v1/replication/rules", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.ReplicationRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

// ── Create ──────────────────────────────────────────────────────────────────

func TestReplicationHandler_Create_OK(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "rule-a")
	assert.NotEmpty(t, id)
}

func TestReplicationHandler_Create_DefaultsCron(t *testing.T) {
	r := mountReplication(t)
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules", map[string]any{
		"name":        "rule-defaults",
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var rule domain.ReplicationRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))
	assert.Equal(t, "0 2 * * *", rule.CronExpr) // defaulted when blank
}

func TestReplicationHandler_Create_BadJSON_400(t *testing.T) {
	r := mountReplication(t)
	rec := doRaw(t, r, http.MethodPost, "/api/v1/replication/rules", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplicationHandler_Create_MissingFields_400(t *testing.T) {
	r := mountReplication(t)
	// each row drops exactly one required field
	cases := []map[string]any{
		{"source_repo": "src", "target_url": "http://x/", "target_repo": "dst"}, // no name
		{"name": "n", "target_url": "http://x/", "target_repo": "dst"},          // no source_repo
		{"name": "n", "source_repo": "src", "target_repo": "dst"},               // no target_url
		{"name": "n", "source_repo": "src", "target_url": "http://x/"},          // no target_repo
	}
	for i, body := range cases {
		rec := do(t, r, http.MethodPost, "/api/v1/replication/rules", body)
		assert.Equalf(t, http.StatusBadRequest, rec.Code, "case %d body=%s", i, rec.Body.String())
	}
}

// ── Update ──────────────────────────────────────────────────────────────────

func TestReplicationHandler_Update_OK(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "before")
	rec := do(t, r, http.MethodPut, "/api/v1/replication/rules/"+id, map[string]any{
		"name":        "after",
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
		"cron_expr":   "0 5 * * *",
	})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var rule domain.ReplicationRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))
	assert.Equal(t, id, rule.ID)
	assert.Equal(t, "after", rule.Name)
}

func TestReplicationHandler_Update_BadJSON_400(t *testing.T) {
	r := mountReplication(t)
	rec := doRaw(t, r, http.MethodPut, "/api/v1/replication/rules/any", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplicationHandler_Update_UnknownID_400(t *testing.T) {
	r := mountReplication(t)
	// mock UpdateRule returns an error for an unknown id → handler maps to 400.
	rec := do(t, r, http.MethodPut, "/api/v1/replication/rules/ghost", map[string]any{
		"name":        "x",
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// A PUT used to skip Create's required-field check entirely, so blanking
// target_url (or source_repo) on an existing rule was accepted with a 200 and
// only surfaced later, as the cron failing every tick with "unsupported
// protocol scheme \"\"".
func TestReplicationHandler_Update_RejectsBlankedRequiredFields(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "before")
	full := map[string]any{
		"name":        "after",
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
	}
	for _, blank := range []string{"name", "source_repo", "target_url", "target_repo"} {
		body := map[string]any{}
		for k, v := range full {
			body[k] = v
		}
		body[blank] = ""
		rec := do(t, r, http.MethodPut, "/api/v1/replication/rules/"+id, body)
		assert.Equalf(t, http.StatusBadRequest, rec.Code, "blanked %s, body=%s", blank, rec.Body.String())
	}
}

// validateCronExpr accepts an empty expression, so an update omitting
// cron_expr used to persist a rule with no schedule at all — silently
// unscheduling it. Update applies Create's default instead.
func TestReplicationHandler_Update_DefaultsMissingCronExpr(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "before")
	rec := do(t, r, http.MethodPut, "/api/v1/replication/rules/"+id, map[string]any{
		"name":        "after",
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
	})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var rule domain.ReplicationRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))
	assert.Equal(t, "0 2 * * *", rule.CronExpr)
}

// ── Delete ──────────────────────────────────────────────────────────────────

func TestReplicationHandler_Delete_204(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "to-delete")
	rec := do(t, r, http.MethodDelete, "/api/v1/replication/rules/"+id, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// ── ManualRun ─────────────────────────────────────────────────────────────────

func TestReplicationHandler_ManualRun_NotFound_404(t *testing.T) {
	r := mountReplication(t)
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules/ghost/run", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReplicationHandler_ManualRun_202(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "run-me")
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules/"+id+"/run", nil)
	require.Equal(t, http.StatusAccepted, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "replication started", body["message"])
}

// ── TestConnection ────────────────────────────────────────────────────────────

func TestReplicationHandler_TestConnection_NotFound_404(t *testing.T) {
	r := mountReplication(t)
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules/ghost/test", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReplicationHandler_TestConnection_Unreachable_502(t *testing.T) {
	r := mountReplication(t)
	// target_url points at a closed port so client.Do fails fast → 502.
	id := replicationCreate(t, r, "unreachable")
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules/"+id+"/test", nil)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// ── ListHistory ───────────────────────────────────────────────────────────────

func TestReplicationHandler_ListHistory_OK(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "with-history")
	rec := do(t, r, http.MethodGet, "/api/v1/replication/rules/"+id+"/history", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var hist []domain.ReplicationHistory
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &hist))
	assert.Empty(t, hist)
}

func TestReplicationHandler_ListHistory_WithLimit_OK(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "limit-history")
	rec := do(t, r, http.MethodGet, "/api/v1/replication/rules/"+id+"/history?limit=5", nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

// A non-positive limit falls back to the default instead of being passed
// through — the guard every sibling paginated handler already applies.
func TestReplicationHandler_ListHistory_NonPositiveLimit_FallsBack(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "neg-history")
	for _, qs := range []string{"limit=-1", "limit=0"} {
		rec := do(t, r, http.MethodGet, "/api/v1/replication/rules/"+id+"/history?"+qs, nil)
		assert.Equal(t, http.StatusOK, rec.Code, qs)
	}
}

// ── Request-context detachment (#254) ───────────────────────────────────────

// ctxCapturingReplRepo records the context each GetRule call arrives with.
type ctxCapturingReplRepo struct {
	*testutil.ReplicationRepo
	got chan context.Context
}

func (r *ctxCapturingReplRepo) GetRule(ctx context.Context, id string) (*domain.ReplicationRule, error) {
	select {
	case r.got <- ctx:
	default:
	}
	return r.ReplicationRepo.GetRule(ctx, id)
}

// A manual run answers 202 and keeps going; net/http cancels the request
// context the moment the handler returns, so the fire-and-forget goroutine
// must not inherit it — with the old wiring the run aborted almost
// immediately with no visible error (#254).
func TestReplicationHandler_ManualRun_SurvivesRequestCancellation(t *testing.T) {
	inner := testutil.NewReplicationRepo()
	repo := &ctxCapturingReplRepo{ReplicationRepo: inner, got: make(chan context.Context, 2)}
	svc := service.NewReplicationService(repo, testutil.NewAssetRepo(), testutil.NewBlobStore(), "test-secret", nil, cleanupNopLog())
	h := handlers.NewReplicationHandler(svc, nil)
	r := gin.New()
	r.POST("/api/v1/replication/rules/:id/run", h.ManualRun)

	rule := &domain.ReplicationRule{Name: "det", SourceRepo: "src", TargetURL: "http://127.0.0.1:1/", TargetRepo: "dst"}
	require.NoError(t, inner.CreateRule(context.Background(), rule))

	reqCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/replication/rules/"+rule.ID+"/run", nil).WithContext(reqCtx)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	cancel() // what a real net/http server does as soon as the handler returns

	// The first GetRule is the handler's own synchronous existence check —
	// request-scoped by design. The goroutine's is the one that must survive.
	<-repo.got
	select {
	case got := <-repo.got:
		assert.NoError(t, got.Err(), "the detached run's context must outlive the request")
	case <-time.After(2 * time.Second):
		t.Fatal("RunRule goroutine never reached the repository")
	}
}

// blockingReplRepo blocks the Nth GetRule call until the test releases it,
// which parks an in-flight run inside RunRule with the guard held.
type blockingReplRepo struct {
	*testutil.ReplicationRepo
	calls   atomic.Int32
	blockOn int32
	release chan struct{}
}

func (r *blockingReplRepo) GetRule(ctx context.Context, id string) (*domain.ReplicationRule, error) {
	if r.calls.Add(1) == r.blockOn {
		<-r.release
	}
	return r.ReplicationRepo.GetRule(ctx, id)
}

// A second manual run while the first is still going is refused visibly:
// RunRule drops the overlapping run, and a 202 for a run that never starts
// tells the operator the opposite of what happened.
func TestReplicationHandler_ManualRun_ConcurrentRunIs409(t *testing.T) {
	inner := testutil.NewReplicationRepo()
	// Call 1 is the first POST's synchronous existence check, call 2 is its
	// detached run — that is the one to park. Call 3 is the second POST's
	// existence check and must not block.
	repo := &blockingReplRepo{ReplicationRepo: inner, blockOn: 2, release: make(chan struct{})}
	defer close(repo.release)
	svc := service.NewReplicationService(repo, testutil.NewAssetRepo(), testutil.NewBlobStore(), "test-secret", nil, cleanupNopLog())
	h := handlers.NewReplicationHandler(svc, nil)
	r := gin.New()
	r.POST("/api/v1/replication/rules/:id/run", h.ManualRun)

	rule := &domain.ReplicationRule{Name: "busy", SourceRepo: "src", TargetURL: "http://127.0.0.1:1/", TargetRepo: "dst"}
	require.NoError(t, inner.CreateRule(context.Background(), rule))
	path := "/api/v1/replication/rules/" + rule.ID + "/run"

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
	require.Equal(t, http.StatusAccepted, w.Code)

	// The detached run has to reach the guard before the second POST asks.
	deadline := time.Now().Add(2 * time.Second)
	for !svc.Running(rule.ID) {
		if time.Now().After(deadline) {
			t.Fatal("the detached run never took the guard")
		}
		time.Sleep(time.Millisecond)
	}

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusConflict, w2.Code)
	assert.Contains(t, w2.Body.String(), "already running")
}
