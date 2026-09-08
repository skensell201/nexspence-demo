package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// ContentSelectorHandler exposes CRUD for CEL content selectors plus the
// privilege attach/detach endpoints.
type ContentSelectorHandler struct {
	svc *service.ContentSelectorService
}

// NewContentSelectorHandler constructs a ContentSelectorHandler backed by the given content selector service.
func NewContentSelectorHandler(svc *service.ContentSelectorService) *ContentSelectorHandler {
	return &ContentSelectorHandler{svc: svc}
}

// List handles GET /service/rest/v1/security/content-selectors
func (h *ContentSelectorHandler) List(c *gin.Context) {
	items, err := h.svc.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.ContentSelector{}
	}
	c.JSON(http.StatusOK, items)
}

// Get handles GET /service/rest/v1/security/content-selectors/:id
func (h *ContentSelectorHandler) Get(c *gin.Context) {
	s, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "content selector not found"})
		return
	}
	c.JSON(http.StatusOK, s)
}

// Create handles POST /service/rest/v1/security/content-selectors
func (h *ContentSelectorHandler) Create(c *gin.Context) {
	var s domain.ContentSelector
	if err := c.ShouldBindJSON(&s); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.Create(c.Request.Context(), &s); err != nil {
		if conflictOnDuplicateName(c, err) {
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, s)
}

// Update handles PUT /service/rest/v1/security/content-selectors/:id
func (h *ContentSelectorHandler) Update(c *gin.Context) {
	var s domain.ContentSelector
	if err := c.ShouldBindJSON(&s); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.ID = c.Param("id")
	if err := h.svc.Update(c.Request.Context(), &s); err != nil {
		if conflictOnDuplicateName(c, err) {
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s)
}

// Delete handles DELETE /service/rest/v1/security/content-selectors/:id
func (h *ContentSelectorHandler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// AttachToPrivilege handles PUT /service/rest/v1/security/privileges/:id/content-selector/:selectorId
func (h *ContentSelectorHandler) AttachToPrivilege(c *gin.Context) {
	if err := h.svc.AttachToPrivilege(c.Request.Context(),
		c.Param("id"), c.Param("selectorId"),
	); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// DetachFromPrivilege handles DELETE /service/rest/v1/security/privileges/:id/content-selector
func (h *ContentSelectorHandler) DetachFromPrivilege(c *gin.Context) {
	if err := h.svc.DetachFromPrivilege(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
