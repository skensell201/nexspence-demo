package alpine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackUnpackIndexTarGz_RoundTrip(t *testing.T) {
	plain := "C:Q1xxx\nP:curl\nV:8.9.0-r0\nA:x86_64\nS:100\nI:200\n\n"

	packed, err := packIndexTarGz([]byte(plain))
	require.NoError(t, err)

	unpacked, err := unpackIndexTarGz(packed)
	require.NoError(t, err)
	assert.Equal(t, plain, unpacked)
}

func TestUnpackIndexTarGz_RejectsNonGzip(t *testing.T) {
	_, err := unpackIndexTarGz([]byte("not a tar.gz"))
	assert.Error(t, err)
}

func TestPathArch(t *testing.T) {
	assert.Equal(t, "x86_64", pathArch("/x86_64/APKINDEX.tar.gz"))
	assert.Equal(t, "aarch64", pathArch("/aarch64/curl-8.9.0-r0.apk"))
	assert.Equal(t, "", pathArch("/"))
}
