// Package cran implements the CRAN (Comprehensive R Archive Network)
// repository protocol for R source packages.
//
// Layout under /repository/:repoName/:
//
//	GET    /src/contrib/PACKAGES[.gz]        → packages index
//	GET    /src/contrib/:pkg_version.tar.gz  → package download
//	PUT    /src/contrib/:pkg_version.tar.gz  → upload package
//	POST   /src/contrib/:pkg_version.tar.gz  → upload package (Nexus-compatible verb)
//	DELETE /src/contrib/:pkg_version.tar.gz  → delete package
//
// Binary packages (Windows .zip, macOS .tgz under bin/windows|macosx/contrib/)
// are out of scope for this first version.
package cran

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// Handler serves the CRAN repository protocol.
type Handler struct{ deps formats.Deps }

// New creates a CRAN format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "cran" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block uploads/deletes, pass reads through to upstream (e.g. cran.r-project.org)
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		ct := "application/octet-stream"
		if strings.Contains(p, "/PACKAGES") {
			ct = "text/plain"
		}
		// Package tarballs are immutable (name+version is permanent by CRAN
		// convention, like npm); PACKAGES/PACKAGES.gz are mutable metadata that
		// change whenever the upstream repo gains a new package, so they're
		// revalidated on a TTL.
		var maxAge time.Duration
		if !strings.HasSuffix(p, ".tar.gz") {
			maxAge = repoproxy.MetadataMaxAge(repo)
		}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", proxyCoords(p), ct, maxAge); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	h.serveHosted(c, repoName, p)
}

// serveHosted routes the hosted (non-proxy) surface of the CRAN protocol.
func (h *Handler) serveHosted(c *gin.Context, repoName, p string) {
	switch {
	// Packages index (plain or gzip)
	case c.Request.Method == http.MethodGet && (strings.HasSuffix(p, "/PACKAGES") || strings.HasSuffix(p, "/PACKAGES.gz")):
		h.servePackagesIndex(c, repoName, p)

	// Download package tarball
	case (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && strings.HasSuffix(p, ".tar.gz"):
		h.serveFile(c, repoName, p)

	// Upload: PUT/POST to a .tar.gz path, or a root-level upload (R clients and
	// `curl --upload-file pkg.tar.gz .../repository/<repo>/` upload to the
	// repository root rather than an explicit src/contrib path). POST also
	// accepts multipart/form-data with a "file"/"package" field (Nexus-compatible).
	case (c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPost) &&
		(p == "/" || strings.HasSuffix(p, ".tar.gz")):
		h.handleUpload(c, repoName, p)

	// Delete package tarball
	case c.Request.Method == http.MethodDelete && strings.HasSuffix(p, ".tar.gz"):
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)

	// A GET this repository doesn't keep an index or tarball for (e.g.
	// PACKAGES.rds, which nexspence never generates) is a missing resource, not
	// a bad method — and a group of hosted members fanning out on it can only
	// skip to the next member on 404, never on 405.
	case c.Request.Method == http.MethodGet:
		c.Status(http.StatusNotFound)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// buildPackagesIndex generates the PACKAGES index from every stored .tar.gz.
//
// DESCRIPTION is not parsed out of the tarball in this first version —
// Depends/Imports/License are left generic rather than fabricated, since a
// wrong dependency line is worse than an absent one.
func (h *Handler) buildPackagesIndex(ctx context.Context, repoName string) ([]byte, error) {
	page, err := h.deps.Components.Search(ctx, domain.SearchParams{
		Repository: repoName, Limit: 1000,
	})
	if err != nil {
		return nil, err
	}
	assetPage, err := h.deps.Assets.List(ctx, repoName, 1000, 0)
	if err != nil {
		return nil, err
	}
	compMap := map[string]*domain.Component{}
	for i := range page.Items {
		compMap[page.Items[i].ID] = &page.Items[i]
	}

	var sb strings.Builder
	for _, a := range assetPage.Items {
		if !strings.HasSuffix(a.Path, ".tar.gz") {
			continue
		}
		comp := compMap[a.ComponentID]
		if comp == nil {
			continue
		}
		fmt.Fprintf(&sb, "Package: %s\n", comp.Name)
		fmt.Fprintf(&sb, "Version: %s\n", comp.Version)
		sb.WriteString("\n")
	}
	return []byte(sb.String()), nil
}

func (h *Handler) servePackagesIndex(c *gin.Context, repoName, p string) {
	data, err := h.buildPackagesIndex(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if strings.HasSuffix(p, ".gz") {
		c.Data(http.StatusOK, "application/x-gzip", gzipBytes(data))
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
}

func gzipBytes(plain []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write(plain)
	_ = gw.Close()
	return buf.Bytes()
}

// cranCoords parses the CRAN "name_version.tar.gz" filename convention. R
// package names never contain underscores (they use dots), so the first
// underscore unambiguously separates name from version — unlike Debian's
// "name_version_arch.deb", there's no third field to account for.
func cranCoords(filename string) (pkgName, version string) {
	stem := strings.TrimSuffix(filename, ".tar.gz")
	if idx := strings.Index(stem, "_"); idx > 0 {
		return stem[:idx], stem[idx+1:]
	}
	return filename, "0.0.0"
}

// proxyCoords derives component coordinates for a file cached from upstream.
// Packages get real name/version coordinates so they browse like hosted ones;
// the PACKAGES index is keyed by its path, since it's a single document
// rather than a version of a package.
func proxyCoords(p string) base.Coords {
	if strings.HasSuffix(p, ".tar.gz") {
		name, version := cranCoords(path.Base(p))
		return base.Coords{Name: name, Version: version}
	}
	return base.Coords{Name: strings.TrimPrefix(p, "/"), Version: "metadata"}
}

func (h *Handler) handleUpload(c *gin.Context, repoName, p string) {
	filename := path.Base(p)
	body := io.Reader(c.Request.Body)
	size := c.Request.ContentLength

	// Nexus-style root POST: the tarball arrives as a multipart file field
	// ("file" or "package") and the filename comes from the part, not the path.
	if !strings.HasSuffix(filename, ".tar.gz") {
		f, fh, err := c.Request.FormFile("file")
		if err != nil {
			f, fh, err = c.Request.FormFile("package")
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected a *.tar.gz path or a multipart 'file' field"})
			return
		}
		defer func() { _ = f.Close() }()
		filename, body, size = fh.Filename, f, fh.Size
	}

	pkgName, version := cranCoords(filename)

	// Normalize root-level uploads into the canonical src/contrib layout so the
	// PACKAGES index (which lists /src/contrib/ assets) still finds them.
	storePath := p
	if !strings.HasPrefix(storePath, "/src/contrib/") {
		storePath = "/src/contrib/" + filename
	}

	coords := base.Coords{Name: pkgName, Version: version}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, storePath, "application/x-gzip",
		coords, body, size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func (h *Handler) serveFile(c *gin.Context, repoName, p string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, p)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		c.Status(http.StatusOK)
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
