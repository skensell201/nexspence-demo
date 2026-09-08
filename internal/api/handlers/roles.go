package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// RoleHandler handles role CRUD and user-role assignment.
type RoleHandler struct {
	roles repository.RoleRepo
	users repository.UserRepo
}

// NewRoleHandler constructs a RoleHandler from the role and user repositories.
func NewRoleHandler(roles repository.RoleRepo, users repository.UserRepo) *RoleHandler {
	return &RoleHandler{roles: roles, users: users}
}

// List handles GET /service/rest/v1/security/roles
func (h *RoleHandler) List(c *gin.Context) {
	roles, err := h.roles.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if roles == nil {
		roles = []domain.Role{}
	}
	c.JSON(http.StatusOK, roles)
}

// Create handles POST /service/rest/v1/security/roles
func (h *RoleHandler) Create(c *gin.Context) {
	var ro domain.Role
	if err := c.ShouldBindJSON(&ro); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if ro.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	ro.Source = "default"
	if err := h.roles.Create(c.Request.Context(), &ro); err != nil {
		if conflictOnDuplicateName(c, err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(ro.Privileges) > 0 {
		if err := h.roles.SetPrivileges(c.Request.Context(), ro.ID, ro.Privileges); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusCreated, ro)
}

// Update handles PUT /service/rest/v1/security/roles/:id
func (h *RoleHandler) Update(c *gin.Context) {
	var ro domain.Role
	if err := c.ShouldBindJSON(&ro); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ro.ID = c.Param("id")
	if err := h.roles.Update(c.Request.Context(), &ro); err != nil {
		if conflictOnDuplicateName(c, err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.roles.SetPrivileges(c.Request.Context(), ro.ID, ro.Privileges); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ro)
}

// Delete handles DELETE /service/rest/v1/security/roles/:id
func (h *RoleHandler) Delete(c *gin.Context) {
	if err := h.roles.Delete(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// SetUserRoles handles PUT /service/rest/v1/security/users/:userId/roles
// Body: {"roleIds": ["uuid1", "uuid2"]}
func (h *RoleHandler) SetUserRoles(c *gin.Context) {
	username := c.Param("userId")
	var req struct {
		RoleIDs []string `json:"roleIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	user, err := h.users.Get(c.Request.Context(), username)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found: " + username})
		return
	}
	if err := h.roles.SetUserRoles(c.Request.Context(), user.ID, req.RoleIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
