package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// BrowseHandler serves Nexspence-native browse APIs.
type BrowseHandler struct {
	deps formats.Deps
	rbac *service.RBACService
}

// NewBrowseHandler constructs a BrowseHandler over the same formats.Deps the
// format handlers are built from. Deleting through the browse API removes the
// same artifacts the registry API removes, so it has to see storage the same
// way: the blob store registry that resolves an asset's own store, and the
// webhook bus that reports a deletion.
func NewBrowseHandler(deps formats.Deps, rbac *service.RBACService) *BrowseHandler {
	return &BrowseHandler{deps: deps, rbac: rbac}
}

// dockerBrowseNode is a Nexus-style folder or leaf in the Docker browse tree.
type dockerBrowseNode struct {
	Kind        string `json:"kind"` // folder | tag | manifest | blob
	Label       string `json:"label"`
	Path        string `json:"path"`
	ImageRef    string `json:"imageRef,omitempty"`
	Version     string `json:"version,omitempty"`
	ComponentID string `json:"componentId,omitempty"`
	// ArtifactType names what the manifest holds — "chart", "image", "wasm" — or
	// repeats the raw media type when it is not one this registry recognizes. It
	// is omitted entirely for a component that carries no OCI metadata.
	ArtifactType string              `json:"artifactType,omitempty"`
	Children     []*dockerBrowseNode `json:"children,omitempty"`
}

// DockerTree handles GET /api/v1/browse/repositories/:name/docker-tree
func (h *BrowseHandler) DockerTree(c *gin.Context) {
	repoName := c.Param("name")
	ctx := c.Request.Context()

	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	if !repo.Format.IsOCIRegistry() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not an OCI registry format"})
		return
	}

	repoNames := []string{repoName}
	if repo.Type == domain.TypeGroup {
		repoNames = domain.GroupMemberNames(repo)
		if len(repoNames) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"repository": repoName,
				"format":     string(repo.Format),
				"root":       &dockerBrowseNode{Kind: "folder", Label: "/", Path: "/"},
			})
			return
		}
	}

	rows, err := h.deps.Components.ListDockerBrowseRows(ctx, repoNames, 3000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	rows = h.rbac.FilterDockerRows(ctx, stringVal(userID), stringSliceVal(roles),
		repoName, repo.AllowAnonymous, rows)

	root := &dockerBrowseNode{Kind: "folder", Label: "/", Path: "/"}
	for _, row := range rows {
		insertDockerBrowseRow(root, row)
	}
	sortBrowseChildren(root)

	c.JSON(http.StatusOK, gin.H{
		"repository": repoName,
		"format":     string(repo.Format),
		"root":       root,
	})
}

// PathTree handles GET /api/v1/browse/repositories/:name/path-tree
// Returns unique directory-level path prefixes from assets in the repository.
// Optional query param: q (substring filter, case-insensitive).
// For Docker repos the /blobs/ and /manifests/ storage prefixes are stripped so
// callers see image-namespace paths (e.g. /da/bas/python/) that match the
// dockerpath used by RBACMiddleware — suitable as content-selector path prefixes.
func (h *BrowseHandler) PathTree(c *gin.Context) {
	repoName := c.Param("name")
	q := c.Query("q")
	ctx := c.Request.Context()

	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}

	var paths []string
	if repo.Format.IsOCIRegistry() {
		raw, err := h.deps.Assets.ListRawAssetPaths(ctx, repoName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		paths = dockerImageDirs(raw, q)
	} else {
		var err error
		paths, err = h.deps.Assets.ListPathsByRepo(ctx, repoName, q)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if paths == nil {
		paths = []string{}
	}

	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	paths = h.rbac.FilterPaths(ctx, stringVal(userID), stringSliceVal(roles),
		repoName, repo.AllowAnonymous, paths)

	c.JSON(http.StatusOK, gin.H{"paths": paths})
}

// dockerImageDirs extracts unique image-namespace directory paths from raw Docker
// asset paths (/blobs/da/bas/python/sha256:… , /manifests/da/bas/python/latest).
//
// Result: /da/, /da/bas/, /da/bas/python/ — all ancestor levels for every image.
// Selecting /da/bas/ in the content-selector picker produces
// path.startsWith("/da/bas/") which matches ALL dockerpath requests for images
// under that namespace (blobs, manifests, tags/list).
func dockerImageDirs(rawAssetPaths []string, q string) []string {
	seen := make(map[string]struct{})

	for _, raw := range rawAssetPaths {
		var rest string
		switch {
		case strings.HasPrefix(raw, "/blobs/"):
			rest = strings.TrimPrefix(raw, "/blobs/")
		case strings.HasPrefix(raw, "/manifests/"):
			rest = strings.TrimPrefix(raw, "/manifests/")
		default:
			continue
		}
		// rest = "da/bas/python/sha256:abc" — strip the last segment to get image name.
		slashIdx := strings.LastIndex(rest, "/")
		if slashIdx < 0 {
			continue
		}
		imageName := rest[:slashIdx] // "da/bas/python"
		if imageName == "" {
			continue
		}

		// Add /da/, /da/bas/, /da/bas/python/ — build incrementally, no double slashes.
		segs := strings.Split(imageName, "/")
		cur := ""
		for _, seg := range segs {
			if seg == "" {
				continue
			}
			cur += seg + "/" // e.g. "da/" → "da/bas/" → "da/bas/python/"
			p := "/" + cur   // e.g. "/da/" → "/da/bas/" → "/da/bas/python/"
			if q == "" || strings.Contains(strings.ToLower(p), strings.ToLower(q)) {
				seen[p] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dockerBrowseCategory(version, samplePath string) string {
	p := samplePath
	if strings.Contains(p, "/blobs/") {
		return "Blobs"
	}
	if strings.Contains(p, "/manifests/") {
		if strings.HasPrefix(version, "sha256:") || strings.HasPrefix(version, "sha512:") {
			return "Manifests"
		}
		return "Tags"
	}
	if strings.HasPrefix(version, "sha256:") || strings.HasPrefix(version, "sha512:") {
		return "Manifests"
	}
	return "Tags"
}

func browseJoin(base, seg string) string {
	if base == "" || base == "/" {
		return "/" + seg
	}
	return strings.TrimRight(base, "/") + "/" + seg
}

func insertDockerBrowseRow(root *dockerBrowseNode, row domain.DockerBrowseRow) {
	image := strings.Trim(row.ImageName, "/")
	if image == "" {
		return
	}
	parts := strings.Split(image, "/")
	cur := root
	curPath := "/"
	for _, seg := range parts {
		if seg == "" {
			continue
		}
		curPath = browseJoin(curPath, seg)
		cur = cur.getOrCreateFolder(seg, curPath)
	}

	cat := dockerBrowseCategory(row.Version, row.SamplePath)
	catPath := browseJoin(cur.Path, cat)
	catNode := cur.getOrCreateFolder(cat, catPath)

	leafKind := "tag"
	switch cat {
	case "Manifests":
		leafKind = "manifest"
	case "Blobs":
		leafKind = "blob"
	}
	leafPath := browseJoin(catNode.Path, row.Version)
	for _, ex := range catNode.Children {
		if ex.Path == leafPath {
			return
		}
	}
	leaf := &dockerBrowseNode{
		Kind:         leafKind,
		Label:        row.Version,
		Path:         leafPath,
		ImageRef:     image,
		Version:      row.Version,
		ComponentID:  row.ComponentID,
		ArtifactType: ociArtifactLabel(row.ArtifactType, row.PredicateType),
	}
	catNode.Children = append(catNode.Children, leaf)
}

func (n *dockerBrowseNode) getOrCreateFolder(label, nodePath string) *dockerBrowseNode {
	for _, ch := range n.Children {
		if ch.Kind == "folder" && ch.Label == label {
			return ch
		}
	}
	ch := &dockerBrowseNode{Kind: "folder", Label: label, Path: nodePath, Children: []*dockerBrowseNode{}}
	n.Children = append(n.Children, ch)
	return ch
}

func sortBrowseChildren(n *dockerBrowseNode) {
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Kind != b.Kind {
			return a.Kind == "folder" && b.Kind != "folder"
		}
		return strings.ToLower(a.Label) < strings.ToLower(b.Label)
	})
	for _, ch := range n.Children {
		sortBrowseChildren(ch)
	}
}

// assetStore returns the physical blob store that actually holds an asset's
// bytes. A repository can be pinned to a blob store of its own, and reading or
// deleting through the default store then addresses a store the asset was never
// written to: the delete silently misses and the read comes back empty.
func (h *BrowseHandler) assetStore(ctx context.Context, asset *domain.Asset) storage.BlobStore {
	if asset != nil && asset.BlobStoreID != "" {
		if bsMeta, err := h.deps.Blobs.GetByID(ctx, asset.BlobStoreID); err == nil {
			return base.PhysicalStore(ctx, h.deps, bsMeta)
		}
	}
	return h.deps.BlobStore
}

// authorizeDelete loads repoName and checks RBAC "delete" permission on path,
// denying the same way RBACMiddleware does (401 unauthenticated / 403
// authenticated-but-forbidden). The browse delete endpoints bypass
// RBACMiddleware entirely (they sit in the authed-only group), so each one
// must call this itself before acting. Returns false when the caller must
// stop — the response has already been written.
func (h *BrowseHandler) authorizeDelete(c *gin.Context, repoName, path string) bool {
	ctx := c.Request.Context()
	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return false
	}
	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	userIDStr, _ := userID.(string)
	rolesSlice, _ := roles.([]string)
	ok, err := h.rbac.CanAccessRepo(ctx, userIDStr, rolesSlice, repo, path, "delete")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "access check failed"})
		return false
	}
	if !ok {
		denyAccess(c, userIDStr, repoName)
		return false
	}
	return true
}

// DeleteByPath handles DELETE /api/v1/browse/repositories/:name/path
// Query param: path=<prefix> (required). Deletes all assets whose path starts with
// the prefix, then removes orphan components.
func (h *BrowseHandler) DeleteByPath(c *gin.Context) {
	repoName := c.Param("name")
	pathPrefix := c.Query("path")
	if pathPrefix == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path query param required"})
		return
	}
	if !h.authorizeDelete(c, repoName, pathPrefix) {
		return
	}

	ctx := c.Request.Context()
	assets, err := h.deps.Assets.ListByRepoAndPath(ctx, repoName, pathPrefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, a := range assets {
		if err := base.DeleteArtifact(ctx, h.deps, repoName, a.Path); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := h.deps.Components.DeleteOrphans(ctx, repoName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteDockerTag handles DELETE /api/v1/browse/repositories/:name/docker-tag
// Query params: image=da/rbi/python&ref=3.15-rc-alpine3.22
// Deletes the tag manifest, its digest alias, and blobs not referenced by any remaining tag.
func (h *BrowseHandler) DeleteDockerTag(c *gin.Context) {
	repoName := c.Param("name")
	imageName := c.Query("image")
	ref := c.Query("ref")
	if imageName == "" || ref == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image and ref query params required"})
		return
	}

	// 1. Load the tag manifest asset and read its content.
	tagPath := "/manifests/" + imageName + "/" + ref
	if !h.authorizeDelete(c, repoName, tagPath) {
		return
	}

	ctx := c.Request.Context()
	tagAsset, err := h.deps.Assets.GetByPath(ctx, repoName, tagPath)
	if err != nil || tagAsset == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "manifest not found"})
		return
	}

	// Read through the store that holds this asset: a repository on its own blob
	// store keeps its manifests there, and reading the default store would return
	// nothing — leaving deletedDigests empty and the layers below never swept.
	deletedDigests := parseManifestDigests(h.assetStore(ctx, tagAsset).Get(ctx, tagAsset.BlobKey))

	// 2. Delete the tag manifest record and its digest alias — two records of one
	// manifest on one blob. DeleteArtifact keeps a blob alive while another asset
	// still references it, so the shared blob goes with whichever record is
	// deleted last and the order of these two no longer matters.
	if err := base.DeleteArtifact(ctx, h.deps, repoName, tagPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	digestAliasPath := "/manifests/" + imageName + "/sha256:" + tagAsset.SHA256
	if digestAliasPath != tagPath {
		if err := base.DeleteArtifact(ctx, h.deps, repoName, digestAliasPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	// 3. Collect digests still referenced by remaining manifests of this image.
	//    This correctly handles shared layers between tags (e.g. latest and 3.15-rc share base layers).
	stillUsed := make(map[string]struct{})
	remaining, _ := h.deps.Assets.ListByRepoAndPath(ctx, repoName, "/manifests/"+imageName+"/")
	for i := range remaining {
		ra := remaining[i]
		for _, d := range parseManifestDigests(h.assetStore(ctx, &ra).Get(ctx, ra.BlobKey)) {
			stillUsed[d] = struct{}{}
		}
	}

	// 4. Delete blob assets not referenced by any remaining manifest.
	for _, digest := range deletedDigests {
		if _, inUse := stillUsed[digest]; inUse {
			continue
		}
		if err := base.DeleteArtifact(ctx, h.deps, repoName, "/blobs/"+imageName+"/"+digest); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	// 5. Remove orphaned component records.
	_ = h.deps.Components.DeleteOrphans(ctx, repoName)

	c.Status(http.StatusNoContent)
}

// parseManifestDigests reads an open blob stream (or error) and returns the config+layer digests.
func parseManifestDigests(rc io.ReadCloser, _ int64, err error) []string {
	if err != nil || rc == nil {
		return nil
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	var m struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	var out []string
	if m.Config.Digest != "" {
		out = append(out, m.Config.Digest)
	}
	for _, l := range m.Layers {
		if l.Digest != "" {
			out = append(out, l.Digest)
		}
	}
	return out
}

// DeleteDockerImage handles DELETE /api/v1/browse/repositories/:name/docker-image
// Query param: image=da/devops/python (image name or namespace prefix).
// Deletes ALL manifests and blobs under the given image/namespace path.
func (h *BrowseHandler) DeleteDockerImage(c *gin.Context) {
	repoName := c.Param("name")
	imageName := strings.Trim(c.Query("image"), "/")
	if imageName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image query param required"})
		return
	}
	if !h.authorizeDelete(c, repoName, "/manifests/"+imageName+"/") {
		return
	}

	ctx := c.Request.Context()
	prefix := imageName + "/"

	// Delete all manifest records, then all layer/config blob records. Each goes
	// through DeleteArtifact, so the bytes are removed from the store that holds
	// them and the deletion is reported like any other.
	manifests, _ := h.deps.Assets.ListByRepoAndPath(ctx, repoName, "/manifests/"+prefix)
	blobs, _ := h.deps.Assets.ListByRepoAndPath(ctx, repoName, "/blobs/"+prefix)
	for _, a := range append(manifests, blobs...) {
		if err := base.DeleteArtifact(ctx, h.deps, repoName, a.Path); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	_ = h.deps.Components.DeleteOrphans(ctx, repoName)
	c.Status(http.StatusNoContent)
}
