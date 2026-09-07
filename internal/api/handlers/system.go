package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/redisclient"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/safego"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ServiceStatus describes the live health of one external dependency.
type ServiceStatus struct {
	Name      string `json:"name"`
	Status    string `json:"status"` // "ok" | "error" | "disabled"
	LatencyMs int    `json:"latency_ms,omitempty"`
	Detail    string `json:"detail"`
	CheckedAt string `json:"checked_at"`
}

// SystemHandler serves system-level diagnostic endpoints.
type SystemHandler struct {
	cfg        *config.Config
	pool       *pgxpool.Pool
	ldap       auth.LDAPAuthenticator // nil when LDAP disabled
	oidc       auth.OIDCAuthenticator // nil when OIDC disabled
	saml       auth.SAMLAuthenticator // nil when SAML disabled
	blobStores repository.BlobStoreRepo
	log        logger.Logger
}

// NewSystemHandler constructs a SystemHandler from the config, DB pool, and optional LDAP/OIDC authenticators.
func NewSystemHandler(cfg *config.Config, pool *pgxpool.Pool, ldap auth.LDAPAuthenticator, oidc auth.OIDCAuthenticator) *SystemHandler {
	return &SystemHandler{cfg: cfg, pool: pool, ldap: ldap, oidc: oidc}
}

// WithBlobStores wires the blob store repo so service status can probe configured stores; returns the handler for chaining.
func (h *SystemHandler) WithBlobStores(r repository.BlobStoreRepo) *SystemHandler {
	h.blobStores = r
	return h
}

// WithSAML wires the SAML authenticator so it appears in service status; returns the handler for chaining.
func (h *SystemHandler) WithSAML(s auth.SAMLAuthenticator) *SystemHandler {
	h.saml = s
	return h
}

// WithLogger wires a logger so panics in the parallel service-status checks are
// recovered and logged instead of crashing the process; returns the handler for chaining.
func (h *SystemHandler) WithLogger(log logger.Logger) *SystemHandler {
	h.log = log
	return h
}

// Services handles GET /api/v1/system/services.
// Runs each check concurrently with a 5-second timeout.
func (h *SystemHandler) Services(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	type checkFn func(context.Context) ServiceStatus
	checks := []checkFn{
		h.checkPostgres,
		h.checkStorage,
	}
	if h.ldap != nil {
		checks = append(checks, h.checkLDAP)
	} else if h.cfg.LDAP.Enabled {
		checks = append(checks, func(_ context.Context) ServiceStatus {
			return disabled("LDAP")
		})
	}
	if h.oidc != nil {
		checks = append(checks, h.checkOIDC)
	} else if h.cfg.OIDC.Enabled {
		checks = append(checks, func(_ context.Context) ServiceStatus {
			return disabled("OIDC")
		})
	}
	if h.saml != nil {
		checks = append(checks, h.checkSAML)
	} else if h.cfg.SAML.Enabled {
		checks = append(checks, func(_ context.Context) ServiceStatus {
			return disabled("SAML")
		})
	}
	checks = append(checks, h.checkRedis)

	// Docker Subdomain Connector status.
	checks = append(checks, func(_ context.Context) ServiceStatus {
		sc := h.cfg.Docker.SubdomainConnector
		if !sc.Enabled {
			return ServiceStatus{
				Name:   "Docker Subdomain Connector",
				Status: "disabled",
				Detail: "set docker.subdomain_connector.enabled=true to activate",
			}
		}
		if sc.BaseDomain == "" {
			return ServiceStatus{
				Name:   "Docker Subdomain Connector",
				Status: "warn",
				Detail: "enabled but docker.subdomain_connector.base_domain is empty",
			}
		}
		return ServiceStatus{
			Name:   "Docker Subdomain Connector",
			Status: "ok",
			Detail: "*." + sc.BaseDomain + " → docker pull <repo>." + sc.BaseDomain + "/<image>:<tag>",
		}
	})

	// Add one check per unique S3 endpoint found in blob_stores table.
	if h.blobStores != nil {
		if stores, err := h.blobStores.List(ctx); err == nil {
			type endpointGroup struct {
				endpoint string
				names    []string
				buckets  []string
				config   map[string]any // config of the first store for this endpoint (used for probe)
			}
			groups := map[string]*endpointGroup{}
			for _, bs := range stores {
				if bs.Type != "s3" {
					continue
				}
				ep, _ := bs.Config["endpoint"].(string)
				bkt, _ := bs.Config["bucket"].(string)
				g, ok := groups[ep]
				if !ok {
					g = &endpointGroup{endpoint: ep, config: bs.Config}
					groups[ep] = g
				}
				g.names = append(g.names, bs.Name)
				if bkt != "" {
					g.buckets = append(g.buckets, bkt)
				}
			}
			for ep, g := range groups {
				ep := ep
				g := g
				checks = append(checks, func(ctx context.Context) ServiceStatus {
					return h.checkS3Endpoint(ctx, ep, g.names, g.buckets, g.config)
				})
			}
		}
	}

	results := make([]ServiceStatus, len(checks))
	var wg sync.WaitGroup
	for i, fn := range checks {
		wg.Add(1)
		go func(idx int, f checkFn) {
			defer wg.Done()
			// wg.Wait() below does NOT protect the process from a panic here:
			// an unrecovered panic in any goroutine crashes the whole program
			// regardless of whether the parent is waiting on it.
			defer safego.Recover(h.log, fmt.Sprintf("system-service-check-%d", idx))()
			results[idx] = f(ctx)
		}(i, fn)
	}
	wg.Wait()

	c.JSON(http.StatusOK, results)
}

func (h *SystemHandler) checkPostgres(ctx context.Context) ServiceStatus {
	start := time.Now()
	err := h.pool.Ping(ctx)
	lat := int(time.Since(start).Milliseconds())
	now := time.Now().UTC().Format(time.RFC3339)

	detail := dsnDetail(h.cfg.Database.DSN)
	stat := h.pool.Stat()
	if stat.TotalConns() > 0 {
		detail = fmt.Sprintf("%s · pool %d/%d", detail, stat.AcquiredConns(), stat.TotalConns())
	}

	if err != nil {
		return ServiceStatus{Name: "PostgreSQL", Status: "error", LatencyMs: lat, Detail: detail, CheckedAt: now}
	}
	return ServiceStatus{Name: "PostgreSQL", Status: "ok", LatencyMs: lat, Detail: detail, CheckedAt: now}
}

func (h *SystemHandler) checkStorage(_ context.Context) ServiceStatus {
	now := time.Now().UTC().Format(time.RFC3339)
	if h.cfg.Storage.DefaultType == "s3" {
		s3 := h.cfg.Storage.S3
		detail := fmt.Sprintf("bucket=%s region=%s", s3.Bucket, s3.Region)
		if s3.Endpoint != "" {
			detail = fmt.Sprintf("endpoint=%s %s", s3.Endpoint, detail)
		}
		return ServiceStatus{Name: "S3 Storage", Status: "ok", Detail: detail, CheckedAt: now}
	}
	path := h.cfg.Storage.Local.BasePath
	if _, err := os.Stat(path); err != nil {
		return ServiceStatus{Name: "Local Storage", Status: "error", Detail: path, CheckedAt: now}
	}
	detail := path
	if free, total, ok := diskUsage(path); ok {
		detail = fmt.Sprintf("%s · free %s / %s", path, fmtBytes(free), fmtBytes(total))
	}
	return ServiceStatus{Name: "Local Storage", Status: "ok", Detail: detail, CheckedAt: now}
}

func (h *SystemHandler) checkS3Endpoint(ctx context.Context, endpoint string, names []string, buckets []string, cfg map[string]any) ServiceStatus {
	now := time.Now().UTC().Format(time.RFC3339)
	displayName := "S3 · AWS"
	if endpoint != "" {
		displayName = "S3 · " + endpoint
	}

	storeNames := strings.Join(names, ", ")
	bucketList := strings.Join(buckets, ", ")
	detail := fmt.Sprintf("stores: %s · buckets: %s", storeNames, bucketList)

	start := time.Now()
	var probeErr error
	physical, err := storage.NewFromConfig(ctx, "s3", cfg)
	if err != nil {
		probeErr = err
	} else {
		_, probeErr = physical.Exists(ctx, "__health__")
	}
	lat := int(time.Since(start).Milliseconds())

	if probeErr != nil {
		return ServiceStatus{Name: displayName, Status: "error", LatencyMs: lat, Detail: fmt.Sprintf("%s · %s", detail, probeErr.Error()), CheckedAt: now}
	}
	return ServiceStatus{Name: displayName, Status: "ok", LatencyMs: lat, Detail: detail, CheckedAt: now}
}

func (h *SystemHandler) checkLDAP(ctx context.Context) ServiceStatus {
	start := time.Now()
	err := h.ldap.TestConnection(ctx)
	lat := int(time.Since(start).Milliseconds())
	now := time.Now().UTC().Format(time.RFC3339)

	detail := fmt.Sprintf("%s:%d · base=%s · bind=%s", h.cfg.LDAP.Host, h.cfg.LDAP.Port, h.cfg.LDAP.SearchBase, h.cfg.LDAP.BindDN)
	if err != nil {
		return ServiceStatus{Name: "LDAP", Status: "error", LatencyMs: lat, Detail: detail, CheckedAt: now}
	}
	return ServiceStatus{Name: "LDAP", Status: "ok", LatencyMs: lat, Detail: detail, CheckedAt: now}
}

func (h *SystemHandler) checkOIDC(ctx context.Context) ServiceStatus {
	start := time.Now()
	err := h.oidc.TestConnection(ctx)
	lat := int(time.Since(start).Milliseconds())
	now := time.Now().UTC().Format(time.RFC3339)

	name := "OIDC"
	if h.cfg.OIDC.DisplayName != "" {
		name = "OIDC · " + h.cfg.OIDC.DisplayName
	}
	detail := fmt.Sprintf("issuer=%s · client=%s", h.cfg.OIDC.Issuer, h.cfg.OIDC.ClientID)
	if err != nil {
		return ServiceStatus{Name: name, Status: "error", LatencyMs: lat, Detail: detail, CheckedAt: now}
	}
	return ServiceStatus{Name: name, Status: "ok", LatencyMs: lat, Detail: detail, CheckedAt: now}
}

func disabled(name string) ServiceStatus {
	return ServiceStatus{Name: name, Status: "disabled", Detail: "not configured", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
}

// dsnDetail extracts host:port and dbname from a DSN without leaking credentials.
func dsnDetail(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			host := u.Host
			db := strings.TrimPrefix(u.Path, "/")
			return fmt.Sprintf("%s/%s", host, db)
		}
	}
	// keyword=value format
	var host, port, db string
	for _, part := range strings.Fields(dsn) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "host":
			host = kv[1]
		case "port":
			port = kv[1]
		case "dbname":
			db = kv[1]
		}
	}
	if host == "" {
		host = "localhost"
	}
	if port != "" {
		host = host + ":" + port
	}
	if db != "" {
		return host + "/" + db
	}
	return host
}

func (h *SystemHandler) checkSAML(_ context.Context) ServiceStatus {
	now := time.Now().UTC().Format(time.RFC3339)
	name := "SAML"
	if h.cfg.SAML.DisplayName != "" {
		name = "SAML · " + h.cfg.SAML.DisplayName
	}
	detail := fmt.Sprintf("entity=%s · acs=%s", h.cfg.SAML.SPEntityID, h.cfg.SAML.ACSURL)
	_, err := h.saml.MetadataXML()
	if err != nil {
		return ServiceStatus{Name: name, Status: "error", Detail: detail + " · " + err.Error(), CheckedAt: now}
	}
	return ServiceStatus{Name: name, Status: "ok", Detail: detail, CheckedAt: now}
}

func (h *SystemHandler) checkRedis(_ context.Context) ServiceStatus {
	now := time.Now().UTC().Format(time.RFC3339)
	if !h.cfg.Redis.Enabled {
		return ServiceStatus{Name: "Redis", Status: "disabled", Detail: "set redis.enabled=true to activate", CheckedAt: now}
	}
	_, err := redisclient.New(h.cfg.Redis)
	if err != nil {
		return ServiceStatus{Name: "Redis", Status: "error", Detail: h.cfg.Redis.Addr + " · " + err.Error(), CheckedAt: now}
	}
	return ServiceStatus{Name: "Redis", Status: "ok", Detail: h.cfg.Redis.Addr, CheckedAt: now}
}

func fmtBytes(b uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/GB)
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/MB)
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/KB)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
