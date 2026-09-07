package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/safego"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// ReplicationHandler serves the content-replication REST endpoints.
type ReplicationHandler struct {
	svc *service.ReplicationService
	log logger.Logger
}

// NewReplicationHandler constructs a ReplicationHandler backed by the given replication service.
func NewReplicationHandler(svc *service.ReplicationService, log logger.Logger) *ReplicationHandler {
	return &ReplicationHandler{svc: svc, log: log}
}

// List handles GET /api/v1/replication/rules
func (h *ReplicationHandler) List(c *gin.Context) {
	rules, err := h.svc.ListRules(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rules)
}

type ruleInput struct {
	Name           string `json:"name"`
	SourceRepo     string `json:"source_repo"`
	TargetURL      string `json:"target_url"`
	TargetRepo     string `json:"target_repo"`
	TargetUsername string `json:"target_username"`
	TargetPassword string `json:"target_password"` // plaintext, never stored
	CronExpr       string `json:"cron_expr"`
	Enabled        bool   `json:"enabled"`
}

// defaultReplicationCron is the schedule a rule falls back to when the caller
// sends none: nightly at 02:00.
const defaultReplicationCron = "0 2 * * *"

// validate rejects an input that would persist a rule the replication cron
// cannot actually run, and fills in the default schedule. Update needs exactly
// the same guard Create does: a PUT that blanks target_url or source_repo used
// to be accepted, and the only signal was the cron failing every tick after
// the fact ("unsupported protocol scheme \"\""). An empty cron_expr is likewise
// accepted by validateCronExpr, so leaving it blank on update silently
// unscheduled the rule instead of keeping it on its old schedule.
//
// It writes the 400 itself and reports whether the caller may proceed.
func (inp *ruleInput) validate(c *gin.Context) bool {
	if inp.Name == "" || inp.SourceRepo == "" || inp.TargetURL == "" || inp.TargetRepo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, source_repo, target_url, target_repo are required"})
		return false
	}
	if inp.CronExpr == "" {
		inp.CronExpr = defaultReplicationCron
	}
	return true
}

// Create handles POST /api/v1/replication/rules
func (h *ReplicationHandler) Create(c *gin.Context) {
	var inp ruleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !inp.validate(c) {
		return
	}
	rule := &domain.ReplicationRule{
		Name:           inp.Name,
		SourceRepo:     inp.SourceRepo,
		TargetURL:      inp.TargetURL,
		TargetRepo:     inp.TargetRepo,
		TargetUsername: inp.TargetUsername,
		CronExpr:       inp.CronExpr,
		Enabled:        inp.Enabled,
	}
	if err := h.svc.CreateRule(c.Request.Context(), rule, inp.TargetPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Detached from the request context (like cleanup.go's RunAll): net/http
	// cancels it the moment this handler returns, which silently killed the
	// (re)scheduling this goroutine exists to perform — the API answered as if
	// the rule was scheduled while its cron entry never materialized (#254).
	safego.Go(h.log, "replication-reload-rule", func() { h.svc.ReloadRule(context.Background(), rule.ID) })
	c.JSON(http.StatusCreated, rule)
}

// Update handles PUT /api/v1/replication/rules/:id
func (h *ReplicationHandler) Update(c *gin.Context) {
	var inp ruleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !inp.validate(c) {
		return
	}
	rule := &domain.ReplicationRule{
		ID:             c.Param("id"),
		Name:           inp.Name,
		SourceRepo:     inp.SourceRepo,
		TargetURL:      inp.TargetURL,
		TargetRepo:     inp.TargetRepo,
		TargetUsername: inp.TargetUsername,
		CronExpr:       inp.CronExpr,
		Enabled:        inp.Enabled,
	}
	if err := h.svc.UpdateRule(c.Request.Context(), rule, inp.TargetPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Detached for the same reason as Create's — see the comment there (#254).
	safego.Go(h.log, "replication-reload-rule", func() { h.svc.ReloadRule(context.Background(), rule.ID) })
	c.JSON(http.StatusOK, rule)
}

// Delete handles DELETE /api/v1/replication/rules/:id
func (h *ReplicationHandler) Delete(c *gin.Context) {
	if err := h.svc.DeleteRule(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ManualRun handles POST /api/v1/replication/rules/:id/run
func (h *ReplicationHandler) ManualRun(c *gin.Context) {
	id := c.Param("id")
	_, err := h.svc.GetRule(c.Request.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// One run per rule: RunRule refuses an overlapping run, and that refusal
	// is invisible from inside the goroutine below — answering 202 for a run
	// that never starts is worse than saying so.
	if h.svc.Running(id) {
		c.JSON(http.StatusConflict, gin.H{"error": "replication rule is already running"})
		return
	}
	// Detached: a run outlives its 202 by design, and the request context is
	// canceled the moment the handler returns — the run would abort almost
	// immediately with no visible error (#254).
	safego.Go(h.log, "replication-manual-run", func() {
		_ = h.svc.RunRule(context.Background(), id)
	})
	c.JSON(http.StatusAccepted, gin.H{"message": "replication started"})
}

// TestConnection handles POST /api/v1/replication/rules/:id/test
func (h *ReplicationHandler) TestConnection(c *gin.Context) {
	err := h.svc.TestConnection(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || err.Error() == "rule not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ListHistory handles GET /api/v1/replication/rules/:id/history
func (h *ReplicationHandler) ListHistory(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		// The n > 0 guard every sibling paginated handler applies: a
		// non-positive value falls back to the default instead of being
		// passed through.
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	hist, err := h.svc.ListHistory(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, hist)
}
