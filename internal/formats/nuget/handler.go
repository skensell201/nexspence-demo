// Package nuget implements the NuGet v2/v3 repository protocol.
//
// NuGet v3 endpoints (under /repository/:repoName/):
//
//	GET  /index.json                        → service index (v3)
//	GET  /v3/registration/:id/index.json    → package registration (metadata)
//	GET  /v3/flatcontainer/:id/index.json   → version list
//	GET  /v3/flatcontainer/:id/:ver/:id.:ver.nupkg → download
//
// NuGet v2 endpoints:
//
//	GET  /FindPackagesById()?id='name'      → OData XML
//	PUT  /v2/package                        → nuget push (multipart)
//	DELETE /v2/packages/:id/:ver            → delete
package nuget

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// Handler serves the NuGet v2/v3 repository protocol.
type Handler struct{ deps formats.Deps }

// New creates a NuGet format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "nuget" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block mutations; rewrite service index; cache packages.
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && p == "/index.json" {
			h.fetchAndRewriteNuGetIndex(c, repo)
			return
		}
		// .nupkg package content is immutable; registration/flat-container index
		// pages are mutable metadata (new versions appear) and revalidate on a TTL.
		var maxAge time.Duration
		if !strings.HasSuffix(p, ".nupkg") {
			maxAge = repoproxy.MetadataMaxAge(repo)
		}
		coords := proxyCoords(p)
		// Resource paths advertised by the rewritten index are the upstream's
		// own paths re-rooted locally — forward them onto the bare origin,
		// not onto a possibly /v3-suffixed remote_url (#349).
		upstreamPath := ""
		if origin := nugetRemoteOrigin(remoteURLOf(repo)); origin != "" {
			upstreamPath = origin + p
		}
		// Registration pages embed absolute upstream URLs (packageContent,
		// @id) — rewrite them on serve so clients pull packages through this
		// proxy (#98); the cache keeps the upstream original.
		var rewrite func([]byte) []byte
		if strings.HasPrefix(p, "/v3/registration/") {
			localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repo.Name
			rewrite = func(b []byte) []byte { return RewriteRegistration(b, localBase) }
		}
		if err := repoproxy.ServeGETRewritten(c, h.deps, repo, p, upstreamPath, coords, "application/octet-stream", maxAge, rewrite); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	switch {
	// v3 service index
	case c.Request.Method == http.MethodGet && p == "/index.json":
		h.serveIndex(c, repoName)

	// v3 flat container: version list
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/v3/flatcontainer/") && strings.HasSuffix(p, "/index.json"):
		pkgID := strings.TrimSuffix(strings.TrimPrefix(p, "/v3/flatcontainer/"), "/index.json")
		pkgID = strings.Trim(pkgID, "/")
		h.serveVersionList(c, repoName, pkgID)

	// v3 flat container: download nupkg
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/v3/flatcontainer/") && strings.HasSuffix(p, ".nupkg"):
		h.serveFlatContainerDownload(c, repoName, p)

	// v3 registration index
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/v3/registration/"):
		h.serveRegistration(c, repoName, p)

	// v2 OData query: FindPackagesById()
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/FindPackagesById"):
		pkgID := c.Query("id")
		pkgID = strings.Trim(pkgID, "'")
		h.serveFindPackages(c, repoName, pkgID)

	// v2 push
	case c.Request.Method == http.MethodPut && p == "/v2/package":
		h.handlePush(c, repoName)

	// v2 delete: DELETE /v2/packages/:id/:ver
	case c.Request.Method == http.MethodDelete && strings.HasPrefix(p, "/v2/packages/"):
		rest := strings.TrimPrefix(p, "/v2/packages/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v2/packages/:id/:version"})
			return
		}
		filePath := "/" + parts[0] + "/" + parts[1] + "/" + parts[0] + "." + parts[1] + ".nupkg"
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, filePath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) serveIndex(c *gin.Context, repoName string) {
	base2 := h.deps.BaseURL + "/repository/" + repoName
	c.JSON(http.StatusOK, gin.H{
		"version": "3.0.0",
		"resources": []gin.H{
			{"@id": base2 + "/v3/flatcontainer/", "@type": "PackageBaseAddress/3.0.0"},
			{"@id": base2 + "/v3/registration/", "@type": "RegistrationsBaseUrl/3.0.0"},
			{"@id": base2 + "/v2/package", "@type": "PackagePublish/2.0.0"},
			{"@id": base2 + "/v2/", "@type": "LegacyGallery/2.0.0"},
		},
	})
}

func (h *Handler) serveVersionList(c *gin.Context, repoName, pkgID string) {
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: strings.ToLower(pkgID), Limit: 200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	versions := make([]string, 0, len(page.Items))
	for _, comp := range page.Items {
		versions = append(versions, comp.Version)
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

// proxyCoords derives component coordinates for a proxied path. A cached
// package must carry its real name and version — the OSV/Trivy scan queries
// by them, so the path-derived fallback name and placeholder version made
// every package pulled through a NuGet proxy invisible to vulnerability
// scanning, the same root cause #336 closed for Cargo.
//
// A proxy repo forwards whatever local path the client requested straight
// onto upstream (upstreamPath := origin + p, above) — unlike a hosted repo,
// it never goes through this file's own "/v3/flatcontainer/" switch-case
// routes. A real client (nuget.exe, dotnet) requests packages at whatever
// address the upstream's own index.json/config.json advertised, which for
// nuget.org and most feeds is "/v3-flatcontainer/" (hyphenated, a sibling of
// "/v3/", not nested inside it — see TestNuGet_ProxyFlatcontainer_
// ResolvesAgainstRealShape). Matching on a specific prefix would silently
// miss that real shape, so this matches by suffix instead: any ".nupkg" path
// is exactly ":id/:ver/:id.:ver.nupkg" — the same 3-segment split
// serveFlatContainerDownload already does for hosted downloads, applied to
// the path's last 3 segments regardless of what comes before them.
// Registration/index pages are versionless metadata and keep the generic
// fallback.
func proxyCoords(p string) base.Coords {
	if !strings.HasSuffix(p, ".nupkg") {
		return base.Coords{}
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 3 {
		return base.Coords{}
	}
	id, ver := parts[len(parts)-3], parts[len(parts)-2]
	if id == "" || ver == "" {
		return base.Coords{}
	}
	return base.Coords{Name: strings.ToLower(id), Version: ver}
}

func (h *Handler) serveFlatContainerDownload(c *gin.Context, repoName, p string) {
	// /v3/flatcontainer/:id/:ver/:id.:ver.nupkg
	parts := strings.Split(strings.TrimPrefix(p, "/v3/flatcontainer/"), "/")
	if len(parts) < 3 {
		c.JSON(http.StatusNotFound, gin.H{"error": "invalid nupkg path"})
		return
	}
	pkgID, version := parts[0], parts[1]
	filePath := "/" + pkgID + "/" + version + "/" + pkgID + "." + version + ".nupkg"

	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/zip", rc, nil)
}

func (h *Handler) serveRegistration(c *gin.Context, repoName, p string) {
	// /v3/registration/:id/index.json
	rest := strings.TrimPrefix(p, "/v3/registration/")
	pkgID := strings.TrimSuffix(rest, "/index.json")
	pkgID = strings.Trim(pkgID, "/")

	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: strings.ToLower(pkgID), Limit: 200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(page.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "package not found"})
		return
	}

	base2 := h.deps.BaseURL + "/repository/" + repoName
	items := make([]gin.H, 0, len(page.Items))
	for _, comp := range page.Items {
		entryURL := base2 + "/v3/registration/" + pkgID + "/" + comp.Version + ".json"
		items = append(items, gin.H{
			"@id":            entryURL,
			"packageContent": base2 + "/v3/flatcontainer/" + pkgID + "/" + comp.Version + "/" + pkgID + "." + comp.Version + ".nupkg",
			"catalogEntry": gin.H{
				"id":        comp.Name,
				"version":   comp.Version,
				"listed":    true,
				"published": comp.CreatedAt.UTC().Format(time.RFC3339),
			},
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"@id":   base2 + "/v3/registration/" + pkgID + "/index.json",
		"count": 1,
		"items": []gin.H{{
			"count": len(items),
			"items": items,
			"lower": page.Items[0].Version,
			"upper": page.Items[len(page.Items)-1].Version,
		}},
	})
}

// OData v2 compatible FindPackagesById response
type feed struct {
	XMLName xml.Name `xml:"feed"`
	XMLNS   string   `xml:"xmlns,attr"`
	Entries []entry  `xml:"entry"`
}
type entry struct {
	XMLName xml.Name `xml:"entry"`
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Content content  `xml:"content"`
}
type content struct {
	Type string `xml:"type,attr"`
	Src  string `xml:"src,attr"`
}

func (h *Handler) serveFindPackages(c *gin.Context, repoName, pkgID string) {
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: strings.ToLower(pkgID), Limit: 200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	base2 := h.deps.BaseURL + "/repository/" + repoName
	f := feed{XMLNS: "http://www.w3.org/2005/Atom"}
	for _, comp := range page.Items {
		f.Entries = append(f.Entries, entry{
			Title: comp.Name + " " + comp.Version,
			ID:    base2 + "/v2/Packages(Id='" + comp.Name + "',Version='" + comp.Version + "')",
			Content: content{
				Type: "application/zip",
				Src:  base2 + "/v3/flatcontainer/" + strings.ToLower(comp.Name) + "/" + comp.Version + "/" + strings.ToLower(comp.Name) + "." + comp.Version + ".nupkg",
			},
		})
	}
	c.Header("Content-Type", "application/atom+xml; charset=utf-8")
	c.XML(http.StatusOK, f)
}

func (h *Handler) handlePush(c *gin.Context, repoName string) {
	if err := c.Request.ParseMultipartForm(64 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	f, fh, err := c.Request.FormFile("package")
	if err != nil {
		// some clients use "file" as field name
		f, fh, err = c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing package file"})
			return
		}
	}
	defer func() { _ = f.Close() }()

	pkgID, version := nupkgCoords(f, fh.Size, fh.Filename)
	filePath := "/" + pkgID + "/" + version + "/" + pkgID + "." + version + ".nupkg"

	coords := base.Coords{Name: pkgID, Version: version}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, filePath, "application/zip", coords, f, fh.Size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

// nuspecMeta is the subset of the .nuspec manifest needed for coordinates.
type nuspecMeta struct {
	Metadata struct {
		ID      string `xml:"id"`
		Version string `xml:"version"`
	} `xml:"metadata"`
}

// nupkgCoords resolves the package id and version for an uploaded .nupkg.
// The .nuspec inside the archive is authoritative; the filename is a fallback
// (version = the trailing dot-separated parts starting at the first digit-led
// part, so "Newtonsoft.Json.13.0.1" → id "newtonsoft.json", version "13.0.1"
// — a naive last-dot split corrupts real semver coordinates, #100).
func nupkgCoords(f io.ReaderAt, size int64, filename string) (string, string) {
	if zr, err := zip.NewReader(f, size); err == nil {
		for _, zf := range zr.File {
			if !strings.HasSuffix(zf.Name, ".nuspec") || strings.Contains(zf.Name, "/") {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				continue
			}
			var meta nuspecMeta
			err = xml.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&meta)
			_ = rc.Close()
			if err == nil && meta.Metadata.ID != "" && meta.Metadata.Version != "" {
				return strings.ToLower(meta.Metadata.ID), meta.Metadata.Version
			}
		}
	}
	name := strings.TrimSuffix(filename, ".nupkg")
	parts := strings.Split(name, ".")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" && parts[i][0] >= '0' && parts[i][0] <= '9' {
			return strings.ToLower(strings.Join(parts[:i], ".")), strings.Join(parts[i:], ".")
		}
	}
	return strings.ToLower(name), "0.0.0"
}

// fetchAndRewriteNuGetIndex fetches the NuGet v3 service index from upstream,
// rewrites all resource @id URLs to point to this proxy, and returns the result.
// Not cached — fetched live so new resource endpoints appear promptly.
func (h *Handler) fetchAndRewriteNuGetIndex(c *gin.Context, repo *domain.Repository) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// The v3 service index is the ONE fixed path in an otherwise fully
	// discoverable protocol, and it lives at /v3/index.json on the real
	// nuget.org (#349). remote_url is the bare origin; a legacy /v3-suffixed
	// value is normalized so it neither breaks discovery nor doubles itself
	// onto the resource paths the index advertises.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nugetRemoteOrigin(remoteBase)+"/v3/index.json", nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upstream URL: " + err.Error()})
		return
	}
	req.Header.Set("Accept", "application/json")

	repoproxy.SetUpstreamAuth(req, repo)
	resp, err := repoproxy.ClientFor(repo).Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed: " + err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("upstream returned %d", resp.StatusCode)})
		return
	}

	var index map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid upstream index.json: " + err.Error()})
		return
	}

	// Rewrite each resource's @id to point through this proxy.
	// Parse the upstream @id URL, keep only its path, prepend our local base.
	localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repo.Name

	if resources, ok := index["resources"].([]any); ok {
		for _, r := range resources {
			res, ok := r.(map[string]any)
			if !ok {
				continue
			}
			id, ok := res["@id"].(string)
			if !ok {
				continue
			}
			parsed, err := url.Parse(id)
			if err != nil {
				continue
			}
			res["@id"] = localBase + parsed.RequestURI()
		}
	}

	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		return
	}
	c.JSON(http.StatusOK, index)
}

// nugetRemoteOrigin normalizes remote_url to the registry's bare origin: a
// legacy configuration carried a /v3 suffix (once the only way the index fetch
// worked), which would double itself onto every already-correct resource path.
func nugetRemoteOrigin(remoteBase string) string {
	return strings.TrimSuffix(strings.TrimRight(remoteBase, "/"), "/v3")
}

// remoteURLOf reads the repository's remote_url, empty when unset.
func remoteURLOf(repo *domain.Repository) string {
	base, err := repoproxy.RemoteURL(repo)
	if err != nil {
		return ""
	}
	return base
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
