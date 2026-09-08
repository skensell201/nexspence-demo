package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
)

func requestLogger(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		fields := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency", time.Since(start),
			"ip", c.ClientIP(),
		}
		// Correlate the log line with the request's trace (#321). The ids come
		// from gin's key store, not c.Request.Context(): otelgin restores the
		// request context to its pre-span value when its own middleware
		// returns, so by the time this post-Next line runs the span is gone
		// from the context — see traceLogStash.
		if tid, ok := c.Get("traceID"); ok {
			fields = append(fields, "trace_id", tid)
			if sid, ok := c.Get("spanID"); ok {
				fields = append(fields, "span_id", sid)
			}
		}
		log.Infow("request", fields...)
	}
}

// traceLogStash copies the request span's ids into gin's per-request key
// store. It must be registered AFTER otelgin: it reads the span from
// c.Request.Context() inside otelgin's scope, and stashes it somewhere that —
// unlike the request context, which otelgin deliberately unwinds on return —
// survives until requestLogger's post-Next summary line. requestLogger itself
// stays first in the chain, so it keeps timing the full chain and keeps
// logging aborted CORS preflights (#321).
func traceLogStash() gin.HandlerFunc {
	return func(c *gin.Context) {
		if sc := trace.SpanContextFromContext(c.Request.Context()); sc.IsValid() {
			c.Set("traceID", sc.TraceID().String())
			c.Set("spanID", sc.SpanID().String())
		}
		c.Next()
	}
}

// corsMiddleware reflects an Origin only when it is present in allowed.
//
// An empty list sends no Access-Control-Allow-Origin at all. It used to mean
// "wildcard", which was exploitable: this API authenticates by Authorization
// header and serves anonymous repositories, so a page the user visits could
// fetch an internal artifact from inside their network and read the response.
// A wildcard now has to be written out as cors_origins: ["*"].
func corsMiddleware(allowed []string) gin.HandlerFunc {
	wildcard := false
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		if o == "*" {
			wildcard = true
		}
		set[o] = struct{}{}
	}
	return func(c *gin.Context) {
		if wildcard {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if origin := c.GetHeader("Origin"); origin != "" {
			if _, ok := set[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Add("Vary", "Origin")
			}
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// DefaultCSP is the policy served with the bundled UI.
//
// The SPA stores its JWT in localStorage — normal for a bearer-token client,
// but it means any future XSS is a full token theft. This is the defense in
// depth for that. Everything the UI needs is same-origin: Vite emits a module
// script and a stylesheet, small assets arrive as data: URIs, and the API is
// on the same host.
//
// style-src keeps 'unsafe-inline' because component libraries inject <style>
// at runtime; script-src deliberately does not, which is the half that matters.
// The UI now references no third party at all: the Geist families it used to
// pull from Google Fonts ship with the bundle (#431), which is why neither
// origin is listed here any more. Nothing may be added back without a reason
// that survives the question this policy exists to ask.
const DefaultCSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// cspExemptPrefixes are the paths that serve user-uploaded content rather than
// the UI. Raw repositories are used to host documentation sites, which carry
// their own scripts and styles; applying the UI policy to them would break a
// shipped feature. They are not covered by this header — the isolation there
// comes from hosting such sites on their own repository, not from CSP.
var cspExemptPrefixes = []string{"/repository/", "/v2/"}

// securityHeaders sets baseline hardening response headers, including the
// Content-Security-Policy for UI and API responses. An empty policy omits the
// CSP header entirely.
func securityHeaders(policy string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		if policy != "" && !hasAnyPrefix(c.Request.URL.Path, cspExemptPrefixes) {
			c.Header("Content-Security-Policy", policy)
		}
		c.Next()
	}
}

// cspPolicy resolves the configured policy: unset means the default, and the
// literal "off" disables the header for operators whose reverse proxy sets its
// own. Distinguishing "unset" from "off" is why this is not just an empty
// string default.
func cspPolicy(cfg *config.Config) string {
	switch cfg.HTTP.CSP {
	case "":
		return DefaultCSP
	case "off":
		return ""
	default:
		return cfg.HTTP.CSP
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// bodyLimit caps request body size at maxMB megabytes, except for paths that
// begin with an exempt prefix (large legitimate artifact uploads).
func bodyLimit(maxMB int, exemptPrefixes []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, p := range exemptPrefixes {
			if strings.HasPrefix(c.Request.URL.Path, p) {
				c.Next()
				return
			}
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, int64(maxMB)<<20)
		c.Next()
	}
}
