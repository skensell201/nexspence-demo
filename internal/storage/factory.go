package storage

import (
	"context"
	"fmt"

	nexspencecfg "github.com/nexspence-oss/nexspence/internal/config"
)

// NewBlobStoreFromConfig creates the appropriate BlobStore implementation
// based on the config's storage.default_type ("local" or "s3").
func NewBlobStoreFromConfig(ctx context.Context, cfg *nexspencecfg.Config) (BlobStore, error) {
	switch cfg.Storage.DefaultType {
	case "s3":
		s3cfg := cfg.Storage.S3
		if s3cfg.Bucket == "" {
			return nil, fmt.Errorf("storage.s3.bucket is required when default_type=s3")
		}
		return NewS3BlobStore(ctx, S3Options{
			Bucket:          s3cfg.Bucket,
			Region:          s3cfg.Region,
			Endpoint:        s3cfg.Endpoint,
			AccessKeyID:     s3cfg.AccessKeyID,
			SecretAccessKey: s3cfg.SecretAccessKey,
			ForcePathStyle:  s3cfg.ForcePathStyle,
			SkipTLSVerify:   s3cfg.SkipTLSVerify,
		})
	default: // "local" or empty
		basePath := cfg.Storage.Local.BasePath
		if basePath == "" {
			basePath = "./data/blobs"
		}
		return NewLocalBlobStore(basePath)
	}
}
