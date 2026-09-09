package alpine

// Group index merging: APKINDEX.tar.gz answers empty for members without any
// matching package, shadowing later members under first-non-404 fan-out —
// same problem apt/#99 and cran solved. Stanzas are unioned, deduped by P:+V:.
//
// Alpine has no Release-equivalent document that describes other index
// documents (no cross-file checksums to keep consistent), so this only
// implements formats.GroupIndexMerger — not GroupIndexDependentMerger.

import (
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if strings.HasSuffix(p, "/APKINDEX.tar.gz") {
		return p, true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(_, _ string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	plain, err := mergeIndexParts(parts)
	if err != nil {
		return nil, "", err
	}
	packed, err := packIndexTarGz(plain)
	if err != nil {
		return nil, "", err
	}
	return packed, "application/gzip", nil
}

// mergeIndexParts unpacks each member's APKINDEX.tar.gz, unions stanzas
// (deduped by P:+V:, first member wins), and returns the merged plain index.
func mergeIndexParts(parts []formats.GroupIndexPart) ([]byte, error) {
	var stanzas []string
	seen := map[string]bool{}
	for _, part := range parts {
		plain, err := unpackIndexTarGz(part.Body)
		if err != nil {
			continue // a malformed member index is skipped, not fatal to the merge
		}
		for _, stanza := range strings.Split(strings.TrimSpace(plain), "\n\n") {
			stanza = strings.TrimSpace(stanza)
			if stanza == "" {
				continue
			}
			key := stanzaKey(stanza)
			if key != "" && seen[key] {
				continue // first member wins per P:+V:
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
	return []byte(sb.String()), nil
}

// stanzaKey extracts "P|V" as the dedup key — Alpine has no Filename: field
// like apt to dedup by instead.
func stanzaKey(stanza string) string {
	var pkg, ver string
	for _, line := range strings.Split(stanza, "\n") {
		if v, found := strings.CutPrefix(line, "P:"); found {
			pkg = v
		}
		if v, found := strings.CutPrefix(line, "V:"); found {
			ver = v
		}
	}
	if pkg == "" {
		return ""
	}
	return pkg + "|" + ver
}
