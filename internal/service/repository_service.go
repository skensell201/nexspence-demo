package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

var (
	// ErrNotFound aliases repository.ErrNotFound so a not-found error raised by the
	// repository layer satisfies errors.Is at the service boundary (and the handler
	// isNotFound check) without re-wrapping.
	ErrNotFound = repository.ErrNotFound
	// ErrAlreadyExists indicates a resource with the same identity already exists.
	ErrAlreadyExists = errors.New("already exists")
	// ErrInvalidInput indicates the caller supplied invalid or incomplete input.
	ErrInvalidInput = errors.New("invalid input")
	// ErrProvisioningRejected indicates an SSO user was rejected by the provisioning policy.
	ErrProvisioningRejected = errors.New("provisioning rejected")
	// ErrProvisioningConflict indicates an SSO login conflicts with an existing user's source.
	ErrProvisioningConflict = errors.New("user source conflict")
)

// RepositoryService handles business logic for Nexus-compatible repository management.
type RepositoryService struct {
	repos     repository.RepositoryRepo
	blobs     repository.BlobStoreRepo
	blobStore storage.BlobStore
	policies  repository.CleanupPolicyRepo
	webhooks  domain.WebhookDispatcher
}

// NewRepositoryService constructs a service for managing repositories and their blob stores.
func NewRepositoryService(
	repos repository.RepositoryRepo,
	blobs repository.BlobStoreRepo,
	blobStore storage.BlobStore,
	policies repository.CleanupPolicyRepo,
) *RepositoryService {
	return &RepositoryService{repos: repos, blobs: blobs, blobStore: blobStore, policies: policies}
}

// WithWebhooks attaches a dispatcher for repository lifecycle events and returns s.
func (s *RepositoryService) WithWebhooks(d domain.WebhookDispatcher) *RepositoryService {
	s.webhooks = d
	return s
}

// List returns repositories, optionally filtered by format and type.
func (s *RepositoryService) List(ctx context.Context, format, repoType string) ([]domain.Repository, error) {
	return s.repos.List(ctx, format, repoType)
}

// Get returns the repository with the given name, or ErrNotFound if none exists.
func (s *RepositoryService) Get(ctx context.Context, name string) (*domain.Repository, error) {
	r, err := s.repos.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("%w: repository %q", ErrNotFound, name)
	}
	return r, nil
}

// Create validates and persists a new repository, then fires a repo.created webhook event.
func (s *RepositoryService) Create(ctx context.Context, r *domain.Repository) error {
	// Validate
	if r.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if r.Format == "" {
		return fmt.Errorf("%w: format is required", ErrInvalidInput)
	}
	if r.Type == "" {
		return fmt.Errorf("%w: type is required", ErrInvalidInput)
	}
	if err := validateNameForFormat(r.Name, r.Format); err != nil {
		return err
	}

	// Check duplicate
	existing, err := s.repos.Get(ctx, r.Name)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if existing != nil {
		return fmt.Errorf("%w: repository %q", ErrAlreadyExists, r.Name)
	}

	if r.ProxyConfig != nil {
		delete(r.ProxyConfig, domain.ProxyPasswordSetKey)
	}
	if r.Type == domain.TypeProxy {
		if r.ProxyConfig == nil {
			return fmt.Errorf("%w: proxy repositories require proxy_config with remote_url", ErrInvalidInput)
		}
		if err := validateRemoteURL(r.ProxyConfig); err != nil {
			return err
		}
	}

	if r.Type == domain.TypeGroup {
		if err := s.validateGroupMembers(ctx, r); err != nil {
			return err
		}
	}

	if err := s.validateCleanupPolicies(ctx, r.Format, r.CleanupPolicyIDs); err != nil {
		return err
	}

	// Validate blob store exists (for hosted/proxy) and enforce quota <= store quota.
	if r.BlobStoreID != nil {
		ref := strings.TrimSpace(*r.BlobStoreID)
		if ref == "" {
			r.BlobStoreID = nil
		} else {
			bs, err := s.blobs.GetByID(ctx, ref)
			if errors.Is(err, repository.ErrNotFound) {
				return fmt.Errorf("%w: blob store %q", ErrNotFound, ref)
			}
			if err != nil {
				return err
			}
			r.BlobStoreID = &bs.ID
			if err := validateRepoQuotaAgainstStore(r.QuotaBytes, bs); err != nil {
				return err
			}
		}
	}

	r.Online = true
	if err := s.repos.Create(ctx, r); err != nil {
		return err
	}
	if s.webhooks != nil {
		s.webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventRepoCreated,
			Timestamp:  time.Now().UTC(),
			Repository: r.Name,
		})
	}
	return nil
}

// Update validates and applies changes to the named repository and returns the updated record.
func (s *RepositoryService) Update(ctx context.Context, name string, updates *domain.Repository) (*domain.Repository, error) {
	r, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}

	// Apply allowed updates
	if updates.Online != r.Online {
		r.Online = updates.Online
	}
	if updates.Description != "" {
		r.Description = updates.Description
	}
	if updates.FormatConfig != nil {
		r.FormatConfig = updates.FormatConfig
	}
	if updates.HTTPConfig != nil {
		r.HTTPConfig = updates.HTTPConfig
	}
	if updates.ProxyConfig != nil {
		r.ProxyConfig = mergeProxyConfig(r.ProxyConfig, updates.ProxyConfig)
		if r.Type == domain.TypeProxy {
			if err := validateRemoteURL(r.ProxyConfig); err != nil {
				return nil, err
			}
		}
	}
	if updates.QuotaBytes != nil {
		r.QuotaBytes = updates.QuotaBytes
	}
	if updates.CleanupPolicyIDs != nil {
		r.CleanupPolicyIDs = updates.CleanupPolicyIDs
	}
	if updates.BlobStoreID != nil {
		id := strings.TrimSpace(*updates.BlobStoreID)
		if id == "" {
			r.BlobStoreID = nil
		} else {
			bs, err := s.blobs.GetByID(ctx, id)
			if errors.Is(err, repository.ErrNotFound) {
				return nil, fmt.Errorf("%w: blob store %q", ErrNotFound, id)
			}
			if err != nil {
				return nil, err
			}
			r.BlobStoreID = &bs.ID
		}
	}
	// Create accepts routingRuleId and Update used to drop it on the floor, so a
	// routing rule could be attached when the repository was made and never
	// changed or detached again — the API answered 200 and the UI reported a
	// saved repository while the row kept whatever it started with. A routing
	// rule is a BLOCK control, so silently not attaching one is the direction
	// that matters. Same convention as blobStoreId above: an absent field means
	// unchanged, an empty string detaches.
	if updates.RoutingRuleID != nil {
		id := strings.TrimSpace(*updates.RoutingRuleID)
		if id == "" {
			r.RoutingRuleID = nil
		} else {
			// Existence is left to the routing_rule_id foreign key, the way
			// Create already leaves it.
			r.RoutingRuleID = &id
		}
	}
	r.AllowAnonymous = updates.AllowAnonymous

	if err := s.validateCleanupPolicies(ctx, r.Format, r.CleanupPolicyIDs); err != nil {
		return nil, err
	}

	if r.Type == domain.TypeGroup {
		if err := s.validateGroupMembers(ctx, r); err != nil {
			return nil, err
		}
	}

	// Enforce repository quota <= blob store quota whenever quota or store changed.
	if r.QuotaBytes != nil && r.BlobStoreID != nil {
		bs, err := s.blobs.GetByID(ctx, *r.BlobStoreID)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
		if bs != nil {
			if err := validateRepoQuotaAgainstStore(r.QuotaBytes, bs); err != nil {
				return nil, err
			}
		}
	}

	if err := s.repos.Update(ctx, r); err != nil {
		return nil, err
	}
	if s.webhooks != nil {
		s.webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventRepoUpdated,
			Timestamp:  time.Now().UTC(),
			Repository: r.Name,
		})
	}
	return r, nil
}

// mergeProxyConfig produces the proxyConfig to persist from the stored one and the
// caller's replacement. The replacement wins wholesale, except for the secrets:
// clients read a redacted config (see domain.RedactedRepository), so an omitted
// proxy_password / remote_password means "unchanged" rather than "clear it".
// Sending an explicit empty value clears the credential. The read-only *_set
// markers a client may echo back are always dropped.
func mergeProxyConfig(stored, updates map[string]any) map[string]any {
	secrets := [][2]string{
		{domain.ProxyPasswordKey, domain.ProxyPasswordSetKey},
		{domain.RemotePasswordKey, domain.RemotePasswordSetKey},
	}
	merged := make(map[string]any, len(updates)+len(secrets))
	for k, v := range updates {
		if k == domain.ProxyPasswordSetKey || k == domain.RemotePasswordSetKey {
			continue
		}
		merged[k] = v
	}
	for _, keys := range secrets {
		secretKey := keys[0]
		if pw, sent := merged[secretKey]; sent {
			if s, _ := pw.(string); s == "" {
				delete(merged, secretKey)
			}
			continue
		}
		if pw, ok := stored[secretKey].(string); ok && pw != "" {
			merged[secretKey] = pw
		}
	}
	return merged
}

// dockerPathComponent is the distribution reference grammar for one path
// component. Docker-family clients parse image references with it, so a
// docker/oci repository whose name fails it exists but can never be pushed to
// or pulled from — `docker tag host/<name>/img` is not even parseable. (#262
// was found via a repository named "docker test", created without complaint
// and then silently unusable.)
var dockerPathComponent = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*$`)

// reservedV2Names are the static routes registered under /v2/ (see
// internal/api/router.go). Gin matches a static segment before the
// /v2/:repoName parameter, so a repository named after one is created
// successfully and then shadowed on the short docker URL — the same silent
// dead end #262 is about, one path segment up.
var reservedV2Names = map[string]bool{
	"repository": true, // the long-form /v2/repository/<repoName>/... dispatch
	"token":      true, // the token endpoint the /v2/ ping challenges towards
	"_catalog":   true, // the instance-level catalog (also fails the grammar above)
}

// validateNameForFormat rejects repository names the format's own clients
// cannot address. Only the OCI-registry formats are gated: their grammar is
// strict and a violating repository is dead on arrival, while other formats
// merely produce percent-encoded URLs. Existing repositories are untouched —
// this runs on create, where the trap is sprung.
func validateNameForFormat(name string, format domain.RepoFormat) error {
	if !format.IsOCIRegistry() {
		return nil
	}
	if !dockerPathComponent.MatchString(name) {
		return fmt.Errorf(
			"%w: %q cannot be addressed by docker clients — use lowercase letters and digits, "+
				"joined by '.', '_' or '-' (e.g. \"docker-test\")",
			ErrInvalidInput, name)
	}
	if reservedV2Names[name] {
		return fmt.Errorf(
			"%w: %q is a registry-level endpoint on the /v2/ surface, so a repository of that "+
				"name could never be reached — pick another name",
			ErrInvalidInput, name)
	}
	return nil
}

// validateRemoteURL rejects a proxy config whose remote_url is missing or blank.
func validateRemoteURL(pc map[string]any) error {
	raw, ok := pc["remote_url"]
	s, strOK := raw.(string)
	if !ok || !strOK || strings.TrimSpace(s) == "" {
		return fmt.Errorf("%w: proxy_config.remote_url must be a non-empty string", ErrInvalidInput)
	}
	return nil
}

// validateRepoQuotaAgainstStore rejects a repository quota that exceeds the
// owning blob store's quota. Either quota being nil (unlimited) passes.
func validateRepoQuotaAgainstStore(repoQuota *int64, bs *domain.BlobStore) error {
	if repoQuota == nil || bs == nil || bs.QuotaBytes == nil {
		return nil
	}
	if *repoQuota > *bs.QuotaBytes {
		return fmt.Errorf("%w: repository quota %d bytes exceeds blob store %q quota %d bytes",
			ErrInvalidInput, *repoQuota, bs.Name, *bs.QuotaBytes)
	}
	return nil
}

func (s *RepositoryService) validateCleanupPolicies(ctx context.Context, repoFormat domain.RepoFormat, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		p, err := s.policies.Get(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: cleanup policy %q does not exist", ErrInvalidInput, id)
			}
			return err
		}
		if p == nil {
			return fmt.Errorf("%w: cleanup policy %q does not exist", ErrInvalidInput, id)
		}
		pf := p.Format
		if pf != "" && pf != "*" && pf != string(repoFormat) {
			return fmt.Errorf("%w: cleanup policy %q targets format %s but repository is %s",
				ErrInvalidInput, p.Name, pf, repoFormat)
		}
	}
	return nil
}

func (s *RepositoryService) validateGroupMembers(ctx context.Context, group *domain.Repository) error {
	names := domain.GroupMemberNames(group)
	if len(names) == 0 {
		return fmt.Errorf("%w: group repositories require formatConfig.member_names with at least one member", ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, dup := seen[name]; dup {
			return fmt.Errorf("%w: group member %q is listed more than once", ErrInvalidInput, name)
		}
		seen[name] = struct{}{}

		m, err := s.repos.Get(ctx, name)
		if errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("%w: group member repository %q does not exist", ErrInvalidInput, name)
		}
		if err != nil {
			return err
		}
		if m.Type == domain.TypeGroup {
			return fmt.Errorf("%w: group member %q cannot be a group repository", ErrInvalidInput, name)
		}
		if m.Format != group.Format {
			return fmt.Errorf("%w: group member %q has format %q, expected %q", ErrInvalidInput, name, m.Format, group.Format)
		}
	}
	return nil
}

// Delete removes the named repository.
func (s *RepositoryService) Delete(ctx context.Context, name string) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	if err := s.repos.Delete(ctx, name); err != nil {
		return err
	}
	if s.webhooks != nil {
		s.webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventRepoDeleted,
			Timestamp:  time.Now().UTC(),
			Repository: name,
		})
	}
	return nil
}
