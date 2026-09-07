package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// DefaultBlobStore returns the store to use for a repository that names none of
// its own.
//
// The store called "default" is the one the schema seeds and the one every
// installation starts with, so it stays the first choice. But nothing forces an
// operator to keep it: deleting the seeded stores and creating a single "minio"
// store is a perfectly ordinary setup, and it used to break every write that
// went through a repository with no blob_store_id — uploads and Nexus
// migrations alike failed with "default blob store not found" (#402). When
// exactly one store exists there is no ambiguity about which one that is, so it
// answers instead.
//
// With several stores configured and none named "default" there is a real
// choice to make, and guessing would silently scatter blobs across stores. That
// case stays an error, naming the stores so the operator can assign one.
func DefaultBlobStore(ctx context.Context, blobs BlobStoreRepo) (*domain.BlobStore, error) {
	bs, err := blobs.Get(ctx, "default")
	if err == nil && bs != nil {
		return bs, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("blob store: %w", err)
	}

	all, listErr := blobs.List(ctx)
	if listErr != nil {
		return nil, fmt.Errorf("list blob stores: %w", listErr)
	}
	switch len(all) {
	case 0:
		return nil, fmt.Errorf("no blob store configured: create one, or assign repository.blobStoreId")
	case 1:
		store := all[0]
		return &store, nil
	default:
		names := make([]string, 0, len(all))
		for i := range all {
			names = append(names, all[i].Name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("no blob store named %q and %d stores to choose from (%v): assign repository.blobStoreId",
			"default", len(all), names)
	}
}
