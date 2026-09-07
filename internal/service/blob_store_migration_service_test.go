package service_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func waitForMigration(t *testing.T, svc *service.BlobStoreMigrationService, repoName string, wantStatus string) *domain.BlobStoreMigration {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m, err := svc.GetLatestByRepo(context.Background(), repoName)
		if err != nil {
			t.Fatalf("GetLatestByRepo: %v", err)
		}
		if m != nil && m.Status == wantStatus {
			return m
		}
		time.Sleep(20 * time.Millisecond)
	}
	m, _ := svc.GetLatestByRepo(context.Background(), repoName)
	t.Fatalf("migration did not reach status %q within 3s; got %v", wantStatus, m)
	return nil
}

func TestBlobStoreMigration_StartCreatesRecord(t *testing.T) {
	ctx := context.Background()

	sourceID := "store-src-001"
	targetID := "store-tgt-002"

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	sourceStore := &domain.BlobStore{
		ID:     sourceID,
		Name:   "source-store",
		Type:   "local",
		Config: map[string]any{"path": srcDir},
	}
	targetStore := &domain.BlobStore{
		ID:     targetID,
		Name:   "target-store",
		Type:   "local",
		Config: map[string]any{"path": dstDir},
	}

	bsID := sourceID
	repo := &domain.Repository{
		ID:          "repo-001",
		Name:        "my-repo",
		Format:      domain.RepoFormat("raw"),
		Type:        domain.TypeHosted,
		Online:      true,
		BlobStoreID: &bsID,
	}

	repoRepo := testutil.NewRepoRepo(repo)
	blobRepo := testutil.NewBlobStoreRepo(sourceStore, targetStore)
	migRepo := testutil.NewBlobStoreMigrationRepo()
	assetRepo := testutil.NewAssetRepo()

	defaultStore, err := storage.NewLocalBlobStore(srcDir)
	if err != nil {
		t.Fatalf("NewLocalBlobStore: %v", err)
	}
	registry := storage.NewRegistry(defaultStore)

	svc := service.NewBlobStoreMigrationService(migRepo, assetRepo, repoRepo, blobRepo, registry)

	m, err := svc.Start(ctx, "my-repo", targetID)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if m.ID == "" {
		t.Error("expected non-empty migration ID")
	}
	if m.RepositoryName != "my-repo" {
		t.Errorf("RepositoryName = %q; want %q", m.RepositoryName, "my-repo")
	}
	if m.TargetStoreID != targetID {
		t.Errorf("TargetStoreID = %q; want %q", m.TargetStoreID, targetID)
	}
}

func TestBlobStoreMigration_StartRejectsActiveConflict(t *testing.T) {
	ctx := context.Background()

	sourceID := "store-src-001"
	targetID := "store-tgt-002"

	bsID := sourceID
	repo := &domain.Repository{
		ID:          "repo-001",
		Name:        "my-repo",
		Format:      domain.RepoFormat("raw"),
		Type:        domain.TypeHosted,
		Online:      true,
		BlobStoreID: &bsID,
	}

	existing := &domain.BlobStoreMigration{
		ID:             "mig-existing",
		RepositoryName: "my-repo",
		SourceStoreID:  sourceID,
		TargetStoreID:  targetID,
		Status:         "running",
		CreatedAt:      time.Now(),
	}

	repoRepo := testutil.NewRepoRepo(repo)
	blobRepo := testutil.NewBlobStoreRepo(
		&domain.BlobStore{ID: sourceID, Name: "source-store", Type: "local"},
		&domain.BlobStore{ID: targetID, Name: "target-store", Type: "local"},
	)
	migRepo := testutil.NewBlobStoreMigrationRepo(existing)
	assetRepo := testutil.NewAssetRepo()

	defaultStore := testutil.NewBlobStore()
	registry := storage.NewRegistry(defaultStore)

	svc := service.NewBlobStoreMigrationService(migRepo, assetRepo, repoRepo, blobRepo, registry)

	_, err := svc.Start(ctx, "my-repo", targetID)
	if err == nil {
		t.Fatal("expected error for active migration conflict, got nil")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %q; want it to contain %q", err.Error(), "already running")
	}
}

func TestBlobStoreMigration_StartRejectsSameStore(t *testing.T) {
	ctx := context.Background()

	storeID := "store-001"

	bsID := storeID
	repo := &domain.Repository{
		ID:          "repo-001",
		Name:        "my-repo",
		Format:      domain.RepoFormat("raw"),
		Type:        domain.TypeHosted,
		Online:      true,
		BlobStoreID: &bsID,
	}

	repoRepo := testutil.NewRepoRepo(repo)
	blobRepo := testutil.NewBlobStoreRepo(
		&domain.BlobStore{ID: storeID, Name: "my-store", Type: "local"},
	)
	migRepo := testutil.NewBlobStoreMigrationRepo()
	assetRepo := testutil.NewAssetRepo()

	defaultStore := testutil.NewBlobStore()
	registry := storage.NewRegistry(defaultStore)

	svc := service.NewBlobStoreMigrationService(migRepo, assetRepo, repoRepo, blobRepo, registry)

	_, err := svc.Start(ctx, "my-repo", storeID)
	if err == nil {
		t.Fatal("expected error when source == target store, got nil")
	}
}

func TestBlobStoreMigration_CompletesWithBlobCopyAndResume(t *testing.T) {
	ctx := context.Background()

	sourceStoreID := "store-src-001"
	targetStoreID := "store-tgt-002"

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcStore, err := storage.NewLocalBlobStore(srcDir)
	if err != nil {
		t.Fatalf("NewLocalBlobStore src: %v", err)
	}
	dstStore, err := storage.NewLocalBlobStore(dstDir)
	if err != nil {
		t.Fatalf("NewLocalBlobStore dst: %v", err)
	}

	// Pre-write blob to source and target (simulate a resumable migration where blob already copied).
	blobKey := "blobkey1"
	if err := srcStore.Put(ctx, blobKey, strings.NewReader("content"), 7); err != nil {
		t.Fatalf("src Put: %v", err)
	}
	if err := dstStore.Put(ctx, blobKey, strings.NewReader("content"), 7); err != nil {
		t.Fatalf("dst Put: %v", err)
	}

	bsID := sourceStoreID
	repo := &domain.Repository{
		ID:          "repo-001",
		Name:        "my-repo",
		Format:      domain.RepoFormat("raw"),
		Type:        domain.TypeHosted,
		Online:      true,
		BlobStoreID: &bsID,
	}

	sourceStoreDomain := &domain.BlobStore{
		ID:     sourceStoreID,
		Name:   "source-store",
		Type:   "local",
		Config: map[string]any{"path": srcDir},
	}
	targetStoreDomain := &domain.BlobStore{
		ID:     targetStoreID,
		Name:   "target-store",
		Type:   "local",
		Config: map[string]any{"path": dstDir},
	}

	assetRepo := testutil.NewAssetRepo()
	assetRepo.MigrationRows = []domain.MigrationAssetRow{
		{BlobKey: blobKey, SourceBlobStoreID: sourceStoreID, SizeBytes: 7},
	}

	repoRepo := testutil.NewRepoRepo(repo)
	blobRepo := testutil.NewBlobStoreRepo(sourceStoreDomain, targetStoreDomain)
	migRepo := testutil.NewBlobStoreMigrationRepo()

	// Build a registry that can resolve both stores.
	// Pre-register both so the registry uses the exact in-memory paths.
	registry := storage.NewRegistry(srcStore)
	// Prime the registry cache with both stores by calling Get once.
	_, err = registry.Get(ctx, storage.BlobStoreDescriptor{
		ID:     sourceStoreID,
		Type:   "local",
		Config: map[string]any{"path": srcDir},
	})
	if err != nil {
		t.Fatalf("registry.Get src: %v", err)
	}
	_, err = registry.Get(ctx, storage.BlobStoreDescriptor{
		ID:     targetStoreID,
		Type:   "local",
		Config: map[string]any{"path": dstDir},
	})
	if err != nil {
		t.Fatalf("registry.Get dst: %v", err)
	}

	svc := service.NewBlobStoreMigrationService(migRepo, assetRepo, repoRepo, blobRepo, registry)

	m, err := svc.Start(ctx, "my-repo", targetStoreID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	final := waitForMigration(t, svc, "my-repo", "done")
	_ = m
	if final.Status != "done" {
		t.Errorf("final status = %q; want %q", final.Status, "done")
	}
}

// The pre-check is not the guarantee: two simultaneous requests both pass it,
// and without Redis the distributed lock is a no-op. The request that loses the
// insert race must get the same conflict, not a raw constraint error — and only
// one migration may exist, since both would otherwise copy the whole dataset,
// each to its own destination.
func TestBlobStoreMigration_StartLosesInsertRace_ReportsConflict(t *testing.T) {
	ctx := context.Background()

	sourceID := "store-src-001"
	targetID := "store-tgt-002"
	bsID := sourceID
	repo := &domain.Repository{
		ID: "repo-001", Name: "my-repo", Format: domain.RepoFormat("raw"),
		Type: domain.TypeHosted, Online: true, BlobStoreID: &bsID,
	}

	migRepo := testutil.NewBlobStoreMigrationRepo()
	// The pre-check finds nothing (no row yet), so the conflict can only come
	// from the insert — exactly the state of the request that arrives second.
	migRepo.CreateErr = &repository.UniqueViolationError{Field: "repository_name"}

	svc := service.NewBlobStoreMigrationService(
		migRepo, testutil.NewAssetRepo(), testutil.NewRepoRepo(repo),
		testutil.NewBlobStoreRepo(
			&domain.BlobStore{ID: sourceID, Name: "source-store", Type: "local"},
			&domain.BlobStore{ID: targetID, Name: "target-store", Type: "local"},
		),
		storage.NewRegistry(testutil.NewBlobStore()),
	)

	_, err := svc.Start(ctx, "my-repo", targetID)
	if !errors.Is(err, service.ErrMigrationAlreadyRunning) {
		t.Fatalf("want ErrMigrationAlreadyRunning, got %v", err)
	}
}

// Barrier-synchronized concurrent starts: whichever layer catches it, exactly
// one migration may be created.
func TestBlobStoreMigration_ConcurrentStarts_OnlyOneWins(t *testing.T) {
	ctx := context.Background()

	// The winning Start launches its runner immediately, and with no assets to
	// copy that runner used to reach "done" before the loser's Start looked for
	// an active migration — which made the loser's second migration correct
	// behavior and the test fail for a reason it was not written to check.
	// Holding the runner at its first asset query keeps the first migration
	// "running" for as long as both goroutines are in flight.
	assets := testutil.NewAssetRepo()
	assets.MigrationListGate = make(chan struct{})
	defer close(assets.MigrationListGate)

	sourceID := "store-src-001"
	bsID := sourceID
	repo := &domain.Repository{
		ID: "repo-001", Name: "my-repo", Format: domain.RepoFormat("raw"),
		Type: domain.TypeHosted, Online: true, BlobStoreID: &bsID,
	}
	migRepo := testutil.NewBlobStoreMigrationRepo()
	svc := service.NewBlobStoreMigrationService(
		migRepo, assets, testutil.NewRepoRepo(repo),
		testutil.NewBlobStoreRepo(
			&domain.BlobStore{ID: sourceID, Name: "source-store", Type: "local"},
			&domain.BlobStore{ID: "store-tgt-a", Name: "target-a", Type: "local"},
			&domain.BlobStore{ID: "store-tgt-b", Name: "target-b", Type: "local"},
		),
		storage.NewRegistry(testutil.NewBlobStore()),
	)

	// Two different destinations, the way two operators would pick them.
	targets := []string{"store-tgt-a", "store-tgt-b"}
	start := make(chan struct{})
	results := make(chan error, len(targets))
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Start(ctx, "my-repo", target)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	created := 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, service.ErrMigrationAlreadyRunning):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if created != 1 {
		t.Fatalf("want exactly 1 migration created, got %d", created)
	}
}

// panickingMigrationAssets blows up at the runner's first asset query — the
// shape of any nil dereference or bad-shape panic inside a detached migration.
type panickingMigrationAssets struct {
	*testutil.AssetRepo
}

func (panickingMigrationAssets) ListForBlobStoreMigration(_ context.Context, _, _ string) ([]domain.MigrationAssetRow, error) {
	panic("migration runner blew up")
}

// safego.Go keeps a panicking migration from taking the process down, but the
// row it leaves behind said "running" forever: the repository stays locked out
// of a re-run by the active-migration check, and the operator watches a job
// that can never finish. The run has to close its own record on the way out.
func TestBlobStoreMigration_RunnerPanics_MigrationIsMarkedFailed(t *testing.T) {
	ctx := context.Background()

	sourceID := "store-src-panic"
	bsID := sourceID
	repo := &domain.Repository{
		ID: "repo-panic", Name: "panic-repo", Format: domain.RepoFormat("raw"),
		Type: domain.TypeHosted, Online: true, BlobStoreID: &bsID,
	}
	svc := service.NewBlobStoreMigrationService(
		testutil.NewBlobStoreMigrationRepo(),
		panickingMigrationAssets{testutil.NewAssetRepo()},
		testutil.NewRepoRepo(repo),
		testutil.NewBlobStoreRepo(
			&domain.BlobStore{ID: sourceID, Name: "source-store", Type: "local"},
			&domain.BlobStore{ID: "store-tgt-panic", Name: "target-store", Type: "local"},
		),
		storage.NewRegistry(testutil.NewBlobStore()),
	)

	if _, err := svc.Start(ctx, "panic-repo", "store-tgt-panic"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	m := waitForMigration(t, svc, "panic-repo", "failed")
	if m.ErrorMessage == nil || !strings.Contains(*m.ErrorMessage, "panic") {
		t.Fatalf("error message %v does not say why the run ended", m.ErrorMessage)
	}

	// And the repository is free to try again.
	if _, err := svc.Start(ctx, "panic-repo", "store-tgt-panic"); err != nil {
		t.Fatalf("a failed migration must not block a re-run, got %v", err)
	}
}
