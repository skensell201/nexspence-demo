package alpine_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/alpine"
)

const (
	stanzaCurl = "C:Q1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nP:curl\nV:8.9.0-r0\nA:x86_64\nS:100\nI:200\n"
	stanzaVim  = "C:Q1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\nP:vim\nV:9.0-r1\nA:x86_64\nS:300\nI:400\n"
)

// rawTarGz builds a single-entry APKINDEX.tar.gz exactly as a member's HTTP
// response body would look, so tests can feed it straight into
// formats.GroupIndexPart.Body.
func rawTarGz(t *testing.T, plain string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "APKINDEX", Mode: 0o644, Size: int64(len(plain))}))
	_, err := tw.Write([]byte(plain))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// untarSingleEntry reads the "APKINDEX" entry back out of a tar.gz — used to
// assert on the plain text a merge produced.
func untarSingleEntry(t *testing.T, tarGz []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(tarGz))
	require.NoError(t, err)
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	require.NoError(t, err)
	require.Equal(t, "APKINDEX", hdr.Name)
	raw, err := io.ReadAll(tr)
	require.NoError(t, err)
	return string(raw)
}

func TestAlpine_GroupIndexSourcePath(t *testing.T) {
	h := alpine.New(formats.Deps{})

	src, ok := h.GroupIndexSourcePath("/x86_64/APKINDEX.tar.gz")
	require.True(t, ok)
	assert.Equal(t, "/x86_64/APKINDEX.tar.gz", src)

	_, ok = h.GroupIndexSourcePath("/x86_64/curl-8.9.0-r0.apk")
	assert.False(t, ok, "package downloads keep first-non-404")
}

func TestAlpine_MergeGroupIndex_UnionsStanzas(t *testing.T) {
	h := alpine.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: rawTarGz(t, stanzaCurl)},
		{Member: "m2", Body: rawTarGz(t, stanzaCurl+"\n"+stanzaVim)}, // curl duplicated
	}

	body, ct, err := h.MergeGroupIndex("g", "/x86_64/APKINDEX.tar.gz", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "gzip")

	plain := untarSingleEntry(t, body)
	assert.Contains(t, plain, "P:curl")
	assert.Contains(t, plain, "P:vim")
	assert.Equal(t, 1, bytes.Count([]byte(plain), []byte("P:curl")), "dedup by P:+V:")
}

func TestAlpine_MergeGroupIndex_DedupPrefersFirstMember(t *testing.T) {
	h := alpine.New(formats.Deps{})
	first := "C:Q1first\nP:curl\nV:8.9.0-r0\nA:x86_64\nS:1\nI:1\n"
	second := "C:Q1second\nP:curl\nV:8.9.0-r0\nA:x86_64\nS:2\nI:2\n"
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: rawTarGz(t, first)},
		{Member: "m2", Body: rawTarGz(t, second)},
	}

	body, _, err := h.MergeGroupIndex("g", "/x86_64/APKINDEX.tar.gz", parts)
	require.NoError(t, err)
	plain := untarSingleEntry(t, body)
	assert.Contains(t, plain, "C:Q1first")
	assert.NotContains(t, plain, "C:Q1second")
}

func TestAlpine_MergeGroupIndex_MalformedMemberIsSkippedNotFatal(t *testing.T) {
	h := alpine.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: []byte("not a valid tar.gz")},
		{Member: "m2", Body: rawTarGz(t, stanzaCurl)},
	}

	body, _, err := h.MergeGroupIndex("g", "/x86_64/APKINDEX.tar.gz", parts)
	require.NoError(t, err)
	assert.Contains(t, untarSingleEntry(t, body), "P:curl")
}

func TestAlpine_MergeGroupIndex_EmptyMemberContributesNothing(t *testing.T) {
	h := alpine.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: rawTarGz(t, "")},
		{Member: "m2", Body: rawTarGz(t, stanzaCurl)},
	}

	body, _, err := h.MergeGroupIndex("g", "/x86_64/APKINDEX.tar.gz", parts)
	require.NoError(t, err)
	assert.Contains(t, untarSingleEntry(t, body), "P:curl")
}
