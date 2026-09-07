package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/safego"
)

// cleanupRunner is the minimal interface CleanupHandler needs from CleanupService.
type cleanupRunner interface {
	RunPolicy(ctx context.Context, id string) error
	RunPolicyResult(ctx context.Context, id string) (*domain.CleanupRunResult, error)
	RunAll(ctx context.Context) error
	ReloadPolicy(ctx context.Context, id string)
	PreviewPolicy(ctx context.Context, id string) (*domain.CleanupPreviewResult, error)
}

// CleanupHandler serves the cleanup-policy REST endpoints and triggers policy runs.
type CleanupHandler struct {
	policies repository.CleanupPolicyRepo
	repos    repository.RepositoryRepo
	runner   cleanupRunner
	log      logger.Logger
}

// NewCleanupHandler constructs a CleanupHandler from the policy/repository repos and the cleanup runner.
func NewCleanupHandler(
	policies repository.CleanupPolicyRepo,
	repos repository.RepositoryRepo,
	runner cleanupRunner,
	log logger.Logger,
) *CleanupHandler {
	return &CleanupHandler{policies: policies, repos: repos, runner: runner, log: log}
}

// List GET /service/rest/v1/cleanup-policies
func (h *CleanupHandler) List(c *gin.Context) {
	policies, err := h.policies.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if policies == nil {
		policies = []domain.CleanupPolicy{}
	}
	c.JSON(http.StatusOK, policies)
}

// Get GET /service/rest/v1/cleanup-policies/:id
func (h *CleanupHandler) Get(c *gin.Context) {
	p, err := h.policies.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// bindPolicy decodes a policy body and applies the input contract shared by
// Create and Update: a name is required, a missing format means "all formats",
// criteria default to empty, and a schedule that the cron scheduler cannot
// parse is rejected here rather than silently dropped at scheduling time.
func bindPolicy(c *gin.Context) (domain.CleanupPolicy, bool) {
	var p domain.CleanupPolicy
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return p, false
	}
	if p.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return p, false
	}
	if p.ScheduleCron != "" {
		if _, err := cron.ParseStandard(p.ScheduleCron); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid scheduleCron: " + err.Error()})
			return p, false
		}
	}
	if p.Format == "" {
		p.Format = "*"
	}
	if p.Criteria == nil {
		p.Criteria = map[string]any{}
	}
	return p, true
}

// Create POST /service/rest/v1/cleanup-policies
func (h *CleanupHandler) Create(c *gin.Context) {
	p, ok := bindPolicy(c)
	if !ok {
		return
	}
	if err := h.policies.Create(c.Request.Context(), &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.runner.ReloadPolicy(c.Request.Context(), p.ID)
	c.JSON(http.StatusCreated, p)
}

// Update PUT /service/rest/v1/cleanup-policies/:id
func (h *CleanupHandler) Update(c *gin.Context) {
	p, ok := bindPolicy(c)
	if !ok {
		return
	}
	p.ID = c.Param("id")
	if err := h.policies.Update(c.Request.Context(), &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.runner.ReloadPolicy(c.Request.Context(), p.ID)
	c.JSON(http.StatusOK, p)
}

// Delete DELETE /service/rest/v1/cleanup-policies/:id
func (h *CleanupHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.repos.DetachCleanupPolicyID(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.policies.Delete(c.Request.Context(), id); err != nil {
		// The first half already ran: repositories no longer reference the
		// policy, but its row survives. A generic 500 reads as "nothing
		// happened"; say what state the operation left behind and that a retry
		// finishes it (DetachCleanupPolicyID is idempotent).
		h.log.Errorw("cleanup policy delete failed after repositories were detached",
			"policy", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("repositories were detached from policy %s, but deleting the policy failed: %v — retry the delete to finish", id, err),
		})
		return
	}
	// Policy is deleted; ReloadPolicy sees it's gone and removes the cron entry.
	h.runner.ReloadPolicy(c.Request.Context(), id)
	c.Status(http.StatusNoContent)
}

// Run POST /service/rest/v1/cleanup-policies/:id/run — trigger policy immediately.
// A single policy runs synchronously and returns its outcome (deleted count,
// freed bytes, or a skip reason) so the UI can report a result instead of a bare
// acknowledgement. Running all policies stays asynchronous.
func (h *CleanupHandler) Run(c *gin.Context) {
	id := c.Param("id")
	if id == "_all" {
		// The client already has its 202; the log is the only place a
		// background failure (an unreachable lock backend, a dead DB) can
		// surface.
		safego.Go(h.log, "cleanup-run-all", func() {
			if err := h.runner.RunAll(context.Background()); err != nil {
				h.log.Errorw("cleanup: background run of all policies failed", "err", err)
			}
		})
		c.JSON(http.StatusAccepted, gin.H{"status": "running all policies"})
		return
	}
	res, err := h.runner.RunPolicyResult(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// Preview POST /api/v1/cleanup-policies/:id/preview — dry-run preview (no deletes)
func (h *CleanupHandler) Preview(c *gin.Context) {
	result, err := h.runner.PreviewPolicy(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
