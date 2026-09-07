package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the root application configuration loaded from file and env overrides.
type Config struct {
	HTTP      HTTPConfig      `mapstructure:"http"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Auth      AuthConfig      `mapstructure:"auth"`
	LDAP      LDAPConfig      `mapstructure:"ldap"`
	OIDC      OIDCConfig      `mapstructure:"oidc"`
	SAML      SAMLConfig      `mapstructure:"saml"`
	Bootstrap BootstrapConfig `mapstructure:"bootstrap"`
	Log       LogConfig       `mapstructure:"log"`
	Search    SearchConfig    `mapstructure:"search"`
	Cleanup   CleanupConfig   `mapstructure:"cleanup"`
	GC        GCConfig        `mapstructure:"gc"`
	Scan      ScanConfig      `mapstructure:"scan"`
	Audit     AuditConfig     `mapstructure:"audit"`
	Docker    DockerConfig    `mapstructure:"docker"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Proxy     ProxyConfig     `mapstructure:"proxy"`
	Outbound  OutboundConfig  `mapstructure:"outbound"`
	Metrics   MetricsConfig   `mapstructure:"metrics"`
	Tracing   TracingConfig   `mapstructure:"tracing"`
}

// TracingConfig governs OpenTelemetry distributed tracing (#302). Disabled by
// default, like Trivy and signing: nexspence ships no trace backend, the
// operator brings one (Jaeger, Tempo, any OTLP receiver) the same way they
// bring Prometheus for /metrics. The "always keep error traces" guarantee is
// tail-sampling in the operator's collector, not a head sampler here — a head
// sampler decides before the handler has produced a status and cannot see
// errors.
type TracingConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// OTLPEndpoint is the host:port of the OTLP receiver (4317 grpc, 4318 http).
	OTLPEndpoint string `mapstructure:"otlp_endpoint"`
	// OTLPProtocol selects the exporter transport: "grpc" (default) or "http".
	OTLPProtocol string `mapstructure:"otlp_protocol"`
	// OTLPInsecure sends plaintext instead of TLS — for a collector on the
	// same host or an internal network.
	OTLPInsecure bool `mapstructure:"otlp_insecure"`
	// SampleRatio head-samples root spans at this ratio (0..1). Child spans
	// follow their parent. Conservative default: full sampling is not viable
	// at real volume.
	SampleRatio float64 `mapstructure:"sample_ratio"`
	// ServiceName is the service.name resource attribute; distinguishes
	// instances when several nexspence deployments share one backend.
	ServiceName string `mapstructure:"service_name"`
	// Environment becomes the deployment.environment resource attribute.
	Environment string `mapstructure:"environment"`
}

// MetricsConfig governs the Prometheus scrape endpoint at /metrics.
type MetricsConfig struct {
	// Public serves /metrics without authentication. It defaults to false
	// because the endpoint shares the public listener with the API, so an
	// anonymous scrape hands out install size, artifact and download counts
	// and the Go runtime fingerprint. Turn it on when the listener is only
	// reachable from a trusted network — a cluster-internal Service, a
	// localhost bind, a reverse proxy that blocks /metrics from outside —
	// and a scrape token would just be one more secret to rotate.
	Public bool `mapstructure:"public"`
}

// OutboundConfig governs where the server is allowed to make requests when the
// target URL comes from configuration: proxy upstreams, webhook endpoints and
// replication targets.
type OutboundConfig struct {
	// AllowedInternalCIDRs lists internal ranges that may be reached anyway.
	// By default every loopback, private, link-local, CGNAT, multicast and
	// broadcast address is refused, which is what stops a proxy repository
	// pointed at a cloud metadata endpoint from turning repo-admin into
	// credential theft. On-prem deployments that genuinely proxy an internal
	// registry name that range here.
	AllowedInternalCIDRs []string `mapstructure:"allowed_internal_cidrs"`
}

// ProxyConfig configures the server-wide outbound proxy used when fetching from
// upstream registries for proxy repositories. All fields are optional; when
// empty, the standard HTTP_PROXY/HTTPS_PROXY/NO_PROXY environment variables are
// honored instead. Per-repository proxy_config overrides these defaults.
type ProxyConfig struct {
	HTTPProxy   string `mapstructure:"http_proxy"`
	HTTPSProxy  string `mapstructure:"https_proxy"`
	SOCKS5Proxy string `mapstructure:"socks5_proxy"`
	NoProxy     string `mapstructure:"no_proxy"`
	Username    string `mapstructure:"username"`
	Password    string `mapstructure:"password"`
}

// BootstrapConfig holds the admin account that is created/synced on every startup.
type BootstrapConfig struct {
	// Enabled runs the startup admin bootstrap. It defaults to true so a fresh
	// install has an account to log in as. Turn it off once real accounts exist
	// and the admin credentials no longer belong in the config file: the whole
	// bootstrap block can then be deleted without the shipped admin/admin123
	// defaults quietly coming back (#243).
	Enabled        bool   `mapstructure:"enabled"`
	AdminUsername  string `mapstructure:"admin_username"`
	AdminPassword  string `mapstructure:"admin_password"`
	AdminEmail     string `mapstructure:"admin_email"`
	AdminFirstName string `mapstructure:"admin_first_name"`
}

// HTTPConfig configures the HTTP server (listen address, timeouts, body limit, TLS).
type HTTPConfig struct {
	Addr            string    `mapstructure:"addr"`
	ReadTimeoutSec  int       `mapstructure:"read_timeout_sec"`
	WriteTimeoutSec int       `mapstructure:"write_timeout_sec"`
	MaxBodyMB       int       `mapstructure:"max_body_mb"`
	CORSOrigins     []string  `mapstructure:"cors_origins"`
	TLS             TLSConfig `mapstructure:"tls"`
	BaseURL         string    `mapstructure:"base_url"`
	// TrustedProxies lists the peers (IPs or CIDRs) allowed to set
	// X-Forwarded-For. Empty — the default — trusts nobody, so the audit log
	// and the rate limiter see the real peer address. Use "*" to trust every
	// hop, which is only safe when the server is unreachable except through a
	// proxy you control.
	TrustedProxies []string `mapstructure:"trusted_proxies"`
	// CSP overrides the Content-Security-Policy served with the UI. Empty uses
	// the built-in policy; the literal "off" omits the header, for deployments
	// whose reverse proxy sets its own.
	CSP string `mapstructure:"csp"`
}

// TLSConfig holds the optional server certificate and key for HTTPS.
type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

// DatabaseConfig holds the PostgreSQL DSN and connection pool settings.
type DatabaseConfig struct {
	DSN        string `mapstructure:"dsn"`
	MaxConns   int    `mapstructure:"max_conns"`
	MinConns   int    `mapstructure:"min_conns"`
	MaxIdleSec int    `mapstructure:"max_idle_sec"`
}

// StorageConfig selects the default blob store backend and its local/S3 settings.
type StorageConfig struct {
	// Default blob store type: "local" or "s3"
	DefaultType string      `mapstructure:"default_type"`
	Local       LocalConfig `mapstructure:"local"`
	S3          S3Config    `mapstructure:"s3"`
}

// LocalConfig holds the base path for the local filesystem blob store.
type LocalConfig struct {
	BasePath string `mapstructure:"base_path"`
}

// S3Config holds credentials and endpoint settings for the S3-compatible blob store.
type S3Config struct {
	Bucket          string `mapstructure:"bucket"`
	Region          string `mapstructure:"region"`
	Endpoint        string `mapstructure:"endpoint"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	ForcePathStyle  bool   `mapstructure:"force_path_style"`
	// SkipTLSVerify disables certificate verification against the endpoint,
	// for an on-prem S3 fronted by a private CA (#403). Off by default.
	SkipTLSVerify bool `mapstructure:"skip_tls_verify"`
}

// AuthConfig holds JWT, bcrypt, anonymous-access, and token-expiry settings.
type AuthConfig struct {
	JWTSecret         string  `mapstructure:"jwt_secret"`
	JWTExpiryHours    int     `mapstructure:"jwt_expiry_hours"`
	AnonymousEnabled  bool    `mapstructure:"anonymous_enabled"`
	PasswordMinLength int     `mapstructure:"password_min_length"`
	BcryptCost        int     `mapstructure:"bcrypt_cost"`
	TokenMaxDays      int     `mapstructure:"token_max_days"`
	RateLimitEnabled  bool    `mapstructure:"rate_limit_enabled"`
	RateLimitRPS      float64 `mapstructure:"rate_limit_rps"`
	RateLimitBurst    float64 `mapstructure:"rate_limit_burst"`
	// AllowInsecureDefaults permits the server to start even when the shipped
	// default JWT secret or admin password ("admin123") is in use. Intended for
	// local dev / quick-start only; production must leave this false.
	AllowInsecureDefaults bool `mapstructure:"allow_insecure_defaults"`
	// EncryptionKey is an optional dedicated key (base64, 32 bytes) for sealing
	// replication credentials. Empty = derive from JWTSecret (legacy).
	EncryptionKey string `mapstructure:"encryption_key"`
}

// LogConfig configures the structured logger level and output format.
type LogConfig struct {
	Level  string `mapstructure:"level"`  // debug, info, warn, error
	Format string `mapstructure:"format"` // json, text
}

// LDAPConfig configures LDAP/Active Directory authentication.
type LDAPConfig struct {
	Enabled            bool            `mapstructure:"enabled"`
	Host               string          `mapstructure:"host"`
	Port               int             `mapstructure:"port"`
	UseTLS             bool            `mapstructure:"use_tls"`   // LDAPS (port 636)
	StartTLS           bool            `mapstructure:"start_tls"` // STARTTLS on plain conn
	InsecureSkipVerify bool            `mapstructure:"insecure_skip_verify"`
	BindDN             string          `mapstructure:"bind_dn"`
	BindPassword       string          `mapstructure:"bind_password"`
	SearchBase         string          `mapstructure:"search_base"`
	SearchFilter       string          `mapstructure:"search_filter"` // {0} → username
	UserAttributes     LDAPUserAttrMap `mapstructure:"user_attributes"`
	GroupBase          string          `mapstructure:"group_base"`
	GroupFilter        string          `mapstructure:"group_filter"`    // {dn} → user DN
	GroupAttribute     string          `mapstructure:"group_attribute"` // attr holding group name
	AutoCreateUsers    bool            `mapstructure:"auto_create_users"`
	TimeoutSec         int             `mapstructure:"timeout_sec"`
	// AdminGroup, when set, automatically grants the nx-admin role to any LDAP user
	// whose group membership includes this group name.
	AdminGroup string `mapstructure:"admin_group"`
	// RoleMappings maps LDAP group names to Nexspence role names (like OIDC/SAML).
	RoleMappings map[string]string `mapstructure:"role_mappings"`
}

// LDAPUserAttrMap maps LDAP attribute names to user profile fields.
type LDAPUserAttrMap struct {
	Email     string `mapstructure:"email"`
	FirstName string `mapstructure:"first_name"`
	LastName  string `mapstructure:"last_name"`
}

// OIDCConfig configures OIDC / OAuth2 SSO authentication.
// One provider per deployment; coexists with local + LDAP.
type OIDCConfig struct {
	Enabled         bool     `mapstructure:"enabled"`
	DisplayName     string   `mapstructure:"display_name"` // button text: "Sign in with {DisplayName}"
	Issuer          string   `mapstructure:"issuer"`
	ClientID        string   `mapstructure:"client_id"`
	ClientSecret    string   `mapstructure:"client_secret"`
	RedirectURL     string   `mapstructure:"redirect_url"`
	FrontendBaseURL string   `mapstructure:"frontend_base_url"`
	Scopes          []string `mapstructure:"scopes"`

	// Provisioning: jit (default) | allowlist | manual.
	Provisioning   string   `mapstructure:"provisioning"`
	EmailAllowlist []string `mapstructure:"email_allowlist"` // glob patterns (path.Match)

	// Role resolution.
	GroupsClaim  string            `mapstructure:"groups_claim"`  // default "groups"
	AdminGroup   string            `mapstructure:"admin_group"`   // claim value → nx-admin
	RoleMappings map[string]string `mapstructure:"role_mappings"` // claim value → Nexspence role name

	// Claim name overrides (provider-specific).
	UsernameClaim string `mapstructure:"username_claim"`
	EmailClaim    string `mapstructure:"email_claim"`
	NameClaim     string `mapstructure:"name_claim"`

	ShowLoginButton    bool   `mapstructure:"show_login_button"`
	CookieSecure       bool   `mapstructure:"cookie_secure"`
	CookieKey          string `mapstructure:"cookie_key"` // base64 32 bytes
	AllowedSkewSeconds int    `mapstructure:"allowed_skew_seconds"`

	// PublicIssuerURL, when set, replaces the internal Issuer URL in auth redirect
	// and SLO URLs sent to the browser. Needed when the IdP is reachable inside the
	// container network under a different hostname than in the browser (e.g., Keycloak
	// in Docker: internal=http://keycloak:8080/realms/x, public=http://localhost:8180/realms/x).
	// Token validation always uses the internal Issuer so iss-claim checks still pass.
	PublicIssuerURL string `mapstructure:"public_issuer_url"`
}

const exampleJWTSecret = "CHANGE_ME_AT_LEAST_32_CHARACTERS_LONG" //nolint:gosec // G101 false positive: this is the known-bad placeholder string we reject at startup, not an actual credential
const jwtSecretMinLen = 32

// DevDefaultJWTSecret is the bootable-but-insecure secret shipped in the
// docker-compose and Helm defaults. Unlike exampleJWTSecret it passes
// ValidateAuth (long enough, not the fatal placeholder) so quick-start works,
// but cmd/server warns at startup when it is in use.
const DevDefaultJWTSecret = "nexspence-dev-default-secret-change-me-in-production" //nolint:gosec // G101 false positive: this is the recognizable dev-default we warn about at startup, not a production credential

// IsDevDefaultJWTSecret reports whether s is the shipped development default.
func IsDevDefaultJWTSecret(s string) bool { return s == DevDefaultJWTSecret }

// DevDefaultOIDCCookieKey is the OIDC state-cookie key hard-coded in the
// docker-compose files and config.yaml.example (base64 of
// "abcdefghijklmnopqrstuvwxyz123456"). It seals the state cookie that protects
// the login flow from CSRF, so an attacker who knows it can forge a state
// cookie matching their own state parameter — cmd/server refuses to start with
// it once OIDC is enabled.
const DevDefaultOIDCCookieKey = "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=" //nolint:gosec // G101: this is the recognizable dev default we refuse to boot with, not a production credential

// IsDevDefaultOIDCCookieKey reports whether s is the shipped development default.
func IsDevDefaultOIDCCookieKey(s string) bool { return s == DevDefaultOIDCCookieKey }

// EncryptionKeyBytes returns the decoded dedicated encryption key, or nil when
// unset. Load() has already validated the encoding and length.
func (a AuthConfig) EncryptionKeyBytes() []byte {
	if a.EncryptionKey == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(a.EncryptionKey)
	if err != nil {
		return nil
	}
	return b
}

// ValidateAuth rejects an empty, placeholder, or too-short JWT signing secret.
func ValidateAuth(a AuthConfig) error {
	if a.JWTSecret == "" {
		return fmt.Errorf("auth.jwt_secret is required (or set NEXSPENCE_AUTH_JWT_SECRET)")
	}
	if a.JWTSecret == exampleJWTSecret {
		return fmt.Errorf("auth.jwt_secret is set to the example placeholder; set a unique secret of at least %d characters", jwtSecretMinLen)
	}
	if len(a.JWTSecret) < jwtSecretMinLen {
		return fmt.Errorf("auth.jwt_secret must be at least %d characters", jwtSecretMinLen)
	}
	if a.EncryptionKey != "" {
		keyBytes, err := base64.StdEncoding.DecodeString(a.EncryptionKey)
		if err != nil || len(keyBytes) != 32 {
			return fmt.Errorf("auth.encryption_key must be base64-encoded 32 bytes")
		}
	}
	return nil
}

// ValidateOIDC returns nil when the OIDC config is usable.
// Called from Load() after unmarshal; exported for unit tests.
func ValidateOIDC(c OIDCConfig) error {
	if !c.Enabled {
		return nil
	}
	if c.Issuer == "" {
		return fmt.Errorf("oidc.issuer is required when oidc.enabled=true")
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return fmt.Errorf("oidc.client_id and oidc.client_secret are required when oidc.enabled=true")
	}
	if c.RedirectURL == "" || c.FrontendBaseURL == "" {
		return fmt.Errorf("oidc.redirect_url and oidc.frontend_base_url are required when oidc.enabled=true")
	}
	if c.Provisioning == "allowlist" && len(c.EmailAllowlist) == 0 {
		return fmt.Errorf("oidc.email_allowlist must be non-empty when oidc.provisioning=allowlist")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(c.CookieKey)
	if err != nil || len(keyBytes) != 32 {
		return fmt.Errorf("oidc.cookie_key must be base64-encoded 32 bytes")
	}
	return nil
}

// SAMLConfig configures SAML 2.0 SP-initiated SSO.
// One IdP per deployment; coexists with local, LDAP, and OIDC.
type SAMLConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	DisplayName     string `mapstructure:"display_name"`
	ShowLoginButton bool   `mapstructure:"show_login_button"`
	FrontendBaseURL string `mapstructure:"frontend_base_url"`

	// IdP metadata source — one of these is required when enabled=true.
	IDPMetadataURL string `mapstructure:"idp_metadata_url"`
	IDPMetadataXML string `mapstructure:"idp_metadata_xml"`

	// SP identity.
	SPEntityID string `mapstructure:"sp_entity_id"`
	ACSURL     string `mapstructure:"acs_url"`

	// SP signing key pair. If empty, an ephemeral RSA-2048 pair is generated at startup.
	SPCertPEM string `mapstructure:"sp_cert_pem"`
	SPKeyPEM  string `mapstructure:"sp_key_pem"`

	// Provisioning: jit (default) | allowlist | manual.
	Provisioning   string   `mapstructure:"provisioning"`
	EmailAllowlist []string `mapstructure:"email_allowlist"`

	// SAML attribute names.
	GroupsAttribute   string `mapstructure:"groups_attribute"`
	EmailAttribute    string `mapstructure:"email_attribute"`
	UsernameAttribute string `mapstructure:"username_attribute"`
	NameAttribute     string `mapstructure:"name_attribute"`

	// Role resolution.
	AdminGroup   string            `mapstructure:"admin_group"`
	RoleMappings map[string]string `mapstructure:"role_mappings"`

	// HMACKey is base64-encoded 32 bytes for signing RelayState. Auto-generated if empty.
	HMACKey string `mapstructure:"hmac_key"`
}

// ValidateSAML returns nil when the SAML config is usable.
func ValidateSAML(c SAMLConfig) error {
	if !c.Enabled {
		return nil
	}
	if c.SPEntityID == "" {
		return fmt.Errorf("saml.sp_entity_id is required when saml.enabled=true")
	}
	if c.ACSURL == "" {
		return fmt.Errorf("saml.acs_url is required when saml.enabled=true")
	}
	if c.IDPMetadataURL == "" && c.IDPMetadataXML == "" {
		return fmt.Errorf("saml.idp_metadata_url or saml.idp_metadata_xml is required when saml.enabled=true")
	}
	if c.Provisioning == "allowlist" && len(c.EmailAllowlist) == 0 {
		return fmt.Errorf("saml.email_allowlist must be non-empty when saml.provisioning=allowlist")
	}
	return nil
}

// SearchConfig configures the built-in PostgreSQL full-text search.
type SearchConfig struct {
	// Full-text search is built into PostgreSQL — no external deps
	// MinQueryLen is the minimum characters before trigram search kicks in
	MinQueryLen int `mapstructure:"min_query_len"`
}

// CleanupConfig holds the default cron schedule for cleanup policies.
type CleanupConfig struct {
	DefaultSchedule string `mapstructure:"default_schedule"`
}

// GCConfig configures scheduled blob garbage collection.
type GCConfig struct {
	Enabled  bool          `mapstructure:"enabled"`
	Schedule string        `mapstructure:"schedule"`
	MinAge   time.Duration `mapstructure:"min_age"`
}

// ScanConfig configures automatic vulnerability scanning of stored artifacts.
type ScanConfig struct {
	// Enabled queues every upload for a background scan and runs the periodic
	// re-scan. Off, scanning stays entirely on-demand.
	Enabled bool `mapstructure:"enabled"`
	// Schedule is the cron expression for the full bulk re-scan, which covers
	// artifacts uploaded before auto-scan was on and re-checks already-scanned
	// ones against newly published CVEs. Empty disables the periodic run while
	// leaving scan-on-upload in place.
	Schedule string `mapstructure:"schedule"`
	// Trivy governs image scanning, which nexspence does not ship a scanner for.
	Trivy TrivyConfig `mapstructure:"trivy"`
}

// TrivyConfig points nexspence at a Trivy binary the operator supplies, and at
// the vulnerability database that binary should read.
//
// Nexspence ships no scanner: the image carries no Trivy and no wrapper for
// one. Enabled=false is the default because a product must not offer what it
// does not deliver — see docs/scanning.md for how an operator supplies it.
//
// Every database and cache field is empty by default and an empty value means
// "do not pass the flag at all", so Trivy's own defaults apply. Restating them
// here would freeze a copy that rots the first time upstream changes one.
// Bin is the exception: it defaults to "trivy" resolved through PATH, since an
// empty bin is not a flag omission but a missing executable to run.
type TrivyConfig struct {
	Enabled          bool     `mapstructure:"enabled"`
	Bin              string   `mapstructure:"bin"`
	DBRepository     []string `mapstructure:"db_repository"`
	JavaDBRepository []string `mapstructure:"java_db_repository"`
	SkipDBUpdate     bool     `mapstructure:"skip_db_update"`
	CacheDir         string   `mapstructure:"cache_dir"`
}

// AuditConfig controls audit-log retention, soft cap, and partition rotation.
type AuditConfig struct {
	RetentionDays    int           `mapstructure:"retention_days"`
	SoftCap          int64         `mapstructure:"soft_cap"`
	RotationInterval time.Duration `mapstructure:"rotation_interval"`
	LookaheadMonths  int           `mapstructure:"lookahead_months"`
}

// DockerConfig holds Docker-specific settings such as the subdomain connector.
type DockerConfig struct {
	SubdomainConnector SubdomainConnectorConfig `mapstructure:"subdomain_connector"`
	// MaxUploadBytes caps a single staged blob upload. The /v2/ upload paths
	// are exempt from http.max_body_mb so large image layers survive intact,
	// which leaves this as their only bound. 0 disables the cap.
	MaxUploadBytes int64 `mapstructure:"max_upload_bytes"`
}

// SubdomainConnectorConfig configures per-repository Docker subdomain routing.
//
// Aliases decouple the client-facing hostname from the repository name (#282):
// each entry maps a full hostname (any DNS name the operator wired up — it
// does not have to sit under BaseDomain) to the repository that should answer
// it, the way Nexus lets an arbitrary DNS name point at a connector port.
// Without an alias, a "<sub>.<base_domain>" host still resolves to the
// repository named "<sub>", exactly as before.
type SubdomainConnectorConfig struct {
	Enabled    bool              `mapstructure:"enabled"`
	BaseDomain string            `mapstructure:"base_domain"`
	Aliases    map[string]string `mapstructure:"aliases"`
}

// RedisConfig holds optional Redis connection settings.
type RedisConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// Load reads configuration from the given file path, applies defaults and
// NEXSPENCE_* env overrides, validates required fields, and returns the Config.
func Load(path string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("http.addr", ":8081")
	v.SetDefault("http.read_timeout_sec", 1800)
	v.SetDefault("http.write_timeout_sec", 1800)
	v.SetDefault("http.max_body_mb", 1024)
	v.SetDefault("http.cors_origins", []string{})
	v.SetDefault("http.trusted_proxies", []string{})
	v.SetDefault("outbound.allowed_internal_cidrs", []string{})
	v.SetDefault("http.csp", "")
	v.SetDefault("http.base_url", "http://localhost:8081")
	// Viper bug: AutomaticEnv + Unmarshal silently skips keys that have no
	// default/config-file value (not in AllKeys). Empty-string defaults ensure
	// these keys are always resolved from env vars when no config file is present.
	v.SetDefault("database.dsn", "")
	v.SetDefault("auth.jwt_secret", "")
	v.SetDefault("auth.encryption_key", "")
	v.SetDefault("storage.s3.endpoint", "")
	v.SetDefault("storage.s3.bucket", "")
	v.SetDefault("storage.s3.region", "")
	v.SetDefault("storage.s3.access_key_id", "")
	v.SetDefault("storage.s3.secret_access_key", "")
	v.SetDefault("storage.s3.force_path_style", false)
	v.SetDefault("storage.s3.skip_tls_verify", false)
	v.SetDefault("database.max_conns", 100)
	v.SetDefault("database.min_conns", 5)
	v.SetDefault("database.max_idle_sec", 300)
	v.SetDefault("storage.default_type", "local")
	v.SetDefault("storage.local.base_path", "./data/blobs")
	v.SetDefault("auth.jwt_expiry_hours", 24)
	v.SetDefault("auth.anonymous_enabled", true)
	v.SetDefault("auth.password_min_length", 8)
	v.SetDefault("auth.bcrypt_cost", 12)
	v.SetDefault("auth.token_max_days", 90)
	v.SetDefault("auth.rate_limit_enabled", true)
	v.SetDefault("auth.rate_limit_rps", 50.0)
	v.SetDefault("auth.rate_limit_burst", 100.0)
	v.SetDefault("auth.allow_insecure_defaults", false)
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("search.min_query_len", 2)
	v.SetDefault("cleanup.default_schedule", "0 */6 * * *")
	v.SetDefault("gc.enabled", true)
	v.SetDefault("gc.schedule", "0 3 * * 0")
	v.SetDefault("gc.min_age", "24h")
	v.SetDefault("scan.enabled", true)
	// Nightly, off-peak: a full re-scan re-queries OSV.dev per component and
	// re-runs Trivy per image.
	v.SetDefault("scan.schedule", "0 3 * * *")
	// Every key gets a default even when the default is the zero value: viper's
	// AutomaticEnv + Unmarshal silently skips keys that are absent from
	// AllKeys, so a key with no default is unreachable from an environment
	// variable when no config file is present. The same reason as the
	// database.dsn / auth.jwt_secret block above.
	v.SetDefault("scan.trivy.enabled", false)
	v.SetDefault("scan.trivy.bin", "trivy")
	v.SetDefault("scan.trivy.db_repository", []string{})
	v.SetDefault("scan.trivy.java_db_repository", []string{})
	v.SetDefault("scan.trivy.skip_db_update", false)
	v.SetDefault("scan.trivy.cache_dir", "")
	v.SetDefault("audit.retention_days", 90)
	v.SetDefault("audit.soft_cap", int64(1_000_000))
	v.SetDefault("audit.rotation_interval", "24h")
	v.SetDefault("audit.lookahead_months", 2)
	v.SetDefault("docker.subdomain_connector.enabled", false)
	v.SetDefault("docker.subdomain_connector.base_domain", "")
	v.SetDefault("docker.max_upload_bytes", int64(10<<30)) // 10 GiB
	v.SetDefault("oidc.enabled", false)
	v.SetDefault("oidc.display_name", "SSO")
	v.SetDefault("oidc.public_issuer_url", "")
	v.SetDefault("oidc.scopes", []string{"openid", "profile", "email", "groups"})
	v.SetDefault("oidc.provisioning", "jit")
	v.SetDefault("oidc.groups_claim", "groups")
	v.SetDefault("oidc.username_claim", "preferred_username")
	v.SetDefault("oidc.email_claim", "email")
	v.SetDefault("oidc.name_claim", "name")
	v.SetDefault("oidc.show_login_button", true)
	v.SetDefault("oidc.cookie_secure", true)
	v.SetDefault("oidc.allowed_skew_seconds", 60)
	v.SetDefault("saml.enabled", false)
	v.SetDefault("saml.display_name", "SAML SSO")
	v.SetDefault("saml.show_login_button", true)
	v.SetDefault("saml.provisioning", "jit")
	v.SetDefault("saml.groups_attribute", "groups")
	v.SetDefault("saml.email_attribute", "email")
	v.SetDefault("saml.username_attribute", "uid")
	v.SetDefault("saml.name_attribute", "displayName")
	v.SetDefault("ldap.enabled", false)
	v.SetDefault("ldap.port", 389)
	v.SetDefault("ldap.search_filter", "(uid={0})")
	v.SetDefault("ldap.user_attributes.email", "mail")
	v.SetDefault("ldap.user_attributes.first_name", "givenName")
	v.SetDefault("ldap.user_attributes.last_name", "sn")
	v.SetDefault("ldap.group_attribute", "cn")
	v.SetDefault("ldap.auto_create_users", true)
	v.SetDefault("ldap.timeout_sec", 10)
	v.SetDefault("bootstrap.enabled", true)
	v.SetDefault("bootstrap.admin_username", "admin")
	v.SetDefault("bootstrap.admin_password", "admin123")
	v.SetDefault("bootstrap.admin_email", "admin@example.com")
	v.SetDefault("bootstrap.admin_first_name", "Admin")
	v.SetDefault("metrics.public", false)
	v.SetDefault("tracing.enabled", false)
	v.SetDefault("tracing.otlp_endpoint", "localhost:4317")
	v.SetDefault("tracing.otlp_protocol", "grpc")
	v.SetDefault("tracing.otlp_insecure", false)
	v.SetDefault("tracing.sample_ratio", 0.1)
	v.SetDefault("tracing.service_name", "nexspence")
	v.SetDefault("tracing.environment", "")
	v.SetDefault("redis.enabled", false)
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.db", 0)

	// Config file
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// Env override: NEXSPENCE_DATABASE_DSN, NEXSPENCE_AUTH_JWT_SECRET, etc.
	v.SetEnvPrefix("NEXSPENCE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		// Config file is optional when all required values come from env.
		// viper.ConfigFileNotFoundError: file not found via search paths.
		// *fs.PathError / errors.Is(ErrNotExist): explicit path given but file absent.
		var cfgNotFound viper.ConfigFileNotFoundError
		if !errors.As(err, &cfgNotFound) && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Database.DSN == "" {
		return nil, fmt.Errorf("database.dsn is required (or set NEXSPENCE_DATABASE_DSN)")
	}
	if err := ValidateAuth(cfg.Auth); err != nil {
		return nil, err
	}
	if err := ValidateOIDC(cfg.OIDC); err != nil {
		return nil, err
	}
	if err := ValidateSAML(cfg.SAML); err != nil {
		return nil, err
	}
	if t := cfg.Tracing; t.Enabled {
		if t.SampleRatio < 0 || t.SampleRatio > 1 {
			return nil, fmt.Errorf("tracing.sample_ratio must be within [0, 1], got %v", t.SampleRatio)
		}
		if t.OTLPProtocol != "grpc" && t.OTLPProtocol != "http" {
			return nil, fmt.Errorf("tracing.otlp_protocol must be \"grpc\" or \"http\", got %q", t.OTLPProtocol)
		}
		if t.OTLPEndpoint == "" {
			return nil, fmt.Errorf("tracing.otlp_endpoint is required when tracing is enabled")
		}
	}

	return &cfg, nil
}
