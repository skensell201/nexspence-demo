//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// Every catalog table whose name column is unique reports a duplicate as
// repository.ErrAlreadyExists naming that field, the way user_repo already
// did — instead of handing the caller Postgres' own text, which carries the
// constraint name, the table behind it and the SQLSTATE.
//
// Both halves are exercised per table: a second Create under a taken name, and
// an Update that renames a row onto one.

// assertNameConflict fails unless err is an ErrAlreadyExists naming "name".
func assertNameConflict(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, repository.ErrAlreadyExists) {
		t.Fatalf("%s: want ErrAlreadyExists, got %v", what, err)
	}
	var uv *repository.UniqueViolationError
	if !errors.As(err, &uv) {
		t.Fatalf("%s: want a UniqueViolationError, got %T", what, err)
	}
	if uv.Field != "name" {
		t.Fatalf("%s: want field %q, got %q", what, "name", uv.Field)
	}
}

func TestBlobStoreRepo_Create_DuplicateName_IsConflict(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	first := makeLocalBS("conflict_bs")
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	assertNameConflict(t, "second Create", repo.Create(ctx, makeLocalBS("conflict_bs")))
}

func TestRoleRepo_DuplicateName_IsConflict(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	taken := makeRole("conflict_role_taken", "local")
	if err := repo.Create(ctx, taken); err != nil {
		t.Fatalf("Create taken: %v", err)
	}
	assertNameConflict(t, "second Create", repo.Create(ctx, makeRole("conflict_role_taken", "local")))

	other := makeRole("conflict_role_other", "local")
	if err := repo.Create(ctx, other); err != nil {
		t.Fatalf("Create other: %v", err)
	}
	other.Name = taken.Name
	assertNameConflict(t, "Update onto a taken name", repo.Update(ctx, other))
}

func TestPrivilegeRepo_DuplicateName_IsConflict(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	// Migration 007 requires a selector for this privilege type.
	cs := makeCS(t, ctx, "conflict_priv_cs", `true`)

	taken := makePriv("conflict_priv_taken", &cs.ID)
	if err := repo.Create(ctx, taken); err != nil {
		t.Fatalf("Create taken: %v", err)
	}
	assertNameConflict(t, "second Create", repo.Create(ctx, makePriv("conflict_priv_taken", &cs.ID)))

	other := makePriv("conflict_priv_other", &cs.ID)
	if err := repo.Create(ctx, other); err != nil {
		t.Fatalf("Create other: %v", err)
	}
	other.Name = taken.Name
	assertNameConflict(t, "Update onto a taken name", repo.Update(ctx, other))
}

func TestContentSelectorRepo_DuplicateName_IsConflict(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	taken := &domain.ContentSelector{Name: "conflict_cs_taken", Expression: `format == "maven2"`}
	if err := repo.Create(ctx, taken); err != nil {
		t.Fatalf("Create taken: %v", err)
	}
	dup := &domain.ContentSelector{Name: "conflict_cs_taken", Expression: `format == "npm"`}
	assertNameConflict(t, "second Create", repo.Create(ctx, dup))

	other := &domain.ContentSelector{Name: "conflict_cs_other", Expression: `format == "npm"`}
	if err := repo.Create(ctx, other); err != nil {
		t.Fatalf("Create other: %v", err)
	}
	other.Name = taken.Name
	assertNameConflict(t, "Update onto a taken name", repo.Update(ctx, other))
}

func TestRoutingRuleRepo_DuplicateName_IsConflict(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	taken := newRR("conflict_rr_taken", "", "BLOCK", []string{".*"})
	insertRR(t, ctx, repo, taken)
	dup := newRR("conflict_rr_taken", "", "BLOCK", []string{".*"})
	assertNameConflict(t, "second Create", repo.Create(ctx, dup))

	other := newRR("conflict_rr_other", "", "BLOCK", []string{".*"})
	insertRR(t, ctx, repo, other)
	other.Name = taken.Name
	assertNameConflict(t, "Update onto a taken name", repo.Update(ctx, other))
}
