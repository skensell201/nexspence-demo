package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── fake Nexus ───────────────────────────────────────────────────────────────

// fakeNexus serves the slice of the Nexus OSS REST API the migration reads.
// Every field is a raw JSON body; empty means "endpoint returns an empty list".
type fakeNexus struct {
	settings     string            // /service/rest/v1/repositorySettings
	settingsCode int               // non-zero forces this status instead of settings
	repositories string            // /service/rest/v1/repositories
	components   map[string]string // repository name → one full component page
	assetPages   map[string]string // repository name → one full /v1/assets page
	assetsCode   int               // non-zero forces this status on /v1/assets
	files        map[string]string // /repository/... path → body
	users        string
	usersCode    int // non-zero forces this status on /security/users
	// usersBySource, when set, answers /security/users per its ?source= filter;
	// a request WITHOUT the filter (or for an absent realm) then gets a 500 —
	// modeling the real instance whose one poisoned realm broke the unfiltered
	// listing (#342/10).
	usersBySource    map[string]string
	roles            string
	privileges       string
	routingRules     string
	contentSelectors string
	// onFile, when set, runs before an artifact byte is served. It lets a test
	// hold the run inside a download and act while it is demonstrably in flight.
	onFile func()

	mu   sync.Mutex
	hits map[string]int
}

func (f *fakeNexus) hit(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hits == nil {
		f.hits = map[string]int{}
	}
	f.hits[path]++
}

func (f *fakeNexus) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakeNexus) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hit(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/service/rest/v1/repositorySettings":
			if f.settingsCode != 0 {
				w.WriteHeader(f.settingsCode)
				return
			}
			_, _ = io.WriteString(w, orEmptyList(f.settings))
		case r.URL.Path == "/service/rest/v1/repositories":
			_, _ = io.WriteString(w, orEmptyList(f.repositories))
		case r.URL.Path == "/service/rest/v1/components":
			body := f.components[r.URL.Query().Get("repository")]
			if body == "" {
				body = `{"items":[],"continuationToken":null}`
			}
			_, _ = io.WriteString(w, body)
		case r.URL.Path == "/service/rest/v1/assets":
			if f.assetsCode != 0 {
				w.WriteHeader(f.assetsCode)
				return
			}
			body := f.assetPages[r.URL.Query().Get("repository")]
			if body == "" {
				body = `{"items":[],"continuationToken":null}`
			}
			_, _ = io.WriteString(w, body)
		case r.URL.Path == "/service/rest/v1/security/users":
			if f.usersCode != 0 {
				w.WriteHeader(f.usersCode)
				return
			}
			if f.usersBySource != nil {
				body, ok := f.usersBySource[r.URL.Query().Get("source")]
				if !ok {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				_, _ = io.WriteString(w, orEmptyList(body))
				return
			}
			_, _ = io.WriteString(w, orEmptyList(f.users))
		case r.URL.Path == "/service/rest/v1/security/roles":
			_, _ = io.WriteString(w, orEmptyList(f.roles))
		case r.URL.Path == "/service/rest/v1/security/privileges":
			_, _ = io.WriteString(w, orEmptyList(f.privileges))
		case r.URL.Path == "/service/rest/v1/security/content-selectors":
			_, _ = io.WriteString(w, orEmptyList(f.contentSelectors))
		case r.URL.Path == "/service/rest/v1/routing-rules":
			_, _ = io.WriteString(w, orEmptyList(f.routingRules))
		case strings.HasPrefix(r.URL.Path, "/repository/"):
			if f.onFile != nil {
				f.onFile()
			}
			body, ok := f.files[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func orEmptyList(s string) string {
	if s == "" {
		return "[]"
	}
	return s
}

// ── harness ──────────────────────────────────────────────────────────────────

// fakeUserStore is the slice of user management the migration needs.
type fakeUserStore struct {
	mu        sync.Mutex
	users     map[string]*domain.User
	passwords map[string]string
	createErr error
	nextID    int
}

func newFakeUserStore(existing ...*domain.User) *fakeUserStore {
	s := &fakeUserStore{users: map[string]*domain.User{}, passwords: map[string]string{}}
	for _, u := range existing {
		s.users[u.Username] = u
	}
	return s
}

func (s *fakeUserStore) Get(_ context.Context, username string) (*domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[username]
	if !ok {
		return nil, service.ErrNotFound
	}
	return u, nil
}

func (s *fakeUserStore) Create(_ context.Context, u *domain.User, plainPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	// Mirror UserService.Create's email-uniqueness translation: the DB's
	// partial unique index allows any number of email-LESS accounts, but a
	// duplicate email surfaces as ErrAlreadyExists naming the field. A fake
	// that accepts everything hides exactly the account loss #342/9 is about.
	if u.Email != "" {
		for _, existing := range s.users {
			if existing.Email == u.Email {
				return fmt.Errorf("%w: user with this email", service.ErrAlreadyExists)
			}
		}
	}
	s.nextID++
	u.ID = fmt.Sprintf("user-%d", s.nextID)
	s.users[u.Username] = u
	s.passwords[u.Username] = plainPassword
	return nil
}

func (s *fakeUserStore) get(username string) *domain.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users[username]
}

func (s *fakeUserStore) password(username string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.passwords[username]
}

type migHarness struct {
	svc        *service.NexusMigrationService
	jobs       *testutil.MigrationRepo
	repos      *testutil.RepoRepo
	assets     *testutil.AssetRepo
	components *testutil.ComponentRepo
	blobs      *testutil.BlobStore
	users      *fakeUserStore
	roles      *testutil.RoleRepo
	privileges *testutil.PrivilegeRepo
	rules      *testutil.RoutingRuleRepo
	csRepo     *testutil.ContentSelectorRepo
	nexus      *httptest.Server
}

func newMigHarness(t *testing.T, fake *fakeNexus, existingUsers ...*domain.User) *migHarness {
	t.Helper()
	srv := fake.start(t)

	repoRepo := testutil.NewRepoRepo()
	blobStore := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	assetRepo := testutil.NewAssetRepo()
	componentRepo := testutil.NewComponentRepo()

	deps := formats.Deps{
		Repos:      repoRepo,
		Components: componentRepo,
		Assets:     assetRepo,
		Blobs:      blobRepo,
		BlobStore:  blobStore,
		// Registry is left nil: per-blob-store routing is not what these tests
		// are about, and without it every write lands in the store they read.
		BaseURL: "http://nexspence.test",
	}

	h := &migHarness{
		jobs:       testutil.NewMigrationRepo(),
		repos:      repoRepo,
		assets:     assetRepo,
		components: componentRepo,
		blobs:      blobStore,
		users:      newFakeUserStore(existingUsers...),
		roles:      testutil.NewRoleRepo(),
		privileges: testutil.NewPrivilegeRepo(),
		rules:      testutil.NewRoutingRuleRepo(),
		csRepo:     testutil.NewContentSelectorRepo(),
		nexus:      srv,
	}
	selectorSvc, err := service.NewContentSelectorService(h.csRepo)
	require.NoError(t, err)

	h.svc = service.NewNexusMigrationService(service.NexusMigrationConfig{
		Jobs:          h.jobs,
		Repos:         service.NewRepositoryService(repoRepo, blobRepo, blobStore, testutil.NewCleanupPolicyRepo()),
		Users:         h.users,
		Roles:         h.roles,
		Privileges:    h.privileges,
		RoutingRules:  h.rules,
		Selectors:     selectorSvc,
		Deps:          deps,
		JWTSecret:     "unit-test-secret",
		Log:           zap.NewNop().Sugar(),
		HTTPClientFor: func(time.Duration) *http.Client { return srv.Client() },
	})
	return h
}

// startJob creates a job with all scopes on and runs it to completion.
func (h *migHarness) startJob(t *testing.T, mutate ...func(*domain.MigrationJob)) *domain.MigrationJob {
	t.Helper()
	job := &domain.MigrationJob{
		SourceURL:           h.nexus.URL,
		SourceUser:          "admin",
		Status:              domain.MigrationPending,
		MigrateRepos:        true,
		MigrateBlobs:        true,
		MigrateUsers:        true,
		MigratePrivileges:   true,
		MigrateRoles:        true,
		MigrateRoutingRules: true,
	}
	for _, m := range mutate {
		m(job)
	}
	require.NoError(t, h.svc.Create(context.Background(), job, "s3cret"))
	return job
}

// waitForStatus polls until the job reaches want, failing the test on timeout.
func (h *migHarness) waitForStatus(t *testing.T, id string, want domain.MigrationJobStatus) *domain.MigrationJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last *domain.MigrationJob
	for time.Now().Before(deadline) {
		j, err := h.jobs.Get(context.Background(), id)
		require.NoError(t, err)
		last = j
		if j.Status == want {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	lastErr := ""
	if last != nil && last.LastError != nil {
		lastErr = *last.LastError
	}
	t.Fatalf("job %s never reached %q (status=%q lastError=%q)", id, want, last.Status, lastErr)
	return nil
}

// ── the bug: a created job never runs ────────────────────────────────────────

func TestNexusMigration_CreatedJobRunsToCompletion(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
		components: map[string]string{
			"raw-hosted": `{"items":[{"name":"a.txt","version":null,"group":"/","format":"raw","assets":[
				{"path":"a.txt","downloadUrl":"%BASE%/repository/raw-hosted/a.txt",
				 "contentType":"text/plain","fileSize":5}]}],"continuationToken":null}`,
		},
		files: map[string]string{"/repository/raw-hosted/a.txt": "hello"},
	}
	h := newMigHarness(t, fake)
	fake.components["raw-hosted"] = strings.ReplaceAll(fake.components["raw-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	assert.Equal(t, 1, done.TotalRepos)
	assert.Equal(t, 1, done.DoneRepos)
	assert.Equal(t, int64(1), done.TotalAssets)
	assert.Equal(t, int64(1), done.DoneAssets)
	assert.Zero(t, done.ErrorCount)
	require.NotNil(t, done.StartedAt)
	require.NotNil(t, done.FinishedAt)

	created, err := h.repos.Get(context.Background(), "raw-hosted")
	require.NoError(t, err)
	assert.Equal(t, domain.FormatRaw, created.Format)
	assert.Equal(t, domain.TypeHosted, created.Type)

	stored, err := h.assets.GetByPath(context.Background(), "raw-hosted", "/a.txt")
	require.NoError(t, err)
	require.NotNil(t, stored)
	body, err := h.blobs.Read(base.BlobKey("raw-hosted", "/a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", body)
}

func TestNexusMigration_UnreachableSourceFailsJobWithError(t *testing.T) {
	fake := &fakeNexus{}
	h := newMigHarness(t, fake)
	// Point the job at a URL that will never answer.
	job := h.startJob(t, func(j *domain.MigrationJob) {
		j.SourceURL = "http://127.0.0.1:1/nexus"
	})

	failed := h.waitForStatus(t, job.ID, domain.MigrationError)
	require.NotNil(t, failed.LastError)
	assert.NotEmpty(t, *failed.LastError)
	require.NotNil(t, failed.FinishedAt)
}

// ── repository creation ──────────────────────────────────────────────────────

func TestNexusMigration_CreatesGroupAfterItsMembers(t *testing.T) {
	fake := &fakeNexus{
		settings: `[
			{"name":"maven-public","format":"maven2","type":"group","online":true,
			 "group":{"memberNames":["maven-releases","maven-central"]}},
			{"name":"maven-central","format":"maven2","type":"proxy","online":true,
			 "proxy":{"remoteUrl":"https://repo1.maven.org/maven2"}},
			{"name":"maven-releases","format":"maven2","type":"hosted","online":true}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Equal(t, 3, done.DoneRepos)

	ctx := context.Background()
	group, err := h.repos.Get(ctx, "maven-public")
	require.NoError(t, err)
	assert.Equal(t, domain.TypeGroup, group.Type)
	assert.Equal(t, []string{"maven-releases", "maven-central"}, domain.GroupMemberNames(group))

	proxy, err := h.repos.Get(ctx, "maven-central")
	require.NoError(t, err)
	assert.Equal(t, "https://repo1.maven.org/maven2", proxy.ProxyConfig["remote_url"])
}

// #342/7: Nexus never validated docker repository names, so a real source can
// hold "Docker-Group" — a name docker clients cannot address. Failing only on
// letter case is repaired by lowercasing; a group referencing the repository
// under its source spelling must resolve the renamed member.
func TestNexusMigration_DockerNameFailingOnlyOnCaseIsLowercased(t *testing.T) {
	fake := &fakeNexus{
		settings: `[
			{"name":"Docker-Hosted","format":"docker","type":"hosted","online":true},
			{"name":"Main-Group","format":"docker","type":"group","online":true,
			 "group":{"memberNames":["Docker-Hosted"]}}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount, "both repositories must migrate: %v", done.LastError)

	ctx := context.Background()
	created, err := h.repos.Get(ctx, "docker-hosted")
	require.NoError(t, err, "the repository migrates under the lowercased name")
	assert.Equal(t, domain.FormatDocker, created.Format)

	group, err := h.repos.Get(ctx, "main-group")
	require.NoError(t, err)
	assert.Equal(t, []string{"docker-hosted"}, domain.GroupMemberNames(group),
		"the group resolves its member through the rename")
}

// A name invalid beyond letter case keeps failing exactly as before.
func TestNexusMigration_DockerNameInvalidBeyondCaseStillFails(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"docker repo","format":"docker","type":"hosted","online":true}]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Equal(t, 1, done.ErrorCount)
	_, err := h.repos.Get(context.Background(), "docker repo")
	assert.Error(t, err)
}

// #342/8: Nexus allows a group to name another group as a member; this schema
// does not. A nested-group member is expanded into the leaf repositories it
// aggregates instead of failing the whole outer group — resolved from the
// SOURCE data, so the outcome does not depend on iteration order.
func TestNexusMigration_NestedGroupMembersAreFlattened(t *testing.T) {
	fake := &fakeNexus{
		settings: `[
			{"name":"outer","format":"raw","type":"group","online":true,
			 "group":{"memberNames":["inner","raw-a"]}},
			{"name":"inner","format":"raw","type":"group","online":true,
			 "group":{"memberNames":["raw-a","raw-b"]}},
			{"name":"raw-a","format":"raw","type":"hosted","online":true},
			{"name":"raw-b","format":"raw","type":"hosted","online":true}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount, "every repository must migrate: %v", done.LastError)

	ctx := context.Background()
	outer, err := h.repos.Get(ctx, "outer")
	require.NoError(t, err, "the outer group must not be dropped over its nested member")
	assert.ElementsMatch(t, []string{"raw-a", "raw-b"}, domain.GroupMemberNames(outer),
		"the nested group flattens to its leaves, deduplicated")

	inner, err := h.repos.Get(ctx, "inner")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"raw-a", "raw-b"}, domain.GroupMemberNames(inner))
}

// A reference cycle between source groups stops contributing members instead
// of recursing forever.
func TestNexusMigration_GroupCycleDoesNotHang(t *testing.T) {
	fake := &fakeNexus{
		settings: `[
			{"name":"g1","format":"raw","type":"group","online":true,
			 "group":{"memberNames":["g2","raw-a"]}},
			{"name":"g2","format":"raw","type":"group","online":true,
			 "group":{"memberNames":["g1","raw-b"]}},
			{"name":"raw-a","format":"raw","type":"hosted","online":true},
			{"name":"raw-b","format":"raw","type":"hosted","online":true}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	ctx := context.Background()
	g1, err := h.repos.Get(ctx, "g1")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"raw-a", "raw-b"}, domain.GroupMemberNames(g1),
		"the cycle stops; every reachable leaf still contributes")
}

func TestNexusMigration_SkipsUnsupportedFormatAndCountsIt(t *testing.T) {
	fake := &fakeNexus{
		settings: `[
			{"name":"bower-things","format":"bower","type":"hosted","online":true},
			{"name":"raw-hosted","format":"raw","type":"hosted","online":true}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	assert.Equal(t, 1, done.DoneRepos, "only the supported repository is created")
	assert.Equal(t, 1, done.ErrorCount)
	require.NotNil(t, done.LastError)
	assert.Contains(t, *done.LastError, "bower")

	_, err := h.repos.Get(context.Background(), "bower-things")
	assert.Error(t, err)
}

func TestNexusMigration_ExistingRepositoryIsReused(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
	}
	h := newMigHarness(t, fake)
	require.NoError(t, h.repos.Create(context.Background(), &domain.Repository{
		Name: "raw-hosted", Format: domain.FormatRaw, Type: domain.TypeHosted, Online: true,
		Description: "pre-existing",
	}))

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount)

	got, err := h.repos.Get(context.Background(), "raw-hosted")
	require.NoError(t, err)
	assert.Equal(t, "pre-existing", got.Description, "an existing repository is left untouched")
}

// ── assets ───────────────────────────────────────────────────────────────────

func TestNexusMigration_SkipsAssetsThatAlreadyExist(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
		components: map[string]string{
			"raw-hosted": `{"items":[{"name":"a.txt","version":null,"group":"/","format":"raw","assets":[
				{"path":"a.txt","downloadUrl":"%BASE%/repository/raw-hosted/a.txt","fileSize":5}]}],
				"continuationToken":null}`,
		},
		files: map[string]string{"/repository/raw-hosted/a.txt": "hello"},
	}
	h := newMigHarness(t, fake)
	fake.components["raw-hosted"] = strings.ReplaceAll(fake.components["raw-hosted"], "%BASE%", h.nexus.URL)

	first := h.startJob(t)
	h.waitForStatus(t, first.ID, domain.MigrationDone)
	downloadsAfterFirst := fake.count("/repository/raw-hosted/a.txt")
	require.Equal(t, 1, downloadsAfterFirst)

	second := h.startJob(t)
	done := h.waitForStatus(t, second.ID, domain.MigrationDone)
	assert.Equal(t, 1, fake.count("/repository/raw-hosted/a.txt"),
		"a re-run must not re-download an asset that is already present")
	assert.Equal(t, int64(1), done.DoneAssets, "skipped assets still count as done")
	assert.Zero(t, done.ErrorCount)
}

func TestNexusMigration_AssetFailureIsCountedNotFatal(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
		components: map[string]string{
			"raw-hosted": `{"items":[{"name":"c","version":null,"group":"/","format":"raw","assets":[
				{"path":"gone.txt","downloadUrl":"%BASE%/repository/raw-hosted/gone.txt","fileSize":1},
				{"path":"ok.txt","downloadUrl":"%BASE%/repository/raw-hosted/ok.txt","fileSize":2}]}],
				"continuationToken":null}`,
		},
		files: map[string]string{"/repository/raw-hosted/ok.txt": "ok"},
	}
	h := newMigHarness(t, fake)
	fake.components["raw-hosted"] = strings.ReplaceAll(fake.components["raw-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	assert.Equal(t, 1, done.ErrorCount)
	assert.Equal(t, int64(1), done.DoneAssets)
	require.NotNil(t, done.LastError)
	assert.Contains(t, *done.LastError, "gone.txt")

	ok, err := h.assets.GetByPath(context.Background(), "raw-hosted", "/ok.txt")
	require.NoError(t, err)
	assert.NotNil(t, ok, "the good asset is still transferred")
}

func TestNexusMigration_ProxyRepositoriesTransferNoAssets(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"maven-central","format":"maven2","type":"proxy","online":true,
			"proxy":{"remoteUrl":"https://repo1.maven.org/maven2"}}]`,
		components: map[string]string{
			"maven-central": `{"items":[{"name":"cached","version":"1","group":"c","format":"maven2","assets":[
				{"path":"c/cached/1/cached-1.jar","downloadUrl":"%BASE%/repository/maven-central/x","fileSize":1}]}],
				"continuationToken":null}`,
		},
	}
	h := newMigHarness(t, fake)
	fake.components["maven-central"] = strings.ReplaceAll(fake.components["maven-central"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Equal(t, int64(0), done.TotalAssets,
		"a proxy repository re-fetches from its own upstream; its cache is not migrated")
	assert.Zero(t, fake.count("/service/rest/v1/components"))
}

// ── OCI registry transfer ────────────────────────────────────────────────────

// Nexus stores registry blobs content-addressed under a placeholder image name
// ("/v2/-/blobs/<digest>") and its component listing names only the manifest.
// Copying what the listing reports would migrate an image nobody can pull, so
// the blobs are read out of the manifest instead.
const (
	ociConfigDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	ociLayerDigest  = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func ociManifest() string {
	return `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + ociConfigDigest + `","size":6},` +
		`"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"` + ociLayerDigest + `","size":5}]}`
}

// dockerFake builds a source whose docker repository behaves like the real one.
func dockerFake() *fakeNexus {
	return &fakeNexus{
		settings: `[{"name":"docker-hosted","format":"docker","type":"hosted","online":true}]`,
		components: map[string]string{
			"docker-hosted": `{"items":[{"name":"demo/alpine","version":"3.20","group":"","format":"docker","assets":[
				{"path":"/v2/demo/alpine/manifests/3.20",
				 "downloadUrl":"%BASE%/repository/docker-hosted/v2/demo/alpine/manifests/3.20",
				 "contentType":"application/vnd.oci.image.manifest.v1+json","fileSize":300}]}],
				"continuationToken":null}`,
		},
		files: map[string]string{
			"/repository/docker-hosted/v2/demo/alpine/manifests/3.20":           ociManifest(),
			"/repository/docker-hosted/v2/demo/alpine/blobs/" + ociConfigDigest: "config",
			"/repository/docker-hosted/v2/demo/alpine/blobs/" + ociLayerDigest:  "layer",
		},
	}
}

func TestNexusMigration_OCIBlobsComeFromTheManifestNotTheListing(t *testing.T) {
	fake := dockerFake()
	h := newMigHarness(t, fake)
	fake.components["docker-hosted"] = strings.ReplaceAll(fake.components["docker-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount)

	ctx := context.Background()
	for _, tc := range []struct{ digest, want string }{
		{ociConfigDigest, "config"},
		{ociLayerDigest, "layer"},
	} {
		asset, err := h.assets.GetByPath(ctx, "docker-hosted", "/blobs/demo/alpine/"+tc.digest)
		require.NoError(t, err, "blob %s must be stored under the image that references it", tc.digest)
		require.NotNil(t, asset)
		body, err := h.blobs.Read(base.BlobKey("docker-hosted", "/blobs/demo/alpine/"+tc.digest))
		require.NoError(t, err)
		assert.Equal(t, tc.want, body)
	}

	manifest, err := h.assets.GetByPath(ctx, "docker-hosted", "/manifests/demo/alpine/3.20")
	require.NoError(t, err)
	require.NotNil(t, manifest)

	// A client that pulls by tag re-fetches the manifest by digest immediately.
	alias, err := h.assets.GetByPath(ctx, "docker-hosted",
		"/manifests/demo/alpine/sha256:"+manifest.SHA256)
	require.NoError(t, err)
	assert.NotNil(t, alias)
}

func TestNexusMigration_OCIManifestIsRegisteredAfterItsBlobs(t *testing.T) {
	fake := dockerFake()
	h := newMigHarness(t, fake)
	fake.components["docker-hosted"] = strings.ReplaceAll(fake.components["docker-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	order := h.assets.CreatedPaths()
	lastBlob, firstManifest := -1, len(order)
	for i, p := range order {
		if strings.HasPrefix(p, "/blobs/") {
			lastBlob = i
		}
		if strings.HasPrefix(p, "/manifests/") && i < firstManifest {
			firstManifest = i
		}
	}
	require.NotEqual(t, -1, lastBlob)
	require.NotEqual(t, len(order), firstManifest)
	assert.Less(t, lastBlob, firstManifest,
		"a manifest must never be servable before the blobs it names")
}

// A listing that does name blobs names them under a placeholder image, which is
// not an image anything here could serve them under.
func TestNexusMigration_OCIPlaceholderBlobEntriesAreIgnored(t *testing.T) {
	fake := dockerFake()
	fake.components["docker-hosted"] = `{"items":[{"name":"demo/alpine","version":"3.20","group":"","format":"docker","assets":[
		{"path":"/v2/demo/alpine/manifests/3.20",
		 "downloadUrl":"%BASE%/repository/docker-hosted/v2/demo/alpine/manifests/3.20",
		 "contentType":"application/vnd.oci.image.manifest.v1+json","fileSize":300},
		{"path":"/v2/-/blobs/` + ociLayerDigest + `",
		 "downloadUrl":"%BASE%/repository/docker-hosted/v2/-/blobs/` + ociLayerDigest + `","fileSize":5}]}],
		"continuationToken":null}`
	h := newMigHarness(t, fake)
	fake.components["docker-hosted"] = strings.ReplaceAll(fake.components["docker-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount)

	ctx := context.Background()
	_, err := h.assets.GetByPath(ctx, "docker-hosted", "/blobs/-/"+ociLayerDigest)
	assert.Error(t, err, "the placeholder image name is not a path anything can pull from")

	_, err = h.assets.GetByPath(ctx, "docker-hosted", "/blobs/demo/alpine/"+ociLayerDigest)
	assert.NoError(t, err, "the blob still arrives, under the image that references it")
}

func TestNexusMigration_OCIIndexBringsItsChildManifests(t *testing.T) {
	const childDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	index := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[` +
		`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + childDigest + `","size":300,` +
		`"platform":{"architecture":"arm64","os":"linux"}}]}`

	fake := dockerFake()
	fake.files["/repository/docker-hosted/v2/demo/alpine/manifests/3.20"] = index
	fake.files["/repository/docker-hosted/v2/demo/alpine/manifests/"+childDigest] = ociManifest()

	h := newMigHarness(t, fake)
	fake.components["docker-hosted"] = strings.ReplaceAll(fake.components["docker-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount)

	ctx := context.Background()
	child, err := h.assets.GetByPath(ctx, "docker-hosted", "/manifests/demo/alpine/"+childDigest)
	require.NoError(t, err, "a child manifest of an index is migrated too")
	require.NotNil(t, child)

	_, err = h.assets.GetByPath(ctx, "docker-hosted", "/blobs/demo/alpine/"+ociLayerDigest)
	require.NoError(t, err, "and so are the blobs that child names")

	_, err = h.assets.GetByPath(ctx, "docker-hosted", "/manifests/demo/alpine/3.20")
	require.NoError(t, err, "the index itself is stored under its tag")
}

// ── security data ────────────────────────────────────────────────────────────

func TestNexusMigration_MigratesPrivilegesSkippingBuiltins(t *testing.T) {
	fake := &fakeNexus{
		privileges: `[
			{"type":"repository-view","name":"view-maven","description":"d","readOnly":false,
			 "format":"maven2","repository":"maven-central","actions":["READ"]},
			{"type":"application","name":"nx-all","description":"","readOnly":true,"domain":"*","actions":["*"]}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	ctx := context.Background()
	got, err := h.privileges.GetByName(ctx, "view-maven")
	require.NoError(t, err)
	assert.Equal(t, domain.PrivilegeTypeRepositoryView, got.Type)
	assert.Equal(t, "maven-central", got.Attrs["repository"])

	_, err = h.privileges.GetByName(ctx, "nx-all")
	assert.Error(t, err, "Nexus built-in privileges are not copied")
}

// #342/2: Nexus's action vocabulary (add/edit/all) must translate into the
// four actions this RBAC engine understands, or migrated accounts silently
// lose their push/delete/admin rights.
func TestNexusMigration_TranslatesPrivilegeActionVocabulary(t *testing.T) {
	fake := &fakeNexus{
		privileges: `[
			{"type":"repository-view","name":"push-maven","description":"","readOnly":false,
			 "format":"maven2","repository":"r","actions":["ADD","EDIT"]},
			{"type":"repository-admin","name":"admin-maven","description":"","readOnly":false,
			 "format":"maven2","repository":"r","actions":["ALL","READ"]}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	ctx := context.Background()
	push, err := h.privileges.GetByName(ctx, "push-maven")
	require.NoError(t, err)
	assert.Equal(t, []string{"write"}, push.Attrs["actions"],
		"add and edit both mean write — translated and deduplicated")

	admin, err := h.privileges.GetByName(ctx, "admin-maven")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"read", "browse", "write", "delete"}, admin.Attrs["actions"],
		"all expands to every action, without doubling the explicit read")
}

// #342/6: repository-content-selector privileges are unusable without the
// selector they name — the selectors must be re-created first (with Nexus's
// "=~" regex operator and its raw-backslash literals translated to CEL), and
// each privilege's ContentSelectorID resolved, or every one of them fails the
// DB constraint with an opaque error.
func TestNexusMigration_MigratesContentSelectorsAndResolvesPrivileges(t *testing.T) {
	fake := &fakeNexus{
		contentSelectors: `[
			{"name":"meta-files","type":"csel","description":"metadata only",
			 "expression":"format == \"maven2\" && path =~ \".*maven-metadata\\.xml.*\""}
		]`,
		privileges: `[
			{"type":"repository-content-selector","name":"cs-priv","description":"","readOnly":false,
			 "contentSelector":"meta-files","repository":"*","actions":["READ"]}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount, "nothing may fail: %v", done.LastError)

	ctx := context.Background()
	sel, err := h.csRepo.GetByName(ctx, "meta-files")
	require.NoError(t, err, "the source's content selector must be re-created here")
	assert.Equal(t, `format == "maven2" && path.matches(".*maven-metadata\\.xml.*")`, sel.Expression,
		"Nexus's =~ becomes CEL matches(), and the raw backslash is doubled into a valid CEL literal")

	priv, err := h.privileges.GetByName(ctx, "cs-priv")
	require.NoError(t, err, "the privilege must migrate now that its selector exists")
	require.NotNil(t, priv.ContentSelectorID)
	assert.Equal(t, sel.ID, *priv.ContentSelectorID)
}

// A privilege naming a selector the source does not define fails as that one
// item, with an error that names the selector — not a raw DB constraint.
func TestNexusMigration_ContentSelectorPrivilegeWithUnknownSelectorFailsClearly(t *testing.T) {
	fake := &fakeNexus{
		privileges: `[
			{"type":"repository-content-selector","name":"orphan-priv","description":"","readOnly":false,
			 "contentSelector":"no-such-selector","repository":"*","actions":["READ"]}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	_, err := h.privileges.GetByName(context.Background(), "orphan-priv")
	assert.Error(t, err, "a privilege with no resolvable selector cannot be created")
	require.NotNil(t, done.LastError)
	assert.Contains(t, *done.LastError, "no-such-selector",
		"the failure must name the missing selector, not a database constraint")
}

func TestNexusMigration_MigratesRolesInDependencyOrderAndFlattensNesting(t *testing.T) {
	fake := &fakeNexus{
		privileges: `[
			{"type":"application","name":"p-base","description":"","readOnly":false,"domain":"users","actions":["read"]},
			{"type":"application","name":"p-dev","description":"","readOnly":false,"domain":"repos","actions":["read"]}
		]`,
		roles: `[
			{"id":"dev","name":"dev","description":"Developers","readOnly":false,
			 "privileges":["p-dev"],"roles":["base"]},
			{"id":"base","name":"base","description":"Base","readOnly":false,
			 "privileges":["p-base"],"roles":[]}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	ctx := context.Background()
	baseRole, err := h.roles.GetByName(ctx, "base")
	require.NoError(t, err)
	dev, err := h.roles.GetByName(ctx, "dev")
	require.NoError(t, err)

	pBase, err := h.privileges.GetByName(ctx, "p-base")
	require.NoError(t, err)
	pDev, err := h.privileges.GetByName(ctx, "p-dev")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{pBase.ID}, h.roles.PrivilegesOf(baseRole.ID))
	assert.ElementsMatch(t, []string{pDev.ID, pBase.ID}, h.roles.PrivilegesOf(dev.ID),
		"a nested role's privileges are flattened into the parent")
}

func TestNexusMigration_RoleCycleDoesNotHangTheJob(t *testing.T) {
	fake := &fakeNexus{
		roles: `[
			{"id":"a","name":"a","description":"","readOnly":false,"privileges":[],"roles":["b"]},
			{"id":"b","name":"b","description":"","readOnly":false,"privileges":[],"roles":["a"]}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	ctx := context.Background()
	_, errA := h.roles.GetByName(ctx, "a")
	_, errB := h.roles.GetByName(ctx, "b")
	assert.NoError(t, errA)
	assert.NoError(t, errB)
	assert.Positive(t, done.ErrorCount, "a role cycle is reported rather than silently dropped")
}

func TestNexusMigration_MigratesLocalUserWithTemporaryPassword(t *testing.T) {
	fake := &fakeNexus{
		roles: `[{"id":"dev","name":"dev","description":"","readOnly":false,"privileges":[],"roles":[]}]`,
		users: `[{"userId":"jdoe","firstName":"Jane","lastName":"Doe","emailAddress":"j@example.com",
			"source":"default","status":"active","roles":["dev"]}]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	u := h.users.get("jdoe")
	require.NotNil(t, u)
	assert.Equal(t, "Jane", u.FirstName)
	assert.Equal(t, "j@example.com", u.Email)
	assert.Equal(t, domain.UserSourceLocal, u.Source)
	assert.Equal(t, domain.UserStatusActive, u.Status)
	assert.True(t, u.MustResetPassword, "a migrated local user must reset the temporary password")
	assert.NotEmpty(t, h.users.password("jdoe"), "a local user gets a random temporary password")

	dev, err := h.roles.GetByName(context.Background(), "dev")
	require.NoError(t, err)
	assert.Equal(t, []string{dev.ID}, h.roles.UserRoleIDs(u.ID))
}

func TestNexusMigration_MigratesExternalUserWithoutCredentials(t *testing.T) {
	fake := &fakeNexus{
		users: `[{"userId":"ldapuser","firstName":"L","lastName":"U","emailAddress":"l@example.com",
			"source":"LDAP","status":"disabled","roles":[]}]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	u := h.users.get("ldapuser")
	require.NotNil(t, u)
	assert.Equal(t, domain.UserSourceLDAP, u.Source)
	assert.Equal(t, domain.UserStatusDisabled, u.Status)
	assert.False(t, u.MustResetPassword)
	assert.Empty(t, h.users.password("ldapuser"), "an external user gets no local credential")
}

func TestNexusMigration_ExistingUserIsSkipped(t *testing.T) {
	existing := &domain.User{ID: "u-existing", Username: "jdoe", Email: "old@example.com"}
	fake := &fakeNexus{
		users: `[{"userId":"jdoe","firstName":"Jane","lastName":"Doe","emailAddress":"new@example.com",
			"source":"default","status":"active","roles":[]}]`,
	}
	h := newMigHarness(t, fake, existing)
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	assert.Equal(t, "old@example.com", h.users.get("jdoe").Email)
}

// #342/1: a hard failure in one stage must not abort the independent stages
// after it. On the reporter's real instance a broken LDAP record 500'd the
// whole /security/users listing — and routing rules, which depend on nothing
// that failed, never even ran.
func TestNexusMigration_StageFailureDoesNotAbortIndependentStages(t *testing.T) {
	fake := &fakeNexus{
		usersCode:    http.StatusInternalServerError,
		privileges:   `[{"type":"application","name":"p-ok","description":"","readOnly":false,"domain":"d","actions":["read"]}]`,
		routingRules: `[{"name":"block-x","description":"","mode":"BLOCK","matchers":["^/x/.*"]}]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	got := h.waitForStatus(t, job.ID, domain.MigrationError)

	require.NotNil(t, got.LastError)
	assert.Contains(t, *got.LastError, "users:",
		"the stage failure is named, so an operator sees WHICH stage broke")

	ctx := context.Background()
	rule, err := h.rules.GetByName(ctx, "block-x")
	require.NoError(t, err, "routing rules depend on nothing that failed — they must still run")
	assert.Equal(t, "BLOCK", rule.Mode)
	_, err = h.privileges.GetByName(ctx, "p-ok")
	require.NoError(t, err, "the privileges stage before the failure keeps its result")
}

// #342/3: Pause must not return before the runner has actually unwound —
// otherwise Pause→Resume can silently no-op or launch a second overlapping
// run, and Pause→Delete can delete the row under a still-writing goroutine.
func TestNexusMigration_PauseBlocksUntilTheRunUnwinds(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	fake := &fakeNexus{
		settings: `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
		components: map[string]string{
			"raw-hosted": `{"items":[{"name":"a.txt","version":null,"group":"/","format":"raw","assets":[
				{"path":"a.txt","downloadUrl":"%BASE%/repository/raw-hosted/a.txt",
				 "contentType":"text/plain","fileSize":5}]}],"continuationToken":null}`,
		},
		files: map[string]string{"/repository/raw-hosted/a.txt": "hello"},
		onFile: func() {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		},
	}
	h := newMigHarness(t, fake)
	fake.components["raw-hosted"] = strings.ReplaceAll(fake.components["raw-hosted"], "%BASE%", h.nexus.URL)
	defer close(release)

	job := h.startJob(t)
	<-entered // the run is demonstrably inside a download

	require.NoError(t, h.svc.Pause(context.Background(), job.ID))

	// No polling: by the time Pause returns, the runner must have unwound and
	// written the final state.
	got, err := h.jobs.Get(context.Background(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationPaused, got.Status,
		"Pause returned before the runner finished unwinding")
}

// #342/5: Resume relaunches only a paused or errored job. A done job must be
// refused, not re-run through the whole pipeline.
func TestNexusMigration_ResumeRefusesFinishedJobs(t *testing.T) {
	fake := &fakeNexus{}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)
	listings := fake.count("/service/rest/v1/repositorySettings")

	err := h.svc.Resume(context.Background(), job.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not resumable")
	assert.ErrorIs(t, err, service.ErrInvalidInput)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, listings, fake.count("/service/rest/v1/repositorySettings"),
		"a refused resume must not have relaunched the pipeline")
}

// The flip side: an errored job IS resumable — that is how an operator retries
// after fixing the cause.
func TestNexusMigration_ResumeRelaunchesErroredJobs(t *testing.T) {
	fake := &fakeNexus{usersCode: http.StatusInternalServerError}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationError)

	fake.usersCode = 0 // the operator fixed the source
	require.NoError(t, h.svc.Resume(context.Background(), job.ID))
	h.waitForStatus(t, job.ID, domain.MigrationDone)
}

// #342/9: two source accounts sharing one email (a real data-quality issue)
// must both migrate — the second without the colliding email, the same state
// any LDAP user without a mail attribute already lives in — instead of being
// dropped entirely.
func TestNexusMigration_EmailCollisionRetriesWithoutEmail(t *testing.T) {
	fake := &fakeNexus{
		users: `[
			{"userId":"svc-ci","firstName":"CI","lastName":"Bot","emailAddress":"ops@example.com","source":"default","status":"active","roles":[]},
			{"userId":"svc-deploy","firstName":"Deploy","lastName":"Bot","emailAddress":"ops@example.com","source":"default","status":"active","roles":[]}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount, "both accounts must migrate: %v", done.LastError)

	first := h.users.get("svc-ci")
	require.NotNil(t, first)
	assert.Equal(t, "ops@example.com", first.Email, "the first account keeps the email")

	second := h.users.get("svc-deploy")
	require.NotNil(t, second, "the colliding account must not be dropped")
	assert.Empty(t, second.Email, "the collision is resolved by dropping only the email")
	assert.NotEmpty(t, h.users.password("svc-deploy"), "the account is otherwise intact")
}

// #342/10: user migration defaults to the local realm only, via Nexus's own
// ?source= filter — an externally-authenticated account migrated onto a fresh
// target is a permanently-unusable login, and one poisoned realm must not take
// the whole unfiltered listing down.
func TestNexusMigration_UsersDefaultToLocalRealmOnly(t *testing.T) {
	fake := &fakeNexus{
		// No unfiltered entry: the real instance's unfiltered listing 500s.
		usersBySource: map[string]string{
			"default": `[{"userId":"jdoe","source":"default","status":"active","roles":[]}]`,
		},
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount, "local-only migration must not touch the poisoned realm: %v", done.LastError)

	require.NotNil(t, h.users.get("jdoe"))
}

// Opting into an external realm pulls its accounts too, each realm listed
// independently.
func TestNexusMigration_UserRealmsOptInLDAP(t *testing.T) {
	fake := &fakeNexus{
		usersBySource: map[string]string{
			"default": `[{"userId":"jdoe","source":"default","status":"active","roles":[]}]`,
			"LDAP":    `[{"userId":"ldap-user","source":"LDAP","status":"active","roles":[]}]`,
		},
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t, func(j *domain.MigrationJob) {
		j.UserRealms = []string{"default", "LDAP"}
	})
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Zero(t, done.ErrorCount, "%v", done.LastError)

	require.NotNil(t, h.users.get("jdoe"))
	ldap := h.users.get("ldap-user")
	require.NotNil(t, ldap)
	assert.Empty(t, h.users.password("ldap-user"), "an external account gets no local credential")
}

// A realm that fails to list is a named stage failure; realms that listed fine
// still migrate their accounts.
func TestNexusMigration_OneRealmListingFailureDoesNotMaskTheOthers(t *testing.T) {
	fake := &fakeNexus{
		usersBySource: map[string]string{
			"default": `[{"userId":"jdoe","source":"default","status":"active","roles":[]}]`,
			// "LDAP" absent → 500, modeling the poisoned realm.
		},
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t, func(j *domain.MigrationJob) {
		j.UserRealms = []string{"default", "LDAP"}
	})
	got := h.waitForStatus(t, job.ID, domain.MigrationError)
	require.NotNil(t, got.LastError)
	assert.Contains(t, *got.LastError, "LDAP", "the failing realm is named")
	require.NotNil(t, h.users.get("jdoe"), "the healthy realm's accounts still migrate")
}

func TestNexusMigration_MigratesRoutingRules(t *testing.T) {
	fake := &fakeNexus{
		routingRules: `[
			{"name":"block-com","description":"no com","mode":"BLOCK","matchers":["^/com/.*"]},
			{"name":"bad-regex","description":"","mode":"ALLOW","matchers":["^/[unclosed"]}
		]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	ctx := context.Background()
	rule, err := h.rules.GetByName(ctx, "block-com")
	require.NoError(t, err)
	assert.Equal(t, "BLOCK", rule.Mode)
	assert.Equal(t, []string{"^/com/.*"}, rule.Matchers)

	_, err = h.rules.GetByName(ctx, "bad-regex")
	assert.Error(t, err, "a matcher that does not compile is rejected, not stored")
	assert.Positive(t, done.ErrorCount)
}

// ── scope flags ──────────────────────────────────────────────────────────────

func TestNexusMigration_ScopeFlagsGateEachStage(t *testing.T) {
	fake := &fakeNexus{
		settings:     `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
		users:        `[{"userId":"jdoe","source":"default","status":"active","roles":[]}]`,
		privileges:   `[{"type":"application","name":"p","description":"","readOnly":false,"domain":"d","actions":["read"]}]`,
		roles:        `[{"id":"r","name":"r","description":"","readOnly":false,"privileges":[],"roles":[]}]`,
		routingRules: `[{"name":"rr","description":"","mode":"BLOCK","matchers":["^/x"]}]`,
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t, func(j *domain.MigrationJob) {
		j.MigrateRepos = true
		j.MigrateBlobs = false
		j.MigrateUsers = false
		j.MigratePrivileges = false
		j.MigrateRoles = false
		j.MigrateRoutingRules = false
	})
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	ctx := context.Background()
	_, err := h.repos.Get(ctx, "raw-hosted")
	assert.NoError(t, err, "the repository scope was on")

	assert.Nil(t, h.users.get("jdoe"))
	_, err = h.privileges.GetByName(ctx, "p")
	assert.Error(t, err)
	_, err = h.roles.GetByName(ctx, "r")
	assert.Error(t, err)
	_, err = h.rules.GetByName(ctx, "rr")
	assert.Error(t, err)
	assert.Zero(t, fake.count("/service/rest/v1/security/users"))
	assert.Zero(t, fake.count("/service/rest/v1/routing-rules"))
}

func TestNexusMigration_BlobsOffStillCreatesRepositories(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
		components: map[string]string{
			"raw-hosted": `{"items":[{"name":"a.txt","version":null,"group":"/","format":"raw","assets":[
				{"path":"a.txt","downloadUrl":"http://unused/","fileSize":5}]}],"continuationToken":null}`,
		},
	}
	h := newMigHarness(t, fake)
	job := h.startJob(t, func(j *domain.MigrationJob) { j.MigrateBlobs = false })
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	_, err := h.repos.Get(context.Background(), "raw-hosted")
	require.NoError(t, err)
	assert.Equal(t, int64(0), done.TotalAssets)
	assert.Zero(t, fake.count("/service/rest/v1/components"))
}

// ── lifecycle ────────────────────────────────────────────────────────────────

func TestNexusMigration_PauseStopsTheRunAndResumeFinishesIt(t *testing.T) {
	var items []string
	files := map[string]string{}
	for i := range 40 {
		p := fmt.Sprintf("f%02d.txt", i)
		items = append(items, fmt.Sprintf(
			`{"name":"%s","version":null,"group":"/","format":"raw","assets":[
				{"path":"%s","downloadUrl":"%%BASE%%/repository/raw-hosted/%s","fileSize":4}]}`, p, p, p))
		files["/repository/raw-hosted/"+p] = "data"
	}
	fake := &fakeNexus{
		settings:   `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
		components: map[string]string{"raw-hosted": `{"items":[` + strings.Join(items, ",") + `],"continuationToken":null}`},
		files:      files,
	}
	// Hold the run inside its first download so Pause provably lands mid-transfer.
	inFlight := make(chan struct{})
	release := make(chan struct{})
	firstCall := make(chan struct{}, 1)
	firstCall <- struct{}{}
	fake.onFile = func() {
		select {
		case <-firstCall:
			close(inFlight)
			<-release
		default:
		}
	}

	h := newMigHarness(t, fake)
	fake.components["raw-hosted"] = strings.ReplaceAll(fake.components["raw-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	<-inFlight
	require.NoError(t, h.svc.Pause(context.Background(), job.ID))
	close(release)

	paused := h.waitForStatus(t, job.ID, domain.MigrationPaused)
	assert.Nil(t, paused.FinishedAt, "a paused job is not finished")
	assert.Less(t, paused.DoneAssets, int64(40), "the run stopped before transferring everything")

	fake.onFile = nil
	require.NoError(t, h.svc.Resume(context.Background(), job.ID))
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)
	assert.Equal(t, int64(40), done.DoneAssets)
	assert.Zero(t, done.ErrorCount)
}

func TestNexusMigration_PauseOnAPendingJobIsAccepted(t *testing.T) {
	h := newMigHarness(t, &fakeNexus{})
	ctx := context.Background()
	job := &domain.MigrationJob{SourceURL: h.nexus.URL, Status: domain.MigrationPending}
	require.NoError(t, h.jobs.Create(ctx, job))

	require.NoError(t, h.svc.Pause(ctx, job.ID))
	got, err := h.jobs.Get(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationPaused, got.Status)
}

func TestNexusMigration_ResumeAllRestartsInterruptedJobs(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
	}
	h := newMigHarness(t, fake)
	ctx := context.Background()

	// A job left "running" by a process that died mid-transfer.
	job := &domain.MigrationJob{
		SourceURL: h.nexus.URL, SourceUser: "admin", Status: domain.MigrationRunning,
		MigrateRepos: true,
	}
	require.NoError(t, h.jobs.Create(ctx, job))
	sealed, err := h.svc.SealPassword("s3cret")
	require.NoError(t, err)
	require.NoError(t, h.jobs.SetSourcePassword(ctx, job.ID, sealed))

	require.NoError(t, h.svc.ResumeAll(ctx))
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	_, err = h.repos.Get(ctx, "raw-hosted")
	assert.NoError(t, err)
}

func TestNexusMigration_ResumeAllLeavesPausedJobsAlone(t *testing.T) {
	h := newMigHarness(t, &fakeNexus{})
	ctx := context.Background()
	job := &domain.MigrationJob{SourceURL: h.nexus.URL, Status: domain.MigrationPaused}
	require.NoError(t, h.jobs.Create(ctx, job))

	require.NoError(t, h.svc.ResumeAll(ctx))
	time.Sleep(50 * time.Millisecond)

	got, err := h.jobs.Get(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationPaused, got.Status)
}

func TestNexusMigration_CreateStoresCredentialsEncrypted(t *testing.T) {
	h := newMigHarness(t, &fakeNexus{})
	job := h.startJob(t)
	h.waitForStatus(t, job.ID, domain.MigrationDone)

	stored, err := h.jobs.Get(context.Background(), job.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stored.SourcePassword)
	assert.NotContains(t, stored.SourcePassword, "s3cret", "the password is not stored in the clear")

	plain, err := h.svc.OpenPassword(stored.SourcePassword)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", plain)
}

// ── preview ──────────────────────────────────────────────────────────────────

func TestNexusMigration_PreviewListsRepositories(t *testing.T) {
	fake := &fakeNexus{
		repositories: `[{"name":"raw-hosted","format":"raw","type":"hosted"},
			{"name":"maven-central","format":"maven2","type":"proxy"}]`,
	}
	h := newMigHarness(t, fake)

	res, err := h.svc.Preview(context.Background(), h.nexus.URL, "admin", "s3cret")
	require.NoError(t, err)
	assert.True(t, res.Reachable)
	assert.Equal(t, 2, res.RepoCount)
	require.Len(t, res.Repos, 2)
	assert.Equal(t, "raw-hosted", res.Repos[0].Name)
	assert.Equal(t, "hosted", res.Repos[0].Type)
}

func TestNexusMigration_PreviewRejectsEmptyURL(t *testing.T) {
	h := newMigHarness(t, &fakeNexus{})
	_, err := h.svc.Preview(context.Background(), "   ", "admin", "s3cret")
	require.ErrorIs(t, err, service.ErrInvalidInput)
}

func TestNexusMigration_PreviewSurfacesUnreachableSource(t *testing.T) {
	h := newMigHarness(t, &fakeNexus{})
	_, err := h.svc.Preview(context.Background(), "http://127.0.0.1:1/nexus", "admin", "s3cret")
	require.Error(t, err)
	assert.NotErrorIs(t, err, service.ErrInvalidInput)
}

func TestNexusMigration_PreviewDoesNotCreateAJob(t *testing.T) {
	fake := &fakeNexus{repositories: `[{"name":"raw-hosted","format":"raw","type":"hosted"}]`}
	h := newMigHarness(t, fake)

	_, err := h.svc.Preview(context.Background(), h.nexus.URL, "admin", "s3cret")
	require.NoError(t, err)

	jobs, err := h.jobs.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

// ── validation ───────────────────────────────────────────────────────────────

func TestNexusMigration_CreateRejectsEmptySourceURL(t *testing.T) {
	h := newMigHarness(t, &fakeNexus{})
	err := h.svc.Create(context.Background(), &domain.MigrationJob{SourceURL: " "}, "pw")
	require.ErrorIs(t, err, service.ErrInvalidInput)
}

func TestNexusMigration_CreateRejectsMalformedSourceURL(t *testing.T) {
	h := newMigHarness(t, &fakeNexus{})
	err := h.svc.Create(context.Background(), &domain.MigrationJob{SourceURL: "nexus.example.com"}, "pw")
	require.ErrorIs(t, err, service.ErrInvalidInput)
}

func TestNexusMigration_ResumeUnknownJobIsNotFound(t *testing.T) {
	h := newMigHarness(t, &fakeNexus{})
	err := h.svc.Resume(context.Background(), "nope")
	require.Error(t, err)
}

// jsonRoundTrip guards the fixtures above against silent typos.
func TestFakeNexusFixturesAreValidJSON(t *testing.T) {
	for _, doc := range []string{
		`[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
	} {
		var v any
		require.NoError(t, json.Unmarshal([]byte(doc), &v))
	}
}

// ── #350: assets with no owning component must be counted, not vanish ────────

// The Components API only returns assets it can group under a component;
// anything else was never planned, never transferred, and never counted. The
// plan is now reconciled against /service/rest/v1/assets — the full listing —
// and every unplanned asset is recorded through the error count.
func TestNexusMigration_NonComponentAssetCountedAsError(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
		components: map[string]string{
			"raw-hosted": `{"items":[{"name":"a.txt","version":null,"group":"/","format":"raw","assets":[
				{"path":"a.txt","downloadUrl":"%BASE%/repository/raw-hosted/a.txt",
				 "contentType":"text/plain","fileSize":5}]}],"continuationToken":null}`,
		},
		assetPages: map[string]string{
			"raw-hosted": `{"items":[
				{"path":"a.txt","downloadUrl":"%BASE%/repository/raw-hosted/a.txt","contentType":"text/plain","fileSize":5},
				{"path":"orphan.bin","downloadUrl":"%BASE%/repository/raw-hosted/orphan.bin","contentType":"application/octet-stream","fileSize":9}
			],"continuationToken":null}`,
		},
		files: map[string]string{"/repository/raw-hosted/a.txt": "hello"},
	}
	h := newMigHarness(t, fake)
	fake.components["raw-hosted"] = strings.ReplaceAll(fake.components["raw-hosted"], "%BASE%", h.nexus.URL)
	fake.assetPages["raw-hosted"] = strings.ReplaceAll(fake.assetPages["raw-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	assert.Equal(t, 1, done.ErrorCount, "the orphan asset must be counted, not silently dropped")
	require.NotNil(t, done.LastError)
	assert.Contains(t, *done.LastError, "orphan.bin")

	// The componentized asset still migrates normally.
	stored, err := h.assets.GetByPath(context.Background(), "raw-hosted", "/a.txt")
	require.NoError(t, err)
	require.NotNil(t, stored)
}

// maven-metadata.xml and its checksum sidecars are the known population of
// non-componentized assets (#350's audit found nothing else, format-wide).
// They are deliberately NOT migrated — both shapes are generated dynamically
// from stored components on every GET — so they must not inflate the error
// count either.
func TestNexusMigration_MavenMetadataAssetsAreNotErrors(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"mvn-hosted","format":"maven2","type":"hosted","online":true}]`,
		components: map[string]string{
			"mvn-hosted": `{"items":[{"name":"widget-app","version":"1.0","group":"com.example","format":"maven2","assets":[
				{"path":"com/example/widget-app/1.0/widget-app-1.0.jar",
				 "downloadUrl":"%BASE%/repository/mvn-hosted/com/example/widget-app/1.0/widget-app-1.0.jar",
				 "contentType":"application/java-archive","fileSize":3}]}],"continuationToken":null}`,
		},
		assetPages: map[string]string{
			"mvn-hosted": `{"items":[
				{"path":"com/example/widget-app/1.0/widget-app-1.0.jar","downloadUrl":"%BASE%/repository/mvn-hosted/com/example/widget-app/1.0/widget-app-1.0.jar","contentType":"application/java-archive","fileSize":3},
				{"path":"com/example/widget-app/maven-metadata.xml","downloadUrl":"%BASE%/repository/mvn-hosted/com/example/widget-app/maven-metadata.xml","contentType":"application/xml","fileSize":10},
				{"path":"com/example/widget-app/maven-metadata.xml.sha1","downloadUrl":"%BASE%/repository/mvn-hosted/com/example/widget-app/maven-metadata.xml.sha1","contentType":"text/plain","fileSize":40}
			],"continuationToken":null}`,
		},
		files: map[string]string{"/repository/mvn-hosted/com/example/widget-app/1.0/widget-app-1.0.jar": "jar"},
	}
	h := newMigHarness(t, fake)
	fake.components["mvn-hosted"] = strings.ReplaceAll(fake.components["mvn-hosted"], "%BASE%", h.nexus.URL)
	fake.assetPages["mvn-hosted"] = strings.ReplaceAll(fake.assetPages["mvn-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	assert.Zero(t, done.ErrorCount,
		"maven-metadata.xml is generated dynamically, its absence from the plan is by design: %v", done.LastError)
}

// An older Nexus without the assets endpoint (or a hardened one refusing it)
// must not fail the migration — the completeness check is best-effort.
func TestNexusMigration_AssetListingUnavailable_Tolerated(t *testing.T) {
	fake := &fakeNexus{
		settings: `[{"name":"raw-hosted","format":"raw","type":"hosted","online":true}]`,
		components: map[string]string{
			"raw-hosted": `{"items":[{"name":"a.txt","version":null,"group":"/","format":"raw","assets":[
				{"path":"a.txt","downloadUrl":"%BASE%/repository/raw-hosted/a.txt",
				 "contentType":"text/plain","fileSize":5}]}],"continuationToken":null}`,
		},
		assetsCode: 404,
		files:      map[string]string{"/repository/raw-hosted/a.txt": "hello"},
	}
	h := newMigHarness(t, fake)
	fake.components["raw-hosted"] = strings.ReplaceAll(fake.components["raw-hosted"], "%BASE%", h.nexus.URL)

	job := h.startJob(t)
	done := h.waitForStatus(t, job.ID, domain.MigrationDone)

	assert.Zero(t, done.ErrorCount)
	assert.Equal(t, int64(1), done.DoneAssets)
}

// ── a panicking run must not wedge the job ───────────────────────────────────

// panickingJobs blows up where the runner stamps the job started — the shape
// of any nil dereference or bad-shape panic inside a detached migration.
type panickingJobs struct {
	*testutil.MigrationRepo
}

func (panickingJobs) SetStarted(_ context.Context, _ string, _ time.Time) error {
	panic("migration runner blew up")
}

// safego.Go keeps a panicking run from taking the process down, but the job it
// left behind said "running" forever — IsActive stops a second run from
// starting and IsResumable refuses to relaunch it, so the operator holds a job
// that can neither finish nor be retried. The run has to settle its own record
// on the way out.
func TestNexusMigration_RunPanics_JobIsMarkedError(t *testing.T) {
	fake := &fakeNexus{settings: `[]`}
	h := newMigHarness(t, fake)
	h.svc = service.NewNexusMigrationService(service.NexusMigrationConfig{
		Jobs:          panickingJobs{h.jobs},
		Repos:         service.NewRepositoryService(h.repos, testutil.NewBlobStoreRepo(), h.blobs, testutil.NewCleanupPolicyRepo()),
		Users:         h.users,
		Roles:         h.roles,
		Privileges:    h.privileges,
		RoutingRules:  h.rules,
		Deps:          formats.Deps{Repos: h.repos, Components: h.components, Assets: h.assets, BlobStore: h.blobs, BaseURL: "http://nexspence.test"},
		JWTSecret:     "unit-test-secret",
		Log:           zap.NewNop().Sugar(),
		HTTPClientFor: func(time.Duration) *http.Client { return h.nexus.Client() },
	})

	job := &domain.MigrationJob{
		SourceURL: h.nexus.URL, SourceUser: "admin",
		Status: domain.MigrationPending, MigrateRepos: true,
	}
	require.NoError(t, h.svc.Create(context.Background(), job, "s3cret"))

	settled := h.waitForStatus(t, job.ID, domain.MigrationError)
	require.NotNil(t, settled.LastError)
	assert.Contains(t, *settled.LastError, "panic")
	assert.True(t, settled.IsResumable(), "a settled job must be retryable")
}
