package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// RoutingRuleHandler handles routing rule CRUD endpoints.
type RoutingRuleHandler struct {
	svc *service.RoutingRuleService
}

// NewRoutingRuleHandler constructs a RoutingRuleHandler backed by the given routing rule service.
func NewRoutingRuleHandler(svc *service.RoutingRuleService) *RoutingRuleHandler {
	return &RoutingRuleHandler{svc: svc}
}

// List handles GET /service/rest/v1/routing-rules
func (h *RoutingRuleHandler) List(c *gin.Context) {
	rules, err := h.svc.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []domain.RoutingRule{}
	}
	c.JSON(http.StatusOK, rules)
}

// Get handles GET /service/rest/v1/routing-rules/:id
func (h *RoutingRuleHandler) Get(c *gin.Context) {
	r, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "routing rule not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

// Create handles POST /service/rest/v1/routing-rules
func (h *RoutingRuleHandler) Create(c *gin.Context) {
	var r domain.RoutingRule
	if err := c.ShouldBindJSON(&r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if r.Matchers == nil {
		r.Matchers = []string{}
	}
	if err := h.svc.Create(c.Request.Context(), &r); err != nil {
		if conflictOnDuplicateName(c, err) {
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, r)
}

// Update handles PUT /service/rest/v1/routing-rules/:id
func (h *RoutingRuleHandler) Update(c *gin.Context) {
	var r domain.RoutingRule
	if err := c.ShouldBindJSON(&r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r.ID = c.Param("id")
	if r.Matchers == nil {
		r.Matchers = []string{}
	}
	if err := h.svc.Update(c.Request.Context(), &r); err != nil {
		if conflictOnDuplicateName(c, err) {
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

// Delete handles DELETE /service/rest/v1/routing-rules/:id
func (h *RoutingRuleHandler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
