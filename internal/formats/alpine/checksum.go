package alpine

// The APKINDEX "C:" field is a checksum computed over the CONTROL segment of
// an .apk, not the whole file. An .apk is a concatenation of independent gzip
// members: an OPTIONAL signature wrapper (one per signing key — real published
// packages, e.g. anything mirrored from a real distro CDN, normally carry
// one), then the control tar.gz (.PKGINFO + install scripts), then the data
// tar.gz. The control member is whichever one is NOT a signature wrapper —
// this package never signs its own uploads (see spec.md), but a re-uploaded
// real-world .apk (the case verified live against dl-cdn.alpinelinux.org)
// always is, so assuming "the control segment is always the first member" is
// wrong and was caught exactly that way: computing Q1 over the genuine
// signature member of a real curl-8.22.0-r0.apk produced a checksum that did
// not match the one in Alpine's own published APKINDEX, while hashing the
// first NON-signature member did, byte for byte.
//
// Getting this wrong produces an index a real apk client rejects as corrupt
// on install, since it recomputes the same hash over the same segment and
// compares. See spec.md's "Contexto" section for the risk this file exists to
// close, and plan.md's live-verification step for how it was actually found.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1" //nolint:gosec // apk protocol checksum (Q1 = SHA-1 prefix), not security
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxSignatureMembers bounds how many leading gzip members this scans looking
// for the control segment — apk-tools supports signing with more than one key,
// but a real .apk carries at most a handful; this is already generous.
const maxSignatureMembers = 8

// maxMemberBytes bounds how much of one decompressed gzip member is read
// while classifying it — a control segment (.PKGINFO + scripts) is a few KB;
// this also caps a signature member, which is smaller still.
const maxMemberBytes = 8 << 20

// controlSegment returns both the exact compressed bytes of an .apk's control
// gzip member (used for the C: checksum) and its decompressed tar content
// (used to parse .PKGINFO) — the first member that is NOT a signature
// wrapper, without needing to know how many signature members (if any)
// precede it.
func controlSegment(apk []byte) (compressed, raw []byte, err error) {
	offset := 0
	for i := 0; i < maxSignatureMembers; i++ {
		if offset >= len(apk) {
			return nil, nil, errors.New("alpine: not a valid .apk — no control segment found (only signature members?)")
		}
		consumed, memberRaw, mErr := decompressOneGzipMember(apk[offset:])
		if mErr != nil {
			return nil, nil, fmt.Errorf("alpine: reading gzip member %d: %w", i, mErr)
		}
		if !isSignatureMember(memberRaw) {
			return apk[offset : offset+consumed], memberRaw, nil
		}
		offset += consumed
	}
	return nil, nil, errors.New("alpine: too many leading signature members")
}

// decompressOneGzipMember decompresses exactly the first gzip member at the
// start of data, returning how many source bytes it consumed and its
// decompressed content.
func decompressOneGzipMember(data []byte) (consumed int, raw []byte, err error) {
	cr := &countingReader{r: bytes.NewReader(data)}
	gz, err := gzip.NewReader(cr)
	if err != nil {
		return 0, nil, fmt.Errorf("not a gzip-framed segment: %w", err)
	}
	gz.Multistream(false) // stop after this member instead of chaining into the next
	raw, err = io.ReadAll(io.LimitReader(gz, maxMemberBytes))
	if err != nil {
		return 0, nil, err
	}
	if cr.n > int64(len(data)) {
		return 0, nil, errors.New("gzip member longer than the remaining input")
	}
	return int(cr.n), raw, nil
}

// isSignatureMember reports whether a decompressed gzip member is an apk
// signature wrapper: a tar whose first entry is named ".SIGN.<algo>...".
// Anything that fails to parse as a tar (including this package's own
// synthetic non-tar test fixtures) is treated as "not a signature" — the
// safe default, since a real control segment is always a tar.
func isSignatureMember(raw []byte) bool {
	hdr, err := tar.NewReader(bytes.NewReader(raw)).Next()
	if err != nil {
		return false
	}
	return strings.HasPrefix(hdr.Name, ".SIGN.")
}

// checksumQ1 computes the APKINDEX C: field: "Q1" + base64(SHA1(control tar.gz)).
func checksumQ1(apk []byte) (string, error) {
	seg, _, err := controlSegment(apk)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(seg) //nolint:gosec // apk protocol checksum, not security
	return "Q1" + base64.StdEncoding.EncodeToString(sum[:]), nil
}

// countingReader counts bytes read from the underlying *bytes.Reader and,
// crucially, implements ReadByte so gzip/flate use it directly instead of
// wrapping it in a bufio.Reader — a bufio wrap would over-read past the gzip
// member's boundary in bulk chunks, making the byte count useless for finding
// where that member actually ends.
type countingReader struct {
	r *bytes.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}
