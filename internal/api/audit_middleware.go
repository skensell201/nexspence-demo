package api

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// AuditMiddleware writes an audit event after each mutating request completes.
// It only records PUT/POST/DELETE/PATCH on key management paths.
func AuditMiddleware(auditRepo repository.AuditRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next() // run handler first

		method := c.Request.Method
		if method != "PUT" && method != "POST" && method != "DELETE" && method != "PATCH" {
			// Also audit OIDC callback GET as a LOGIN event (no mutation on
			// our side, but it is a security-relevant user-identification event).
			if method != "GET" || !strings.HasPrefix(c.Request.URL.Path, "/api/v1/auth/oidc/callback") {
				return
			}
		}

		path := c.Request.URL.Path
		if !isAuditablePath(path) {
			return
		}

		userID, _ := c.Get("userID")
		username, _ := c.Get("username")
		userIDStr, _ := userID.(string)
		usernameStr, _ := username.(string)
		if usernameStr == "" {
			usernameStr = "anonymous"
		}

		status := c.Writer.Status()
		result := "success"
		if status >= 400 && status < 500 {
			result = "denied"
		} else if status >= 500 {
			result = "failure"
		}

		domainStr, action, entityType, entityName, ctxData := classifyPath(method, path, c)

		// Merge audit_source (set by LoginOIDC handler path) into Context.
		// Allows UI/SIEM to distinguish oidc / ldap / local logins without
		// special-casing the action or path.
		if src, ok := c.Get("audit_source"); ok {
			if ctxData == nil {
				ctxData = map[string]any{}
			}
			ctxData["source"] = src
		}

		e := &domain.AuditEvent{
			UserID:     strPtr(userIDStr),
			Username:   usernameStr,
			RemoteIP:   c.ClientIP(),
			UserAgent:  c.Request.UserAgent(),
			Domain:     domainStr,
			Action:     action,
			EntityType: entityType,
			EntityName: entityName,
			Context:    ctxData,
			Result:     result,
		}
		go func() { _ = auditRepo.Write(context.Background(), e) }()
	}
}

func isAuditablePath(path string) bool {
	prefixes := []string{
		"/service/rest/v1/repositories",
		"/service/rest/v1/security/users",
		"/service/rest/v1/security/roles",
		"/service/rest/v1/security/privileges",
		"/service/rest/v1/security/content-selectors",
		"/service/rest/v1/blobstores",
		"/service/rest/v1/cleanup-policies",
		"/api/v1/webhooks",
		"/api/v1/browse/",
		"/api/v1/login",
		"/api/v1/auth/oidc/callback",
		"/repository/",
		"/v2/",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// lastSegment returns the substring after the final '/' in p (or p itself if none).
func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// classifyPath maps (method, path) to audit fields and any additional context.
// The returned ctxData map is non-nil and may be empty.
func classifyPath(method, path string, c *gin.Context) (domainStr, action, entityType, entityName string, ctxData map[string]any) {
	ctxData = map[string]any{}

	// Login is classified specially: action=LOGIN, entityName=username attempted.
	if strings.HasPrefix(path, "/api/v1/login") ||
		strings.HasPrefix(path, "/api/v1/auth/oidc/callback") {
		return "SECURITY", "LOGIN", "USER", c.GetString("username"), ctxData
	}

	switch {
	case strings.HasPrefix(path, "/service/rest/v1/security/users"):
		domainStr = "SECURITY"
		entityType = "USER"
		entityName = c.Param("userId")
	case strings.HasPrefix(path, "/service/rest/v1/security/roles"):
		domainStr = "SECURITY"
		entityType = "ROLE"
		entityName = c.Param("id")
	case strings.HasPrefix(path, "/service/rest/v1/security/privileges"):
		domainStr = "SECURITY"
		entityType = "PRIVILEGE"
		entityName = c.Param("id")
	case strings.HasPrefix(path, "/service/rest/v1/security/content-selectors"):
		domainStr = "SECURITY"
		entityType = "CONTENT_SELECTOR"
		entityName = c.Param("id")
	case strings.HasPrefix(path, "/api/v1/webhooks"):
		domainStr = "SYSTEM"
		entityType = "WEBHOOK"
		entityName = c.Param("id")
	case strings.HasPrefix(path, "/service/rest/v1/repositories"):
		domainStr = "REPOSITORY"
		entityType = "REPOSITORY"
		entityName = c.Param("name")
	case strings.HasPrefix(path, "/service/rest/v1/blobstores"):
		domainStr = "BLOBSTORE"
		entityType = "BLOBSTORE"
		entityName = c.Param("name")
	case strings.HasPrefix(path, "/service/rest/v1/cleanup-policies"):
		domainStr = "CLEANUP"
		entityType = "CLEANUP_POLICY"
		entityName = c.Param("id")
	case strings.HasPrefix(path, "/repository/"):
		domainStr = "REPOSITORY"
		entityType = "ARTIFACT"
		entityName = c.Param("repoName")
		if p := c.Param("path"); p != "" {
			ctxData["path"] = strings.TrimPrefix(p, "/")
		}
	case strings.HasPrefix(path, "/v2/"):
		domainStr = "REPOSITORY"
		entityType = "ARTIFACT"
		entityName = c.Param("repoName")
		if strings.Contains(path, "/manifests/") {
			ctxData["path"] = "manifests/" + lastSegment(path)
		} else if strings.Contains(path, "/blobs/") {
			ctxData["path"] = "blobs/" + lastSegment(path)
		}
	default:
		domainStr = "SYSTEM"
	}

	switch method {
	case "POST":
		action = "CREATE"
	case "PUT":
		action = "UPDATE"
	case "DELETE":
		action = "DELETE"
	case "PATCH":
		action = "UPDATE"
	default:
		action = method
	}
	return domainStr, action, entityType, entityName, ctxData
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
