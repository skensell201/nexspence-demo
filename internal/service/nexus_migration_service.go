package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/netguard"
	"github.com/nexspence-oss/nexspence/internal/nexusclient"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/safego"
	"github.com/nexspence-oss/nexspence/internal/tracing"
)

// errMigrationPaused unwinds a run that an operator parked. It is not a
// failure: the job keeps its progress and can be resumed.
var errMigrationPaused = errors.New("migration paused")

const (
	// migrationHTTPTimeout bounds a single request to the source Nexus. Asset
	// downloads share it, so it is generous rather than snappy.
	migrationHTTPTimeout = 5 * time.Minute
	// previewTimeout bounds the read-only reachability check, which runs inside
	// an operator's request and must answer quickly either way.
	previewTimeout = 10 * time.Second
)

// nexusFormats maps a Nexus repository format onto the Nexspence format that
// speaks the same protocol. A format absent from this map has no handler here,
// so its repository is reported and skipped rather than created half-working.
var nexusFormats = map[string]domain.RepoFormat{
	"maven2":    domain.FormatMaven2,
	"npm":       domain.FormatNPM,
	"docker":    domain.FormatDocker,
	"pypi":      domain.FormatPyPI,
	"go":        domain.FormatGo,
	"nuget":     domain.FormatNuGet,
	"helm":      domain.FormatHelm,
	"raw":       domain.FormatRaw,
	"apt":       domain.FormatApt,
	"yum":       domain.FormatYum,
	"cargo":     "cargo",
	"conan":     "conan",
	"conda":     "conda",
	"terraform": "terraform",
	"rubygems":  domain.FormatRubyGems,
}

// nexusPrivilegeTypes maps Nexus privilege types onto the Nexspence ones. The
// two products share the model, so this is a rename rather than a translation.
var nexusPrivilegeTypes = map[string]domain.PrivilegeType{
	"wildcard":                    domain.PrivilegeTypeWildcard,
	"repository-view":             domain.PrivilegeTypeRepositoryView,
	"repository-admin":            domain.PrivilegeTypeRepositoryAdmin,
	"application":                 domain.PrivilegeTypeApplication,
	"script":                      domain.PrivilegeTypeScript,
	"repository-content-selector": domain.PrivilegeTypeRepositoryContentSelector,
}

// MigrationUserStore is the slice of user management a migration needs:
// look an account up, and create one with a password. *UserService satisfies it.
type MigrationUserStore interface {
	Get(ctx context.Context, username string) (*domain.User, error)
	Create(ctx context.Context, u *domain.User, plainPassword string) error
}

// NexusMigrationConfig holds the collaborators of a NexusMigrationService.
type NexusMigrationConfig struct {
	Jobs         repository.MigrationRepo
	Repos        *RepositoryService
	Users        MigrationUserStore
	Roles        repository.RoleRepo
	Privileges   repository.PrivilegeRepo
	RoutingRules repository.RoutingRuleRepo
	// Selectors re-creates the source's content selectors (and validates their
	// translated CEL), so repository-content-selector privileges can resolve
	// the selector they name instead of failing the DB constraint.
	Selectors *ContentSelectorService
	// Deps is the storage waist every format handler writes through; the
	// migration ingests assets the same way an upload does.
	Deps formats.Deps
	// JWTSecret and EncryptionKey seal the stored source credential, exactly as
	// they do for replication targets.
	JWTSecret     string
	EncryptionKey []byte
	Log           logger.Logger
	// HTTPClientFor builds the client used to reach the source Nexus. Defaults
	// to the SSRF-guarded netguard client; tests override it for loopback.
	HTTPClientFor func(timeout time.Duration) *http.Client
}

// NexusMigrationService runs Nexus → Nexspence migration jobs: it reads a
// source instance over its REST API and re-creates its repositories, artifacts,
// security model and routing rules here.
//
// One job runs in one goroutine. Pausing cancels it; resuming starts a fresh
// pass that skips whatever is already present, which is also what makes a job
// survive a process restart — ResumeAll re-attaches to jobs left running.
type NexusMigrationService struct {
	jobs         repository.MigrationRepo
	repos        *RepositoryService
	users        MigrationUserStore
	roles        repository.RoleRepo
	privileges   repository.PrivilegeRepo
	routingRules repository.RoutingRuleRepo
	selectors    *ContentSelectorService
	deps         formats.Deps
	log          logger.Logger

	primaryKey []byte
	legacyKey  []byte

	newClient func(timeout time.Duration) *http.Client

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	// dones lets Pause block until the runner has actually unwound: the channel
	// is created in launch and closed in run's deferred cleanup, AFTER the final
	// status is written. Returning from Pause on cancel() alone raced Resume
	// (a second overlapping run) and DeleteJob (deleting the row under a
	// still-writing goroutine) — #342.
	dones map[string]chan struct{}
}

// NewNexusMigrationService constructs the migration runner.
func NewNexusMigrationService(cfg NexusMigrationConfig) *NexusMigrationService {
	s := &NexusMigrationService{
		jobs:         cfg.Jobs,
		repos:        cfg.Repos,
		users:        cfg.Users,
		roles:        cfg.Roles,
		privileges:   cfg.Privileges,
		routingRules: cfg.RoutingRules,
		selectors:    cfg.Selectors,
		deps:         cfg.Deps,
		log:          cfg.Log,
		newClient:    cfg.HTTPClientFor,
		cancels:      make(map[string]context.CancelFunc),
		dones:        make(map[string]chan struct{}),
	}
	if s.newClient == nil {
		s.newClient = netguard.Client
	}
	legacy := deriveKey(cfg.JWTSecret)
	if len(cfg.EncryptionKey) == 32 {
		s.primaryKey = cfg.EncryptionKey
		s.legacyKey = legacy
	} else {
		s.primaryKey = legacy
	}
	return s
}

// SealPassword encrypts a Nexus password for storage on the job row. The
// error is real: swallowing it used to return "", indistinguishable from "no
// password provided", and the job then authenticated with an empty credential
// forever, failing every item with a misleading per-item error (#342).
func (s *NexusMigrationService) SealPassword(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	sealed, err := sealWithKey(s.primaryKey, plain)
	if err != nil {
		return "", fmt.Errorf("seal the source credential: %w", err)
	}
	return sealed, nil
}

// OpenPassword decrypts a stored source credential, falling back to the legacy
// jwt-derived key for rows sealed before a dedicated key was configured.
func (s *NexusMigrationService) OpenPassword(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	plain, err := openWithKey(s.primaryKey, sealed)
	if err == nil {
		return plain, nil
	}
	if s.legacyKey != nil {
		if lp, lerr := openWithKey(s.legacyKey, sealed); lerr == nil {
			return lp, nil
		}
	}
	return "", err
}

// ── lifecycle ───────────────────────────────────────────────────────────────

// Create validates the job, seals its credential, persists it and starts the
// run. The job row exists before the run begins, so a source that turns out to
// be unreachable is reported on the job rather than swallowed by the request.
func (s *NexusMigrationService) Create(ctx context.Context, job *domain.MigrationJob, password string) error {
	if err := validateNexusURL(job.SourceURL); err != nil {
		return err
	}
	job.SourceURL = strings.TrimRight(strings.TrimSpace(job.SourceURL), "/")
	job.Status = domain.MigrationPending
	sealed, err := s.SealPassword(password)
	if err != nil {
		return err
	}
	job.SourcePassword = sealed

	if err := s.jobs.Create(ctx, job); err != nil {
		return err
	}
	s.launch(job.ID)
	return nil
}

// pauseUnwindCeiling caps how long Pause waits for the runner to unwind. The
// runner reacts to cancellation within one aborted request in practice; the
// ceiling only guards against a pathological source that ignores the closed
// connection.
const pauseUnwindCeiling = 30 * time.Second

// Pause stops the run and parks the job. A job that is not running is parked
// where it stands, so an operator can stop one before it ever starts.
//
// Pause returns only after the runner has actually unwound and written the
// final state (#342): returning on cancel() alone let an immediate Resume
// no-op against the still-registered run (or start a second overlapping one),
// and let DeleteJob remove the row under a goroutine still writing progress.
func (s *NexusMigrationService) Pause(ctx context.Context, id string) error {
	job, err := s.jobs.Get(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	cancel, running := s.cancels[id]
	done := s.dones[id]
	s.mu.Unlock()
	if running {
		// The runner writes the paused status when it unwinds, so progress
		// recorded between here and there is not lost.
		cancel()
		if done != nil {
			select {
			case <-done:
			case <-time.After(pauseUnwindCeiling):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	if !job.IsActive() {
		return fmt.Errorf("%w: migration job is already %s", ErrInvalidInput, job.Status)
	}
	return s.jobs.UpdateStatus(ctx, id, domain.MigrationPaused)
}

// Resume starts a fresh pass over the job. Everything already migrated is
// skipped, so resuming costs only the work that is left. Only a paused or
// errored job is resumable (#342): a finished one would relist the entire
// source for nothing, and Pause's status guard deserves a symmetric sibling.
func (s *NexusMigrationService) Resume(ctx context.Context, id string) error {
	job, err := s.jobs.Get(ctx, id)
	if err != nil {
		return err
	}
	if !job.IsResumable() {
		return fmt.Errorf("%w: migration job is %s, not resumable", ErrInvalidInput, job.Status)
	}
	s.launch(id)
	return nil
}

// ResumeAll re-attaches to every job that was still active when the process
// stopped. Called once on startup: without it a migration interrupted by a
// restart would sit in "running" forever with nothing behind it.
func (s *NexusMigrationService) ResumeAll(ctx context.Context) error {
	active, err := s.jobs.ListActive(ctx)
	if err != nil {
		return err
	}
	for _, job := range active {
		s.launch(job.ID)
	}
	return nil
}

// launch starts the runner unless this process is already running the job.
func (s *NexusMigrationService) launch(id string) {
	s.mu.Lock()
	if _, busy := s.cancels[id]; busy {
		s.mu.Unlock()
		return
	}
	//nolint:gosec // the run must outlive the request; cancel is stored below and always invoked in run's defer
	ctx, cancel := context.WithCancel(context.Background())
	s.cancels[id] = cancel
	s.dones[id] = make(chan struct{})
	s.mu.Unlock()

	safego.Go(s.log, "nexus-migration-run", func() { s.run(ctx, id) })
}

// ── preview ─────────────────────────────────────────────────────────────────

// NexusPreviewRepo is one repository as reported by a preflight check.
type NexusPreviewRepo struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Type   string `json:"type"`
}

// NexusPreview is the result of a preflight "can we reach this Nexus" check.
type NexusPreview struct {
	Reachable bool               `json:"reachable"`
	RepoCount int                `json:"repoCount"`
	Repos     []NexusPreviewRepo `json:"repos"`
}

// Preview reads the repository list from a Nexus instance without creating
// anything. It is the "Test connection" path: pure read, safe to repeat.
func (s *NexusMigrationService) Preview(ctx context.Context, sourceURL, username, password string) (*NexusPreview, error) {
	if err := validateNexusURL(sourceURL); err != nil {
		return nil, err
	}
	client := s.clientFor(sourceURL, username, password, previewTimeout)
	repos, err := client.ListRepositories(ctx)
	if err != nil {
		return nil, err
	}
	out := &NexusPreview{Reachable: true, RepoCount: len(repos), Repos: make([]NexusPreviewRepo, 0, len(repos))}
	for _, r := range repos {
		out.Repos = append(out.Repos, NexusPreviewRepo{Name: r.Name, Format: r.Format, Type: r.Type})
	}
	return out, nil
}

func (s *NexusMigrationService) clientFor(sourceURL, username, password string, timeout time.Duration) *nexusclient.Client {
	return nexusclient.New(sourceURL, username, password, timeout).
		WithHTTPClient(s.newClient(timeout))
}

func validateNexusURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("%w: sourceUrl is required", ErrInvalidInput)
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: sourceUrl must be an absolute http(s) URL", ErrInvalidInput)
	}
	return nil
}

// ── the run ─────────────────────────────────────────────────────────────────

// progress accumulates what a run has done and writes it through to the job row.
// Every counter update goes through here, so the numbers an operator watches and
// the numbers the runner acts on are the same numbers.
type progress struct {
	jobs  repository.MigrationRepo
	jobID string

	totalRepos  int
	doneRepos   int
	totalAssets int64
	doneAssets  int64
	errorCount  int
	lastError   *string
}

func (p *progress) setTotals(ctx context.Context, totalRepos int, totalAssets int64) {
	p.totalRepos, p.totalAssets = totalRepos, totalAssets
	_ = p.jobs.SetTotals(ctx, p.jobID, totalRepos, totalAssets)
}

func (p *progress) flush(ctx context.Context) {
	_ = p.jobs.UpdateProgress(ctx, p.jobID, p.doneRepos, p.doneAssets, p.errorCount, p.lastError)
}

// fail records one non-fatal problem. A migration copies thousands of
// independent things; one that will not come across is counted and named, and
// the rest of the run continues.
func (p *progress) fail(ctx context.Context, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	p.errorCount++
	p.lastError = &msg
	p.flush(ctx)
}

func (s *NexusMigrationService) run(ctx context.Context, jobID string) {
	defer func() {
		s.mu.Lock()
		if cancel, ok := s.cancels[jobID]; ok {
			cancel()
			delete(s.cancels, jobID)
		}
		// Closed AFTER the final status write below (this defer runs last):
		// whoever unblocks from Pause reads the already-settled job.
		if done, ok := s.dones[jobID]; ok {
			close(done)
			delete(s.dones, jobID)
		}
		s.mu.Unlock()
	}()

	// Progress and status are written on a background context: a paused or
	// canceled run still has to record where it stopped.
	// The root span hangs off it too — the import goroutine has no HTTP
	// request behind it, so without one the run is invisible in traces (#302).
	bg, span := tracing.StartRoot(context.Background(), "nexus_migration.run",
		attribute.String("migration.job_id", jobID))
	defer span.End()

	job, err := s.jobs.Get(bg, jobID)
	if err != nil {
		s.logf(bg, "migration: cannot load job %s: %v", jobID, err)
		return
	}
	password, err := s.OpenPassword(job.SourcePassword)
	if err != nil {
		s.finish(bg, jobID, domain.MigrationError,
			fmt.Sprintf("cannot decrypt the stored Nexus credential: %v", err))
		return
	}
	if err := s.jobs.SetStarted(bg, jobID, time.Now().UTC()); err != nil {
		s.logf(bg, "migration: cannot mark job %s started: %v", jobID, err)
		return
	}

	client := s.clientFor(job.SourceURL, job.SourceUser, password, migrationHTTPTimeout)
	p := &progress{jobs: s.jobs, jobID: jobID}

	runErr := s.runStages(ctx, client, job, p)
	switch {
	case errors.Is(runErr, errMigrationPaused):
		_ = s.jobs.UpdateStatus(bg, jobID, domain.MigrationPaused)
	case runErr != nil:
		s.finish(bg, jobID, domain.MigrationError, runErr.Error())
	default:
		_ = s.jobs.FinishJob(bg, jobID, domain.MigrationDone, nil)
	}
}

func (s *NexusMigrationService) finish(ctx context.Context, jobID string, status domain.MigrationJobStatus, msg string) {
	_ = s.jobs.FinishJob(ctx, jobID, status, &msg)
}

// runStages walks the fixed sequence. Each stage is gated by its own scope
// flag, tolerates per-item problems internally (counted on the job), and
// returns a hard error only when it cannot proceed at all. A hard error in one
// stage must not rob the independent stages after it of their chance to run —
// on a real source, one broken LDAP record 500'd the users listing and
// routing rules never even started (#342). Every stage failure is named and
// collected; only a pause aborts the walk outright, because a pause must not
// leave later stages half-done behind the operator's back.
func (s *NexusMigrationService) runStages(ctx context.Context, client *nexusclient.Client,
	job *domain.MigrationJob, p *progress,
) error {
	bg := context.Background()

	var stageErrs []error
	runStage := func(name string, fn func() error) error {
		err := fn()
		if err == nil {
			return nil
		}
		if errors.Is(err, errMigrationPaused) {
			return err
		}
		stageErrs = append(stageErrs, fmt.Errorf("%s: %w", name, err))
		return nil
	}

	if job.MigrateRepos {
		var hosted []migratedRepo
		if err := runStage("repositories", func() error {
			var e error
			hosted, e = s.migrateRepositories(ctx, client, p)
			return e
		}); err != nil {
			return err
		}
		// Blobs genuinely depend on the repository listing — hosted is empty
		// when that stage failed hard, so there is nothing to transfer.
		if job.MigrateBlobs && len(hosted) > 0 {
			if err := runStage("blobs", func() error {
				return s.migrateAssets(ctx, client, hosted, p)
			}); err != nil {
				return err
			}
		}
	}

	// Privileges first: a role references them by name, so they must exist
	// before the roles that name them are wired up.
	if job.MigratePrivileges {
		if err := runStage("privileges", func() error { return s.migratePrivileges(ctx, client, p) }); err != nil {
			return err
		}
	}
	if job.MigrateRoles {
		if err := runStage("roles", func() error { return s.migrateRoles(ctx, client, p) }); err != nil {
			return err
		}
	}
	if job.MigrateUsers {
		if err := runStage("users", func() error { return s.migrateUsers(ctx, client, job, p) }); err != nil {
			return err
		}
	}
	if job.MigrateRoutingRules {
		if err := runStage("routing rules", func() error { return s.migrateRoutingRules(ctx, client, p) }); err != nil {
			return err
		}
	}
	p.flush(bg)
	if err := checkPaused(ctx); err != nil {
		return err
	}
	return errors.Join(stageErrs...)
}

func checkPaused(ctx context.Context) error {
	if ctx.Err() != nil {
		return errMigrationPaused
	}
	return nil
}

// ── repositories ────────────────────────────────────────────────────────────

// migratedRepo pairs a source repository with the name it was created under
// here — identical except when a docker/OCI name was repaired by lowercasing
// (#342): Nexus never validated the grammar docker clients parse names with.
type migratedRepo struct {
	src       nexusclient.Repository
	localName string
}

// migrateRepositories re-creates the source repositories here and returns the
// hosted ones, which are the only ones holding artifacts worth transferring.
func (s *NexusMigrationService) migrateRepositories(ctx context.Context, client *nexusclient.Client,
	p *progress,
) ([]migratedRepo, error) {
	bg := context.Background()

	source, err := client.ListRepositoriesWithConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("list repositories on the source Nexus: %w", err)
	}
	p.setTotals(bg, len(source), p.totalAssets)

	// hosted → proxy → group, so a group's members already exist when the
	// group naming them is created.
	ordered := make([]nexusclient.Repository, 0, len(source))
	for _, want := range []string{"hosted", "proxy", "group"} {
		for _, r := range source {
			if r.Type == want {
				ordered = append(ordered, r)
			}
		}
	}

	// srcByName lets a group expand a nested-group member from SOURCE data,
	// independent of what has migrated so far; localNames resolves a member
	// through any rename its repository picked up.
	srcByName := make(map[string]nexusclient.Repository, len(source))
	for _, r := range source {
		srcByName[r.Name] = r
	}
	localNames := make(map[string]string, len(source))

	var hosted []migratedRepo
	for _, src := range ordered {
		if err := checkPaused(ctx); err != nil {
			return nil, err
		}
		format, ok := nexusFormats[src.Format]
		if !ok {
			p.fail(bg, "repository %q: format %q has no Nexspence equivalent", src.Name, src.Format)
			continue
		}
		localName := migratedRepoName(src.Name, format)
		if err := s.ensureRepository(ctx, src, format, localName, srcByName, localNames, p); err != nil {
			p.fail(bg, "repository %q: %v", src.Name, err)
			continue
		}
		localNames[src.Name] = localName
		p.doneRepos++
		p.flush(bg)
		if src.Type == "hosted" {
			hosted = append(hosted, migratedRepo{src: src, localName: localName})
		}
	}
	return hosted, nil
}

// migratedRepoName is the name the source repository is created under here.
// A docker/OCI name failing the naming grammar ONLY on letter case is
// lowercased — Nexus itself never validated it, so a real source can hold
// "Docker-Group" (#342). A name invalid beyond case is returned unchanged and
// fails the same create-time validation as before.
func migratedRepoName(name string, format domain.RepoFormat) string {
	if validateNameForFormat(name, format) == nil {
		return name
	}
	lower := strings.ToLower(name)
	if lower != name && validateNameForFormat(lower, format) == nil {
		return lower
	}
	return name
}

// ensureRepository creates the repository unless one of that name is already
// here. An existing repository is left exactly as it is: a re-run must not
// rewrite configuration an operator has since changed.
func (s *NexusMigrationService) ensureRepository(ctx context.Context, src nexusclient.Repository,
	format domain.RepoFormat, localName string,
	srcByName map[string]nexusclient.Repository, localNames map[string]string, p *progress,
) error {
	existing, err := s.deps.Repos.Get(ctx, localName)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if existing != nil {
		return nil
	}

	repo := &domain.Repository{
		Name:        localName,
		Format:      format,
		Type:        domain.RepoType(src.Type),
		Online:      true,
		Description: "Migrated from Nexus",
	}
	switch src.Type {
	case "proxy":
		if src.RemoteURL == "" {
			return fmt.Errorf("proxy repository has no remote URL on the source")
		}
		repo.ProxyConfig = map[string]any{"remote_url": src.RemoteURL}
	case "group":
		// Nexus allows a group to nest another group; this schema does not. A
		// nested member is expanded into the leaves it aggregates — resolved
		// from the SOURCE data, so the outcome is independent of iteration
		// order — instead of failing the entire outer group (#342).
		members := make([]string, 0, len(src.MemberNames))
		for _, name := range flattenGroupMembers(src, srcByName) {
			local, ok := localNames[name]
			if !ok {
				local = name
			}
			if _, err := s.deps.Repos.Get(ctx, local); err == nil {
				members = append(members, local)
				continue
			}
			p.fail(context.Background(),
				"group %q: member %q was not migrated and is left out", src.Name, name)
		}
		repo.FormatConfig = map[string]any{"member_names": members}
	}
	return s.repos.Create(ctx, repo)
}

// flattenGroupMembers expands a source group's member list into the leaf
// (hosted/proxy) repositories it ultimately aggregates, deduplicated in first-
// seen order. A group already being expanded is not re-entered, so a reference
// cycle just stops contributing further members — the same guard the role
// flattening uses. An unknown member name is kept as a leaf; the caller's
// existence check reports it.
func flattenGroupMembers(src nexusclient.Repository, byName map[string]nexusclient.Repository) []string {
	var out []string
	seen := make(map[string]bool)
	expanded := map[string]bool{src.Name: true}
	var walk func(names []string)
	walk = func(names []string) {
		for _, n := range names {
			if member, known := byName[n]; known && member.Type == "group" {
				if expanded[n] {
					continue
				}
				expanded[n] = true
				walk(member.MemberNames)
				continue
			}
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	walk(src.MemberNames)
	return out
}

// ── assets ──────────────────────────────────────────────────────────────────

// plannedAsset is one unit to transfer.
//
// For an ordinary format that is one file. For an OCI registry it is one
// manifest, which brings the blobs it names along with it — see
// transferOCIManifest for why those cannot be planned from the listing.
type plannedAsset struct {
	repo        string
	path        string // Nexspence asset path, leading slash
	downloadURL string
	contentType string
	size        int64
	coords      base.Coords
	// isOCIManifest marks a registry manifest: image is coords.Name and the tag
	// or digest is coords.Version.
	isOCIManifest bool
}

func (s *NexusMigrationService) migrateAssets(ctx context.Context, client *nexusclient.Client,
	hosted []migratedRepo, p *progress,
) error {
	bg := context.Background()

	for _, m := range hosted {
		if err := checkPaused(ctx); err != nil {
			return err
		}
		planned, err := s.planAssets(ctx, client, m, p)
		if err != nil {
			p.fail(bg, "repository %q: listing components: %v", m.src.Name, err)
			continue
		}
		p.setTotals(bg, p.totalRepos, p.totalAssets+int64(len(planned)))

		for _, a := range planned {
			if err := checkPaused(ctx); err != nil {
				return err
			}
			if err := s.transferAsset(ctx, client, a); err != nil {
				p.fail(bg, "asset %s%s: %v", a.repo, a.path, err)
				continue
			}
			p.doneAssets++
			p.flush(bg)
		}
	}
	return nil
}

// planAssets lists every component in the repository and turns it into the set
// of files to transfer, ordered so a manifest never lands before its blobs.
func (s *NexusMigrationService) planAssets(ctx context.Context, client *nexusclient.Client,
	m migratedRepo, p *progress,
) ([]plannedAsset, error) {
	src := m.src
	isOCI := nexusFormats[src.Format].IsOCIRegistry()

	var planned []plannedAsset
	token := ""
	for {
		if err := checkPaused(ctx); err != nil {
			return nil, err
		}
		components, next, err := client.ListComponents(ctx, src.Name, token)
		if err != nil {
			return nil, err
		}
		for _, comp := range components {
			for _, a := range comp.Assets {
				pa := plannedAsset{
					repo:        m.localName,
					downloadURL: a.DownloadURL,
					contentType: a.ContentType,
					size:        a.SizeBytes,
					coords:      base.Coords{Group: comp.Group, Name: comp.Name, Version: comp.Version},
				}
				if isOCI {
					translated, ok := ociManifestPath(a.Path)
					if !ok {
						// Every other registry path is a blob, and Nexus lists
						// those under a placeholder image name that no client
						// could pull them from. They come across with the
						// manifest that names them instead.
						continue
					}
					pa.path = translated.path
					pa.isOCIManifest = true
					pa.coords = base.Coords{Name: translated.image, Version: translated.reference}
				} else {
					pa.path = normalizeAssetPath(a.Path)
				}
				if pa.contentType == "" {
					pa.contentType = "application/octet-stream"
				}
				planned = append(planned, pa)
			}
		}
		if next == "" {
			break
		}
		token = next
	}

	// The Components API only lists assets it can group under a component;
	// anything else (#350: both maven-metadata.xml shapes and their checksum
	// sidecars) is invisible to the plan. Reconcile against the FULL asset
	// listing so nothing vanishes without a trace. OCI repositories are
	// exempt: their blob assets are deliberately unplanned — they come across
	// with the manifests that name them.
	if !isOCI {
		s.reconcilePlanAgainstAssetListing(ctx, client, m, planned, p)
	}

	return planned, nil
}

// reconcilePlanAgainstAssetListing walks GET /service/rest/v1/assets and
// records every asset the component-based plan missed. maven-metadata.xml and
// its sidecars are the known — and by design unmigrated — population: both
// shapes are generated dynamically from stored components on every GET, so a
// literal copy would only go stale. Anything else is counted through the
// job's error path. A source without the endpoint skips the check.
func (s *NexusMigrationService) reconcilePlanAgainstAssetListing(ctx context.Context,
	client *nexusclient.Client, m migratedRepo, planned []plannedAsset, p *progress,
) {
	known := make(map[string]bool, len(planned))
	for _, pa := range planned {
		known[pa.path] = true
	}
	token := ""
	for {
		if err := checkPaused(ctx); err != nil {
			return
		}
		assets, next, err := client.ListAssets(ctx, m.src.Name, token)
		if err != nil {
			s.logf(ctx, "migration: asset listing unavailable for %s — completeness check skipped: %v", m.src.Name, err)
			return
		}
		for _, a := range assets {
			ap := normalizeAssetPath(a.Path)
			if known[ap] || isMavenMetadataSidecarPath(ap) {
				continue
			}
			p.fail(ctx, "repo %s: asset %s has no owning component and was not planned for transfer", m.src.Name, ap)
		}
		if next == "" {
			return
		}
		token = next
	}
}

// isMavenMetadataSidecarPath reports whether the path names an aggregate or
// per-version maven-metadata.xml or one of its checksum sidecars.
func isMavenMetadataSidecarPath(p string) bool {
	name := path.Base(p)
	if name == "maven-metadata.xml" {
		return true
	}
	for _, ext := range []string{".sha1", ".md5", ".sha256"} {
		if name == "maven-metadata.xml"+ext {
			return true
		}
	}
	return false
}

// transferAsset brings one planned unit across, skipping whatever is already
// here — which is what makes a resumed or repeated job cheap instead of
// destructive.
func (s *NexusMigrationService) transferAsset(ctx context.Context, client *nexusclient.Client, a plannedAsset) error {
	if a.isOCIManifest {
		return s.transferOCIManifest(ctx, client, a.repo, a.coords.Name, a.coords.Version, 0)
	}

	existing, err := s.deps.Assets.GetByPath(ctx, a.repo, a.path)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if existing != nil {
		return nil
	}

	body, err := client.DownloadAsset(ctx, a.downloadURL)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	_, err = base.StoreArtifact(ctx, s.deps, a.repo, a.path, a.contentType, a.coords, body, a.size)
	return err
}

// maxManifestDepth bounds index → manifest recursion. One level is what the
// spec describes; the bound is there so a source that points an index at
// itself cannot spin this forever.
const maxManifestDepth = 4

// ociManifest is the slice of a manifest document the migration reads: what it
// references, and what kind of document it is.
type ociManifestDoc struct {
	MediaType string `json:"mediaType"`
	Config    struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Digest string `json:"digest"`
	} `json:"layers"`
	// Manifests is non-empty on an index (a multi-platform image), whose
	// children are manifests rather than blobs.
	Manifests []struct {
		Digest string `json:"digest"`
	} `json:"manifests"`
}

// transferOCIManifest brings one image manifest across along with everything it
// names, in that order: blobs first, then the manifest, so the registry never
// serves a manifest whose layers are still missing.
//
// The blobs cannot be taken from the component listing. Nexus stores registry
// blobs content-addressed and lists them under a placeholder image name
// ("/v2/-/blobs/<digest>"), which is not a path any client can pull from — and
// its component listing reports only the manifest in the first place. The
// manifest is therefore read, parsed, and used as the index of what to fetch.
func (s *NexusMigrationService) transferOCIManifest(ctx context.Context, client *nexusclient.Client,
	repoName, image, reference string, depth int,
) error {
	if depth > maxManifestDepth {
		return fmt.Errorf("manifest %s/%s nests deeper than %d levels", image, reference, maxManifestDepth)
	}

	body, contentType, err := client.DownloadManifest(ctx, repoName, image, reference)
	if err != nil {
		return err
	}

	var doc ociManifestDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("parse manifest %s:%s: %w", image, reference, err)
	}
	if contentType == "" {
		contentType = doc.MediaType
	}
	if contentType == "" {
		contentType = "application/vnd.docker.distribution.manifest.v2+json"
	}

	// An index names manifests; a manifest names blobs. Either way the things
	// it points at are stored before it is.
	for _, child := range doc.Manifests {
		if child.Digest == "" {
			continue
		}
		if err := s.transferOCIManifest(ctx, client, repoName, image, child.Digest, depth+1); err != nil {
			return err
		}
	}
	digests := make([]string, 0, len(doc.Layers)+1)
	if doc.Config.Digest != "" {
		digests = append(digests, doc.Config.Digest)
	}
	for _, l := range doc.Layers {
		if l.Digest != "" {
			digests = append(digests, l.Digest)
		}
	}
	for _, digest := range digests {
		if err := s.ensureOCIBlob(ctx, client, repoName, image, digest); err != nil {
			return err
		}
	}

	manifestPath := "/manifests/" + image + "/" + reference
	existing, err := s.deps.Assets.GetByPath(ctx, repoName, manifestPath)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if existing != nil {
		return nil
	}

	coords := base.Coords{Name: image, Version: reference}
	res, err := base.StoreArtifact(ctx, s.deps, repoName, manifestPath, contentType, coords,
		bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	s.registerManifestDigestAlias(ctx, plannedAsset{
		repo: repoName, contentType: contentType, coords: coords,
	}, res)
	return nil
}

// ensureOCIBlob copies one blob unless the image already has it. Layers are
// shared between images, so this check is what keeps a repository-wide
// migration from re-transferring the same base layer for every tag.
func (s *NexusMigrationService) ensureOCIBlob(ctx context.Context, client *nexusclient.Client,
	repoName, image, digest string,
) error {
	blobPath := "/blobs/" + image + "/" + digest
	existing, err := s.deps.Assets.GetByPath(ctx, repoName, blobPath)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if existing != nil {
		return nil
	}

	body, err := client.DownloadBlob(ctx, repoName, image, digest)
	if err != nil {
		return fmt.Errorf("blob %s: %w", digest, err)
	}
	defer func() { _ = body.Close() }()

	_, err = base.StoreArtifact(ctx, s.deps, repoName, blobPath, "application/octet-stream",
		base.Coords{Name: image, Version: digest}, body, 0)
	return err
}

// registerManifestDigestAlias points the manifest's content digest at the same
// stored bytes, the way a registry push does: a client that pulls by tag
// immediately re-fetches the manifest by digest, and gets a 404 without this.
func (s *NexusMigrationService) registerManifestDigestAlias(ctx context.Context, a plannedAsset, res *base.StoreResult) {
	digestRef := "sha256:" + res.SHA256
	if a.coords.Version == digestRef {
		return // already stored under its digest
	}
	repo, err := s.deps.Repos.Get(ctx, a.repo)
	if err != nil || repo == nil {
		return
	}
	aliasPath := "/manifests/" + a.coords.Name + "/" + digestRef
	if _, err := base.RegisterStoredBlob(ctx, s.deps, repo,
		aliasPath, a.contentType,
		base.Coords{Name: a.coords.Name, Version: digestRef},
		res.Asset.BlobKey,
		res.SHA256, res.SHA1, res.MD5, res.Size,
		res.Asset.BlobStoreID, "",
	); err != nil {
		s.logf(ctx, "migration: cannot register digest alias %s%s: %v", a.repo, aliasPath, err)
	}
}

// normalizeAssetPath gives a Nexus asset path the leading slash every path
// stored here carries.
func normalizeAssetPath(p string) string {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

type ociPath struct {
	path      string
	image     string
	reference string
}

// ociManifestPath rewrites a Nexus registry manifest path —
// "/v2/<image>/manifests/<tag-or-digest>" — into the layout the OCI handler
// here reads. Anything else, including a blob path, is not a manifest and is
// reported as such.
func ociManifestPath(raw string) (ociPath, bool) {
	p := strings.TrimPrefix(normalizeAssetPath(raw), "/")
	p = strings.TrimPrefix(p, "v2/")

	i := strings.LastIndex(p, "/manifests/")
	if i <= 0 {
		return ociPath{}, false
	}
	image, reference := p[:i], p[i+len("/manifests/"):]
	if image == "" || reference == "" {
		return ociPath{}, false
	}
	return ociPath{
		path:      "/manifests/" + image + "/" + reference,
		image:     image,
		reference: reference,
	}, true
}

// ── privileges ──────────────────────────────────────────────────────────────

func (s *NexusMigrationService) migratePrivileges(ctx context.Context, client *nexusclient.Client, p *progress) error {
	bg := context.Background()

	// Content selectors first: a repository-content-selector privilege is
	// rejected by the DB outright unless it names its selector, so without
	// this every such privilege fails with a raw constraint violation (#342).
	selectorIDs, err := s.migrateContentSelectors(ctx, client, p)
	if err != nil {
		return err
	}

	source, err := client.ListPrivileges(ctx)
	if err != nil {
		return fmt.Errorf("list privileges on the source Nexus: %w", err)
	}
	for _, src := range source {
		if err := checkPaused(ctx); err != nil {
			return err
		}
		if src.ReadOnly {
			continue // a Nexus built-in; this instance ships its own
		}
		if existing, _ := s.privileges.GetByName(ctx, src.Name); existing != nil {
			continue
		}
		privType, ok := nexusPrivilegeTypes[src.Type]
		if !ok {
			p.fail(bg, "privilege %q: unknown type %q", src.Name, src.Type)
			continue
		}
		priv := &domain.Privilege{
			Name:        src.Name,
			Description: src.Description,
			Type:        privType,
			Attrs:       normalizePrivilegeAttrs(src.Attrs),
		}
		if privType == domain.PrivilegeTypeRepositoryContentSelector {
			selName, _ := src.Attrs["contentSelector"].(string)
			id, ok := selectorIDs[selName]
			if !ok {
				p.fail(bg, "privilege %q: content selector %q was not migrated (missing on the source, or its expression could not be translated)", src.Name, selName)
				continue
			}
			priv.ContentSelectorID = &id
		}
		if err := s.privileges.Create(ctx, priv); err != nil {
			p.fail(bg, "privilege %q: %v", src.Name, err)
		}
	}
	return nil
}

// migrateContentSelectors re-creates the source's content selectors and
// returns a name→ID map for resolving privileges. A selector whose expression
// cannot be translated or validated is a per-item failure, not fatal.
func (s *NexusMigrationService) migrateContentSelectors(ctx context.Context, client *nexusclient.Client, p *progress) (map[string]string, error) {
	bg := context.Background()
	ids := make(map[string]string)
	if s.selectors == nil {
		return ids, nil
	}
	source, err := client.ListContentSelectors(ctx)
	if err != nil {
		return nil, fmt.Errorf("list content selectors on the source Nexus: %w", err)
	}
	existing, err := s.selectors.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list local content selectors: %w", err)
	}
	byName := make(map[string]string, len(existing))
	for _, sel := range existing {
		byName[sel.Name] = sel.ID
	}
	for _, src := range source {
		if err := checkPaused(ctx); err != nil {
			return nil, err
		}
		if id, ok := byName[src.Name]; ok {
			ids[src.Name] = id // idempotent re-run
			continue
		}
		sel := &domain.ContentSelector{
			Name:        src.Name,
			Description: src.Description,
			Expression:  translateNexusSelectorExpression(src.Expression),
		}
		// Create validates against the exact CEL environment selectors are
		// evaluated under later, so a migrated selector is guaranteed to still
		// compile when it matters.
		if err := s.selectors.Create(ctx, sel); err != nil {
			p.fail(bg, "content selector %q: %v", src.Name, err)
			continue
		}
		ids[src.Name] = sel.ID
	}
	return ids, nil
}

// nexusSelectorRegexRe matches Nexus's "field =~ \"regex\"" — a Sonatype
// extension to CEL for regex match, a hard syntax error in plain CEL.
var nexusSelectorRegexRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.]*)\s*=~\s*"([^"]*)"`)

// translateNexusSelectorExpression rewrites a Nexus CSEL expression into the
// CEL dialect selectors are evaluated under here. Two divergences matter on
// real data (#342):
//
//   - `field =~ "regex"` becomes `field.matches("regex")`.
//   - Nexus's grammar passes a backslash straight through as a regex escape
//     (`\.` for a literal dot) — never an escape in its own string literal.
//     CEL disagrees: `\.` is a hard syntax error, and `\\` is the only way to
//     spell one literal backslash. Every backslash inside the captured
//     literal is doubled, which decodes back to exactly the regex Nexus meant.
//
// Anything else passes through unchanged; validation happens at Create.
func translateNexusSelectorExpression(expr string) string {
	return nexusSelectorRegexRe.ReplaceAllStringFunc(expr, func(m string) string {
		sub := nexusSelectorRegexRe.FindStringSubmatch(m)
		pattern := strings.ReplaceAll(sub[2], `\`, `\\`)
		return sub[1] + `.matches("` + pattern + `")`
	})
}

// nexusActionVocabulary maps Nexus's action names onto the four actions this
// RBAC engine understands (actionAllowed compares exact lowercase strings).
// "add" and "edit" are both write; an untranslated one silently strips
// push/delete rights from every migrated account (#342).
var nexusActionVocabulary = map[string]string{
	"read":   "read",
	"browse": "browse",
	"add":    "write",
	"edit":   "write",
	"delete": "delete",
}

// nexusActionAll is what Nexus's "all" expands to.
var nexusActionAll = []string{"read", "browse", "write", "delete"}

// normalizePrivilegeAttrs lowercases the action names and translates Nexus's
// vocabulary (add/edit → write, all → every action). Nexus reports them
// uppercase ("READ"); every check here compares against lowercase. An
// unrecognized action is kept as-is rather than silently dropped.
func normalizePrivilegeAttrs(attrs map[string]any) map[string]any {
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if k != "actions" {
			out[k] = v
			continue
		}
		raw, ok := v.([]any)
		if !ok {
			out[k] = v
			continue
		}
		seen := make(map[string]bool, len(raw))
		actions := make([]string, 0, len(raw))
		add := func(a string) {
			if !seen[a] {
				seen[a] = true
				actions = append(actions, a)
			}
		}
		for _, item := range raw {
			s, ok := item.(string)
			if !ok {
				continue
			}
			lower := strings.ToLower(s)
			if lower == "all" || lower == "*" {
				for _, a := range nexusActionAll {
					add(a)
				}
				continue
			}
			if translated, ok := nexusActionVocabulary[lower]; ok {
				add(translated)
				continue
			}
			add(lower)
		}
		out[k] = actions
	}
	return out
}

// ── roles ───────────────────────────────────────────────────────────────────

func (s *NexusMigrationService) migrateRoles(ctx context.Context, client *nexusclient.Client, p *progress) error {
	bg := context.Background()

	source, err := client.ListRoles(ctx)
	if err != nil {
		return fmt.Errorf("list roles on the source Nexus: %w", err)
	}

	byName := make(map[string]nexusclient.Role, len(source))
	for _, r := range source {
		if r.ReadOnly {
			continue // a Nexus built-in; this instance ships its own
		}
		byName[roleKey(r)] = r
	}

	ordered, cyclic := topoSortRoles(byName)
	if len(cyclic) > 0 {
		p.fail(bg, "roles %s reference each other in a cycle; each keeps only its own privileges",
			strings.Join(cyclic, ", "))
	}

	// Nexspence has no nested-role table, so a nested role is flattened: the
	// parent takes the union of its own privileges and everything it inherits.
	// Dependency order is what makes one pass enough.
	effective := make(map[string]map[string]bool, len(ordered))
	for _, name := range ordered {
		src := byName[name]
		own := make(map[string]bool, len(src.Privileges))
		for _, priv := range src.Privileges {
			own[priv] = true
		}
		for _, nested := range src.Roles {
			for priv := range effective[nested] {
				own[priv] = true
			}
		}
		effective[name] = own

		if err := checkPaused(ctx); err != nil {
			return err
		}
		if err := s.ensureRole(ctx, src, own); err != nil {
			p.fail(bg, "role %q: %v", src.Name, err)
		}
	}
	return nil
}

// roleKey is the name a nested reference resolves against. Nexus nests roles by
// id, and for every role it creates the id and the name are the same string;
// the id is what a reference carries, so it is the key.
func roleKey(r nexusclient.Role) string {
	if r.ID != "" {
		return r.ID
	}
	return r.Name
}

// ensureRole creates the role unless one of that name is already here, then
// attaches the privileges it resolves to.
func (s *NexusMigrationService) ensureRole(ctx context.Context, src nexusclient.Role, privilegeNames map[string]bool) error {
	role, err := s.roles.GetByName(ctx, src.Name)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if role == nil {
		role = &domain.Role{
			Name:        src.Name,
			Description: src.Description,
			Source:      "migrated",
		}
		if err := s.roles.Create(ctx, role); err != nil {
			return err
		}
	}

	ids := make([]string, 0, len(privilegeNames))
	for name := range privilegeNames {
		priv, err := s.privileges.GetByName(ctx, name)
		if err != nil || priv == nil {
			continue // a built-in this instance does not carry, or one that failed to migrate
		}
		ids = append(ids, priv.ID)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil
	}
	return s.roles.SetPrivileges(ctx, role.ID, ids)
}

// topoSortRoles orders roles so every role follows the roles it nests, and
// reports the ones caught in a cycle. Cyclic roles are still returned — last,
// and without their inherited privileges — because dropping a role silently is
// worse than migrating a flatter version of it.
func topoSortRoles(byName map[string]nexusclient.Role) (ordered []string, cyclic []string) {
	const (
		unvisited = 0
		active    = 1
		done      = 2
	)
	state := make(map[string]int, len(byName))
	inCycle := map[string]bool{}

	var visit func(name string)
	visit = func(name string) {
		switch state[name] {
		case done:
			return
		case active:
			inCycle[name] = true
			return
		}
		state[name] = active
		for _, nested := range byName[name].Roles {
			if _, known := byName[nested]; known {
				visit(nested)
			}
		}
		state[name] = done
		ordered = append(ordered, name)
	}

	for _, name := range sortedKeys(byName) {
		visit(name)
	}
	for _, name := range sortedKeys(byName) {
		if inCycle[name] {
			cyclic = append(cyclic, name)
		}
	}
	return ordered, cyclic
}

func sortedKeys(m map[string]nexusclient.Role) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ── users ───────────────────────────────────────────────────────────────────

func (s *NexusMigrationService) migrateUsers(ctx context.Context, client *nexusclient.Client,
	job *domain.MigrationJob, p *progress,
) error {
	bg := context.Background()

	// Realms are listed one at a time via ?source= (#342): an externally-
	// authenticated account on a fresh target is a permanently-unusable login,
	// so only the realms the operator asked for come across — local by default
	// — and one poisoned realm's listing failure neither takes the others down
	// nor is silently masked as stage success.
	realms := job.UserRealms
	if len(realms) == 0 {
		realms = []string{"default"}
	}
	var realmErrs []error
	for _, realm := range realms {
		if err := checkPaused(ctx); err != nil {
			return err
		}
		source, err := client.ListUsersBySource(ctx, realm)
		if err != nil {
			realmErrs = append(realmErrs, fmt.Errorf("realm %q: list users on the source Nexus: %w", realm, err))
			continue
		}
		for _, src := range source {
			if err := checkPaused(ctx); err != nil {
				return err
			}
			if src.UserID == "" {
				continue
			}
			existing, err := s.users.Get(ctx, src.UserID)
			if err != nil && !isNotFound(err) {
				p.fail(bg, "user %q: %v", src.UserID, err)
				continue
			}
			if existing != nil {
				continue // an account of that name is already here; it is not overwritten
			}
			if err := s.createMigratedUser(ctx, src); err != nil {
				p.fail(bg, "user %q: %v", src.UserID, err)
			}
		}
	}
	return errors.Join(realmErrs...)
}

func (s *NexusMigrationService) createMigratedUser(ctx context.Context, src nexusclient.User) error {
	source := mapUserSource(src.Source)
	status := domain.UserStatusDisabled
	if strings.EqualFold(src.Status, "active") {
		status = domain.UserStatusActive
	}

	user := &domain.User{
		Username:  src.UserID,
		Email:     src.Email,
		FirstName: src.FirstName,
		LastName:  src.LastName,
		Status:    status,
		Source:    source,
	}

	// A Nexus password hash cannot come across, so a local account gets a random
	// one it cannot know and is asked to change it. Externally-authenticated
	// accounts get no local credential at all — their identity provider still
	// owns them.
	password := ""
	if source == domain.UserSourceLocal {
		generated, err := randomPassword()
		if err != nil {
			return err
		}
		password = generated
		user.MustResetPassword = true
	}

	if err := s.users.Create(ctx, user, password); err != nil {
		// An email colliding with an already-present account drops ONLY the
		// email and retries (#342): "no email" is a first-class account state
		// here (the partial unique index exists for LDAP users without a mail
		// attribute), while dropping the whole account loses a credential an
		// operator may depend on. Username collisions were already skipped by
		// the existence check above.
		if errors.Is(err, ErrAlreadyExists) && strings.Contains(err.Error(), "email") && user.Email != "" {
			user.Email = ""
			err = s.users.Create(ctx, user, password)
		}
		if err != nil {
			return err
		}
	}

	roleIDs := make([]string, 0, len(src.Roles))
	for _, name := range src.Roles {
		role, err := s.roles.GetByName(ctx, name)
		if err != nil || role == nil {
			continue // a role that was not migrated cannot be granted
		}
		roleIDs = append(roleIDs, role.ID)
	}
	if len(roleIDs) == 0 {
		return nil
	}
	return s.roles.SetUserRoles(ctx, user.ID, roleIDs)
}

// mapUserSource translates a Nexus realm name. An unrecognized realm becomes a
// local account: it keeps the person able to sign in, which is the point of
// migrating them at all.
func mapUserSource(source string) domain.UserSource {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "ldap":
		return domain.UserSourceLDAP
	case "oidc", "openid", "openid connect":
		return domain.UserSourceOIDC
	case "saml":
		return domain.UserSourceSAML
	default:
		return domain.UserSourceLocal
	}
}

func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ── routing rules ───────────────────────────────────────────────────────────

func (s *NexusMigrationService) migrateRoutingRules(ctx context.Context, client *nexusclient.Client, p *progress) error {
	bg := context.Background()

	source, err := client.ListRoutingRules(ctx)
	if err != nil {
		return fmt.Errorf("list routing rules on the source Nexus: %w", err)
	}
	for _, src := range source {
		if err := checkPaused(ctx); err != nil {
			return err
		}
		if existing, _ := s.routingRules.GetByName(ctx, src.Name); existing != nil {
			continue
		}
		mode := strings.ToUpper(strings.TrimSpace(src.Mode))
		if mode != "ALLOW" && mode != "BLOCK" {
			p.fail(bg, "routing rule %q: unknown mode %q", src.Name, src.Mode)
			continue
		}
		if bad, err := firstInvalidMatcher(src.Matchers); err != nil {
			p.fail(bg, "routing rule %q: matcher %q does not compile: %v", src.Name, bad, err)
			continue
		}
		rule := &domain.RoutingRule{
			Name:        src.Name,
			Description: src.Description,
			Mode:        mode,
			Matchers:    src.Matchers,
		}
		if err := s.routingRules.Create(ctx, rule); err != nil {
			p.fail(bg, "routing rule %q: %v", src.Name, err)
		}
	}
	return nil
}

// firstInvalidMatcher reports the first pattern that will not compile. A rule
// is stored whole or not at all: a half-applied ALLOW rule would quietly widen
// or narrow what the source instance permitted.
func firstInvalidMatcher(matchers []string) (string, error) {
	for _, m := range matchers {
		if _, err := regexp.Compile(m); err != nil {
			return m, err
		}
	}
	return "", nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func isNotFound(err error) bool {
	return errors.Is(err, repository.ErrNotFound) || errors.Is(err, ErrNotFound)
}

func (s *NexusMigrationService) logf(ctx context.Context, format string, args ...any) {
	if s.log != nil {
		// The run's trace_id rides along, so a warning can be tied to the
		// exact nexus_migration.run trace it belongs to (#321).
		logger.WithTraceContext(ctx, s.log).Warnf(format, args...)
	}
}
