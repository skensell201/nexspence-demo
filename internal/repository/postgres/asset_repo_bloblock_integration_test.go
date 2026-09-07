//go:build integration

package postgres

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── DeleteIfUnchanged ─────────────────────────────────────────────────────────

func TestAssetRepo_DeleteIfUnchanged_DeletesMatchingSnapshot(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "diu_ok")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/diu/ok.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	deleted, err := repo.DeleteIfUnchanged(ctx, a.ID, a.BlobKey, nil, a.LastModified)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteIfUnchanged = false for an unchanged row, want true")
	}
	if got, _ := repo.Get(ctx, a.ID); got != nil {
		t.Error("row still present after DeleteIfUnchanged reported deletion")
	}
}

// A download between the staleness scan and the delete bumps last_downloaded;
// the delete keyed to the scan's snapshot must then refuse, or cleanup erases
// an artifact a client just fetched.
func TestAssetRepo_DeleteIfUnchanged_RefusesWhenDownloadedSinceScan(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "diu_dl")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/diu/downloaded.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The scan saw last_downloaded = NULL; a client download then bumps it.
	if err := repo.IncrementDownloads(ctx, map[string]int64{a.ID: 1}); err != nil {
		t.Fatalf("IncrementDownloads: %v", err)
	}

	deleted, err := repo.DeleteIfUnchanged(ctx, a.ID, a.BlobKey, nil, a.LastModified)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if deleted {
		t.Fatal("DeleteIfUnchanged deleted a row whose last_downloaded moved after the scan")
	}
	if got, _ := repo.Get(ctx, a.ID); got == nil {
		t.Error("row vanished even though DeleteIfUnchanged reported no deletion")
	}
}

// A re-upload to the same path reuses the row (upsert) but may swap blob_key;
// deleting by the scan's stale snapshot would erase the fresh content.
func TestAssetRepo_DeleteIfUnchanged_RefusesWhenBlobKeyChanged(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "diu_bk")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/diu/reuploaded.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	oldKey := a.BlobKey
	oldLastModified := a.LastModified

	fresh := makeAsset(p, "/diu/reuploaded.bin")
	fresh.BlobKey = "blobkey_fresh_content"
	if err := repo.Create(ctx, fresh); err != nil {
		t.Fatalf("Create (upsert): %v", err)
	}
	if fresh.ID != a.ID {
		t.Fatalf("upsert changed the row ID (%s → %s); test setup assumption broken", a.ID, fresh.ID)
	}

	deleted, err := repo.DeleteIfUnchanged(ctx, a.ID, oldKey, nil, oldLastModified)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if deleted {
		t.Fatal("DeleteIfUnchanged deleted a row whose blob_key changed after the scan")
	}
	if got, _ := repo.Get(ctx, a.ID); got == nil {
		t.Error("row vanished even though DeleteIfUnchanged reported no deletion")
	}
}

// The real-world shape of a same-path re-upload race: BlobKey is
// sha256(repoName+":"+filePath), content-independent, so a re-upload to the
// same path always keeps the SAME blob key — only last_modified changes. The
// previous test (RefusesWhenBlobKeyChanged) exercises a blob_key change the
// production upsert can never actually produce; this one reproduces the
// sequence that really happens and is what the last_modified term exists for.
func TestAssetRepo_DeleteIfUnchanged_RefusesWhenSamePathReuploadedWithSameBlobKey(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "diu_reup")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/diu/reuploaded-same-key.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	staleLastModified := a.LastModified

	// Re-upload to the exact same path: Create's ON CONFLICT upsert keeps the
	// same row (same BlobKey, since it's derived only from repo+path) but
	// bumps last_modified.
	reupload := makeAsset(p, "/diu/reuploaded-same-key.bin")
	if err := repo.Create(ctx, reupload); err != nil {
		t.Fatalf("Create (re-upload): %v", err)
	}
	if reupload.ID != a.ID || reupload.BlobKey != a.BlobKey {
		t.Fatalf("re-upload didn't keep the same row/blob_key; test setup assumption broken")
	}

	deleted, err := repo.DeleteIfUnchanged(ctx, a.ID, a.BlobKey, nil, staleLastModified)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if deleted {
		t.Fatal("DeleteIfUnchanged deleted a row that was re-uploaded to the same path/blob_key after the scan")
	}
	if got, _ := repo.Get(ctx, a.ID); got == nil {
		t.Error("row vanished even though DeleteIfUnchanged reported no deletion")
	}
}

// ── WithBlobKeyLock ───────────────────────────────────────────────────────────

func TestAssetRepo_WithBlobKeyLock_MutuallyExcludes(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	repo := NewAssetRepo(pool)

	const key = "lock_race_key"
	var inside atomic.Int32
	var overlapped atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := repo.WithBlobKeyLock(ctx, key, func(ctx context.Context) error {
				if inside.Add(1) > 1 {
					overlapped.Store(true)
				}
				time.Sleep(50 * time.Millisecond)
				inside.Add(-1)
				return nil
			})
			if err != nil {
				t.Errorf("WithBlobKeyLock: %v", err)
			}
		}()
	}
	wg.Wait()
	if overlapped.Load() {
		t.Fatal("two callers were inside WithBlobKeyLock for the same key at once")
	}
}

func TestAssetRepo_WithBlobKeyLock_PropagatesFnError(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	repo := NewAssetRepo(pool)

	wantErr := context.Canceled
	err := repo.WithBlobKeyLock(ctx, "lock_err_key", func(ctx context.Context) error {
		return wantErr
	})
	if err == nil || err != wantErr {
		t.Fatalf("WithBlobKeyLock error = %v, want %v", err, wantErr)
	}
}
