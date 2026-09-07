package storage_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

// tlsEndpoint starts an S3-shaped HTTPS server with a certificate signed by an
// authority the client has never heard of — the shape of a MinIO behind a
// private CA (#403).
func tlsEndpoint(t *testing.T) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestS3_UntrustedCertificate_FailsByDefault(t *testing.T) {
	bs, err := storage.NewS3BlobStore(context.Background(), storage.S3Options{
		Bucket: "b", Region: "us-east-1", Endpoint: tlsEndpoint(t),
		AccessKeyID: "key", SecretAccessKey: "secret", ForcePathStyle: true,
	})
	require.NoError(t, err)

	_, err = bs.Exists(context.Background(), "some-key")
	require.Error(t, err, "an unverifiable certificate must not be accepted silently")
	assert.Contains(t, strings.ToLower(err.Error()), "certificate")
}

// With the opt-in, the same endpoint answers: the request reaches the server
// and comes back as an ordinary S3 answer rather than a TLS failure.
func TestS3_SkipTLSVerify_ReachesTheEndpoint(t *testing.T) {
	bs, err := storage.NewS3BlobStore(context.Background(), storage.S3Options{
		Bucket: "b", Region: "us-east-1", Endpoint: tlsEndpoint(t),
		AccessKeyID: "key", SecretAccessKey: "secret", ForcePathStyle: true,
		SkipTLSVerify: true,
	})
	require.NoError(t, err)

	exists, err := bs.Exists(context.Background(), "some-key")
	require.NoError(t, err)
	assert.True(t, exists, "the stub endpoint answers 200 to HeadObject")
}

// The store config round-trips through JSONB, so the flag has to survive both
// the boolean the frontend sends and the string a hand-edited row can hold.
func TestNewFromConfig_S3_SkipTLSVerifyIsRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"bool", true},
		{"string", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bs, err := storage.NewFromConfig(context.Background(), "s3", map[string]any{
				"bucket":          "b",
				"region":          "us-east-1",
				"endpoint":        tlsEndpoint(t),
				"access_key":      "key",
				"secret_key":      "secret",
				"skip_tls_verify": tc.value,
			})
			require.NoError(t, err)
			exists, err := bs.Exists(context.Background(), "some-key")
			require.NoError(t, err)
			assert.True(t, exists)
		})
	}
}

func TestNewFromConfig_S3_SkipTLSVerifyDefaultsOff(t *testing.T) {
	bs, err := storage.NewFromConfig(context.Background(), "s3", map[string]any{
		"bucket": "b", "region": "us-east-1", "endpoint": tlsEndpoint(t),
		"access_key": "key", "secret_key": "secret",
	})
	require.NoError(t, err)
	_, err = bs.Exists(context.Background(), "some-key")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "certificate")
}
