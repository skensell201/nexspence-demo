package cran_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/cran"
)

const (
	stanzaDplyr   = "Package: dplyr\nVersion: 1.1.4\n"
	stanzaGgplot2 = "Package: ggplot2\nVersion: 3.5.0\n"
)

func TestCRAN_GroupIndexSourcePath(t *testing.T) {
	h := cran.New(formats.Deps{})

	src, ok := h.GroupIndexSourcePath("/src/contrib/PACKAGES")
	require.True(t, ok)
	assert.Equal(t, "/src/contrib/PACKAGES", src)

	// .gz fans out on the PLAIN document; the merger gzips the result.
	src, ok = h.GroupIndexSourcePath("/src/contrib/PACKAGES.gz")
	require.True(t, ok)
	assert.Equal(t, "/src/contrib/PACKAGES", src)

	_, ok = h.GroupIndexSourcePath("/src/contrib/dplyr_1.1.4.tar.gz")
	assert.False(t, ok, "package downloads keep first-non-404")
}

func TestCRAN_MergeGroupIndex_UnionsStanzas(t *testing.T) {
	h := cran.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(stanzaDplyr + "\n")},
		{Member: "m2", Body: []byte(stanzaDplyr + "\n" + stanzaGgplot2 + "\n")}, // dplyr duplicated
	}

	body, ct, err := h.MergeGroupIndex("g", "/src/contrib/PACKAGES", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "text/plain")
	out := string(body)
	assert.Contains(t, out, "Package: dplyr")
	assert.Contains(t, out, "Package: ggplot2")
	assert.Equal(t, 1, bytes.Count(body, []byte("Package: dplyr")), "dedup by Package+Version")
}

func TestCRAN_MergeGroupIndex_GzipOutput(t *testing.T) {
	h := cran.New(formats.Deps{})
	parts := []formats.GroupIndexPart{{Member: "m1", Body: []byte(stanzaDplyr + "\n")}}

	body, ct, err := h.MergeGroupIndex("g", "/src/contrib/PACKAGES.gz", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "gzip")

	zr, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	plain, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Contains(t, string(plain), "Package: dplyr")
}

func TestCRAN_MergeGroupIndex_EmptyMemberContributesNothing(t *testing.T) {
	h := cran.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: []byte("")},
		{Member: "m2", Body: []byte(stanzaDplyr + "\n")},
	}

	body, _, err := h.MergeGroupIndex("g", "/src/contrib/PACKAGES", parts)
	require.NoError(t, err)
	assert.Contains(t, string(body), "Package: dplyr")
}

func TestCRAN_MergeGroupIndex_DedupPrefersFirstMember(t *testing.T) {
	h := cran.New(formats.Deps{})
	// Same Package+Version from two members with different bodies (simulated
	// via a distinguishing extra field) — first member's stanza must win.
	first := "Package: dplyr\nVersion: 1.1.4\nX-Source: m1\n"
	second := "Package: dplyr\nVersion: 1.1.4\nX-Source: m2\n"
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(first + "\n")},
		{Member: "m2", Body: []byte(second + "\n")},
	}

	body, _, err := h.MergeGroupIndex("g", "/src/contrib/PACKAGES", parts)
	require.NoError(t, err)
	assert.Contains(t, string(body), "X-Source: m1")
	assert.NotContains(t, string(body), "X-Source: m2")
}
