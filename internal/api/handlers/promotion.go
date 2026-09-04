package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// PromotionHandler serves the staging and build-promotion REST endpoints.
type PromotionHandler struct {
	svc     *service.PromotionService
	repos   repository.RepositoryRepo
	rbacSvc *service.RBACService
}

// NewPromotionHandler constructs a PromotionHandler backed by the given promotion service.
func NewPromotionHandler(svc *service.PromotionService) *PromotionHandler {
	return &PromotionHandler{svc: svc}
}

// WithRBAC wires the repository repo and RBAC service so ListRules/
// GetComponentRules hide rules naming a repository the caller has no browse
// privilege over. Nil-safe: unset, both endpoints keep their prior
// (unfiltered) behavior.
func (h *PromotionHandler) WithRBAC(repos repository.RepositoryRepo, rbacSvc *service.RBACService) *PromotionHandler {
	h.repos = repos
	h.rbacSvc = rbacSvc
	return h
}

// visibleRules filters rules to ones the caller can browse either FromRepo or
// ToRepo. Nil-safe: with WithRBAC unset, returns rules unchanged.
func (h *PromotionHandler) visibleRules(c *gin.Context, rules []domain.PromotionRule) []domain.PromotionRule {
	if h.repos == nil || h.rbacSvc == nil {
		return rules
	}
	ctx := c.Request.Context()
	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	userIDStr, _ := userID.(string)
	rolesSlice, _ := roles.([]string)

	repoCache := map[string]*domain.Repository{}
	canBrowse := func(name string) bool {
		repo, cached := repoCache[name]
		if !cached {
			repo, _ = h.repos.Get(ctx, name)
			repoCache[name] = repo
		}
		if repo == nil {
			return false
		}
		ok, _ := h.rbacSvc.CanAccessRepo(ctx, userIDStr, rolesSlice, repo, "", "browse")
		return ok
	}

	visible := make([]domain.PromotionRule, 0, len(rules))
	for _, r := range rules {
		if canBrowse(r.FromRepo) || canBrowse(r.ToRepo) {
			visible = append(visible, r)
		}
	}
	return visible
}

// ListRules handles GET /api/v1/promotion/rules
func (h *PromotionHandler) ListRules(c *gin.Context) {
	rules, err := h.svc.ListRules(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []domain.PromotionRule{}
	}
	// This endpoint sits under the non-admin authed group and, unfiltered,
	// returns every promotion rule system-wide — full topology (source repo,
	// target repo, path filter, approval gate) regardless of privilege.
	rules = h.visibleRules(c, rules)
	c.JSON(http.StatusOK, rules)
}

type promotionRuleInput struct {
	Name                  string `json:"name"`
	FromRepo              string `json:"from_repo"`
	ToRepo                string `json:"to_repo"`
	PathFilter            string `json:"path_filter"`
	RequireScanPass       bool   `json:"require_scan_pass"`
	RequireManualApproval bool   `json:"require_manual_approval"`
}

// CreateRule handles POST /api/v1/promotion/rules (admin only)
func (h *PromotionHandler) CreateRule(c *gin.Context) {
	var inp promotionRuleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule := &domain.PromotionRule{
		Name: inp.Name, FromRepo: inp.FromRepo, ToRepo: inp.ToRepo,
		PathFilter: inp.PathFilter, RequireScanPass: inp.RequireScanPass,
		RequireManualApproval: inp.RequireManualApproval,
	}
	if err := h.svc.CreateRule(c.Request.Context(), rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, rule)
}

// UpdateRule handles PUT /api/v1/promotion/rules/:id (admin only)
func (h *PromotionHandler) UpdateRule(c *gin.Context) {
	var inp promotionRuleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule := &domain.PromotionRule{
		ID: c.Param("id"), Name: inp.Name, FromRepo: inp.FromRepo, ToRepo: inp.ToRepo,
		PathFilter: inp.PathFilter, RequireScanPass: inp.RequireScanPass,
		RequireManualApproval: inp.RequireManualApproval,
	}
	if err := h.svc.UpdateRule(c.Request.Context(), rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rule)
}

// DeleteRule handles DELETE /api/v1/promotion/rules/:id (admin only)
func (h *PromotionHandler) DeleteRule(c *gin.Context) {
	if err := h.svc.DeleteRule(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// GetComponentRules handles GET /api/v1/components/:id/promotion-rules
func (h *PromotionHandler) GetComponentRules(c *gin.Context) {
	rules, err := h.svc.ListRulesForComponent(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []domain.PromotionRule{}
	}
	rules = h.visibleRules(c, rules)
	c.JSON(http.StatusOK, rules)
}

// Promote handles POST /api/v1/promotion/promote
// Body: { "rule_id": "...", "component_ids": ["..."] }
func (h *PromotionHandler) Promote(c *gin.Context) {
	var body struct {
		RuleID       string   `json:"rule_id"`
		ComponentIDs []string `json:"component_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.RuleID == "" || len(body.ComponentIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id and component_ids are required"})
		return
	}
	userID, _ := c.Get("userID")
	uid, _ := userID.(string)

	requests, err := h.svc.Promote(c.Request.Context(), body.RuleID, body.ComponentIDs, uid)
	if err != nil {
		// Two rules covering the same component is a conflict in the rules, not
		// a malformed request: the caller is told to make the configuration
		// unambiguous rather than to fix its own payload (#366).
		if errors.Is(err, service.ErrAmbiguousPromotionRule) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

// ListRequests handles GET /api/v1/promotion/requests?status=pending
func (h *PromotionHandler) ListRequests(c *gin.Context) {
	status := c.Query("status")
	requests, err := h.svc.ListRequests(c.Request.Context(), status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if requests == nil {
		requests = []domain.PromotionRequest{}
	}
	c.JSON(http.StatusOK, requests)
}

// Approve handles POST /api/v1/promotion/requests/:id/approve (admin only)
func (h *PromotionHandler) Approve(c *gin.Context) {
	reviewerID, _ := c.Get("userID")
	uid, _ := reviewerID.(string)
	if err := h.svc.Approve(c.Request.Context(), c.Param("id"), uid); err != nil {
		// A rule created while the request sat pending can make it ambiguous
		// only at approval time; same answer as on Promote (#366).
		if errors.Is(err, service.ErrAmbiguousPromotionRule) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Reject handles POST /api/v1/promotion/requests/:id/reject (admin only)
// Body: { "reason": "..." }
func (h *PromotionHandler) Reject(c *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	reviewerID, _ := c.Get("userID")
	uid, _ := reviewerID.(string)
	if err := h.svc.Reject(c.Request.Context(), c.Param("id"), uid, body.Reason); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
