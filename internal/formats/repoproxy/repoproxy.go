// Package repoproxy implements read-through caching for proxy-type repositories.
package repoproxy

import (
	"bytes"
	"context"
	"crypto/md5"  //nolint:gosec // md5/sha1 required for artifact-protocol checksums (Maven .md5/.sha1, npm shasum), not security
	"crypto/sha1" //nolint:gosec // md5/sha1 required for artifact-protocol checksums (Maven .md5/.sha1, npm shasum), not security
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/http/httpproxy"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/netguard"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// UpstreamClient is the shared HTTP client used to fetch artifacts from upstream
// remotes on cache miss when no explicit per-repo/global proxy is configured.
// Its dialer is SSRF-guarded (remote_url is user-configured): connections that
// resolve to internal addresses are refused. It honors the standard
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY environment variables via Transport.Proxy;
// for env-configured proxies the guard still applies, so internal proxies must
// be set via per-repo proxy_config or SetGlobalProxy (see proxyclient.go),
// which route through a client that permits the trusted proxy address.
var UpstreamClient = &http.Client{
	Transport: &http.Transport{
		Proxy: envProxyFromRequest,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: netguard.DialControl,
		}).DialContext,
		MaxIdleConns:        128,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	},
	Timeout: 5 * time.Minute,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 12 {
			return fmt.Errorf("stopped after 12 redirects")
		}
		return nil
	},
}

// envProxyFromRequest resolves the proxy for a request from the process
// environment (HTTP_PROXY/HTTPS_PROXY/NO_PROXY), read fresh each call.
func envProxyFromRequest(req *http.Request) (*url.URL, error) {
	return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
}

var hopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailers":            true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// RejectMutation responds 405 for mutating methods on a proxy repository.
func RejectMutation(c *gin.Context, repo *domain.Repository) bool {
	if repo == nil || repo.Type != domain.TypeProxy {
		return false
	}
	switch c.Request.Method {
	case http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete:
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error": "proxy repository is read-only (use a hosted repository to publish)",
		})
		return true
	default:
		return false
	}
}

// RemoteURL extracts proxy_config.remote_url.
func RemoteURL(repo *domain.Repository) (string, error) {
	if repo.ProxyConfig == nil {
		return "", fmt.Errorf("proxy_config.remote_url is required for proxy repositories")
	}
	raw, ok := repo.ProxyConfig["remote_url"]
	if !ok {
		return "", fmt.Errorf("proxy_config.remote_url is required for proxy repositories")
	}
	s, ok := raw.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("proxy_config.remote_url must be a non-empty string")
	}
	return strings.TrimRight(s, "/"), nil
}

// JoinURL joins the remote base URL with the repository-relative artifact path.
func JoinURL(remoteBase, repoRelativePath string) (string, error) {
	// An absolute upstream URL passes through: a handler hands one over when
	// the real registry splits its API across hosts (crates.io serves
	// downloads on a different host than its sparse index, #347). Only
	// handler code ever builds upstreamPath — client paths arrive normalized
	// and relative — so this is not reachable from request input, and the
	// SSRF guard on the upstream client still applies to the fetch itself.
	if strings.HasPrefix(repoRelativePath, "http://") || strings.HasPrefix(repoRelativePath, "https://") {
		if _, err := url.Parse(repoRelativePath); err != nil {
			return "", fmt.Errorf("invalid upstream url: %w", err)
		}
		return repoRelativePath, nil
	}
	u, err := url.Parse(remoteBase)
	if err != nil {
		return "", fmt.Errorf("invalid remote_url: %w", err)
	}
	suffix := strings.Trim(repoRelativePath, "/")
	merged := path.Join(strings.TrimSuffix(u.Path, "/"), suffix)
	u.Path = "/" + strings.TrimPrefix(merged, "/")
	// A caller may hand over a pre-escaped segment — NPMMetadataPath encodes a
	// scoped package as "@scope%2Fname". Serializing that from Path alone would
	// escape the "%" again ("%252F"), a URL registry.npmjs.org answers with 405.
	// When the merged path IS a valid encoding, record it as the raw form and
	// keep the decoded form in Path, so String() emits it exactly once-escaped.
	// A stray "%" that is not a valid escape fails PathUnescape and keeps
	// today's escape-once behavior.
	if unescaped, uerr := url.PathUnescape(u.Path); uerr == nil && unescaped != u.Path {
		u.RawPath = u.Path
		u.Path = unescaped
	}
	return u.String(), nil
}

func copyRespHeaders(dst http.Header, src http.Header) {
	for k, vv := range src {
		if hopHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func applyChecksumHeaders(c *gin.Context, a *domain.Asset) {
	if a.SHA256 != "" {
		c.Header("X-Checksum-SHA256", a.SHA256)
		c.Header("ETag", `"`+a.SHA256+`"`)
	}
	if a.SHA1 != "" {
		c.Header("X-Checksum-SHA1", a.SHA1)
	}
	if a.MD5 != "" {
		c.Header("X-Checksum-MD5", a.MD5)
	}
}

// DefaultMetadataMaxAge is the freshness window applied to proxied repository
// metadata (indexes, Release/InRelease, packuments, repodata, simple-index
// pages, …) when a repository does not override it via
// proxy_config.metadata_max_age. Immutable artifacts (.deb, .tgz, .jar, blobs
// addressed by digest) are never revalidated — callers pass maxAge == 0 for those.
const DefaultMetadataMaxAge = 10 * time.Minute

// maxRewrittenMetadataBytes caps storeAndServeResponse's and
// serveCachedAsset's buffered io.ReadAll — the only unbounded reads in this
// codebase, both gated on rewrite != nil (npm packuments, PyPI simple pages,
// NuGet registration/flatcontainer indexes: the shared path any proxy that
// rewrites a response before serving it goes through). Every other metadata
// read here (OCI manifests/referrers, npm's own age-filter packument read,
// PyPI's simple index reader, RubyGems gemspecs) already caps its ReadAll
// with a LimitReader; this was the one exception, reachable from any proxy
// repository whose remote_url a caller can point at a hostile, compromised,
// or simply oversized-by-accident upstream.
const maxRewrittenMetadataBytes = 32 << 20

// MetadataMaxAge returns the freshness TTL to use for proxied metadata on this
// repository. It reads proxy_config["metadata_max_age"], interpreted as a number
// of seconds (JSON numbers arrive as float64; strings are parsed). Any unset,
// non-positive, or invalid value falls back to DefaultMetadataMaxAge.
//
// Handlers call this for metadata/index paths and pass the result as the maxAge
// argument to ServeGET; for immutable artifact paths they pass 0 instead.
func MetadataMaxAge(repo *domain.Repository) time.Duration {
	if repo == nil || repo.ProxyConfig == nil {
		return DefaultMetadataMaxAge
	}
	raw, ok := repo.ProxyConfig["metadata_max_age"]
	if !ok {
		return DefaultMetadataMaxAge
	}
	var secs float64
	switch v := raw.(type) {
	case float64:
		secs = v
	case float32:
		secs = float64(v)
	case int:
		secs = float64(v)
	case int64:
		secs = float64(v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return DefaultMetadataMaxAge
		}
		secs = f
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return DefaultMetadataMaxAge
		}
		secs = f
	default:
		return DefaultMetadataMaxAge
	}
	if secs <= 0 {
		return DefaultMetadataMaxAge
	}
	return time.Duration(secs * float64(time.Second))
}

// MinimumPackageAge returns the minimum age an upstream package version must
// have before this proxy repository serves it (#323) — a supply-chain gate: a
// freshly published malicious version stays invisible until it has had time to
// be detected and reported. Read from proxy_config["minimum_package_age"] in
// seconds (the metadata_max_age convention). Absent, zero, negative or invalid
// means DISABLED (0) — the policy is opt-in, so unlike MetadataMaxAge there is
// no positive default to fall back to.
func MinimumPackageAge(repo *domain.Repository) time.Duration {
	if repo == nil || repo.ProxyConfig == nil {
		return 0
	}
	raw, ok := repo.ProxyConfig["minimum_package_age"]
	if !ok {
		return 0
	}
	var secs float64
	switch v := raw.(type) {
	case float64:
		secs = v
	case float32:
		secs = float64(v)
	case int:
		secs = float64(v)
	case int64:
		secs = float64(v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0
		}
		secs = f
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0
		}
		secs = f
	default:
		return 0
	}
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// cacheFetchStore resolves the physical blob store that holds a cached asset.
func cacheFetchStore(ctx context.Context, d formats.Deps, asset *domain.Asset) storage.BlobStore {
	if asset.BlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil && bsMeta != nil {
			return base.PhysicalStore(ctx, d, bsMeta)
		}
	}
	return d.BlobStore
}

// serveCachedAsset streams (or, for HEAD, describes) a cached asset to the client.
// A non-nil rewrite is applied to the body on the way out (the cache keeps the
// upstream original). It takes ownership of rc and closes it.
func serveCachedAsset(c *gin.Context, d formats.Deps, asset *domain.Asset, rc io.ReadCloser, rewrite func([]byte) []byte) {
	defer func() { _ = rc.Close() }()
	// Count only real GETs so a HEAD probe + GET pull don't double-count.
	if c.Request.Method == http.MethodGet && d.Downloads != nil {
		d.Downloads.Add(asset.ID)
	}
	applyChecksumHeaders(c, asset)
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Type", asset.ContentType)
		if asset.SizeBytes > 0 && rewrite == nil {
			c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		}
		c.Status(http.StatusOK)
		return
	}
	if rewrite != nil {
		// Reading one byte past the cap is what makes an overflow visible: a
		// reader limited to exactly the cap returns the trimmed prefix and a
		// nil error, which would rewrite (and serve) a silently truncated
		// document instead of rejecting it.
		body, err := io.ReadAll(io.LimitReader(rc, maxRewrittenMetadataBytes+1))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cache read: " + err.Error()})
			return
		}
		if len(body) > maxRewrittenMetadataBytes {
			c.JSON(http.StatusBadGateway, gin.H{"error": "cached metadata exceeds the rewrite size limit"})
			return
		}
		c.Data(http.StatusOK, asset.ContentType, rewrite(body))
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
}

// FetchUpstreamOnce performs a single upstream GET and returns the raw response
// without caching anything. It exists for computed registry endpoints — the
// referrers API, the catalog — whose responses are generated per request and are
// not artifacts, so the blob-backed cache in ServeGET does not apply.
//
// upstreamPath is the repository-relative path; rawQuery is the already-encoded
// query string (without the leading "?") and must be passed separately, because
// JoinURL percent-escapes whatever it is handed as a path — a "?" glued on would
// arrive upstream as %3F, i.e. part of the digest, not a filter.
//
// The caller must close the response body.
func FetchUpstreamOnce(ctx context.Context, repo *domain.Repository, upstreamPath, rawQuery string, hdr http.Header) (*http.Response, error) {
	baseRemote, err := RemoteURL(repo)
	if err != nil {
		return nil, err
	}
	upstream, err := JoinURL(baseRemote, upstreamPath)
	if err != nil {
		return nil, err
	}
	if rawQuery != "" {
		upstream += "?" + rawQuery
	}
	return fetchUpstreamWithDockerHubAuth(ctx, repo, ClientFor(repo), http.MethodGet, upstream, baseRemote, hdr)
}

// ServeGET serves a cached asset or fetches upstream, streaming to the client
// and persisting to the blob store on success. repo must be TypeProxy.
// upstreamPath, when non-empty, is used only for the upstream URL (e.g. npm scoped metadata);
// the cache key and DB asset path remain repoRelativePath.
//
// maxAge controls metadata freshness. maxAge == 0 means the content is immutable
// (artifacts, blobs addressed by digest): a cache hit is served forever without
// contacting upstream. maxAge > 0 marks the path as mutable metadata (apt
// Release/InRelease/Packages, npm packuments, yum repodata, pypi simple pages, …):
// when the cached copy is older than maxAge it is revalidated against upstream with
// a conditional request before being served. See revalidateAndServe.
func ServeGET(c *gin.Context, d formats.Deps, repo *domain.Repository, repoRelativePath, upstreamPath string,
	coords base.Coords, defaultContentType string, maxAge time.Duration,
) error {
	return ServeGETRewritten(c, d, repo, repoRelativePath, upstreamPath, coords, defaultContentType, maxAge, nil)
}

// ServeGETRewritten is ServeGET for metadata documents whose body must be
// transformed before serving — e.g. rewriting embedded upstream download URLs
// to this proxy (#98). The blob cache stores the upstream ORIGINAL and rewrite
// runs on every response (cache hit, miss, and revalidation), so a BaseURL
// change never invalidates cached metadata. A nil rewrite streams exactly like
// ServeGET. rewrite must be pure and tolerate arbitrary input (on malformed
// bodies, return the input unchanged).
func ServeGETRewritten(c *gin.Context, d formats.Deps, repo *domain.Repository, repoRelativePath, upstreamPath string,
	coords base.Coords, defaultContentType string, maxAge time.Duration, rewrite func([]byte) []byte,
) error {
	ctx := c.Request.Context()
	if repo.Type != domain.TypeProxy {
		return fmt.Errorf("repoproxy: repository %q is not a proxy", repo.Name)
	}

	upJoin := upstreamPath
	if upJoin == "" {
		upJoin = repoRelativePath
	}

	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
	default:
		return fmt.Errorf("repoproxy: unsupported method %s", c.Request.Method)
	}

	asset, err := d.Assets.GetByPath(ctx, repo.Name, repoRelativePath)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("repoproxy: asset lookup: %w", err)
	}
	if asset != nil {
		rc, _, blobErr := cacheFetchStore(ctx, d, asset).Get(ctx, asset.BlobKey)
		if blobErr == nil {
			// Metadata freshness: a stale cached copy of mutable metadata is
			// revalidated against upstream before serving. Immutable content
			// (maxAge == 0) and HEAD probes always serve straight from cache.
			if maxAge > 0 && c.Request.Method == http.MethodGet && time.Since(asset.LastModified) > maxAge {
				return revalidateAndServe(c, d, repo, asset, repoRelativePath, upJoin, coords, defaultContentType, rc, rewrite)
			}
			serveCachedAsset(c, d, asset, rc, rewrite)
			return nil
		}
		// Blob file missing from cache storage (storage path changed or file deleted).
		// Fall through to upstream fetch so the client gets content and the cache is repaired.
	}

	return fetchAndCache(c, d, repo, repoRelativePath, upJoin, coords, defaultContentType, rewrite)
}

// fetchAndCache handles a cache miss (or a cache entry whose blob went missing):
// it fetches from upstream, forwarding the client's own conditional headers, and
// on success stores the blob and serves it.
func fetchAndCache(c *gin.Context, d formats.Deps, repo *domain.Repository,
	repoRelativePath, upJoin string, coords base.Coords, defaultContentType string, rewrite func([]byte) []byte,
) error {
	ctx := c.Request.Context()

	baseRemote, err := RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil
	}
	upstream, err := JoinURL(baseRemote, upJoin)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil
	}

	upHdr := http.Header{}
	if ac := c.GetHeader("Accept"); ac != "" {
		upHdr.Set("Accept", ac)
	}
	if inm := c.GetHeader("If-None-Match"); inm != "" {
		upHdr.Set("If-None-Match", inm)
	}
	// Do not forward client Authorization to Docker Hub — Docker sends Nexspence Basic
	// credentials; Hub would reject them. Docker Hub anonymous pulls use auth.docker.io token.

	// Docker/registry clients often probe with HEAD. Upstream HEAD has no body, so we cannot
	// cache — always use GET upstream when we need to populate the blob (HEAD or GET miss).
	upstreamMethod := c.Request.Method
	if upstreamMethod == http.MethodHead {
		upstreamMethod = http.MethodGet
	}

	resp, err := fetchUpstreamWithDockerHubAuth(ctx, repo, ClientFor(repo), upstreamMethod, upstream, baseRemote, upHdr)
	if err != nil {
		DispatchProxyError(d, repo.Name, repoRelativePath, upstream, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed: " + err.Error()})
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		copyRespHeaders(c.Writer.Header(), resp.Header)
		c.Status(http.StatusNotModified)
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		copyRespHeaders(c.Writer.Header(), resp.Header)
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c.Writer, resp.Body)
		return nil
	}

	return storeAndServeResponse(c, d, repo, repoRelativePath, defaultContentType, coords, resp, rewrite)
}

// revalidateAndServe is invoked when a cached metadata asset is older than its
// TTL. It asks upstream for a fresh copy with a conditional request and:
//   - 304 Not Modified → refresh the asset's freshness timestamp, serve the cache;
//   - 2xx             → replace the cached blob and serve the new copy;
//   - upstream error / other status → serve the stale cache so clients (e.g.
//     `apt update`) keep working, and record a proxy_error for the failure.
//
// It takes ownership of rc (the open cache blob) and always closes it.
func revalidateAndServe(c *gin.Context, d formats.Deps, repo *domain.Repository, asset *domain.Asset,
	repoRelativePath, upJoin string, coords base.Coords, defaultContentType string, rc io.ReadCloser, rewrite func([]byte) []byte,
) error {
	ctx := c.Request.Context()

	baseRemote, err := RemoteURL(repo)
	if err != nil {
		serveCachedAsset(c, d, asset, rc, rewrite)
		return nil
	}
	upstream, err := JoinURL(baseRemote, upJoin)
	if err != nil {
		serveCachedAsset(c, d, asset, rc, rewrite)
		return nil
	}

	upHdr := http.Header{}
	if ac := c.GetHeader("Accept"); ac != "" {
		upHdr.Set("Accept", ac)
	}
	// Conditional revalidation: request a fresh body only if the resource changed
	// since we cached it. We seed If-Modified-Since from the asset's stored
	// timestamp (the moment we last fetched/validated). We deliberately do NOT
	// derive If-None-Match from our SHA256: upstreams don't recognize our content
	// hash as their ETag, and per RFC 7232 If-None-Match would take precedence over
	// If-Modified-Since, forcing a 200 on every request and defeating revalidation.
	if !asset.LastModified.IsZero() {
		upHdr.Set("If-Modified-Since", asset.LastModified.UTC().Format(http.TimeFormat))
	}

	resp, err := fetchUpstreamWithDockerHubAuth(ctx, repo, ClientFor(repo), http.MethodGet, upstream, baseRemote, upHdr)
	if err != nil {
		// Upstream unreachable → serve stale cache so metadata consumers keep working.
		DispatchProxyError(d, repo.Name, repoRelativePath, upstream, err)
		serveCachedAsset(c, d, asset, rc, rewrite)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		// Upstream confirms our copy is current: refresh freshness and serve cache.
		// Use context.Background so the touch survives request cancellation.
		if d.Assets != nil {
			_ = d.Assets.TouchLastModified(context.Background(), asset.ID)
		}
		serveCachedAsset(c, d, asset, rc, rewrite)
		return nil
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		// Upstream returned a newer copy: replace the cached blob and serve it.
		_ = rc.Close()
		return storeAndServeResponse(c, d, repo, repoRelativePath, defaultContentType, coords, resp, rewrite)
	default:
		// Any other status (404/410/5xx): don't discard a good cache on a transient
		// upstream hiccup — serve stale and record the anomaly.
		DispatchProxyError(d, repo.Name, repoRelativePath, upstream,
			fmt.Errorf("revalidation returned status %d", resp.StatusCode))
		serveCachedAsset(c, d, asset, rc, rewrite)
		return nil
	}
}

// storeAndServeResponse streams a successful (2xx) upstream response to the
// client while persisting it to the blob store and registering the DB asset,
// replacing any prior cache entry for repoRelativePath.
func storeAndServeResponse(c *gin.Context, d formats.Deps, repo *domain.Repository,
	repoRelativePath, defaultContentType string, coords base.Coords, resp *http.Response, rewrite func([]byte) []byte,
) error {
	ctx := c.Request.Context()

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = defaultContentType
	}

	// Rewritten metadata cannot stream: buffer the upstream body, persist the
	// ORIGINAL to the cache (hashes over the original), and serve the client
	// the transformed copy. Metadata documents are small, so buffering is fine.
	if rewrite != nil {
		// Same cap and +1 trick as serveCachedAsset's re-read below: a
		// hostile, compromised, or simply oversized-by-accident upstream
		// otherwise buffers without limit before any quota/size check runs.
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRewrittenMetadataBytes+1))
		if readErr != nil {
			return fmt.Errorf("proxy metadata read: %w", readErr)
		}
		if len(body) > maxRewrittenMetadataBytes {
			return fmt.Errorf("proxy metadata exceeds the %d byte rewrite size limit", maxRewrittenMetadataBytes)
		}
		if err := storeOriginal(ctx, c, d, repo, repoRelativePath, ct, coords, body); err != nil {
			return err
		}
		copyRespHeaders(c.Writer.Header(), resp.Header)
		c.Writer.Header().Del("Content-Length") // length changes after rewrite
		if c.Request.Method == http.MethodHead {
			c.Header("Content-Type", ct)
			c.Status(resp.StatusCode)
			return nil
		}
		c.Data(resp.StatusCode, ct, rewrite(body))
		return nil
	}

	copyRespHeaders(c.Writer.Header(), resp.Header)
	c.Header("Content-Type", ct)
	c.Status(resp.StatusCode)

	// Quota gate (#189): when caching this artifact would exceed the repository
	// or blob-store quota, serve it straight from upstream and skip the cache —
	// clients keep working, the cache stops growing. Any check failure skips the
	// cache the same way: a DB blip during the check is not a license to cache
	// with no quota applied (#328).
	if resp.ContentLength > 0 {
		if qErr := base.CheckQuota(ctx, d, repo, resp.ContentLength); qErr != nil {
			if c.Request.Method != http.MethodHead {
				_, _ = io.Copy(c.Writer, resp.Body)
			}
			return nil
		}
	}

	blobKey := base.BlobKey(repo.Name, repoRelativePath)
	sha256h := sha256.New()
	sha1h := sha1.New() //nolint:gosec // protocol checksum, not security
	md5h := md5.New()   //nolint:gosec // protocol checksum, not security

	// Resolve the physical blob store for this repo so the write location matches
	// what RegisterStoredBlob will record in the DB asset row.
	resolvedID, resolvedName, physStore := base.ResolveBlobStore(ctx, d, repo)

	pr, pw := io.Pipe()
	putErrCh := make(chan error, 1)
	go func() {
		err := physStore.Put(ctx, blobKey, pr, resp.ContentLength)
		// The io.Copy below runs on the client's own request goroutine, so a Put
		// that returns without draining pr — a disk-full or permission failure
		// fails before reading a single byte — would block that copy on
		// pw.Write forever: no response, no error, no timeout (#367). Closing
		// the read end makes the pending write return io.ErrClosedPipe at once,
		// and is a no-op once the pipe has already drained normally.
		_ = pr.Close()
		putErrCh <- err
	}()

	hashes := io.MultiWriter(sha256h, sha1h, md5h)
	clientSink := io.Writer(c.Writer)
	if c.Request.Method == http.MethodHead {
		clientSink = io.Discard
	}
	mw := io.MultiWriter(pw, hashes, clientSink)

	written, copyErr := io.Copy(mw, resp.Body)
	_ = pw.CloseWithError(copyErr)
	putErr := <-putErrCh

	if copyErr != nil || putErr != nil {
		// No cleanup by blob key here (#198). A failed Put publishes nothing —
		// it stages under its own temp path and removes it on error (#196) — and
		// a copy error always fails the Put too, since the pipe is closed with
		// that error. So the only thing at blobKey is someone else's data: the
		// copy already cached at this path, or one a concurrent fill just
		// published and registered. Deleting it leaves an asset row with no
		// bytes behind it; leaving it costs nothing.
		return fmt.Errorf("proxy cache write: %w", errors.Join(copyErr, putErr))
	}

	sha256sum := hex.EncodeToString(sha256h.Sum(nil))
	sha1sum := hex.EncodeToString(sha1h.Sum(nil))
	md5sum := hex.EncodeToString(md5h.Sum(nil))

	size := written
	if size <= 0 {
		if s, e := physStore.Size(ctx, blobKey); e == nil {
			size = s
		}
	}

	// Post-write quota gate for upstreams that don't declare Content-Length: the
	// client already has the bytes, so just drop the over-quota blob unregistered.
	// A check that failed to evaluate drops the blob the same way — fail closed,
	// but the client keeps its stream (#328).
	if resp.ContentLength <= 0 && size > 0 {
		if qErr := base.CheckQuota(ctx, d, repo, size); qErr != nil {
			dropUnreferencedBlob(ctx, d, repo, repoRelativePath, physStore, blobKey)
			return nil //nolint:nilerr // deliberate: skip the cache, never fail the served download
		}
	}

	// Use context.Background so DB registration survives request context cancellation
	// after streaming (client closes connection once all bytes are received).
	regAsset, regErr := base.RegisterStoredBlob(context.Background(), d, repo, repoRelativePath, ct, coords, blobKey, sha256sum, sha1sum, md5sum, size, resolvedID, resolvedName)
	if regErr != nil {
		return regErr
	}
	// Count a download only for GET. Otherwise a HEAD probe + GET hit would
	// double-count the same pull.
	if regAsset != nil && regAsset.ID != "" && c.Request.Method == http.MethodGet && d.Downloads != nil {
		d.Downloads.Add(regAsset.ID)
	}
	return nil
}

// dropUnreferencedBlob removes a blob this request wrote but will not register,
// but only while no asset row points at that path. The blob key is shared by
// every request caching the same artifact, so an existing row means the bytes
// belong to a cache entry — either the one that was already there or one a
// concurrent fill just registered — and deleting them would leave that row
// pointing at nothing (#198). The check narrows the window to the gap between
// the lookup and the delete; it cannot close it without store-level ownership.
func dropUnreferencedBlob(ctx context.Context, d formats.Deps, repo *domain.Repository,
	repoRelativePath string, physStore storage.BlobStore, blobKey string,
) {
	if d.Assets != nil {
		existing, err := d.Assets.GetByPath(ctx, repo.Name, repoRelativePath)
		if err == nil && existing != nil {
			return
		}
	}
	_ = physStore.Delete(ctx, blobKey)
}

// storeOriginal persists an already-buffered upstream body to the blob store
// and registers the DB asset (the buffered counterpart of the streaming path
// in storeAndServeResponse, used when a rewrite forces buffering).
func storeOriginal(ctx context.Context, c *gin.Context, d formats.Deps, repo *domain.Repository,
	repoRelativePath, ct string, coords base.Coords, body []byte,
) error {
	// Quota gate (#189): over-quota metadata is served (rewritten) but not
	// cached; a check that failed to evaluate skips the cache the same way (#328).
	if qErr := base.CheckQuota(ctx, d, repo, int64(len(body))); qErr != nil {
		return nil //nolint:nilerr // deliberate: skip the cache, never fail the served response
	}

	blobKey := base.BlobKey(repo.Name, repoRelativePath)
	resolvedID, resolvedName, physStore := base.ResolveBlobStore(ctx, d, repo)
	if err := physStore.Put(ctx, blobKey, bytes.NewReader(body), int64(len(body))); err != nil {
		return fmt.Errorf("proxy cache write: %w", err)
	}
	sha256sum := fmt.Sprintf("%x", sha256.Sum256(body))
	sha1sum := fmt.Sprintf("%x", sha1.Sum(body)) //nolint:gosec // protocol checksum, not security
	md5sum := fmt.Sprintf("%x", md5.Sum(body))   //nolint:gosec // protocol checksum, not security

	// context.Background so DB registration survives request cancellation.
	regAsset, regErr := base.RegisterStoredBlob(context.Background(), d, repo, repoRelativePath, ct, coords,
		blobKey, sha256sum, sha1sum, md5sum, int64(len(body)), resolvedID, resolvedName)
	if regErr != nil {
		return regErr
	}
	if regAsset != nil && regAsset.ID != "" && c.Request.Method == http.MethodGet && d.Downloads != nil {
		d.Downloads.Add(regAsset.ID)
	}
	return nil
}

// DispatchProxyError records an upstream fetch/revalidation failure via the
// webhook bus (the package's proxy-error reporting channel), if configured.
// Exported because format handlers that talk upstream outside ServeGET — the OCI
// referrers endpoint, for one — must report a failure the same way; formats.Deps
// carries no logger, so this bus is the only operator-facing channel they have.
//
// path is the path on THIS side: the repository-relative path the client asked
// for, which for a cached artifact is also its asset path and cache key. It is
// not the upstream path — the upstream side of the request is the separate
// upstream argument, which carries the full URL actually requested. Every caller
// must pass the same side, or the payload's "path" key would mean one thing per
// caller and be unreadable to whoever consumes the webhook.
func DispatchProxyError(d formats.Deps, repoName, path, upstream string, cause error) {
	if d.Webhooks == nil {
		return
	}
	d.Webhooks.Dispatch(domain.WebhookPayload{
		Event:      domain.EventProxyError,
		Timestamp:  time.Now(),
		Repository: repoName,
		Asset: map[string]any{
			"path":     path,
			"upstream": upstream,
			"error":    cause.Error(),
		},
	})
}

// CargoIndexUpstreamPath strips the local /index/ route prefix before a
// sparse-index request leaves for the upstream registry (#347): the prefix is
// this codebase's own URL scheme, and index.crates.io's real keys are
// "se/rd/serde", not "index/se/rd/serde". Non-index paths pass through.
func CargoIndexUpstreamPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "/index/"); ok {
		return "/" + rest
	}
	return p
}

// NPMMetadataPath returns the path segment npmjs.org uses for metadata (scoped packages use %2F).
func NPMMetadataPath(pkgPath string) string {
	pkg := strings.Trim(strings.TrimPrefix(pkgPath, "/"), "/")
	if strings.HasPrefix(pkg, "@") {
		slash := strings.Index(pkg, "/")
		if slash > 0 {
			return pkg[:slash] + "%2F" + pkg[slash+1:]
		}
	}
	return pkg
}
