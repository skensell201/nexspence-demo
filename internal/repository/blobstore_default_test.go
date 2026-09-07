package repository_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestDefaultBlobStore_PrefersTheStoreNamedDefault(t *testing.T) {
	blobs := testutil.NewBlobStoreRepo(
		&domain.BlobStore{ID: "bs-minio", Name: "minio", Type: "s3"},
		&domain.BlobStore{ID: "bs-default", Name: "default", Type: "local"},
	)
	bs, err := repository.DefaultBlobStore(context.Background(), blobs)
	if err != nil {
		t.Fatalf("DefaultBlobStore: %v", err)
	}
	if bs.Name != "default" {
		t.Fatalf("picked %q, want the store named \"default\"", bs.Name)
	}
}

// The reported shape of #402: the seeded stores are deleted and one store of
// the operator's own is left. Every implicit write used to fail with
// "default blob store not found".
func TestDefaultBlobStore_FallsBackToTheOnlyStore(t *testing.T) {
	blobs := testutil.NewBlobStoreRepo(&domain.BlobStore{ID: "bs-minio", Name: "minio", Type: "s3"})
	bs, err := repository.DefaultBlobStore(context.Background(), blobs)
	if err != nil {
		t.Fatalf("DefaultBlobStore: %v", err)
	}
	if bs.ID != "bs-minio" {
		t.Fatalf("picked %q, want the only configured store", bs.ID)
	}
}

func TestDefaultBlobStore_SeveralStoresNoDefault_NamesThem(t *testing.T) {
	blobs := testutil.NewBlobStoreRepo(
		&domain.BlobStore{ID: "bs-minio", Name: "minio", Type: "s3"},
		&domain.BlobStore{ID: "bs-cold", Name: "cold", Type: "s3"},
	)
	_, err := repository.DefaultBlobStore(context.Background(), blobs)
	if err == nil {
		t.Fatal("want an error when there is a real choice to make")
	}
	for _, want := range []string{"cold", "minio", "assign repository.blobStoreId"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestDefaultBlobStore_NoStoresAtAll(t *testing.T) {
	blobs := testutil.NewBlobStoreRepo()
	// The mock seeds "default" when constructed empty, so drop it to model a
	// database whose blob_stores table really is empty.
	if err := blobs.Delete(context.Background(), "default"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := repository.DefaultBlobStore(context.Background(), blobs)
	if err == nil || !strings.Contains(err.Error(), "no blob store configured") {
		t.Fatalf("got %v, want a \"no blob store configured\" error", err)
	}
}
