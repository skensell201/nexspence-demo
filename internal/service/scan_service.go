package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// scanTrivyErrorMessage returns a concise error string from a failed Trivy run.
// It detects well-known OCI registry errors and returns a human-readable message
// instead of the full verbose Trivy output.
func scanTrivyErrorMessage(runErr error, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	msg := stderr
	if msg == "" {
		msg = runErr.Error()
	}
	switch {
	case strings.Contains(msg, "MANIFEST_UNKNOWN"):
		return "image manifest not found in registry — re-push the image to make it scannable"
	case strings.Contains(msg, "UNAUTHORIZED"):
		return "registry authentication failed — check scan credentials in config"
	case strings.Contains(msg, "MANIFEST_INVALID"):
		return "image manifest is invalid or corrupted"
	case strings.Contains(msg, "unable to find the specified image"):
		return "image not found in registry — re-push the image to make it scannable"
	case strings.Contains(msg, "no such file or directory") && strings.Contains(msg, "docker.sock"):
		return "Docker socket not available — ensure --image-src remote is set (internal error)"
	}
	return msg
}

// TrivyErrorMessage turns a failed Trivy run into one sentence an operator can
// act on. It is a method rather than a function because the database failure is
// only actionable with the repositories that were actually tried.
func (s *ScanService) TrivyErrorMessage(runErr error, stderr string) string {
	if trivyDBFailure(stderr) {
		msg := "could not fetch the vulnerability database from " + s.dbRepositoriesForMessage()
		if cause := firstStderrLine(stderr); cause != "" {
			msg += " (" + cause + ")"
		}
		return msg
	}
	return scanTrivyErrorMessage(runErr, stderr)
}

// trivyDBFailure recognizes the database-download failure among Trivy's error
// output. Matching on substrings is fragile by nature, so it is deliberately
// broad: a false positive still says "database", which is where the operator
// should look, and a false negative only loses the friendlier wording.
func trivyDBFailure(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "vulnerability db") ||
		strings.Contains(lower, "vulnerability database") ||
		strings.Contains(lower, "db error") ||
		strings.Contains(lower, "failed to download db") ||
		strings.Contains(lower, "java db")
}

// firstStderrLine returns the first non-empty, trimmed line of stderr — the
// root cause Trivy reports before its usually-noisier detail lines.
func firstStderrLine(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func (s *ScanService) dbRepositoriesForMessage() string {
	msg := "the Trivy defaults"
	if len(s.trivy.DBRepository) > 0 {
		msg = strings.Join(s.trivy.DBRepository, ", ")
	}
	if len(s.trivy.JavaDBRepository) > 0 {
		msg += "; Java DB from " + strings.Join(s.trivy.JavaDBRepository, ", ")
	}
	return msg
}

const scanErrorMaxLen = 8000

func truncateScanError(s string) string {
	if len(s) <= scanErrorMaxLen {
		return s
	}
	return s[:scanErrorMaxLen] + "…"
}

// DockerScanImageRef builds a pull reference for this Nexor instance (Docker API under /v2/repository/<repo>/…).
// Trivy uses it to fetch layers from Nexor instead of interpreting name:tag as Docker Hub.
// Example: base http://localhost:8081, repo my-docker, image da/nginx, tag 1.0 → localhost:8081/repository/my-docker/da/nginx:1.0
func DockerScanImageRef(baseURL, repoName, imageName, version string) string {
	baseURL = strings.TrimSpace(baseURL)
	repoName = strings.TrimSpace(repoName)
	imageName = strings.Trim(strings.TrimSpace(imageName), "/")
	version = strings.TrimSpace(version)
	if baseURL == "" || repoName == "" || imageName == "" {
		return ""
	}
	if version == "" {
		version = "latest"
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	pathPrefix := strings.TrimSpace(u.Path)
	pathPrefix = strings.TrimSuffix(pathPrefix, "/")
	if pathPrefix == "" || pathPrefix == "/" {
		pathPrefix = ""
	}
	return u.Host + pathPrefix + "/repository/" + repoName + "/" + imageName + ":" + version
}

func httpBaseURLInsecure(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && strings.EqualFold(u.Scheme, "http")
}

// ScanService scans a component for vulnerabilities using Trivy.
type ScanService struct {
	Components   repository.ComponentRepo
	HTTPBaseURL  string                    // e.g. http://localhost:8081 — used to build registry pull refs for hosted images
	TrivyTimeout time.Duration             // per-scan wall-clock limit (0 = no extra timeout); default 10m
	ScanResults  repository.ScanResultRepo // may be nil; if set, each scan is persisted here
	OSVClient    *OSVClient                // used for non-Docker formats
	scanUsername string                    // registry credentials handed to Trivy via TRIVY_USERNAME/TRIVY_PASSWORD (see TrivyEnv)
	scanPassword string

	// trivy is the operator's scanner: nexspence ships none, so everything
	// about it — whether it exists at all, where it is — comes from config.
	trivy TrivyOptions

	// Probe cache. nowFn is a seam so tests can hold time still; it is
	// nil-safe through the now() helper.
	statusMu sync.Mutex
	status   ScannerStatus
	statusAt time.Time
	nowFn    func() time.Time

	// trivyMu serializes Trivy CLI runs. Trivy's on-disk cache (BoltDB) is not safe for concurrent
	// processes; parallel scans caused "cache may be in use by another process: timeout".
	trivyMu sync.Mutex

	// queue carries component ids awaiting an automatic scan. It is buffered and
	// dropped-on-full: an upload must never wait on a scanner, and must never be
	// refused because one is behind. What the queue drops, the daily bulk scan
	// picks up.
	queue chan string
}

func (s *ScanService) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// autoScanQueueSize bounds the outstanding automatic scans. Past this, uploads
// keep succeeding and the overflow waits for the next bulk scan.
const autoScanQueueSize = 256

// NewScanService constructs a vulnerability scanning service backed by Trivy and OSV.dev.
func NewScanService(components repository.ComponentRepo, httpBaseURL string) *ScanService {
	return &ScanService{
		Components:   components,
		HTTPBaseURL:  strings.TrimSpace(httpBaseURL),
		trivy:        TrivyOptions{Bin: "trivy"},
		TrivyTimeout: 10 * time.Minute,
		OSVClient:    NewOSVClient(),
		queue:        make(chan string, autoScanQueueSize),
	}
}

// TriggerAsync requests a background scan of componentID and returns immediately.
//
// It is the seam the storage layer uses after an artifact is stored, so it is on
// the upload path and must behave like it: never block, never fail the caller.
// A full queue drops the request rather than applying backpressure to an upload
// — the nightly bulk scan covers what is dropped.
func (s *ScanService) TriggerAsync(componentID string) {
	if componentID == "" || s.queue == nil {
		return
	}
	select {
	case s.queue <- componentID:
	default:
		log.Printf("nexor: auto-scan queue full, skipping component=%s (next bulk scan will cover it)", componentID)
	}
}

// StartScheduler drains the automatic-scan queue and runs a full bulk re-scan on
// the given cron schedule, until ctx is done. Run it as a goroutine.
//
// A blank schedule keeps the queue worker but disables the periodic scan; an
// invalid one disables the periodic scan with a log line rather than taking the
// server down over a config typo.
//
// The queue is drained by this single goroutine, one component at a time. That
// is not a throughput compromise: Trivy's on-disk cache tolerates only one
// process at a time anyway (see trivyMu), so a wider pool would serialize on
// the same lock while holding more scans in flight.
func (s *ScanService) StartScheduler(ctx context.Context, schedule string) {
	if strings.TrimSpace(schedule) == "" {
		s.drainQueue(ctx)
		return
	}

	// SkipIfStillRunning, unlike the other schedulers: a full re-scan re-queries
	// OSV.dev per component and re-runs Trivy per image, so on a large registry
	// it can outlast its own interval. Overlapping runs would queue up behind
	// trivyMu and never catch up.
	// Recover goes first (outermost) so a job panic can't also skip
	// SkipIfStillRunning's own cleanup — the wrapping wouldn't survive a panic
	// escaping through it otherwise.
	c := cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger), cron.SkipIfStillRunning(cron.DiscardLogger)))
	if _, err := c.AddFunc(schedule, func() {
		scanned, failed, err := s.BulkScan(ctx, "")
		if err != nil {
			log.Printf("nexor: scheduled bulk scan failed: %v", err)
			return
		}
		log.Printf("nexor: scheduled bulk scan done scanned=%d failed=%d", scanned, failed)
	}); err != nil {
		log.Printf("nexor: scan scheduler disabled, invalid schedule %q: %v", schedule, err)
		s.drainQueue(ctx)
		return
	}
	c.Start()
	defer c.Stop()

	s.drainQueue(ctx)
}

// isImageFormat reports whether a component is scanned by Trivy rather than by OSV.
func isImageFormat(format string) bool {
	switch strings.ToLower(format) {
	case "docker", "oci":
		return true
	}
	return false
}

// skipAutoScan reports whether a queued component is not worth a scanner run.
//
// A component that cannot be read is left to Scan to report — this only filters
// what is knowably pointless.
func (s *ScanService) skipAutoScan(ctx context.Context, componentID string) bool {
	comp, err := s.Components.Get(ctx, componentID)
	if err != nil || comp == nil {
		return false
	}
	if isDigestAlias(comp.Version) {
		return true
	}
	// No scanner, no scan — and no error per artifact either. A capability the
	// operator has not provided is not an upload failure.
	return isImageFormat(comp.Format) && !s.Scanner(ctx).Ready()
}

// drainQueue scans queued components sequentially until ctx is done.
func (s *ScanService) drainQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case componentID := <-s.queue:
			if s.skipAutoScan(ctx, componentID) {
				continue
			}
			// An unscannable format is a normal upload, not a failure: every
			// artifact is queued because the storage layer does not know which
			// formats have a scanner, and Scan is the thing that does.
			if _, err := s.Scan(ctx, componentID, ""); err != nil {
				log.Printf("nexor: auto-scan skipped component=%s: %v", componentID, err)
			}
		}
	}
}

// WithScanResults attaches a repository for persisting scan results and returns s.
func (s *ScanService) WithScanResults(repo repository.ScanResultRepo) *ScanService {
	s.ScanResults = repo
	return s
}

// WithCredentials sets the credentials used to pull images for scanning and returns s.
func (s *ScanService) WithCredentials(username, password string) *ScanService {
	s.scanUsername = username
	s.scanPassword = password
	return s
}

// WithTrivy sets the operator-supplied scanner options and returns s.
func (s *ScanService) WithTrivy(opts TrivyOptions) *ScanService {
	s.trivy = opts
	return s
}

// TrivyOptions returns the scanner options in force.
func (s *ScanService) TrivyOptions() TrivyOptions { return s.trivy }

// Scan runs trivy against imageRef, persists the result in component.Extra["scan_result"],
// and returns it. Components of the two OCI Distribution formats (docker, oci) go to
// Trivy, a few language formats go to OSV, and anything else gets a clear error.
func (s *ScanService) Scan(ctx context.Context, componentID, imageRef string) (*domain.ScanResult, error) {
	comp, err := s.Components.Get(ctx, componentID)
	if err != nil {
		return nil, err
	}
	if comp == nil {
		return nil, fmt.Errorf("component %s not found", componentID)
	}
	format := strings.ToLower(comp.Format)
	// A cached metadata placeholder (an npm packument, a cargo index entry, a
	// pypi simple page) is not a package version: scanning it would answer
	// "ok, 0 vulnerabilities" — indistinguishable from a clean scan of the
	// real artifact and exactly the confusion #347 reports.
	if metadataPlaceholderVersions[comp.Version] {
		return nil, fmt.Errorf("component %q@%q is a cached metadata artifact, not a package version — scan the versioned package component instead", comp.Name, comp.Version)
	}
	switch {
	case isImageFormat(format):
		// Falls through to the Trivy path below. An oci repository holds the same
		// content a docker one does — an ORAS artifact is an image by structure —
		// so refusing it on its format label would be wrong. A non-image artifact
		// (a chart, a signature) surfaces as a Trivy error instead of being
		// refused up front, which is the more honest answer.
	case format == "maven" || format == "maven2" || format == "npm" || format == "pypi" || format == "cargo":
		return s.scanOSV(ctx, comp)
	default:
		return nil, fmt.Errorf("vulnerability scanning is not supported for format %q", comp.Format)
	}

	// The capability is checked before any work: a component that cannot be
	// scanned should cost nothing and should say why. A binary that vanishes
	// within the ready-cache TTL surfaces as a failed scan result rather than
	// a refusal — deliberate, it self-heals at the next probe.
	if st := s.Scanner(ctx); !st.Ready() {
		return nil, &ScannerUnavailableError{Status: st}
	}

	ref := strings.TrimSpace(imageRef)
	if ref == "" {
		if full := DockerScanImageRef(s.HTTPBaseURL, comp.Repository, comp.Name, comp.Version); full != "" {
			ref = full
		} else {
			ref = comp.Name
			if comp.Version != "" {
				ref += ":" + comp.Version
			}
		}
	}

	// Defense in depth on top of the "--" separator in TrivyScanArgs: a
	// flag-shaped reference is never a real image, so refuse it loudly instead
	// of letting a misparse report "no vulnerabilities".
	if strings.HasPrefix(ref, "-") {
		return nil, fmt.Errorf("invalid imageRef %q: must not start with %q", ref, "-")
	}

	result := &domain.ScanResult{
		ScannedAt: time.Now().UTC(),
		ImageRef:  ref,
	}

	bin := s.trivy.BinOrDefault()
	args := TrivyScanArgs(s.trivy, ref, httpBaseURLInsecure(s.HTTPBaseURL))

	// Apply per-scan timeout to guard against trivy hanging (e.g. on first DB download).
	scanCtx := ctx
	if s.TrivyTimeout > 0 {
		var cancel context.CancelFunc
		scanCtx, cancel = context.WithTimeout(ctx, s.TrivyTimeout)
		defer cancel()
	}

	var stderrBuf bytes.Buffer
	var out []byte
	var runErr error
	func() {
		s.trivyMu.Lock()
		defer s.trivyMu.Unlock()
		// #nosec G204 — argv is built from config and DB state, never from a shell string.
		cmd := exec.CommandContext(scanCtx, bin, args...)
		cmd.Env = TrivyEnv(os.Environ(), s.scanUsername, s.scanPassword)
		cmd.Stderr = &stderrBuf
		out, runErr = cmd.Output()
	}()

	if runErr != nil && len(out) == 0 {
		// Trivy may exit non-zero when vulnerabilities are found — stdout is
		// still parsed below when present. Empty stdout plus an error is a real
		// failure, and the details are on stderr.
		msg := s.TrivyErrorMessage(runErr, stderrBuf.String())
		log.Printf("nexor: trivy scan failed component=%s imageRef=%q: %s", componentID, ref, msg)
		result.Status = domain.ScanStatusFailed
		result.Error = truncateScanError(msg)
		s.persist(ctx, comp, result, "trivy")
		return result, nil
	}

	result.Findings, result.Summary = parseTrivyJSON(out)
	result.Status = domain.ScanStatusOK

	s.persist(ctx, comp, result, "trivy")
	return result, nil
}

// GetResult returns the cached scan result stored in component.Extra["scan_result"], or nil.
func (s *ScanService) GetResult(ctx context.Context, componentID string) (*domain.ScanResult, error) {
	comp, err := s.Components.Get(ctx, componentID)
	if err != nil {
		return nil, err
	}
	if comp == nil {
		return nil, fmt.Errorf("component %s not found", componentID)
	}
	raw, ok := comp.Extra["scan_result"]
	if !ok || raw == nil {
		return nil, nil //nolint:nilnil // component has no cached scan result yet; nil result is the documented "not scanned" signal
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var sr domain.ScanResult
	if err := json.Unmarshal(b, &sr); err != nil {
		return nil, err
	}
	return &sr, nil
}

func (s *ScanService) persistResult(ctx context.Context, comp *domain.Component, result *domain.ScanResult) error {
	b, _ := json.Marshal(result)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	return s.Components.UpdateExtra(ctx, comp.ID, map[string]any{"scan_result": raw})
}

// persist stores a finished scan in both places it lives: the component's cached
// scan_result and the scan_results history row the dashboard aggregates.
//
// Both writes are best-effort — the result is already on its way back to the
// caller, and a failed write must not turn a successful scan into an error. It
// must not be invisible either: a dropped write leaves the dashboard showing
// stale or missing data for that component, so each failure is logged with the
// component and the scanner that produced the result.
//
// Deliberately no retry: Scan runs inside the request that asked for it, and
// blocking that request to retry a cache write costs the caller more than the
// stale row does. The daily bulk re-scan is what eventually repairs it.
func (s *ScanService) persist(ctx context.Context, comp *domain.Component, result *domain.ScanResult, scanner string) {
	if err := s.persistResult(ctx, comp, result); err != nil {
		log.Printf("nexor: scan result not persisted to component component=%s scanner=%s: %v", comp.ID, scanner, err)
	}
	s.persistScanRow(ctx, comp, result, scanner)
}

func (s *ScanService) scanOSV(ctx context.Context, comp *domain.Component) (*domain.ScanResult, error) {
	ecosystem := FormatToEcosystem(comp.Format)
	if ecosystem == "" {
		return nil, fmt.Errorf("format %q not supported for scanning", comp.Format)
	}

	result := &domain.ScanResult{
		ScannedAt: time.Now().UTC(),
		Status:    domain.ScanStatusOK,
	}

	// OSV identifies Maven packages as "group:artifact"; the bare artifact
	// name matches nothing.
	pkgName := comp.Name
	if ecosystem == "Maven" && comp.Group != "" {
		pkgName = comp.Group + ":" + comp.Name
	}
	vulns, err := s.OSVClient.Query(ctx, pkgName, comp.Version, ecosystem)
	if err != nil {
		result.Status = domain.ScanStatusFailed
		result.Error = err.Error()
		s.persist(ctx, comp, result, "osv")
		return result, nil //nolint:nilerr // best-effort scan: OSV query failure is recorded in result.Error and returned as a failed-status result; propagating the error would break the UI scan response
	}

	var findings []domain.CVEFinding
	var summary domain.ScanSummary
	for _, v := range vulns {
		findings = append(findings, domain.CVEFinding{
			ID:       v.ID,
			Severity: v.Severity,
			Title:    v.Summary,
		})
		switch v.Severity {
		case SeverityMalicious:
			summary.Malicious++
		case "CRITICAL":
			summary.Critical++
		case "HIGH":
			summary.High++
		case "MEDIUM":
			summary.Medium++
		case "LOW":
			summary.Low++
		default:
			summary.Unknown++
		}
		summary.Total++
	}
	result.Findings = findings
	result.Summary = summary

	s.persist(ctx, comp, result, "osv")
	return result, nil
}

func (s *ScanService) persistScanRow(ctx context.Context, comp *domain.Component, result *domain.ScanResult, scanner string) {
	if s.ScanResults == nil {
		return
	}
	row := &domain.ScanResultRow{
		ComponentID: comp.ID,
		Scanner:     scanner,
		Status:      result.Status,
		Malicious:   result.Summary.Malicious,
		Critical:    result.Summary.Critical,
		High:        result.Summary.High,
		Medium:      result.Summary.Medium,
		Low:         result.Summary.Low,
		Unknown:     result.Summary.Unknown,
		Total:       result.Summary.Total,
		ScannedAt:   result.ScannedAt,
		Error:       result.Error,
	}
	if err := s.ScanResults.Insert(ctx, row); err != nil {
		log.Printf("nexor: scan result row not inserted component=%s scanner=%s: %v", comp.ID, scanner, err)
	}
}

// metadataPlaceholderVersions are the version labels format handlers register
// cached metadata under (an npm packument, a pypi simple page, a cargo index
// entry). They are files, not package versions — no advisory database can say
// anything about them.
var metadataPlaceholderVersions = map[string]bool{
	"metadata":    true,
	"simple-page": true,
	"index":       true,
}

// isDigestAlias reports whether a component version is a content digest rather
// than a release.
//
// An OCI push registers every layer and the manifest's digest alias as their own
// components, versioned by digest. They duplicate the tagged image: scanning
// them means one scanner run per layer against a reference that is not an image,
// and — on the bounded auto-scan queue — that wasted work would crowd out the
// manifest scan that actually says something.
func isDigestAlias(version string) bool {
	return strings.HasPrefix(version, "sha256:")
}

// BulkScan scans every component in a repository (skipping SHA digest aliases),
// returning the count of scanned and failed components.
func (s *ScanService) BulkScan(ctx context.Context, repoName string) (scanned int, failed int, err error) {
	// 500 is the most the repository layer hands out per Search call, so the
	// full set is only covered by walking the continuation token.
	const pageLimit = 500
	offset := 0
	for {
		page, err := s.Components.Search(ctx, domain.SearchParams{Repository: repoName, Limit: pageLimit, Offset: offset})
		if err != nil {
			return scanned, failed, err
		}
		for _, comp := range page.Items {
			if isDigestAlias(comp.Version) {
				continue
			}
			if isImageFormat(comp.Format) && !s.Scanner(ctx).Ready() {
				continue
			}
			_, scanErr := s.Scan(ctx, comp.ID, "")
			if scanErr != nil {
				failed++
			} else {
				scanned++
			}
		}
		if page.ContinuationToken == nil {
			break
		}
		next, convErr := strconv.Atoi(*page.ContinuationToken)
		if convErr != nil {
			return scanned, failed, fmt.Errorf("malformed continuation token %q: %w", *page.ContinuationToken, convErr)
		}
		if next <= offset {
			break
		}
		offset = next
	}
	return scanned, failed, nil
}

// GetSummary returns aggregated vulnerability counts across all scan results.
func (s *ScanService) GetSummary(ctx context.Context) (*domain.SecuritySummary, error) {
	if s.ScanResults == nil {
		return &domain.SecuritySummary{}, nil
	}
	return s.ScanResults.Aggregate(ctx)
}

// ListVulnerabilities returns matching vulnerability rows and the total count for the filter.
func (s *ScanService) ListVulnerabilities(ctx context.Context, f domain.VulnFilter) ([]*domain.VulnRow, int, error) {
	if s.ScanResults == nil {
		return nil, 0, nil
	}
	return s.ScanResults.List(ctx, f)
}

// ── Trivy JSON parsing ────────────────────────────────────────

type trivyReport struct {
	Results []trivyResult `json:"Results"`
}

type trivyResult struct {
	Vulnerabilities []trivyVuln `json:"Vulnerabilities"`
}

type trivyVuln struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
}

// ParseTrivyJSONForTest exposes parseTrivyJSON for package-level tests.
func ParseTrivyJSONForTest(data []byte) ([]domain.CVEFinding, domain.ScanSummary) {
	return parseTrivyJSON(data)
}

func parseTrivyJSON(data []byte) ([]domain.CVEFinding, domain.ScanSummary) {
	var report trivyReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, domain.ScanSummary{}
	}

	var findings []domain.CVEFinding
	var summary domain.ScanSummary
	seen := map[string]struct{}{}

	for _, res := range report.Results {
		for _, v := range res.Vulnerabilities {
			key := v.VulnerabilityID + "/" + v.PkgName
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}

			findings = append(findings, domain.CVEFinding{
				ID:           v.VulnerabilityID,
				Severity:     v.Severity,
				PkgName:      v.PkgName,
				InstalledVer: v.InstalledVersion,
				FixedVersion: v.FixedVersion,
				Title:        v.Title,
			})

			switch strings.ToUpper(v.Severity) {
			case "CRITICAL":
				summary.Critical++
			case "HIGH":
				summary.High++
			case "MEDIUM":
				summary.Medium++
			case "LOW":
				summary.Low++
			default:
				summary.Unknown++
			}
			summary.Total++
		}
	}
	return findings, summary
}
