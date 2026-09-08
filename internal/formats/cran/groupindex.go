package cran

// Group index merging: PACKAGES answers 200-empty for members without any
// package, shadowing later members under first-non-404 fan-out — same problem
// apt/#99 solved. Stanzas are unioned, deduped by Package+Version.
//
// Unlike apt, CRAN has no Release-equivalent document that describes other
// index documents (no cross-file checksums to keep consistent), so this only
// implements formats.GroupIndexMerger — not GroupIndexDependentMerger.

import (
	"errors"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// errRDSUnsupported is returned by MergeGroupIndex for PACKAGES.rds: it is a
// binary R serialization format, not text stanzas, so there is no merged
// document to produce.
var errRDSUnsupported = errors.New("cran: PACKAGES.rds is a binary format the group cannot merge; client falls back to PACKAGES.gz")

// GroupIndexSourcePath implements formats.GroupIndexMerger. The .gz and .rds
// flavors both fan out on the PLAIN document — members serve plain text, the
// merger gzips the merged result for .gz and refuses to produce one for .rds
// (see MergeGroupIndex).
//
// PACKAGES.rds must be claimed here even though the group can't answer it: R
// requests it before PACKAGES.gz/PACKAGES (utils::available.packages), so
// left unclaimed it would fall to first-non-404 fan-out — a hosted member
// answers 405 there, but a proxy member relays its upstream's real
// PACKAGES.rds, silently shadowing every hosted-only package (the exact
// shadowing #99's merger exists to prevent, just one level up the R client's
// own fallback chain).
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if strings.HasSuffix(p, "/PACKAGES") || strings.HasSuffix(p, "/PACKAGES.gz") {
		return strings.TrimSuffix(p, ".gz"), true
	}
	if strings.HasSuffix(p, "/PACKAGES.rds") {
		return strings.TrimSuffix(p, ".rds"), true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(_, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	if strings.HasSuffix(p, ".rds") {
		// Refusing degrades to the first member's plain-text body (see
		// group.Handler.serveMergedIndex) — not valid RDS either, but R's own
		// readRDS/available.packages fallback treats a read error exactly like a
		// failed download and retries PACKAGES.gz, which merges correctly.
		return nil, "", errRDSUnsupported
	}
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
