package cran

// Group index merging: PACKAGES answers 200-empty for members without any
// package, shadowing later members under first-non-404 fan-out — same problem
// apt/#99 solved. Stanzas are unioned, deduped by Package+Version.
//
// Unlike apt, CRAN has no Release-equivalent document that describes other
// index documents (no cross-file checksums to keep consistent), so this only
// implements formats.GroupIndexMerger — not GroupIndexDependentMerger.

import (
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger. The .gz flavor
// fans out on the PLAIN document — members serve plain text, the merger
// gzips the merged result.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if strings.HasSuffix(p, "/PACKAGES") || strings.HasSuffix(p, "/PACKAGES.gz") {
		return strings.TrimSuffix(p, ".gz"), true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(_, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	plain := mergePackages(parts)
	if strings.HasSuffix(p, ".gz") {
		return gzipBytes(plain), "application/x-gzip", nil
	}
	return plain, "text/plain; charset=utf-8", nil
}

// mergePackages unions the members' stanzas, deduped by Package+Version (first
// member wins) — CRAN has no Filename: field like apt to dedup by instead.
func mergePackages(parts []formats.GroupIndexPart) []byte {
	var stanzas []string
	seen := map[string]bool{}
	for _, part := range parts {
		for _, stanza := range strings.Split(strings.TrimSpace(string(part.Body)), "\n\n") {
			stanza = strings.TrimSpace(stanza)
			if stanza == "" {
				continue
			}
			key := stanzaKey(stanza)
			if key != "" && seen[key] {
				continue // first member wins per Package+Version
			}
			if key != "" {
				seen[key] = true
			}
			stanzas = append(stanzas, stanza)
		}
	}

	var sb strings.Builder
	for _, s := range stanzas {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	return []byte(sb.String())
}

// stanzaKey extracts "Package|Version" as the dedup key.
func stanzaKey(stanza string) string {
	var pkg, ver string
	for _, line := range strings.Split(stanza, "\n") {
		if v, found := strings.CutPrefix(line, "Package:"); found {
			pkg = strings.TrimSpace(v)
		}
		if v, found := strings.CutPrefix(line, "Version:"); found {
			ver = strings.TrimSpace(v)
		}
	}
	if pkg == "" {
		return ""
	}
	return pkg + "|" + ver
}
