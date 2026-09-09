package alpine

// .PKGINFO is the real metadata file abuild embeds in every control segment —
// confirmed live against a real curl-8.22.0-r0.apk downloaded from
// dl-cdn.alpinelinux.org. Unlike APKINDEX (stanzas, blank-line separated), it
// is one "key = value" pair per line, with repeatable keys for depend/provides.
//
// Parsing it is what makes a hosted repository's index actually installable
// end to end: without a D: (depends) line a real apk client has no way to
// know curl needs libcurl, so `apk add curl` "succeeds" (the package itself
// downloads and verifies fine) but the binary fails to run — exactly what
// live verification against a real apk client caught.

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
)

// pkgInfo is the slice of .PKGINFO that APKINDEX generation needs.
type pkgInfo struct {
	Name          string
	Version       string
	Description   string
	License       string
	InstalledSize int64
	Depends       []string
	Provides      []string
}

// parsePKGInfo reads the .PKGINFO entry out of a decompressed control tar.
func parsePKGInfo(controlTar []byte) (*pkgInfo, error) {
	tr := tar.NewReader(bytes.NewReader(controlTar))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("alpine: control segment has no .PKGINFO entry")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name != ".PKGINFO" {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(tr, maxMemberBytes))
		if err != nil {
			return nil, err
		}
		info := decodePKGInfo(string(raw))
		if info.Name == "" || info.Version == "" {
			return nil, errors.New("alpine: .PKGINFO is missing pkgname/pkgver")
		}
		return info, nil
	}
}

func decodePKGInfo(text string) *pkgInfo {
	info := &pkgInfo{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "pkgname":
			info.Name = value
		case "pkgver":
			info.Version = value
		case "pkgdesc":
			info.Description = value
		case "license":
			info.License = value
		case "size":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				info.InstalledSize = n
			}
		case "depend":
			info.Depends = append(info.Depends, value)
		case "provides":
			info.Provides = append(info.Provides, value)
		}
	}
	return info
}
