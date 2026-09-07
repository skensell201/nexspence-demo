package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// ScanHandler serves the vulnerability-scan REST endpoints.
type ScanHandler struct {
	svc        *service.ScanService
	components repository.ComponentRepo
	repos      repository.RepositoryRepo
	rbacSvc    *service.RBACService
}

// NewScanHandler constructs a ScanHandler backed by the given scan service.
func NewScanHandler(svc *service.ScanService) *ScanHandler {
	return &ScanHandler{svc: svc}
}

// WithRBAC wires the component/repository repos and RBAC service so
// GetScanResult can hide a scan result the caller has no browse privilege
// over. Nil-safe: unset, GetScanResult keeps its prior (unfiltered) behavior.
func (h *ScanHandler) WithRBAC(components repository.ComponentRepo, repos repository.RepositoryRepo, rbacSvc *service.RBACService) *ScanHandler {
	h.components = components
	h.repos = repos
	h.rbacSvc = rbacSvc
	return h
}

// Scan triggers a Trivy vulnerability scan for a Docker component.
// POST /api/v1/components/:id/scan
// Body (optional): {"imageRef": "registry/image:tag"}
func (h *ScanHandler) Scan(c *gin.Context) {
	id := c.Param("id")

	var body struct {
		ImageRef string `json:"imageRef"`
	}
	_ = c.ShouldBindJSON(&body)

	result, err := h.svc.Scan(c.Request.Context(), id, body.ImageRef)
	if err != nil {
		var unavailable *service.ScannerUnavailableError
		if errors.As(err, &unavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":   unavailable.Status.Message,
				"scanner": unavailable.Status,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// GetScanResult returns the cached scan result for a component.
// GET /api/v1/components/:id/scan
// With ?export=csv|json it returns the finding list as a downloadable file.
func (h *ScanHandler) GetScanResult(c *gin.Context) {
	export, invalid := exportFormat(c)
	if invalid {
		c.JSON(http.StatusBadRequest, gin.H{"error": `export must be "csv" or "json"`})
		return
	}

	id := c.Param("id")

	// Unlike ComponentHandler.Get (which filters by RBAC and cites #291: "a
	// direct GET by id must answer the same question... or knowing a UUID
	// becomes a key to metadata and scan results the caller has no privilege
	// over"), this sibling endpoint never checked repository visibility. 404
	// (not 403) on denial keeps the id unguessable, matching that convention.
	if h.rbacSvc != nil && h.components != nil && h.repos != nil {
		comp, cerr := h.components.Get(c.Request.Context(), id)
		if cerr != nil || comp == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "component not found"})
			return
		}
		repo, rerr := h.repos.Get(c.Request.Context(), comp.Repository)
		if rerr != nil || repo == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "component not found"})
			return
		}
		userID, _ := c.Get("userID")
		roles, _ := c.Get("roles")
		ok, aerr := h.rbacSvc.CanAccessRepo(c.Request.Context(), stringVal(userID), stringSliceVal(roles), repo, "", "browse")
		if aerr != nil || !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "component not found"})
			return
		}
	}

	result, err := h.svc.GetResult(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if result == nil {
		// No cached scan yet — not an error (avoid 404 in logs / monitoring).
		// Nothing to download either: an empty file would read as "scanned,
		// nothing found", which is a different and much more reassuring claim.
		c.Status(http.StatusNoContent)
		return
	}
	if export != "" {
		h.exportScanResult(c, export, result)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Summary returns aggregated vulnerability counts across all scanned components.
// GET /api/v1/security/summary
func (h *ScanHandler) Summary(c *gin.Context) {
	summary, err := h.svc.GetSummary(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, summary)
}

// maxVulnPageSize caps one page of vulnerability rows, the same ceiling
// AuditRepo.List and the component listing apply. Without it a client-set limit
// reaches the SQL LIMIT clause unbounded and one request can serialize every
// scanned component in the registry. The ?export= path is unaffected: it sets
// its own (much higher) row cap after this parsing.
const maxVulnPageSize = 1000

// Vulnerabilities returns a paginated list of vulnerability rows.
// GET /api/v1/security/vulnerabilities?repo=&severity=&format=&limit=&offset=
//
// With ?export=csv|json it returns the whole filtered set as a downloadable
// file instead of a page.
func (h *ScanHandler) Vulnerabilities(c *gin.Context) {
	export, invalid := exportFormat(c)
	if invalid {
		c.JSON(http.StatusBadRequest, gin.H{"error": `export must be "csv" or "json"`})
		return
	}

	limit := 50
	offset := 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > maxVulnPageSize {
				n = maxVulnPageSize
			}
			limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	f := domain.VulnFilter{
		Repo:     c.Query("repo"),
		Severity: c.Query("severity"),
		Format:   c.Query("format"),
		Limit:    limit,
		Offset:   offset,
	}
	if export != "" {
		h.exportVulnerabilities(c, export, f)
		return
	}
	items, total, err := h.svc.ListVulnerabilities(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []*domain.VulnRow{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

// ScannerStatus reports whether image scanning is available and, when it is,
// which binary is providing it.
// GET /api/v1/security/scanner
//
// Admin-only, the same gate as triggering a scan: whoever may scan may see what
// they are scanning with, and the resolved path is not handed to everyone.
func (h *ScanHandler) ScannerStatus(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.Scanner(c.Request.Context()))
}

// BulkScanHandler triggers a synchronous bulk scan across all components (or one repo).
// POST /api/v1/security/scan/bulk
// Body (optional): {"repo": "my-repo"}
func (h *ScanHandler) BulkScanHandler(c *gin.Context) {
	var body struct {
		Repo string `json:"repo"`
	}
	_ = c.ShouldBindJSON(&body)

	scanned, failed, err := h.svc.BulkScan(c.Request.Context(), body.Repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"scanned": scanned, "failed": failed})
}
