package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// PrivilegeHandler serves the privilege REST endpoints.
type PrivilegeHandler struct {
	repo     repository.PrivilegeRepo
	roleRepo repository.RoleRepo
}

// NewPrivilegeHandler constructs a PrivilegeHandler from the privilege and role repositories.
func NewPrivilegeHandler(repo repository.PrivilegeRepo, roleRepo repository.RoleRepo) *PrivilegeHandler {
	return &PrivilegeHandler{repo: repo, roleRepo: roleRepo}
}

// List handles GET /service/rest/v1/security/privileges
func (h *PrivilegeHandler) List(c *gin.Context) {
	items, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.Privilege{}
	}
	c.JSON(http.StatusOK, items)
}

// Get handles GET /service/rest/v1/security/privileges/:id
func (h *PrivilegeHandler) Get(c *gin.Context) {
	p, err := h.repo.Get(c.Request.Context(), c.Param("id"))
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "privilege not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Create handles POST /service/rest/v1/security/privileges
func (h *PrivilegeHandler) Create(c *gin.Context) {
	var p domain.Privilege
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if p.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if p.Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
		return
	}
	if err := h.repo.Create(c.Request.Context(), &p); err != nil {
		if conflictOnDuplicateName(c, err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, p)
}

// Update handles PUT /service/rest/v1/security/privileges/:id
func (h *PrivilegeHandler) Update(c *gin.Context) {
	var p domain.Privilege
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p.ID = c.Param("id")
	if err := h.repo.Update(c.Request.Context(), &p); err != nil {
		if conflictOnDuplicateName(c, err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Delete handles DELETE /service/rest/v1/security/privileges/:id
func (h *PrivilegeHandler) Delete(c *gin.Context) {
	if err := h.repo.Delete(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// SetRolePrivileges handles PUT /service/rest/v1/security/roles/:id/privileges
// Body: {"privilegeIds": ["uuid1", "uuid2"]}
func (h *PrivilegeHandler) SetRolePrivileges(c *gin.Context) {
	roleID := c.Param("id")
	var req struct {
		PrivilegeIDs []string `json:"privilegeIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.roleRepo.SetPrivileges(c.Request.Context(), roleID, req.PrivilegeIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// RoleMap handles GET /api/v1/security/privilege-role-map
// Returns map of privilege ID → role names that include it.
func (h *PrivilegeHandler) RoleMap(c *gin.Context) {
	m, err := h.repo.PrivilegeRoleMap(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, m)
}

// ListRolePrivileges handles GET /service/rest/v1/security/roles/:id/privileges
func (h *PrivilegeHandler) ListRolePrivileges(c *gin.Context) {
	items, err := h.repo.ListByRole(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.Privilege{}
	}
	c.JSON(http.StatusOK, items)
}

// MyPrivileges handles GET /api/v1/me/privileges
// Returns the current user's effective privileges (via their roles).
func (h *PrivilegeHandler) MyPrivileges(c *gin.Context) {
	var userID string
	if uid, ok := c.Get("userID"); ok {
		userID, _ = uid.(string)
	}
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	roles, err := h.roleRepo.GetUserRoles(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	seen := make(map[string]struct{})
	var result []domain.Privilege
	for _, role := range roles {
		privs, err := h.repo.ListByRole(c.Request.Context(), role.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, p := range privs {
			if _, dup := seen[p.ID]; !dup {
				seen[p.ID] = struct{}{}
				result = append(result, p)
			}
		}
	}
	if result == nil {
		result = []domain.Privilege{}
	}
	c.JSON(http.StatusOK, result)
}
