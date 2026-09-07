package api

import (
	"context"
	"encoding/base64"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	uiembed "github.com/nexspence-oss/nexspence"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/auth/loginguard"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
	"github.com/nexspence-oss/nexspence/internal/formats/cargo"
	"github.com/nexspence-oss/nexspence/internal/formats/conan"
	"github.com/nexspence-oss/nexspence/internal/formats/conda"
	"github.com/nexspence-oss/nexspence/internal/formats/gomod"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/helm"
	"github.com/nexspence-oss/nexspence/internal/formats/maven"
	"github.com/nexspence-oss/nexspence/internal/formats/npm"
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/formats/pypi"
	"github.com/nexspence-oss/nexspence/internal/formats/raw"
	"github.com/nexspence-oss/nexspence/internal/formats/rubygems"
	"github.com/nexspence-oss/nexspence/internal/formats/terraform"
	"github.com/nexspence-oss/nexspence/internal/formats/yum"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/redisclient"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/safego"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// NewRouter wires all routes and returns a ready http.Handler.
//
// ctx is the lifetime of the background work the router starts — the cleanup,
// GC, replication, download-counter and scan schedulers. Canceling it is how
// they are told to stop; they used to be launched with context.Background(),
// which meant none of them ever stopped before the process did.
//
//nolint:gocyclo // large router wiring function; splitting would hurt readability
func NewRouter(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, log logger.Logger, version string) http.Handler {
	if cfg.Log.Level != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	// ── Redis client (optional, for HA deployments) ───────────────────────────
	var rdb *redisclient.Client
	if cfg.Redis.Enabled {
		var err error
		rdb, err = redisclient.New(cfg.Redis)
		if err != nil {
			log.Warn("redis connection failed, running without Redis (single-node mode)", "err", err)
		} else {
			log.Info("redis connected", "addr", cfg.Redis.Addr)
		}
	}

	// ── Distributed locker ────────────────────────────────────
	var locker distlock.Locker = distlock.NoopLocker{}
	if rdb != nil {
		locker = distlock.NewRedisLocker(rdb)
	}

	// ── Failed-login throttle ─────────────────────────────────
	// Keyed on the account, not the address: the IP bucket in the rate limiter
	// is only as trustworthy as http.trusted_proxies. Backed by Redis when it is
	// available so attempts cannot be spread across HA nodes for a bigger budget.
	var guardStore loginguard.Store
	if rdb != nil {
		guardStore = loginguard.NewRedisStore(rdb)
	}
	loginGuard := loginguard.New(guardStore, loginguard.DefaultPolicy())

	// ── Repositories / services ───────────────────────────────
	repoRepo := postgres.NewRepositoryRepo(pool)
	blobRepo := postgres.NewBlobStoreRepo(pool)
	userRepo := postgres.NewUserRepo(pool)
	roleRepo := postgres.NewRoleRepo(pool)
	componentRepo := postgres.NewComponentRepo(pool)
	assetRepo := postgres.NewAssetRepo(pool)
	cleanupRepo := postgres.NewCleanupPolicyRepo(pool)
	auditRepo := postgres.NewAuditRepo(pool)
	userTokenRepo := postgres.NewUserTokenRepo(pool)
	webhookRepo := postgres.NewWebhookRepo(pool)
	migrationRepo := postgres.NewMigrationRepo(pool)
	privilegeRepo := postgres.NewPrivilegeRepo(pool)
	csRepo := postgres.NewContentSelectorRepo(pool)
	rbacRepo := postgres.NewRBACRepo(pool)
	rrRepo := postgres.NewRoutingRuleRepo(pool)
	replRepo := postgres.NewReplicationRepo(pool)
	promotionRepo := postgres.NewPromotionRepo(pool)
	scanRepo := postgres.NewScanResultRepo(pool)
	selectorSvc, svcErr := service.NewContentSelectorService(csRepo)
	if svcErr != nil {
		panic("content selector service init: " + svcErr.Error())
	}

	authSvc := auth.NewService(
		cfg.Auth.JWTSecret,
		cfg.Auth.JWTExpiryHours,
		cfg.Auth.BcryptCost,
	)

	localBlob, err := storage.NewBlobStoreFromConfig(context.Background(), cfg)
	if err != nil {
		panic("failed to init blob store: " + err.Error())
	}
	blobRegistry := storage.NewRegistry(localBlob)

	repoSvc := service.NewRepositoryService(repoRepo, blobRepo, localBlob, cleanupRepo)
	userSvc := service.NewUserService(userRepo, roleRepo, authSvc, log)
	var ldapSvc auth.LDAPAuthenticator
	if svc := auth.NewLDAPService(cfg.LDAP); svc != nil {
		ldapSvc = svc
		userSvc.WithLDAP(svc, cfg.LDAP)
	}

	// OIDC is optional; NewOIDCService performs discovery and will fail
	// startup if the IdP is unreachable or misconfigured (loud > lazy).
	var oidcSvc auth.OIDCAuthenticator
	var oidcSealer *auth.CookieSealer
	if cfg.OIDC.Enabled {
		svc, err := oidcInitWithRetry(context.Background(), cfg.OIDC, log)
		if err != nil {
			log.Error("oidc init failed — IdP unreachable or misconfigured", "err", err)
			os.Exit(1)
		}
		oidcSvc = svc
		keyBytes, decErr := base64.StdEncoding.DecodeString(cfg.OIDC.CookieKey)
		if decErr != nil {
			panic("oidc cookie_key base64: " + decErr.Error())
		}
		sealer, sErr := auth.NewCookieSealer(keyBytes)
		if sErr != nil {
			panic("oidc cookie sealer: " + sErr.Error())
		}
		oidcSealer = sealer
		userSvc.WithOIDC(oidcSvc, cfg.OIDC)
	}

	// SAML is optional; fails startup if IdP metadata is unreachable or misconfigured.
	var samlSvc auth.SAMLAuthenticator
	if cfg.SAML.Enabled {
		svc, err := auth.NewSAMLService(cfg.SAML)
		if err != nil {
			panic("saml init: " + err.Error())
		}
		samlSvc = svc
		userSvc.WithSAML(svc, cfg.SAML)
	}

	tokenSvc := service.NewTokenService(userTokenRepo, userRepo)
	webhookSvc := service.NewWebhookService(webhookRepo).WithLogger(log)
	repoSvc.WithWebhooks(webhookSvc)
	cleanupSvc := service.NewCleanupService(cleanupRepo, repoRepo, assetRepo, blobRepo, localBlob, log)
	cleanupSvc.WithLocker(locker)
	// Resolve each asset's physical store so cleanup deletes blobs from S3 /
	// group members, not just the global default store.
	cleanupSvc.WithResolver(blobRegistry)
	// Prune components left empty after a run so the repo doesn't keep showing rows.
	cleanupSvc.WithComponents(componentRepo)

	// Start per-policy cron scheduler in background (default: cfg.Cleanup.DefaultSchedule).
	safego.Go(log, "cleanup-cron-scheduler", func() { cleanupSvc.StartCronScheduler(ctx, cfg.Cleanup.DefaultSchedule) })

	gcSvc := &service.BlobGCService{
		Assets:        assetRepo,
		Stores:        blobRepo,
		Resolver:      blobRegistry,
		Locker:        locker,
		Log:           log,
		DefaultMinAge: cfg.GC.MinAge,
	}
	if cfg.GC.Enabled {
		safego.Go(log, "gc-cron-scheduler", func() { gcSvc.StartCronScheduler(ctx, cfg.GC.Schedule, cfg.GC.MinAge) })
	}

	replSvc := service.NewReplicationService(replRepo, assetRepo, localBlob, cfg.Auth.JWTSecret, cfg.Auth.EncryptionKeyBytes(), log)
	// Read each asset from its own physical store, so replication keeps working
	// once a repository is pointed at S3 or a second local store.
	replSvc.WithResolver(blobRepo, blobRegistry)
	safego.Go(log, "replication-cron-scheduler", func() { replSvc.StartCronScheduler(ctx) })

	promotionSvc, err := service.NewPromotionService(
		promotionRepo, componentRepo, assetRepo, repoRepo, blobRepo, scanRepo, blobRegistry,
	)
	if err != nil {
		panic("promotion service init: " + err.Error())
	}
	promotionSvc.WithWebhooks(webhookSvc)

	// Debounced download counter: in-memory aggregation, periodic batched flush.
	dlCounter := service.NewDownloadCounter(assetRepo, log)
	safego.Go(log, "download-counter", func() { dlCounter.Start(ctx, 10*time.Second) })

	// ── Format handlers ───────────────────────────────────────
	// Built before the format handlers because they take Deps by value: a
	// handler constructed without the checker would keep a nil copy of it.
	rbacSvc := service.NewRBACService(rbacRepo, repoRepo, log, cfg.Auth.AnonymousEnabled)

	// Built here rather than with the other handlers below: the format handlers
	// take Deps by value, so the scan trigger has to exist before Deps does or
	// every handler keeps a nil copy of it.
	scanSvc := service.NewScanService(componentRepo, cfg.HTTP.BaseURL).
		WithScanResults(scanRepo).
		WithCredentials(cfg.Bootstrap.AdminUsername, cfg.Bootstrap.AdminPassword).
		WithTrivy(service.TrivyOptions{
			Enabled:          cfg.Scan.Trivy.Enabled,
			Bin:              cfg.Scan.Trivy.Bin,
			DBRepository:     cfg.Scan.Trivy.DBRepository,
			JavaDBRepository: cfg.Scan.Trivy.JavaDBRepository,
			SkipDBUpdate:     cfg.Scan.Trivy.SkipDBUpdate,
			CacheDir:         cfg.Scan.Trivy.CacheDir,
		})
	// Say once, at boot, what image scanning can do here. Without this the only
	// way to learn that the binary is missing is to press a button in the UI.
	st := scanSvc.Scanner(ctx)
	// Infow, not Info: logger.Logger is a *zap.SugaredLogger, whose Info
	// concatenates its arguments into the message — the keys and values only
	// become fields through the …w form, and scanning.md tells operators to
	// read `state` off this record.
	log.Infow("image scanner", "state", st.State, "message", st.Message)
	// A nil trigger in Deps is what disables scan-on-upload, so the config
	// switch is applied by not wiring the service rather than by a flag the
	// storage layer would have to consult.
	var scanTrigger formats.ScanTrigger
	if cfg.Scan.Enabled {
		scanTrigger = scanSvc
		safego.Go(log, "scan-cron-scheduler", func() { scanSvc.StartScheduler(ctx, cfg.Scan.Schedule) })
	}

	formatDeps := formats.Deps{
		Repos:        repoRepo,
		Components:   componentRepo,
		Assets:       assetRepo,
		Blobs:        blobRepo,
		BlobStore:    localBlob,
		Registry:     blobRegistry,
		BaseURL:      cfg.HTTP.BaseURL,
		Webhooks:     webhookSvc,
		Downloads:    dlCounter,
		RoutingRules: rrRepo,
		RBAC:         rbacSvc,
		Scanner:      scanTrigger,
		// Conan's login handshake hands the client a token it then sends as
		// Bearer, so it has to be the same JWT OptionalAuth validates.
		Tokens: authSvc,
		// /v2/ upload paths are exempt from the global body cap, so this is the
		// only bound on a staged blob upload (issue #208).
		MaxUploadBytes: cfg.Docker.MaxUploadBytes,
	}
	formatRegistry := map[string]formats.FormatHandler{
		"raw":       raw.New(formatDeps),
		"maven2":    maven.New(formatDeps),
		"npm":       npm.New(formatDeps),
		"pypi":      pypi.New(formatDeps),
		"go":        gomod.New(formatDeps),
		"helm":      helm.New(formatDeps),
		"nuget":     nuget.New(formatDeps),
		"cargo":     cargo.New(formatDeps),
		"conan":     conan.New(formatDeps),
		"conda":     conda.New(formatDeps),
		"apt":       apt.New(formatDeps),
		"terraform": terraform.New(formatDeps),
		"rubygems":  rubygems.New(formatDeps),
		"yum":       yum.New(formatDeps),
		"docker":    oci.New(formatDeps),
	}
	// The OCI Distribution protocol is served under two labels — same handler,
	// different presentation (proxy defaults, UI, command hints).
	formatRegistry["oci"] = formatRegistry["docker"]
	// Group handler needs a reference to the registry to fan-out to members.
	groupHandler := group.New(formatDeps, formatRegistry)

	// ── Handlers ──────────────────────────────────────────────
	authH := handlers.NewAuthHandler(userSvc, log).WithConfig(*cfg).WithLoginGuard(loginGuard)
	repoH := handlers.NewRepositoryHandler(repoSvc, rbacSvc)
	userH := handlers.NewUserHandler(userSvc)
	blobH := handlers.NewBlobStoreHandler(blobRepo).WithUsageDeps(repoRepo, assetRepo).WithRegistry(blobRegistry).WithGC(gcSvc)
	componentH := handlers.NewComponentHandler(componentRepo, assetRepo, repoRepo, cfg.HTTP.BaseURL).WithRBAC(rbacSvc)
	// The same Deps the format handlers get: a browse delete removes the same
	// artifacts a registry delete does, from the same stores, with the same
	// webhook.
	browseH := handlers.NewBrowseHandler(formatDeps, rbacSvc)
	cleanupH := handlers.NewCleanupHandler(cleanupRepo, repoRepo, cleanupSvc, log)
	auditH := handlers.NewAuditHandler(auditRepo)
	scanH := handlers.NewScanHandler(scanSvc)
	tokenH := handlers.NewTokenHandler(tokenSvc, userSvc, cfg.Auth.TokenMaxDays)
	webhookH := handlers.NewWebhookHandler(webhookSvc)
	replH := handlers.NewReplicationHandler(replSvc, log)
	promotionH := handlers.NewPromotionHandler(promotionSvc)
	roleH := handlers.NewRoleHandler(roleRepo, userRepo)
	privH := handlers.NewPrivilegeHandler(privilegeRepo, roleRepo)
	csH := handlers.NewContentSelectorHandler(selectorSvc)
	accessGraphH := handlers.NewAccessGraphHandler(userRepo, roleRepo, privilegeRepo, csRepo)
	rrSvc := service.NewRoutingRuleService(rrRepo)
	rrH := handlers.NewRoutingRuleHandler(rrSvc)
	systemH := handlers.NewSystemHandler(cfg, pool, ldapSvc, oidcSvc).WithBlobStores(blobRepo).WithSAML(samlSvc).WithLogger(log)
	nexusMigSvc := service.NewNexusMigrationService(service.NexusMigrationConfig{
		Jobs:          migrationRepo,
		Repos:         repoSvc,
		Users:         userSvc,
		Roles:         roleRepo,
		Privileges:    privilegeRepo,
		RoutingRules:  rrRepo,
		Selectors:     selectorSvc,
		Deps:          formatDeps,
		JWTSecret:     cfg.Auth.JWTSecret,
		EncryptionKey: cfg.Auth.EncryptionKeyBytes(),
		Log:           log,
	})
	// Re-attach to migrations interrupted by a restart. Without this a job left
	// running when the process stopped sits in "running" with nothing behind it —
	// which is exactly how a migration used to look even on a healthy server.
	safego.Go(log, "nexus-migration-resume-all", func() { _ = nexusMigSvc.ResumeAll(ctx) })
	migrationH := handlers.NewMigrationHandler(migrationRepo, nexusMigSvc)
	ldapH := handlers.NewLDAPHandler(cfg.LDAP, ldapSvc)
	tasksH := handlers.NewTasksHandler(cleanupTaskAdapter{repo: cleanupRepo, svc: cleanupSvc}, replSvc)
	blobMigrationRepo := postgres.NewBlobStoreMigrationRepo(pool)
	blobMigSvc := service.NewBlobStoreMigrationService(blobMigrationRepo, assetRepo, repoRepo, blobRepo, blobRegistry).WithLogger(log)
	blobMigSvc.WithLocker(locker)
	blobMigH := handlers.NewBlobStoreMigrationHandler(blobMigSvc)

	// Resume any migrations that were interrupted by a server restart — on the
	// background context, so a shutdown mid-resume interrupts it the same way
	// the restart that left it unfinished did.
	safego.Go(log, "blob-migration-resume-all", func() { _ = blobMigSvc.ResumeAll(ctx) })
	backupSvc := &service.BackupService{
		BlobStores: blobRepo,
		Repos:      repoRepo,
		Users:      userRepo,
		Roles:      roleRepo,
		Policies:   cleanupRepo,
		Components: componentRepo,
		Assets:     assetRepo,
		BlobStore:  localBlob,
	}
	backupH := handlers.NewBackupHandler(backupSvc)
	rbacMW := handlers.RBACMiddleware(rbacSvc, repoRepo)

	// ── Gin engine ────────────────────────────────────────────
	r := gin.New()
	// Decide whose X-Forwarded-For we believe before any middleware reads
	// ClientIP. main validates the same list at startup, so an error here means
	// the config changed underneath us: keep the fail-closed engine default of
	// trusting nobody rather than silently trusting everyone.
	if err := applyTrustedProxies(r, cfg.HTTP.TrustedProxies); err != nil {
		log.Error("invalid http.trusted_proxies, trusting no proxy", "err", err)
		if err := applyTrustedProxies(r, nil); err != nil {
			log.Error("failed to reset trusted proxies", "err", err)
		}
	}
	r.Use(gin.Recovery())
	r.Use(requestLogger(log))
	r.Use(corsMiddleware(cfg.HTTP.CORSOrigins))
	r.Use(securityHeaders(cspPolicy(cfg)))
	r.Use(bodyLimit(cfg.HTTP.MaxBodyMB, []string{"/repository/", "/v2/", "/api/v1/repositories/import", "/service/rest/v1/components"}))
	r.Use(handlers.MetricsMiddleware())
	if cfg.Tracing.Enabled {
		// otelgin names spans by the Gin route template (c.FullPath()), never
		// the raw path — repository names and IDs must not explode span-name
		// cardinality (#302).
		svcName := cfg.Tracing.ServiceName
		if svcName == "" {
			svcName = "nexspence"
		}
		r.Use(otelgin.Middleware(svcName))
		// Inside otelgin's scope: stash the span ids where requestLogger's
		// post-Next summary can still see them (#321).
		r.Use(traceLogStash())
	}
	r.Use(AuditMiddleware(log, auditRepo))
	if cfg.Auth.RateLimitEnabled {
		r.Use(RateLimitMiddleware(log, cfg.Auth.RateLimitRPS, cfg.Auth.RateLimitBurst))
	}

	// Health probes: no auth, but inside the middleware chain — registering them
	// earlier meant gin snapshotted an empty chain for them, leaving the
	// readiness path without Recovery.
	registerHealthRoutes(r, handlers.LivenessHandler(), handlers.ReadinessHandler(log, pool, rdb))

	authMW := handlers.AuthMiddleware(userSvc, tokenSvc, loginGuard, log)
	adminMW := handlers.AdminRequired()

	// ── Public endpoints ──────────────────────────────────────
	r.GET("/service/rest/v1/status/check", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Auth
	r.POST("/api/v1/login", authH.Login)
	// Nexus-compat login (used by some clients)
	r.POST("/service/rest/v1/security/users/login", authH.Login)

	// Public: feature-detection for LoginPage (whether SSO button should render).
	r.GET("/api/v1/auth/config", authH.Config)

	// Public: token creation policy (max expiry days).
	r.GET("/api/v1/auth/token-policy", tokenH.TokenPolicy)

	// OIDC redirect flow — only registered when oidc.enabled=true.
	// Audit events fire via the global AuditMiddleware above (callback GET
	// whitelisted). Routes are public so the pre-auth redirect flow works.
	var oidcH *handlers.OIDCHandler
	if oidcSvc != nil && oidcSealer != nil {
		oidcH = handlers.NewOIDCHandler(oidcSvc, userSvc, userRepo, oidcSealer, cfg.OIDC, log)
		r.GET("/api/v1/auth/oidc/login", oidcH.Login)
		r.GET("/api/v1/auth/oidc/callback", oidcH.Callback)
	}

	if samlSvc != nil {
		samlH := handlers.NewSAMLHandler(samlSvc, userSvc, cfg.SAML, log)
		r.GET("/api/v1/auth/saml/metadata", samlH.Metadata)
		r.GET("/api/v1/auth/saml/login", samlH.Login)
		r.POST("/api/v1/auth/saml/acs", samlH.ACS)
	}

	// Prometheus scrape endpoint — Bearer auth unless metrics.public is set.
	registerMetricsRoute(r, cfg.Metrics.Public, authMW)

	// ── Authenticated endpoints (all valid users) ────────────
	authed := r.Group("", authMW)
	{
		authed.GET("/service/rest/v1/status", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
		authed.GET("/service/rest/v1/status/writable", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		// ── My profile ────────────────────────────────────────
		authed.GET("/api/v1/me", authH.Me)
		authed.GET("/api/v1/me/privileges", privH.MyPrivileges)
		authed.PUT("/api/v1/me/change-password", userH.ChangePassword)

		// ── Repositories (read) ───────────────────────────────
		authed.GET("/service/rest/v1/repositories", repoH.List)
		authed.GET("/service/rest/v1/repositories/:name", repoH.Get)
		authed.GET("/api/v1/repositories", repoH.List)
		authed.GET("/api/v1/repositories/:name/quota", componentH.GetQuota)

		// ── Browse ────────────────────────────────────────────
		authed.GET("/api/v1/browse/repositories/:name/docker-tree", browseH.DockerTree)
		authed.GET("/api/v1/browse/repositories/:name/oci-referrers", browseH.OCIReferrers)
		authed.GET("/api/v1/browse/repositories/:name/raw-tree", browseH.RawTree)
		authed.GET("/api/v1/browse/repositories/:name/path-tree", browseH.PathTree)
		authed.DELETE("/api/v1/browse/repositories/:name/path", browseH.DeleteByPath)
		authed.DELETE("/api/v1/browse/repositories/:name/docker-tag", browseH.DeleteDockerTag)
		authed.DELETE("/api/v1/browse/repositories/:name/docker-image", browseH.DeleteDockerImage)

		// ── Components & Assets (read + search) ───────────────
		authed.GET("/service/rest/v1/components", componentH.List)
		authed.GET("/service/rest/v1/components/:id", componentH.Get)
		authed.GET("/service/rest/v1/assets", func(c *gin.Context) {
			componentH.SearchAssets(c)
		})
		authed.GET("/service/rest/v1/assets/:id", func(c *gin.Context) {
			id := c.Param("id")
			a, err := assetRepo.Get(c.Request.Context(), id)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			if a == nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "asset not found"})
				return
			}
			a.DownloadURL = cfg.HTTP.BaseURL + "/repository/" + a.Repository + a.Path
			c.JSON(http.StatusOK, a)
		})

		// ── Search ────────────────────────────────────────────
		authed.GET("/service/rest/v1/search", componentH.Search)
		authed.GET("/service/rest/v1/search/assets", componentH.SearchAssets)
		authed.GET("/service/rest/v1/search/assets/download", componentH.SearchAssetsDownload)

		// ── Metrics (authenticated) ───────────────────────────
		authed.GET("/api/v1/metrics", handlers.MetricsHandler(pool))
		authed.GET("/api/v1/metrics/history", handlers.HistoryHandler())
		authed.GET("/api/v1/metrics/repos", handlers.ReposHandler(pool))

		// ── API tokens (current user) ─────────────────────────
		authed.GET("/api/v1/tokens", tokenH.List)
		authed.POST("/api/v1/tokens", tokenH.Create)
		authed.DELETE("/api/v1/tokens/:id", tokenH.Delete)

		// ── OIDC logout (authenticated users only) ────────────
		if oidcH != nil {
			authed.GET("/api/v1/auth/oidc/logout", oidcH.Logout)
		}

		// ── Vulnerability scan (read) ─────────────────────────
		authed.GET("/api/v1/components/:id/scan", scanH.GetScanResult)

		// ── Blob stores (read) ────────────────────────────────
		authed.GET("/service/rest/v1/blobstores", blobH.List)
		authed.GET("/service/rest/v1/blobstores/:name", blobH.Get)
		authed.GET("/api/v1/blob-stores/:name/usage", blobH.Usage)

		// ── Cleanup policies (read) ───────────────────────────
		authed.GET("/service/rest/v1/cleanup-policies", cleanupH.List)
		authed.GET("/service/rest/v1/cleanup-policies/:id", cleanupH.Get)

		// ── Roles (read) ──────────────────────────────────────
		authed.GET("/service/rest/v1/security/roles", roleH.List)

		// ── Privileges (read) ─────────────────────────────────
		authed.GET("/service/rest/v1/security/privileges", privH.List)
		authed.GET("/service/rest/v1/security/privileges/:id", privH.Get)
		authed.GET("/service/rest/v1/security/roles/:id/privileges", privH.ListRolePrivileges)
		authed.GET("/api/v1/security/privilege-role-map", privH.RoleMap)

		// ── Content Selectors (read) ──────────────────────────
		authed.GET("/service/rest/v1/security/content-selectors", csH.List)
		authed.GET("/service/rest/v1/security/content-selectors/:id", csH.Get)

		// ── Replication rules (read) ──────────────────────────
		authed.GET("/api/v1/replication/rules", replH.List)
		authed.GET("/api/v1/replication/rules/:id/history", replH.ListHistory)

		// ── Promotion (authed) ──────────────────────────────────────
		authed.GET("/api/v1/promotion/rules", promotionH.ListRules)
		authed.GET("/api/v1/promotion/requests", promotionH.ListRequests)
		authed.GET("/api/v1/components/:id/promotion-rules", promotionH.GetComponentRules)
		authed.POST("/api/v1/promotion/promote", promotionH.Promote)
	}

	// ── Admin-only endpoints (nx-admin role required) ─────────
	admin := r.Group("", authMW, adminMW)
	{
		// ── Repositories (write) ──────────────────────────────
		admin.POST("/service/rest/v1/repositories/:format/:type", repoH.Create)
		admin.PUT("/service/rest/v1/repositories/:format/:type/:name", repoH.Update)
		admin.PATCH("/service/rest/v1/repositories/:name", repoH.Patch)
		admin.DELETE("/service/rest/v1/repositories/:name", repoH.Delete)
		admin.POST("/api/v1/repositories", func(c *gin.Context) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "use /service/rest/v1/repositories/:format/:type"})
		})

		// ── Blob stores (write) ───────────────────────────────
		admin.POST("/api/v1/blobstores/test", blobH.TestConnection)
		admin.POST("/service/rest/v1/blobstores/:type", blobH.Create)
		admin.PUT("/service/rest/v1/blobstores/:type/:name", blobH.Update)
		admin.DELETE("/service/rest/v1/blobstores/:name", blobH.Delete)
		admin.POST("/api/v1/blobstores/:name/compact", blobH.Compact)
		admin.POST("/api/v1/blobstores/recompute-usage", blobH.RecomputeUsage)

		// ── Users ─────────────────────────────────────────────
		admin.GET("/service/rest/v1/security/users", userH.List)
		admin.GET("/service/rest/v1/security/users/:userId", userH.Get)
		admin.POST("/service/rest/v1/security/users", userH.Create)
		admin.PUT("/service/rest/v1/security/users/:userId", userH.Update)
		admin.DELETE("/service/rest/v1/security/users/:userId", userH.Delete)
		admin.PUT("/service/rest/v1/security/users/:userId/change-password", userH.ChangePassword)

		// ── Components & Assets (delete) ──────────────────────
		admin.DELETE("/service/rest/v1/components/:id", componentH.Delete)
		admin.PUT("/service/rest/v1/components/:id/tags", componentH.SetTags)
		admin.DELETE("/service/rest/v1/assets/:id", func(c *gin.Context) {
			if err := assetRepo.Delete(c.Request.Context(), c.Param("id")); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.Status(http.StatusNoContent)
		})

		// ── Cleanup policies (write + run) ────────────────────
		admin.POST("/service/rest/v1/cleanup-policies", cleanupH.Create)
		admin.PUT("/service/rest/v1/cleanup-policies/:id", cleanupH.Update)
		admin.DELETE("/service/rest/v1/cleanup-policies/:id", cleanupH.Delete)
		admin.POST("/service/rest/v1/cleanup-policies/:id/run", cleanupH.Run)
		admin.POST("/api/v1/cleanup-policies/:id/preview", cleanupH.Preview)

		// ── Roles (write) ─────────────────────────────────────
		admin.POST("/service/rest/v1/security/roles", roleH.Create)
		admin.PUT("/service/rest/v1/security/roles/:id", roleH.Update)
		admin.DELETE("/service/rest/v1/security/roles/:id", roleH.Delete)
		admin.PUT("/service/rest/v1/security/users/:userId/roles", roleH.SetUserRoles)

		// ── Privileges (write) ────────────────────────────────
		admin.POST("/service/rest/v1/security/privileges", privH.Create)
		admin.PUT("/service/rest/v1/security/privileges/:id", privH.Update)
		admin.DELETE("/service/rest/v1/security/privileges/:id", privH.Delete)
		admin.PUT("/service/rest/v1/security/roles/:id/privileges", privH.SetRolePrivileges)

		// ── Content Selectors (write) ─────────────────────────
		admin.POST("/service/rest/v1/security/content-selectors", csH.Create)
		admin.PUT("/service/rest/v1/security/content-selectors/:id", csH.Update)
		admin.DELETE("/service/rest/v1/security/content-selectors/:id", csH.Delete)
		admin.PUT("/service/rest/v1/security/privileges/:id/content-selector/:selectorId", csH.AttachToPrivilege)
		admin.DELETE("/service/rest/v1/security/privileges/:id/content-selector", csH.DetachFromPrivilege)

		// ── Security access graph (admin) ────────────────────
		admin.GET("/api/v1/security/access-graph", accessGraphH.Get)

		// ── Webhooks (admin) ──────────────────────────────────
		admin.GET("/api/v1/webhooks", webhookH.List)
		admin.GET("/api/v1/webhooks/:id", webhookH.Get)
		admin.POST("/api/v1/webhooks", webhookH.Create)
		admin.PUT("/api/v1/webhooks/:id", webhookH.Update)
		admin.DELETE("/api/v1/webhooks/:id", webhookH.Delete)
		admin.POST("/api/v1/webhooks/:id/test", webhookH.Test)

		// ── Replication rules (write) ─────────────────────────
		admin.POST("/api/v1/replication/rules", replH.Create)
		admin.PUT("/api/v1/replication/rules/:id", replH.Update)
		admin.DELETE("/api/v1/replication/rules/:id", replH.Delete)
		admin.POST("/api/v1/replication/rules/:id/run", replH.ManualRun)
		admin.POST("/api/v1/replication/rules/:id/test", replH.TestConnection)

		// ── Promotion (admin) ───────────────────────────────────────
		admin.POST("/api/v1/promotion/rules", promotionH.CreateRule)
		admin.PUT("/api/v1/promotion/rules/:id", promotionH.UpdateRule)
		admin.DELETE("/api/v1/promotion/rules/:id", promotionH.DeleteRule)
		admin.POST("/api/v1/promotion/requests/:id/approve", promotionH.Approve)
		admin.POST("/api/v1/promotion/requests/:id/reject", promotionH.Reject)

		// ── Audit log ─────────────────────────────────────────
		admin.GET("/service/rest/v1/audit", auditH.List)

		// ── Vulnerability scan (trigger) ──────────────────────
		admin.POST("/api/v1/components/:id/scan", scanH.Scan)

		// ── Vulnerability dashboard ────────────────────────────
		admin.GET("/api/v1/security/summary", scanH.Summary)
		admin.GET("/api/v1/security/vulnerabilities", scanH.Vulnerabilities)
		admin.POST("/api/v1/security/scan/bulk", scanH.BulkScanHandler)
		admin.GET("/api/v1/security/scanner", scanH.ScannerStatus)

		// ── System ────────────────────────────────────────────
		admin.GET("/service/rest/v1/tasks", tasksH.List)
		admin.POST("/service/rest/v1/tasks/:id/run", tasksH.Run)
		admin.GET("/service/rest/v1/security/ldap", ldapH.NexusList)
		admin.GET("/service/rest/v1/routing-rules", rrH.List)
		admin.GET("/service/rest/v1/routing-rules/:id", rrH.Get)
		admin.POST("/service/rest/v1/routing-rules", rrH.Create)
		admin.PUT("/service/rest/v1/routing-rules/:id", rrH.Update)
		admin.DELETE("/service/rest/v1/routing-rules/:id", rrH.Delete)

		// Migration
		admin.POST("/api/v1/migration/preview", migrationH.Preview)
		admin.GET("/api/v1/migration/jobs", migrationH.ListJobs)
		admin.POST("/api/v1/migration/jobs", migrationH.CreateJob)
		admin.GET("/api/v1/migration/jobs/:id", migrationH.GetJob)
		admin.POST("/api/v1/migration/jobs/:id/pause", migrationH.PauseJob)
		admin.POST("/api/v1/migration/jobs/:id/resume", migrationH.ResumeJob)
		admin.DELETE("/api/v1/migration/jobs/:id", migrationH.DeleteJob)

		// ── Blob store migration ─────────────────────────────────
		admin.POST("/api/v1/repositories/:name/migrate-blob-store", blobMigH.Start)
		admin.GET("/api/v1/repositories/:name/blob-store-migration", blobMigH.GetLatest)
		admin.DELETE("/api/v1/repositories/:name/blob-store-migration", blobMigH.Cancel)

		// ── Backup / Restore (full system) ───────────────────────
		admin.GET("/api/v1/backup/export", backupH.Export)
		admin.POST("/api/v1/backup/restore", backupH.Restore)

		// ── Per-repository Export / Import ───────────────────────
		admin.GET("/api/v1/repositories/:name/export", backupH.ExportRepo)
		admin.POST("/api/v1/repositories/import", backupH.ImportRepo)

		// System info + service health
		admin.GET("/api/v1/system/info", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"version": version,
				"product": "Nexspence",
			})
		})
		admin.GET("/api/v1/system/services", systemH.Services)
	}

	// ── Artifact protocol endpoints ───────────────────────────
	// Route /repository/:repoName/* to the appropriate format handler.
	// The format is looked up from the repository record in the DB.
	repo := r.Group("/repository/:repoName", handlers.OptionalAuth(userSvc, tokenSvc, loginGuard, log), rbacMW)
	{
		repo.Any("/*path", func(c *gin.Context) {
			repoName := c.Param("repoName")
			ctx := c.Request.Context()
			if uid, ok := c.Get("userID"); ok {
				if id, ok2 := uid.(string); ok2 && id != "" {
					uname, _ := c.Get("username")
					uStr, _ := uname.(string)
					ctx = requestctx.WithUser(ctx, id, uStr)
				}
			}
			c.Request = c.Request.WithContext(ctx)

			repoDef, err := repoRepo.Get(ctx, repoName)
			if err != nil || repoDef == nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + repoName})
				return
			}
			if !repoDef.Online {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repository is offline"})
				return
			}

			// Group repositories fan-out to member repos.
			if repoDef.Type == domain.TypeGroup {
				groupHandler.ServeHTTP(c)
				return
			}

			handler, ok := formatRegistry[string(repoDef.Format)]
			if !ok {
				c.JSON(http.StatusNotImplemented, gin.H{
					"error": "format not yet implemented: " + string(repoDef.Format),
				})
				return
			}

			handler.ServeHTTP(c)
		})
	}

	// ── Docker registry v2 API ────────────────────────────────
	// Two URL styles are supported (both fully functional):
	//   Long:  docker tag <image> localhost:8081/repository/<repoName>/<image>:<tag>
	//          API at /v2/repository/:repoName/*
	//   Short: docker tag <image> localhost:8081/<repoName>/<image>:<tag>
	//          API at /v2/:repoName/*
	// Gin static-segment priority ensures /v2/repository/... always matches the long-path
	// group first; the short-path group catches everything else under /v2/.
	// GET/HEAD /v2/ — OCI version check. The unauthenticated ping always
	// answers 401 with a Bearer challenge pointing at /v2/token — Bearer
	// alone, never Basic alongside (see dockerChallenge for why). A 200 here
	// makes docker treat the registry as public and silently drop stored
	// credentials on subsequent requests (#260) — which then fail RBAC as
	// "anonymous" and surface to users as push/pull failures even though
	// `docker login` reported success. The token endpoint keeps anonymous
	// `docker pull` working (see Phase 26): credential-less clients receive an
	// anonymous token there, and per-repo RBAC decides their reads exactly as
	// before. A side effect: "token" is a reserved name on the /v2/ surface,
	// like "repository" already is — validateNameForFormat refuses both.
	dockerV2Root := handlers.DockerV2Auth(userSvc, tokenSvc, loginGuard, log)
	r.GET("/v2/", dockerV2Root)
	r.HEAD("/v2/", dockerV2Root)
	dockerTokenH := handlers.DockerToken(
		authSvc, userSvc, tokenSvc, cfg.Auth.AnonymousEnabled,
		time.Duration(cfg.Auth.JWTExpiryHours)*time.Hour, loginGuard, log)
	r.GET("/v2/token", dockerTokenH)
	r.POST("/v2/token", dockerTokenH) // containerd/BuildKit try the OAuth2 POST first
	// Instance-level catalog (#261). A static route: without it the path fell
	// into /v2/:repoName as a phantom repository named "_catalog", whose
	// failed lookup denied even admins. On a subdomain host the rewriter still
	// maps /v2/_catalog to that repository's own catalog, which is the right
	// scope for a host that IS one repository.
	dockerCatalogH := []gin.HandlerFunc{
		handlers.OptionalAuth(userSvc, tokenSvc, loginGuard, log),
		handlers.DockerCatalog(repoRepo, rbacSvc, assetRepo),
	}
	r.GET("/v2/_catalog", dockerCatalogH...)
	r.HEAD("/v2/_catalog", dockerCatalogH...) // net/http drops the body for HEAD

	dockerV2H := serveDockerV2(repoRepo, groupHandler, formatRegistry)
	v2docker := r.Group("/v2/repository", handlers.OptionalAuth(userSvc, tokenSvc, loginGuard, log), handlers.RBACMiddleware(rbacSvc, repoRepo))
	v2docker.Any("/:repoName/*dockerpath", dockerV2H)

	// Short-path Docker: /v2/:repoName/* — no "repository/" segment required.
	// Gin static segments take priority, so /v2/repository/:repoName/... still
	// matches v2docker above; this group catches everything else under /v2/.
	v2short := r.Group("/v2", handlers.OptionalAuth(userSvc, tokenSvc, loginGuard, log), handlers.RBACMiddleware(rbacSvc, repoRepo))
	v2short.Any("/:repoName/*dockerpath", dockerV2H)

	// ── Frontend static (production build) ────────────────────
	ui := serveUI(cfg)
	r.NoRoute(func(c *gin.Context) {
		ui(c)
	})

	if sc := cfg.Docker.SubdomainConnector; sc.Enabled && (sc.BaseDomain != "" || len(sc.Aliases) > 0) {
		return NewSubdomainRewriter(r, sc.BaseDomain, sc.Aliases)
	}
	return r
}

// serveDockerV2 returns a gin.HandlerFunc that dispatches Docker OCI v2 API
// requests for a named repository. Registered on both the long-path route
// (/v2/repository/:repoName/*dockerpath) and the short-path route
// (/v2/:repoName/*dockerpath) so they share identical dispatch logic.
func serveDockerV2(
	repoRepo repository.RepositoryRepo,
	groupH formats.FormatHandler,
	fmtRegistry map[string]formats.FormatHandler,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		repoName := c.Param("repoName")
		dockerPath := c.Param("dockerpath") // e.g. /da/devops/alpine/manifests/3.22.1
		ctx := c.Request.Context()
		if uid, ok := c.Get("userID"); ok {
			if id, ok2 := uid.(string); ok2 && id != "" {
				uname, _ := c.Get("username")
				uStr, _ := uname.(string)
				ctx = requestctx.WithUser(ctx, id, uStr)
			}
		}
		c.Request = c.Request.WithContext(ctx)

		repoDef, err := repoRepo.Get(ctx, repoName)
		if err != nil || repoDef == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + repoName})
			return
		}
		if !repoDef.Online {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repository is offline"})
			return
		}

		if repoDef.Type == domain.TypeGroup {
			if !repoDef.Format.IsOCIRegistry() {
				c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not an OCI registry"})
				return
			}
			c.Params = gin.Params{
				{Key: "repoName", Value: repoName},
				{Key: "path", Value: "/v2" + dockerPath},
			}
			groupH.ServeHTTP(c)
			return
		}

		if !repoDef.Format.IsOCIRegistry() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not an OCI registry"})
			return
		}

		// Rewrite params so the registry handler sees what it expects:
		//   c.Param("repoName") = <repoName>
		//   c.Param("path")     = /v2/<imagePath>/<endpoint>
		c.Params = gin.Params{
			{Key: "repoName", Value: repoName},
			{Key: "path", Value: "/v2" + dockerPath},
		}
		h, ok := fmtRegistry[string(repoDef.Format)]
		if !ok {
			// Defensive backstop: RepoFormat.IsOCIRegistry and fmtRegistry are two
			// separate format lists that should always agree. This only fires
			// if they drift apart; not reachable in normal operation.
			c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not an OCI registry"})
			return
		}
		h.ServeHTTP(c)
	}
}

// cleanupTaskAdapter adapts the cleanup repo (List) and service (RunPolicy) to the
// TasksHandler's cleanup dependency, exposing cleanup policies as Nexus-compat tasks.
type cleanupTaskAdapter struct {
	repo repository.CleanupPolicyRepo
	svc  *service.CleanupService
}

func (a cleanupTaskAdapter) List(ctx context.Context) ([]domain.CleanupPolicy, error) {
	return a.repo.List(ctx)
}

func (a cleanupTaskAdapter) RunPolicy(ctx context.Context, id string) error {
	return a.svc.RunPolicy(ctx, id)
}

func serveUI(_ *config.Config) gin.HandlerFunc {
	uiFS, ok := resolveUIFS()
	if !ok {
		return uiPlaceholder()
	}
	return uiHandler(uiFS)
}

// resolveUIFS picks the frontend filesystem: the embedded assets when the binary
// was built with -tags embed_ui, otherwise the first on-disk dist directory.
func resolveUIFS() (fs.FS, bool) {
	if embedded, ok := uiembed.FrontendFS(); ok {
		if _, err := fs.Stat(embedded, "index.html"); err == nil {
			return embedded, true
		}
	}
	for _, dir := range []string{"./frontend/dist", "/app/frontend/dist"} {
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			return os.DirFS(dir), true
		}
	}
	return nil, false
}

// uiHandler serves a single-page app from uiFS: real files are served directly,
// and any unknown path (or any directory without its own index.html) falls back
// to index.html. A directory listing is never exposed.
func uiHandler(uiFS fs.FS) gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(uiFS))
	return func(c *gin.Context) {
		reqPath := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		if reqPath == "" {
			serveIndex(c, uiFS)
			return
		}
		info, err := fs.Stat(uiFS, reqPath)
		if err != nil {
			serveIndex(c, uiFS)
			return
		}
		if info.IsDir() {
			if _, err := fs.Stat(uiFS, path.Join(reqPath, "index.html")); err != nil {
				serveIndex(c, uiFS)
				return
			}
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}

func serveIndex(c *gin.Context, uiFS fs.FS) {
	f, err := uiFS.Open("index.html")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer func() { _ = f.Close() }()
	if rs, ok := f.(io.ReadSeeker); ok {
		// ServeContent sets Content-Type, a weak ETag from size, and handles
		// conditional GETs (304) for the SPA entrypoint. Zero modtime = "unknown".
		http.ServeContent(c.Writer, c.Request, "index.html", time.Time{}, rs)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func uiPlaceholder() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<!DOCTYPE html>
<html style="background:#070b14;color:#e5e7eb;font-family:system-ui;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0">
<body style="text-align:center">
  <div>
    <div style="font-size:48px;margin-bottom:16px">📦</div>
    <h1 style="color:#dbeafe;margin-bottom:8px">Nexspence</h1>
    <p style="color:rgba(229,231,235,0.6);margin-bottom:24px">Frontend not built yet</p>
    <code style="background:rgba(255,255,255,0.08);padding:10px 16px;border-radius:8px;color:#93c5fd">
      cd frontend &amp;&amp; npm run build
    </code>
    <p style="margin-top:24px;color:rgba(229,231,235,0.4);font-size:13px">
      API: <a href="/service/rest/v1/status/check" style="color:#3b82f6">/service/rest/v1/status/check</a>
    </p>
  </div>
</body></html>`))
	}
}

// oidcInitWithRetry retries OIDC discovery for up to 60 seconds.
// Keycloak takes ~30s to start in Docker, so nexspence would otherwise
// crash before the IdP is ready.
func oidcInitWithRetry(ctx context.Context, cfg config.OIDCConfig, log logger.Logger) (auth.OIDCAuthenticator, error) {
	deadline := time.Now().Add(60 * time.Second)
	var err error
	for {
		var svc auth.OIDCAuthenticator
		svc, err = auth.NewOIDCService(ctx, cfg)
		if err == nil {
			return svc, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		log.Warn("oidc discovery not ready, retrying in 3s", "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
