package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type blobStoreRepo struct {
	db *pgxpool.Pool
}

// NewBlobStoreRepo returns a postgres-backed BlobStoreRepo.
func NewBlobStoreRepo(db *pgxpool.Pool) *blobStoreRepo {
	return &blobStoreRepo{db: db}
}

func (r *blobStoreRepo) List(ctx context.Context) ([]domain.BlobStore, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, type, config, quota_bytes, used_bytes, created_at, updated_at
		FROM blob_stores ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stores []domain.BlobStore
	for rows.Next() {
		bs, err := scanBlobStore(rows)
		if err != nil {
			return nil, err
		}
		stores = append(stores, *bs)
	}
	return stores, rows.Err()
}

func (r *blobStoreRepo) Get(ctx context.Context, name string) (*domain.BlobStore, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, type, config, quota_bytes, used_bytes, created_at, updated_at
		FROM blob_stores WHERE name = $1`, name)
	bs, err := scanBlobStore(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return bs, err
}

func (r *blobStoreRepo) GetByID(ctx context.Context, id string) (*domain.BlobStore, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, type, config, quota_bytes, used_bytes, created_at, updated_at
		FROM blob_stores WHERE id = $1`, id)
	bs, err := scanBlobStore(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return bs, err
}

func (r *blobStoreRepo) Create(ctx context.Context, b *domain.BlobStore) error {
	cfg, _ := json.Marshal(b.Config)
	return translateNameUnique(r.db.QueryRow(ctx, `
		INSERT INTO blob_stores (name, type, config, quota_bytes)
		VALUES ($1,$2,$3,$4)
		RETURNING id, created_at, updated_at`,
		b.Name, b.Type, cfg, b.QuotaBytes,
	).Scan(&b.ID, &b.CreatedAt, &b.UpdatedAt))
}

func (r *blobStoreRepo) Update(ctx context.Context, b *domain.BlobStore) error {
	cfg, _ := json.Marshal(b.Config)
	_, err := r.db.Exec(ctx, `
		UPDATE blob_stores SET type=$1, config=$2, quota_bytes=$3, updated_at=NOW()
		WHERE name=$4`,
		b.Type, cfg, b.QuotaBytes, b.Name,
	)
	return err
}

func (r *blobStoreRepo) Delete(ctx context.Context, name string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM blob_stores WHERE name=$1`, name)
	return err
}

func (r *blobStoreRepo) UpdateUsedBytes(ctx context.Context, name string, delta int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE blob_stores SET used_bytes = GREATEST(0, used_bytes + $1) WHERE name=$2`,
		delta, name,
	)
	return err
}

// recomputeUsedBytesSQL restates every store's used_bytes as the bytes it
// holds: one size per distinct blob key, since several assets routinely name
// one stored object (an OCI manifest's tag and its digest alias, a
// cross-repository mount). Rows that disagree about the size of a key — an
// alias left behind by an in-place overwrite — are read at their largest, which
// is the reading that cannot let a store overfill.
//
// Kept in step with migration 024, which runs the same statement once to repair
// deployments whose counters were inflated by the old per-asset increment.
const recomputeUsedBytesSQL = `
	UPDATE blob_stores bs
	SET used_bytes = COALESCE((
		    SELECT SUM(k.size_bytes) FROM (
		        SELECT DISTINCT ON (a.blob_key) a.size_bytes
		        FROM assets a
		        WHERE COALESCE(a.blob_store_id,
		                       (SELECT d.id FROM blob_stores d WHERE d.name = 'default')) = bs.id
		          AND a.blob_key IS NOT NULL AND TRIM(a.blob_key) <> ''
		        ORDER BY a.blob_key, a.size_bytes DESC
		    ) k
		), 0),
	    updated_at = NOW()`

// RecomputeUsedBytes restates every blob store's used_bytes from the assets that
// reference it. It repairs counters that drifted — the per-asset inflation this
// release fixes, a store whose blobs were removed out of band, a restored
// backup. One statement, so it never loses a concurrent increment the way a
// read-modify-write would; an upload that commits its asset row while the
// statement is in flight can still land on either side of the new figure, which
// is why the repair is repeatable rather than a one-shot.
func (r *blobStoreRepo) RecomputeUsedBytes(ctx context.Context) error {
	_, err := r.db.Exec(ctx, recomputeUsedBytesSQL)
	return err
}

func scanBlobStore(row scanner) (*domain.BlobStore, error) {
	var bs domain.BlobStore
	var cfgRaw []byte
	err := row.Scan(&bs.ID, &bs.Name, &bs.Type, &cfgRaw,
		&bs.QuotaBytes, &bs.UsedBytes, &bs.CreatedAt, &bs.UpdatedAt)
	if err != nil {
		return nil, err
	}
	unmarshalJSONB(cfgRaw, &bs.Config, "blob_stores", bs.ID, "config")
	return &bs, nil
}
