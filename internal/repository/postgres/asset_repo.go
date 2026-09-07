package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type assetRepo struct {
	db *pgxpool.Pool
}

// NewAssetRepo returns a postgres-backed AssetRepo.
func NewAssetRepo(db *pgxpool.Pool) *assetRepo {
	return &assetRepo{db: db}
}

const assetSelectCols = `
	a.id, a.component_id, a.repository_id, rep.name,
	a.path, a.blob_store_id, a.blob_key,
	a.size_bytes, a.content_type,
	a.sha1, a.sha256, a.md5,
	a.last_modified, a.last_downloaded, a.download_count, a.created_at,
	a.uploader_id, u.username`

const assetFromJoin = `FROM assets a
	JOIN repositories rep ON rep.id = a.repository_id
	LEFT JOIN users u ON u.id = a.uploader_id`

func (r *assetRepo) List(ctx context.Context, repoName string, limit, offset int) (*domain.Page[domain.Asset], error) {
	q := fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 ORDER BY a.path LIMIT $2 OFFSET $3`,
		assetSelectCols, assetFromJoin)

	rows, err := r.db.Query(ctx, q, repoName, limit+1, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var token *string
	if len(items) > limit {
		items = items[:limit]
		next := strconv.Itoa(offset + limit)
		token = &next
	}
	return &domain.Page[domain.Asset]{Items: items, ContinuationToken: token}, nil
}

func (r *assetRepo) Get(ctx context.Context, id string) (*domain.Asset, error) {
	q := fmt.Sprintf(`SELECT %s %s WHERE a.id = $1`, assetSelectCols, assetFromJoin)
	row := r.db.QueryRow(ctx, q, id)
	a, err := scanAsset(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return a, err
}

func (r *assetRepo) GetByPath(ctx context.Context, repoName, path string) (*domain.Asset, error) {
	q := fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 AND a.path = $2`, assetSelectCols, assetFromJoin)
	row := r.db.QueryRow(ctx, q, repoName, path)
	a, err := scanAsset(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return a, err
}

func (r *assetRepo) ListByComponentID(ctx context.Context, componentID string) ([]domain.Asset, error) {
	q := fmt.Sprintf(`SELECT %s %s WHERE a.component_id = $1 ORDER BY a.path`, assetSelectCols, assetFromJoin)
	rows, err := r.db.Query(ctx, q, componentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *assetRepo) ListByComponentIDs(ctx context.Context, componentIDs []string) (map[string][]domain.Asset, error) {
	if len(componentIDs) == 0 {
		return map[string][]domain.Asset{}, nil
	}
	q := fmt.Sprintf(`SELECT %s %s WHERE a.component_id = ANY($1) ORDER BY a.component_id, a.path`, assetSelectCols, assetFromJoin)
	rows, err := r.db.Query(ctx, q, componentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]domain.Asset)
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out[a.ComponentID] = append(out[a.ComponentID], *a)
	}
	return out, rows.Err()
}

func (r *assetRepo) SearchAssets(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Asset], error) {
	args := []any{}
	i := 1
	where := "WHERE 1=1"

	if len(p.RepositoryNames) > 0 {
		ph := make([]string, len(p.RepositoryNames))
		for j := range p.RepositoryNames {
			ph[j] = fmt.Sprintf("$%d", i)
			args = append(args, p.RepositoryNames[j])
			i++
		}
		where += " AND rep.name IN (" + strings.Join(ph, ",") + ")"
	} else if p.Repository != "" {
		where += fmt.Sprintf(" AND rep.name = $%d", i)
		args = append(args, p.Repository)
		i++
	}
	if p.Format != "" {
		where += fmt.Sprintf(" AND a.content_type ILIKE $%d", i)
		args = append(args, "%"+p.Format+"%")
		i++
	}
	if p.Name != "" {
		where += fmt.Sprintf(" AND a.path ILIKE $%d", i)
		args = append(args, "%"+p.Name+"%")
		i++
	}
	if p.SHA256 != "" {
		where += fmt.Sprintf(" AND a.sha256 = $%d", i)
		args = append(args, p.SHA256)
		i++
	}

	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	q := fmt.Sprintf(`SELECT %s %s %s ORDER BY a.path LIMIT $%d OFFSET $%d`,
		assetSelectCols, assetFromJoin, where, i, i+1)
	args = append(args, limit+1, p.Offset)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var token *string
	if len(items) > limit {
		items = items[:limit]
		next := strconv.Itoa(p.Offset + limit)
		token = &next
	}
	return &domain.Page[domain.Asset]{Items: items, ContinuationToken: token}, nil
}

func (r *assetRepo) ListStale(ctx context.Context, format string, repoNames []string, lastDownloadedDays, artifactAgeDays int, pathPrefix, nameGlob string, retainNVersions int, limit int) ([]domain.Asset, error) {
	if limit <= 0 {
		limit = 500
	}
	args := []any{}
	i := 1

	// When retainNVersions > 0, build a CTE that finds the N newest component
	// versions per (repository_id, group_id, name). We pass repoNames as $1
	// and retainNVersions as $2, then reuse $1 in the WHERE clause below.
	var ctePrefix, cteExclude string
	repoArgIdx := 0
	// repoNames is always non-empty here when the service calls us (policies with no
	// attached repos are skipped before reaching ListStale). The guard ensures we do
	// not build a CTE that references an empty array.
	if retainNVersions > 0 && len(repoNames) > 0 {
		ctePrefix = fmt.Sprintf(`
WITH retained_comps AS (
  SELECT id FROM (
    SELECT comp2.id,
      ROW_NUMBER() OVER (
        PARTITION BY comp2.repository_id, comp2.group_id, comp2.name
        ORDER BY comp2.version_sort DESC, comp2.created_at DESC
      ) rn
    FROM components comp2
    WHERE comp2.repository_id IN (
      SELECT id FROM repositories WHERE name = ANY($%d::text[])
    )
  ) r WHERE rn <= $%d
)
`, i, i+1)
		repoArgIdx = i
		args = append(args, repoNames, retainNVersions)
		i += 2
		cteExclude = " AND comp.id NOT IN (SELECT id FROM retained_comps)"
	}

	where := "WHERE 1=1"

	if len(repoNames) > 0 {
		if repoArgIdx > 0 {
			// repoNames already in args as $repoArgIdx — reuse it
			where += fmt.Sprintf(" AND rep.name = ANY($%d::text[])", repoArgIdx)
		} else {
			where += fmt.Sprintf(" AND rep.name = ANY($%d::text[])", i)
			args = append(args, repoNames)
			i++
		}
	}

	if format != "" && format != "*" {
		where += fmt.Sprintf(" AND comp.format = $%d", i)
		args = append(args, format)
		i++
	}
	if lastDownloadedDays > 0 {
		where += fmt.Sprintf(" AND (a.last_downloaded IS NULL OR a.last_downloaded < NOW() - INTERVAL '1 day' * $%d)", i)
		args = append(args, lastDownloadedDays)
		i++
	}
	if artifactAgeDays > 0 {
		where += fmt.Sprintf(" AND a.created_at < NOW() - INTERVAL '1 day' * $%d", i)
		args = append(args, artifactAgeDays)
		i++
	}
	if pathPrefix != "" {
		where += fmt.Sprintf(` AND a.path LIKE $%d ESCAPE '\'`, i)
		args = append(args, likePrefix(pathPrefix))
		i++
	}
	if nameGlob != "" {
		like := globToLike(nameGlob)
		where += fmt.Sprintf(" AND a.path LIKE $%d", i)
		args = append(args, like)
		i++
	}
	where += cteExclude
	args = append(args, limit)

	q := fmt.Sprintf(`%sSELECT %s %s
		JOIN components comp ON comp.id = a.component_id
		%s ORDER BY a.created_at ASC LIMIT $%d`,
		ctePrefix, assetSelectCols, assetFromJoin, where, i)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *assetRepo) Create(ctx context.Context, a *domain.Asset) error {
	return r.db.QueryRow(ctx, `
		INSERT INTO assets
		  (component_id, repository_id, path, blob_store_id, blob_key,
		   size_bytes, content_type, sha1, sha256, md5, uploader_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (repository_id, path) DO UPDATE SET
		  blob_store_id = EXCLUDED.blob_store_id,
		  blob_key     = EXCLUDED.blob_key,
		  size_bytes   = EXCLUDED.size_bytes,
		  content_type = EXCLUDED.content_type,
		  sha1         = EXCLUDED.sha1,
		  sha256       = EXCLUDED.sha256,
		  md5          = EXCLUDED.md5,
		  uploader_id  = COALESCE(EXCLUDED.uploader_id, assets.uploader_id),
		  last_modified = NOW()
		RETURNING id, created_at, last_modified`,
		a.ComponentID, a.RepositoryID, a.Path, a.BlobStoreID, a.BlobKey,
		a.SizeBytes, a.ContentType, nullStr(a.SHA1), nullStr(a.SHA256), nullStr(a.MD5),
		nullStr(a.UploaderID),
	).Scan(&a.ID, &a.CreatedAt, &a.LastModified)
}

func (r *assetRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM assets WHERE id = $1`, id)
	return err
}

// DeleteIfUnchanged deletes the row only if it is still exactly what an earlier
// scan read — same blob key, same last_downloaded, same last_modified.
// last_modified is the term that actually catches a re-upload to the same
// path: BlobKey is sha256(repoName+":"+filePath), content-independent, so a
// re-upload always keeps the same blob key — only last_modified changes.
// IS NOT DISTINCT FROM makes the NULL comparison work: most never-downloaded
// rows carry NULL, and NULL = NULL would refuse every one of them.
func (r *assetRepo) DeleteIfUnchanged(ctx context.Context, id, blobKey string, lastDownloaded *time.Time, lastModified time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM assets WHERE id = $1 AND blob_key = $2 AND last_downloaded IS NOT DISTINCT FROM $3 AND last_modified = $4`,
		id, blobKey, lastDownloaded, lastModified)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// WithBlobKeyLock serializes callers read-then-acting on one blob key via a
// transaction-scoped Postgres advisory lock, so mutual exclusion holds across
// processes, not just this one. The transaction exists only to scope the lock;
// fn's own statements run on the pool's normal connections.
func (r *assetRepo) WithBlobKeyLock(ctx context.Context, blobKey string, fn func(ctx context.Context) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, blobKey); err != nil {
		return err
	}
	if err := fn(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TouchLastModified sets last_modified = NOW() for the asset, extending the
// proxy metadata freshness window after a successful upstream revalidation (304).
func (r *assetRepo) TouchLastModified(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `UPDATE assets SET last_modified = NOW() WHERE id = $1`, id)
	return err
}

func (r *assetRepo) ListAllBlobRefs(ctx context.Context) ([]domain.BlobRef, error) {
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT blob_key, COALESCE(blob_store_id::text, '')
		 FROM assets WHERE blob_key IS NOT NULL AND TRIM(blob_key) <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []domain.BlobRef
	for rows.Next() {
		var ref domain.BlobRef
		if err := rows.Scan(&ref.BlobKey, &ref.BlobStoreID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// SumSizeByRepo returns the bytes the repository occupies: one size per stored
// object, since several assets can name one — an OCI manifest's tag and its
// digest alias, a blob mounted from another image in the same repository.
// Charging a repository once per asset row put it over its quota at half the
// bytes it actually stored (issue #146). Rows that disagree about the size of a
// key are read at their largest, the reading that cannot let a quota overrun.
func (r *assetRepo) SumSizeByRepo(ctx context.Context, repoName string) (int64, error) {
	row := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(k.size_bytes), 0) FROM (
			SELECT DISTINCT ON (a.blob_key) a.size_bytes
			FROM assets a
			JOIN repositories rep ON rep.id = a.repository_id
			WHERE rep.name = $1
			ORDER BY a.blob_key, a.size_bytes DESC
		) k`, repoName)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// IncrementDownloads applies batched download-count increments in one transaction:
// one statement for assets, one aggregated per parent component. Unknown IDs are ignored.
func (r *assetRepo) IncrementDownloads(ctx context.Context, counts map[string]int64) error {
	if len(counts) == 0 {
		return nil
	}
	ids := make([]string, 0, len(counts))
	ns := make([]int64, 0, len(counts))
	for id, n := range counts {
		ids = append(ids, id)
		ns = append(ns, n)
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE assets a SET
		  download_count = a.download_count + c.n,
		  last_downloaded = NOW()
		FROM (SELECT unnest($1::uuid[]) AS id, unnest($2::bigint[]) AS n) c
		WHERE a.id = c.id`, ids, ns); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE components co SET
		  last_downloaded = NOW(),
		  download_count = co.download_count + agg.n
		FROM (
		  SELECT a.component_id, SUM(c.n) AS n
		  FROM (SELECT unnest($1::uuid[]) AS id, unnest($2::bigint[]) AS n) c
		  JOIN assets a ON a.id = c.id
		  GROUP BY a.component_id
		) agg
		WHERE co.id = agg.component_id`, ids, ns); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanAsset(row scanner) (*domain.Asset, error) {
	var a domain.Asset
	var sha1, sha256, md5 sql.NullString
	var uploaderID sql.NullString
	var uploaderName sql.NullString
	err := row.Scan(
		&a.ID, &a.ComponentID, &a.RepositoryID, &a.Repository,
		&a.Path, &a.BlobStoreID, &a.BlobKey,
		&a.SizeBytes, &a.ContentType,
		&sha1, &sha256, &md5,
		&a.LastModified, &a.LastDownloaded, &a.DownloadCount, &a.CreatedAt,
		&uploaderID, &uploaderName,
	)
	if err != nil {
		return nil, err
	}
	a.SHA1 = sha1.String
	a.SHA256 = sha256.String
	a.MD5 = md5.String
	if uploaderID.Valid {
		a.UploaderID = uploaderID.String
	}
	if uploaderName.Valid {
		a.UploaderUsername = uploaderName.String
	}
	return &a, nil
}

func globToLike(glob string) string {
	var b strings.Builder
	for _, c := range glob {
		switch c {
		case '%', '_':
			b.WriteRune('\\')
			b.WriteRune(c)
		case '*':
			b.WriteByte('%')
		case '?':
			b.WriteByte('_')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// likePrefix turns a literal path prefix into a LIKE pattern that matches it
// and nothing else. The metacharacters are not exotic here: "_" is the Conan
// placeholder for an absent user/channel and a common character in package
// names, and "%" reaches a prefix through a percent-encoded request segment.
// Left unescaped, either one widens a prefix query into a wildcard scan over
// the whole repository. Pair with `ESCAPE '\'` in the query.
func likePrefix(prefix string) string {
	escaped := strings.ReplaceAll(prefix, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "%", `\%`)
	escaped = strings.ReplaceAll(escaped, "_", `\_`)
	return escaped + "%"
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ListPathsByRepo returns unique directory-level path prefixes derived from
// asset paths in the given repository. q is an optional case-insensitive
// substring filter applied after prefix extraction.
func (r *assetRepo) ListPathsByRepo(ctx context.Context, repoName, q string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT a.path
		 FROM assets a
		 JOIN repositories rep ON rep.id = a.repository_id
		 WHERE rep.name = $1
		 ORDER BY a.path
		 LIMIT 5000`,
		repoName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		// extract all directory prefixes: /da/devops/foo.jar → /da/, /da/devops/
		for {
			idx := strings.LastIndex(p, "/")
			if idx <= 0 {
				break
			}
			p = p[:idx+1]
			if q == "" || strings.Contains(strings.ToLower(p), strings.ToLower(q)) {
				seen[p] = struct{}{}
			}
			p = p[:idx]
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (r *assetRepo) ListRawAssetPaths(ctx context.Context, repoName string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT a.path
		 FROM assets a
		 JOIN repositories rep ON rep.id = a.repository_id
		 WHERE rep.name = $1
		 ORDER BY a.path
		 LIMIT 5000`,
		repoName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *assetRepo) ListRawBrowseAssets(ctx context.Context, repoNames []string) ([]domain.RawBrowseAsset, error) {
	rows, err := r.db.Query(ctx,
		`SELECT a.path, a.size_bytes, COALESCE(a.sha256, ''), COALESCE(a.content_type, ''),
		        c.updated_at, COALESCE(a.component_id::text, ''), rep.name
		 FROM assets a
		 JOIN components c ON c.id = a.component_id
		 JOIN repositories rep ON rep.id = c.repository_id
		 WHERE rep.name = ANY($1)
		   AND lower(trim(rep.format)) = 'raw'
		 ORDER BY a.path`,
		repoNames,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RawBrowseAsset
	for rows.Next() {
		var a domain.RawBrowseAsset
		if err := rows.Scan(&a.Path, &a.SizeBytes, &a.SHA256, &a.ContentType, &a.UpdatedAt, &a.ComponentID, &a.RepoName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListOCIImageNames returns the distinct image names the given OCI Distribution
// repositories hold. It answers GET /v2/<repo>/_catalog.
//
// The order is unspecified: the catalog has to be sorted the way Go compares
// strings, because that is what the ?last= cursor is resolved against, and a
// database ORDER BY sorts under the server's collation — which for anything but
// C places "img-a" and "img/a" differently. The caller sorts.
//
// The names are cut out of the manifest asset paths — /manifests/<name>/<ref> —
// rather than read off components.name, because a component row is not evidence
// of a pullable image: every blob upload registers one, so the components table
// carries a row per layer and per abandoned upload as well. Joining components
// to their manifest assets would answer the same question, but this way the
// answer is one column from one table.
//
// The DISTINCT is done in SQL on purpose. The alternative — ListByRepoAndPath
// with a "/manifests/" prefix and the reduction in Go — ships one full asset row
// per manifest, which for a registry holding tags and per-manifest digest
// aliases is a few hundred thousand rows to produce a few thousand names.
//
// The scan is bounded by repository, which idx_assets_repo_path (repository_id,
// path) serves. The LIKE is redundant with the pattern below — it is the pattern
// that decides what is a manifest — and is there so the planner drops the blob
// rows, the bulk of the table, before the regexp runs on them.
func (r *assetRepo) ListOCIImageNames(ctx context.Context, repoNames []string) ([]string, error) {
	if len(repoNames) == 0 {
		return nil, nil
	}
	// The reference is the last segment and never contains a slash — a tag has
	// no '/' in the OCI grammar and a digest is <algorithm>:<hex> — so the greedy
	// group is exactly the image name, however many segments it has.
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT image FROM (
		     SELECT substring(a.path from '^/manifests/(.+)/[^/]+$') AS image
		     FROM assets a
		     JOIN repositories rep ON rep.id = a.repository_id
		     WHERE rep.name = ANY($1) AND a.path LIKE '/manifests/%'
		 ) named
		 WHERE image IS NOT NULL AND image <> ''`,
		repoNames,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (r *assetRepo) ListByRepoAndPath(ctx context.Context, repoName, pathPrefix string) ([]domain.Asset, error) {
	var q string
	var args []any
	if pathPrefix == "" {
		q = fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 ORDER BY a.path`,
			assetSelectCols, assetFromJoin)
		args = []any{repoName}
	} else {
		q = fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 AND a.path LIKE $2 ESCAPE '\' ORDER BY a.path`,
			assetSelectCols, assetFromJoin)
		args = []any{repoName, likePrefix(pathPrefix)}
	}
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *assetRepo) CountByBlobKey(ctx context.Context, blobKey, excludeID string) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM assets WHERE blob_key = $1 AND id != $2`,
		blobKey, excludeID,
	).Scan(&count)
	return count, err
}

// CountByBlobKeyInStore reports how many assets still reference blobKey on
// blobStoreID — the guard before deleting a physical blob from that one store.
func (r *assetRepo) CountByBlobKeyInStore(ctx context.Context, blobKey, blobStoreID string) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM assets WHERE blob_key = $1 AND blob_store_id = $2::uuid`,
		blobKey, blobStoreID,
	).Scan(&count)
	return count, err
}

// ListForBlobStoreMigration returns distinct (blob_key, blob_store_id, size_bytes) for all
// assets in repoName whose blob_store_id differs from targetStoreID.
func (r *assetRepo) ListForBlobStoreMigration(ctx context.Context, repoName, targetStoreID string) ([]domain.MigrationAssetRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT a.blob_key, a.blob_store_id::text, a.size_bytes
		FROM assets a
		JOIN repositories rep ON rep.id = a.repository_id
		WHERE rep.name = $1
		  AND a.blob_key IS NOT NULL AND a.blob_key != ''
		  AND a.blob_store_id IS NOT NULL
		  AND a.blob_store_id != $2::uuid
		ORDER BY a.blob_key`, repoName, targetStoreID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.MigrationAssetRow
	for rows.Next() {
		var row domain.MigrationAssetRow
		if err := rows.Scan(&row.BlobKey, &row.SourceBlobStoreID, &row.SizeBytes); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// UpdateBlobStoreForBlobKey updates blob_store_id for all assets in repoName with the given blob_key.
func (r *assetRepo) UpdateBlobStoreForBlobKey(ctx context.Context, blobKey, repoName, newBlobStoreID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE assets SET blob_store_id = $1::uuid
		WHERE blob_key = $2
		  AND repository_id = (SELECT id FROM repositories WHERE name = $3)`,
		newBlobStoreID, blobKey, repoName)
	return err
}
