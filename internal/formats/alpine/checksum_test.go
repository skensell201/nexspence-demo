package alpine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1" //nolint:gosec // matching the protocol's own algorithm, not security
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gzipMember(data []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write(data)
	_ = gw.Close()
	return buf.Bytes()
}

// fakeApk concatenates two independent gzip members, mimicking an unsigned
// real .apk (control tar.gz + data tar.gz, no signature member).
func fakeApk(control, data []byte) []byte {
	return append(gzipMember(control), gzipMember(data)...)
}

func TestControlSegment_IsolatesFirstMemberOnly(t *testing.T) {
	control := []byte("control-tar-gz-payload")
	data := []byte("data-tar-gz-payload-much-longer-than-the-control-one-above")
	apk := fakeApk(control, data)

	seg, _, err := controlSegment(apk)
	require.NoError(t, err)
	assert.Equal(t, gzipMember(control), seg)
	assert.NotEqual(t, apk, seg, "must not return the whole concatenated file")
}

func TestChecksumQ1_ComputedOverControlSegmentOnly(t *testing.T) {
	control := []byte("control-tar-gz-payload")
	data := []byte("unrelated-data-segment")
	apk := fakeApk(control, data)

	sum := sha1.Sum(gzipMember(control)) //nolint:gosec
	want := "Q1" + base64.StdEncoding.EncodeToString(sum[:])

	got, err := checksumQ1(apk)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestChecksumQ1_DiffersWhenDataSegmentChanges(t *testing.T) {
	control := []byte("same-control")
	apk1 := fakeApk(control, []byte("data-v1"))
	apk2 := fakeApk(control, []byte("data-v2-different-length-too"))

	sum1, err := checksumQ1(apk1)
	require.NoError(t, err)
	sum2, err := checksumQ1(apk2)
	require.NoError(t, err)
	assert.Equal(t, sum1, sum2, "checksum must depend only on the control segment, not the data segment")
}

func TestChecksumQ1_RejectsNonGzipInput(t *testing.T) {
	_, err := checksumQ1([]byte("this is not a gzip file at all"))
	assert.Error(t, err)
}

func TestChecksumQ1_RejectsTruncatedGzip(t *testing.T) {
	control := []byte("control-tar-gz-payload")
	truncated := gzipMember(control)
	truncated = truncated[:len(truncated)-4] // cut off the trailer
	_, err := checksumQ1(truncated)
	assert.Error(t, err)
}

// tarWith builds a one-entry tar (uncompressed) — used to build realistic
// signature/control members below, since isSignatureMember only recognizes a
// real tar's first entry name.
func tarWith(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// signedApk builds a 3-member .apk matching what a real signed package looks
// like (a real curl-8.22.0-r0.apk downloaded from dl-cdn.alpinelinux.org was
// used to confirm this shape live): a signature member first, then control,
// then data — each its own gzip member.
func signedApk(t *testing.T, controlTar, dataTar []byte) []byte {
	t.Helper()
	sig := tarWith(t, ".SIGN.RSA.test-key.rsa.pub", []byte("fake-signature-bytes"))
	out := append([]byte{}, gzipMember(sig)...)
	out = append(out, gzipMember(controlTar)...)
	out = append(out, gzipMember(dataTar)...)
	return out
}

// This is the regression the live verification against the real Alpine CDN
// caught: hashing the FIRST gzip member (the signature) instead of the first
// one that is not a signature produced a checksum that did not match
// Alpine's own published APKINDEX for the same real .apk.
func TestChecksumQ1_SkipsLeadingSignatureMember(t *testing.T) {
	control := tarWith(t, ".PKGINFO", []byte("pkgname=curl\npkgver=8.22.0-r0\n"))
	data := tarWith(t, "usr/bin/curl", []byte("binary-content"))

	unsigned := append(gzipMember(control), gzipMember(data)...)
	signed := signedApk(t, control, data)

	wantSum := sha1.Sum(gzipMember(control)) //nolint:gosec
	want := "Q1" + base64.StdEncoding.EncodeToString(wantSum[:])

	gotUnsigned, err := checksumQ1(unsigned)
	require.NoError(t, err)
	assert.Equal(t, want, gotUnsigned, "unsigned .apk: control is the first member")

	gotSigned, err := checksumQ1(signed)
	require.NoError(t, err)
	assert.Equal(t, want, gotSigned, "signed .apk: control is the first member AFTER the signature — same checksum either way")
}

func TestChecksumQ1_SkipsMultipleSignatureMembers(t *testing.T) {
	control := tarWith(t, ".PKGINFO", []byte("pkgname=multi\npkgver=1.0-r0\n"))
	data := tarWith(t, "usr/bin/multi", []byte("binary-content"))
	sig1 := tarWith(t, ".SIGN.RSA.key1.rsa.pub", []byte("sig1"))
	sig2 := tarWith(t, ".SIGN.RSA.key2.rsa.pub", []byte("sig2"))

	apk := append([]byte{}, gzipMember(sig1)...)
	apk = append(apk, gzipMember(sig2)...)
	apk = append(apk, gzipMember(control)...)
	apk = append(apk, gzipMember(data)...)

	wantSum := sha1.Sum(gzipMember(control)) //nolint:gosec
	want := "Q1" + base64.StdEncoding.EncodeToString(wantSum[:])

	got, err := checksumQ1(apk)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestChecksumQ1_OnlySignatureMembers_Errors(t *testing.T) {
	sig := tarWith(t, ".SIGN.RSA.key1.rsa.pub", []byte("sig"))
	_, err := checksumQ1(gzipMember(sig))
	assert.Error(t, err, "a .apk with no control segment at all must not silently checksum the signature")
}
