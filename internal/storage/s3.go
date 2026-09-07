package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3BlobStore stores blobs as S3 objects.
// Keys are stored as object keys with a two-level prefix shard: ab/cd/<key>.
// Compatible with AWS S3, MinIO, Ceph S3, and any S3-compatible API.
type S3BlobStore struct {
	client         *s3.Client
	uploader       *manager.Uploader //nolint:staticcheck // migration to feature/s3/transfermanager is a breaking API change; deferred to a dedicated refactor
	bucket         string
	forcePathStyle bool
}

// S3Options configures the S3 blob store.
type S3Options struct {
	Bucket          string
	Region          string
	Endpoint        string // custom endpoint for MinIO / Ceph
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

// NewS3BlobStore creates an S3BlobStore and validates connectivity by checking the bucket.
func NewS3BlobStore(ctx context.Context, opts S3Options) (*S3BlobStore, error) {
	if opts.Bucket == "" {
		return nil, fmt.Errorf("s3: bucket name is required")
	}

	cfgOpts := []func(*config.LoadOptions) error{
		config.WithRegion(opts.Region),
	}

	if opts.AccessKeyID != "" && opts.SecretAccessKey != "" {
		cfgOpts = append(cfgOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(opts.AccessKeyID, opts.SecretAccessKey, ""),
		))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("s3: load config: %w", err)
	}

	s3Opts := []func(*s3.Options){}
	if opts.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(opts.Endpoint)
		})
	}
	if opts.ForcePathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)
	uploader := manager.NewUploader(client, func(u *manager.Uploader) { //nolint:staticcheck // migration to feature/s3/transfermanager is a breaking API change; deferred to a dedicated refactor
		// 10 MB per part; AWS minimum is 5 MB; max concurrent parts = 5.
		u.PartSize = 10 * 1024 * 1024
		u.Concurrency = 5
	})

	return &S3BlobStore{
		client:         client,
		uploader:       uploader,
		bucket:         opts.Bucket,
		forcePathStyle: opts.ForcePathStyle,
	}, nil
}

// objectKey shards the blob key into a two-level prefix to avoid hot-spotting.
func (s *S3BlobStore) objectKey(key string) string {
	if len(key) >= 4 {
		return key[:2] + "/" + key[2:4] + "/" + key
	}
	return key
}

// Put uploads a blob using the S3 multipart manager.
// Files larger than PartSize (10 MB) are uploaded in parallel parts automatically.
func (s *S3BlobStore) Put(ctx context.Context, key string, r io.Reader, declaredSize int64) (err error) {
	ctx, span := blobSpan(ctx, "blobstore.s3.put", key, declaredSize)
	defer func() { finishSpan(span, err) }()
	_, err = s.uploader.Upload(ctx, &s3.PutObjectInput{ //nolint:staticcheck // migration to feature/s3/transfermanager is a breaking API change; deferred to a dedicated refactor
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
		Body:   r,
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

// Get fetches the object for key and returns its body and size.
func (s *S3BlobStore) Get(ctx context.Context, key string) (rc io.ReadCloser, size int64, err error) {
	ctx, span := blobSpan(ctx, "blobstore.s3.get", key, -1)
	defer func() { finishSpan(span, err) }()
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, 0, fmt.Errorf("blob not found: %s", key)
		}
		return nil, 0, fmt.Errorf("s3 get %s: %w", key, err)
	}
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return out.Body, size, nil
}

// Delete removes the object for key; a missing object is not an error.
func (s *S3BlobStore) Delete(ctx context.Context, key string) (err error) {
	ctx, span := blobSpan(ctx, "blobstore.s3.delete", key, -1)
	defer func() { finishSpan(span, err) }()
	// An unfinished append holds multipart parts that no listing shows and no
	// object delete reclaims, so dropping the key without aborting first would
	// leak them permanently — including when GC collects an abandoned upload
	// session. Best effort: a failed abort must not block the delete.
	_ = s.AbortAppend(ctx, key)
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	return nil
}

// Exists reports whether an object is stored for key.
func (s *S3BlobStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("s3 head %s: %w", key, err)
}

// Size returns the byte size of the object for key.
func (s *S3BlobStore) Size(ctx context.Context, key string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return 0, fmt.Errorf("blob not found: %s", key)
		}
		return 0, fmt.Errorf("s3 head %s: %w", key, err)
	}
	if out.ContentLength != nil {
		return *out.ContentLength, nil
	}
	return 0, nil
}

// ListKeys returns all blob keys in the bucket by stripping the two-level shard prefix.
func (s *S3BlobStore) ListKeys(ctx context.Context) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return keys, fmt.Errorf("s3 list keys: %w", err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil || isAppendMetaObject(*obj.Key) {
				continue
			}
			// Object key = "ab/cd/abcdef..." → blob key = "abcdef..."
			parts := strings.SplitN(*obj.Key, "/", 3)
			if len(parts) == 3 {
				keys = append(keys, parts[2])
			} else {
				keys = append(keys, *obj.Key)
			}
		}
	}
	return keys, nil
}

// blobKeyFromObjectKey reverses objectKey's two-level sharding:
// "ab/cd/abcdef..." → "abcdef...".
func blobKeyFromObjectKey(objKey string) string {
	parts := strings.SplitN(objKey, "/", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return objKey
}

// ListEntries returns every object's blob key, size and last-modified time.
func (s *S3BlobStore) ListEntries(ctx context.Context) ([]BlobEntry, error) {
	var entries []BlobEntry
	// A chunked OCI upload session's real activity — every AppendBlob call —
	// only ever rewrites the .append-meta side-object (saveAppendState),
	// never the plain placeholder Put that create() makes at session start.
	// Left alone, an entry's ModTime stays pinned to session start no matter
	// how recently a chunk landed, and GC's min_age grace period can abort a
	// session that is still actively receiving data. Track each key's most
	// recent .append-meta ModTime here (still excluded from the returned
	// entries themselves) and use it below when it postdates the
	// placeholder's own.
	metaModTimes := make(map[string]time.Time)
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return entries, fmt.Errorf("s3 list entries: %w", err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			if isAppendMetaObject(*obj.Key) {
				if obj.LastModified == nil {
					continue
				}
				key := blobKeyFromObjectKey(strings.TrimSuffix(*obj.Key, s3AppendMetaSuffix))
				if mt := *obj.LastModified; mt.After(metaModTimes[key]) {
					metaModTimes[key] = mt
				}
				continue
			}
			key := blobKeyFromObjectKey(*obj.Key)
			var size int64
			if obj.Size != nil {
				size = *obj.Size
			}
			var mt time.Time
			if obj.LastModified != nil {
				mt = *obj.LastModified
			}
			entries = append(entries, BlobEntry{Key: key, Size: size, ModTime: mt})
		}
	}
	// A page split between a placeholder and its .append-meta companion (list
	// order is lexicographic, not paired) means the correlation can only
	// happen once every page has been seen.
	for i := range entries {
		if mt, ok := metaModTimes[entries[i].Key]; ok && mt.After(entries[i].ModTime) {
			entries[i].ModTime = mt
		}
	}
	return entries, nil
}

// UsedBytes sums the size of all objects in the bucket.
// This iterates all objects and may be slow on large buckets.
// For production, prefer S3 Storage Lens metrics or a cached counter.
func (s *S3BlobStore) UsedBytes(ctx context.Context) (int64, error) {
	var total int64
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return total, fmt.Errorf("s3 list: %w", err)
		}
		for _, obj := range page.Contents {
			if obj.Key != nil && isAppendMetaObject(*obj.Key) {
				continue // bookkeeping, not stored blob content
			}
			if obj.Size != nil {
				total += *obj.Size
			}
		}
	}
	return total, nil
}

// PresignGetURL returns a time-limited URL that allows direct GET of the blob from S3.
func (s *S3BlobStore) PresignGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	pc := s3.NewPresignClient(s.client)
	req, err := pc.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("s3 presign get %s: %w", key, err)
	}
	return req.URL, nil
}

// PresignPutURL returns a time-limited URL that allows direct PUT of a blob to S3.
func (s *S3BlobStore) PresignPutURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	pc := s3.NewPresignClient(s.client)
	req, err := pc.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("s3 presign put %s: %w", key, err)
	}
	return req.URL, nil
}

// ConfigureLifecycle sets a bucket lifecycle rule that expires objects after expirationDays.
// Pass 0 to remove all lifecycle rules.
func (s *S3BlobStore) ConfigureLifecycle(ctx context.Context, expirationDays int32) error {
	if expirationDays == 0 {
		_, err := s.client.DeleteBucketLifecycle(ctx, &s3.DeleteBucketLifecycleInput{
			Bucket: aws.String(s.bucket),
		})
		if err != nil && !isNotFound(err) {
			return fmt.Errorf("s3 delete lifecycle: %w", err)
		}
		return nil
	}
	_, err := s.client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String(s.bucket),
		LifecycleConfiguration: &types.BucketLifecycleConfiguration{
			Rules: []types.LifecycleRule{
				{
					ID:     aws.String("nexspence-blob-expiration"),
					Status: types.ExpirationStatusEnabled,
					Filter: &types.LifecycleRuleFilter{Prefix: aws.String("")},
					Expiration: &types.LifecycleExpiration{
						Days: aws.Int32(expirationDays),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("s3 put lifecycle: %w", err)
	}
	return nil
}

// isNotFound returns true when err represents a 404/NoSuchKey from S3.
func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *types.NotFound
	return errors.As(err, &nf)
}
