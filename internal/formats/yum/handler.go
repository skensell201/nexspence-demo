// Package yum implements the Yum/DNF RPM repository protocol.
//
// Standard yum repository layout under /repository/:repoName/:
//
//	GET  /repodata/repomd.xml                  → repository metadata index
//	GET  /repodata/primary.xml[.gz]            → primary packages metadata
//	GET  /repodata/filelists.xml[.gz]          → file lists
//	GET  /repodata/other.xml[.gz]              → changelog data
//	GET  /:path/*.rpm                          → download RPM
//	PUT  /:path/*.rpm                          → upload RPM
//	DELETE /:path/*.rpm                        → delete RPM
package yum

import (
	"encoding/xml"
	"fmt"
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

// Handler serves the Yum/DNF RPM repository protocol.
type Handler struct{ deps formats.Deps }

// New creates a Yum format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "yum" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block uploads/deletes, pass reads through to upstream (e.g. dl.fedoraproject.org)
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		ct := "application/octet-stream"
		if strings.HasSuffix(p, ".xml") {
			ct = "application/xml"
		}
		// repodata/ holds mutable indexes (repomd.xml + referenced metadata);
		// everything else is an immutable .rpm addressed by name-version.
		var maxAge time.Duration
		if strings.Contains(p, "/repodata/") {
			maxAge = repoproxy.MetadataMaxAge(repo)
		}
		coords := proxyCoords(p)
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, ct, maxAge); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	switch {
	// repomd.xml
	case c.Request.Method == http.MethodGet && p == "/repodata/repomd.xml":
		h.serveRepomd(c, repoName)

	// primary.xml (or .gz)
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/repodata/primary"):
		h.servePrimary(c, repoName, p)

	// filelists.xml, other.xml — return empty valid XML for now
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/repodata/"):
		h.serveOtherMetadata(c, repoName, p)

	// Upload RPM
	case c.Request.Method == http.MethodPut && strings.HasSuffix(p, ".rpm"):
		h.handleUpload(c, repoName, p)

	// Delete RPM
	case c.Request.Method == http.MethodDelete && strings.HasSuffix(p, ".rpm"):
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)

	// Download RPM or HEAD
	case (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && strings.HasSuffix(p, ".rpm"):
		h.serveFile(c, repoName, p)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// repomdXML is the minimal repomd.xml structure
type repomdXML struct {
	XMLName  xml.Name      `xml:"repomd"`
	XMLNS    string        `xml:"xmlns,attr"`
	Revision int64         `xml:"revision"`
	Data     []repomdEntry `xml:"data"`
}
type repomdEntry struct {
	Type         string       `xml:"type,attr"`
	Location     repomdLoc    `xml:"location"`
	Checksum     repomdCksum  `xml:"checksum"`
	OpenChecksum *repomdCksum `xml:"open-checksum,omitempty"`
	Timestamp    int64        `xml:"timestamp"`
	Size         int64        `xml:"size,omitempty"`
	OpenSize     int64        `xml:"open-size,omitempty"`
}
type repomdLoc struct {
	Href string `xml:"href,attr"`
}
type repomdCksum struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

func (h *Handler) serveRepomd(c *gin.Context, repoName string) {
	docs, err := h.buildRepodata(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/xml; charset=utf-8", docs.Repomd)
}

// primaryXML is the minimal primary.xml structure
type primaryXML struct {
	XMLName  xml.Name     `xml:"metadata"`
	XMLNS    string       `xml:"xmlns,attr"`
	Count    int          `xml:"packages,attr"`
	Packages []rpmPackage `xml:"package"`
}
type rpmPackage struct {
	Type     string      `xml:"type,attr"`
	Name     string      `xml:"name"`
	Arch     string      `xml:"arch"`
	Version  rpmVersion  `xml:"version"`
	Checksum rpmChecksum `xml:"checksum"`
	Size     rpmSize     `xml:"size"`
	Location rpmLoc      `xml:"location"`
}
type rpmVersion struct {
	Epoch string `xml:"epoch,attr"`
	Ver   string `xml:"ver,attr"`
	Rel   string `xml:"rel,attr"`
}
type rpmSize struct {
	Package int64 `xml:"package,attr"`
}
type rpmLoc struct {
	Href string `xml:"href,attr"`
}

func (h *Handler) servePrimary(c *gin.Context, repoName, p string) {
	docs, err := h.buildRepodata(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if strings.HasSuffix(p, ".gz") {
		c.Data(http.StatusOK, "application/x-gzip", docs.PrimaryGz)
		return
	}
	c.Data(http.StatusOK, "application/xml; charset=utf-8", docs.Primary)
}

// serveOtherMetadata serves filelists/other (and unknown repodata paths as
// valid empty metadata) from the same snapshot repomd was built from.
func (h *Handler) serveOtherMetadata(c *gin.Context, repoName, p string) {
	docs, err := h.buildRepodata(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	gzipped := strings.HasSuffix(p, ".gz")
	var plain, gz []byte
	switch {
	case strings.HasPrefix(p, "/repodata/filelists"):
		plain, gz = docs.Filelists, docs.FilelistsGz
	case strings.HasPrefix(p, "/repodata/other"):
		plain, gz = docs.Other, docs.OtherGz
	default:
		plain = []byte(xml.Header + `<metadata xmlns="http://linux.duke.edu/metadata/common" packages="0"/>`)
		gz = gzipBytes(plain)
	}
	if gzipped {
		c.Data(http.StatusOK, "application/x-gzip", gz)
		return
	}
	c.Data(http.StatusOK, "application/xml; charset=utf-8", plain)
}

// rpmCoords parses NVR (name-version-release) coordinates from an RPM
// filename: name-version-release.arch.rpm — the last two dash segments are
// version and release ("curl-8.0.0-1.x86_64.rpm" → curl / 8.0.0).
func rpmCoords(filename string) base.Coords {
	name := strings.TrimSuffix(filename, ".rpm")
	if dot := strings.LastIndex(name, "."); dot > 0 { // strip .arch
		name = name[:dot]
	}
	parts := strings.Split(name, "-")
	pkgName, version := name, "0"
	if len(parts) >= 3 {
		pkgName = strings.Join(parts[:len(parts)-2], "-")
		version = parts[len(parts)-2]
	} else if len(parts) == 2 {
		pkgName, version = parts[0], parts[1]
	}
	return base.Coords{Name: pkgName, Version: version}
}

// proxyCoords derives component coordinates for a proxied path. A cached RPM
// must carry its real name and version — the OSV/Trivy scan queries by them,
// so the path-derived fallback name and placeholder version "1" made every
// RPM pulled through a Yum proxy invisible to vulnerability scanning, the
// same root cause #336 closed for Cargo. repodata/ holds mutable index files,
// not versioned packages, and keeps the generic fallback.
func proxyCoords(p string) base.Coords {
	if strings.HasSuffix(p, ".rpm") {
		return rpmCoords(path.Base(p))
	}
	return base.Coords{}
}

func (h *Handler) handleUpload(c *gin.Context, repoName, p string) {
	coords := rpmCoords(path.Base(p))
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, p, "application/x-rpm",
		coords, c.Request.Body, c.Request.ContentLength); err != nil {
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
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/x-rpm", rc, nil)
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
