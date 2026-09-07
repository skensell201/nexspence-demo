package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// RepositoryHandler handles repository management endpoints.
type RepositoryHandler struct {
	svc     *service.RepositoryService
	rbacSvc *service.RBACService
}

// NewRepositoryHandler constructs a RepositoryHandler from the repository and RBAC services.
func NewRepositoryHandler(svc *service.RepositoryService, rbacSvc *service.RBACService) *RepositoryHandler {
	return &RepositoryHandler{svc: svc, rbacSvc: rbacSvc}
}

// List handles GET /service/rest/v1/repositories and GET /api/v1/repositories
func (h *RepositoryHandler) List(c *gin.Context) {
	format := c.Query("format")
	repoType := c.Query("type")

	repos, err := h.svc.List(c.Request.Context(), format, repoType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repos == nil {
		repos = []domain.Repository{}
	}

	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	repos = h.rbacSvc.FilterRepos(c.Request.Context(),
		stringVal(userID), stringSliceVal(roles), repos)

	c.JSON(http.StatusOK, domain.RedactedRepositories(repos))
}

func stringVal(v any) string        { s, _ := v.(string); return s }
func stringSliceVal(v any) []string { s, _ := v.([]string); return s }

// Get handles GET /service/rest/v1/repositories/:name
func (h *RepositoryHandler) Get(c *gin.Context) {
	name := c.Param("name")
	r, err := h.svc.Get(c.Request.Context(), name)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// List and ComponentHandler.GetQuota both filter by RBAC (GetQuota citing
	// #295 explicitly); this sibling get-by-name endpoint didn't, letting any
	// authenticated user read a private repository's full config (format,
	// blob store, remote proxy URL, allowAnonymous) just by knowing or
	// guessing its name.
	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	visible := h.rbacSvc.FilterRepos(c.Request.Context(),
		stringVal(userID), stringSliceVal(roles), []domain.Repository{*r})
	if len(visible) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	c.JSON(http.StatusOK, domain.RedactedRepository(*r))
}

// Create handles POST /service/rest/v1/repositories/:format/:type
func (h *RepositoryHandler) Create(c *gin.Context) {
	format := c.Param("format")
	repoType := c.Param("type")

	var r domain.Repository
	if err := c.ShouldBindJSON(&r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r.Format = domain.RepoFormat(format)
	r.Type = domain.RepoType(repoType)

	if err := h.svc.Create(c.Request.Context(), &r); err != nil {
		if isAlreadyExists(err) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if isInvalidInput(err) || isNotFound(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, domain.RedactedRepository(r))
}

// Update handles PUT /service/rest/v1/repositories/:format/:type/:name
func (h *RepositoryHandler) Update(c *gin.Context) {
	name := c.Param("name")

	var updates domain.Repository
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	r, err := h.svc.Update(c.Request.Context(), name, &updates)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if isInvalidInput(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, domain.RedactedRepository(*r))
}

// Patch handles PATCH /service/rest/v1/repositories/:name — partial update (currently: online toggle only)
func (h *RepositoryHandler) Patch(c *gin.Context) {
	name := c.Param("name")
	var body struct {
		Online bool `json:"online"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing, err := h.svc.Get(c.Request.Context(), name)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	r, err := h.svc.Update(c.Request.Context(), name, &domain.Repository{
		Online:         body.Online,
		AllowAnonymous: existing.AllowAnonymous,
	})
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, domain.RedactedRepository(*r))
}

// Delete handles DELETE /service/rest/v1/repositories/:name
func (h *RepositoryHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	if err := h.svc.Delete(c.Request.Context(), name); err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
