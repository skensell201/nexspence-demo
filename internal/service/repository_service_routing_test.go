package service_test

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func routingSvc(repos *testutil.RepoRepo) *service.RepositoryService {
	return service.NewRepositoryService(
		repos, testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), testutil.NewCleanupPolicyRepo(),
	)
}

func existingRepo(t *testing.T, repos *testutil.RepoRepo, svc *service.RepositoryService, rule *string) *domain.Repository {
	t.Helper()
	r := &domain.Repository{
		Name: "r1", Format: domain.FormatRaw, Type: domain.TypeHosted, RoutingRuleID: rule,
	}
	if err := svc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return r
}

// Create accepted routingRuleId and Update dropped it, so a routing rule could
// be attached when the repository was made and never changed afterwards. The
// API answered 200 and the UI reported a saved repository while the row kept
// whatever it started with — and a routing rule is a BLOCK control, so the
// direction that matters is the one where nothing gets attached.
func TestRepositoryService_Update_AttachesRoutingRule(t *testing.T) {
	repos := testutil.NewRepoRepo()
	svc := routingSvc(repos)
	existingRepo(t, repos, svc, nil)

	const ruleID = "b60ff2a3-17cc-4065-9726-bf8fcb52b974"
	updated, err := svc.Update(context.Background(), "r1", &domain.Repository{
		Online: true, RoutingRuleID: strPtr(ruleID),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.RoutingRuleID == nil || *updated.RoutingRuleID != ruleID {
		t.Fatalf("routing rule after update: %v, want %q", updated.RoutingRuleID, ruleID)
	}
	// And it is on the stored row, not only on the returned copy.
	stored, err := repos.Get(context.Background(), "r1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.RoutingRuleID == nil || *stored.RoutingRuleID != ruleID {
		t.Fatalf("stored routing rule: %v, want %q", stored.RoutingRuleID, ruleID)
	}
}

// Same convention as blobStoreId: an empty string detaches.
func TestRepositoryService_Update_DetachesRoutingRuleOnEmptyString(t *testing.T) {
	repos := testutil.NewRepoRepo()
	svc := routingSvc(repos)
	existingRepo(t, repos, svc, strPtr("b60ff2a3-17cc-4065-9726-bf8fcb52b974"))

	updated, err := svc.Update(context.Background(), "r1", &domain.Repository{
		Online: true, RoutingRuleID: strPtr(""),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.RoutingRuleID != nil {
		t.Fatalf("routing rule after detach: %v, want nil", *updated.RoutingRuleID)
	}
}

// An absent field means "unchanged" — an update that only toggles `online`
// must not quietly detach the rule.
func TestRepositoryService_Update_KeepsRoutingRuleWhenFieldOmitted(t *testing.T) {
	repos := testutil.NewRepoRepo()
	svc := routingSvc(repos)
	const ruleID = "b60ff2a3-17cc-4065-9726-bf8fcb52b974"
	existingRepo(t, repos, svc, strPtr(ruleID))

	updated, err := svc.Update(context.Background(), "r1", &domain.Repository{Online: true})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.RoutingRuleID == nil || *updated.RoutingRuleID != ruleID {
		t.Fatalf("routing rule after unrelated update: %v, want %q", updated.RoutingRuleID, ruleID)
	}
}
