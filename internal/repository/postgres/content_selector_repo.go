package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// ContentSelectorRepo is a postgres-backed implementation of repository.ContentSelectorRepo.
type ContentSelectorRepo struct{ pool *pgxpool.Pool }

// NewContentSelectorRepo returns a postgres-backed ContentSelectorRepo.
func NewContentSelectorRepo(pool *pgxpool.Pool) *ContentSelectorRepo {
	return &ContentSelectorRepo{pool: pool}
}

// List returns all content selectors ordered by name.
func (r *ContentSelectorRepo) List(ctx context.Context) ([]domain.ContentSelector, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, description, expression, created_at, updated_at
		 FROM content_selectors ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("content_selectors list: %w", err)
	}
	defer rows.Close()
	var out []domain.ContentSelector
	for rows.Next() {
		var s domain.ContentSelector
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Expression,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get returns the content selector with the given id, or repository.ErrNotFound.
func (r *ContentSelectorRepo) Get(ctx context.Context, id string) (*domain.ContentSelector, error) {
	var s domain.ContentSelector
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, description, expression, created_at, updated_at
		 FROM content_selectors WHERE id = $1`, id).
		Scan(&s.ID, &s.Name, &s.Description, &s.Expression, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("content_selectors get: %w", err)
	}
	return &s, nil
}

// GetByName returns the content selector with the given name, or repository.ErrNotFound.
func (r *ContentSelectorRepo) GetByName(ctx context.Context, name string) (*domain.ContentSelector, error) {
	var s domain.ContentSelector
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, description, expression, created_at, updated_at
		 FROM content_selectors WHERE name = $1`, name).
		Scan(&s.ID, &s.Name, &s.Description, &s.Expression, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("content_selectors get by name: %w", err)
	}
	return &s, nil
}

// Create inserts a new content selector and populates its generated fields.
func (r *ContentSelectorRepo) Create(ctx context.Context, s *domain.ContentSelector) error {
	return translateNameUnique(r.pool.QueryRow(ctx,
		`INSERT INTO content_selectors (name, description, expression)
		 VALUES ($1, $2, $3)
		 RETURNING id, created_at, updated_at`,
		s.Name, s.Description, s.Expression).
		Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt))
}

// Update overwrites the content selector identified by s.ID.
func (r *ContentSelectorRepo) Update(ctx context.Context, s *domain.ContentSelector) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE content_selectors
		 SET name=$1, description=$2, expression=$3, updated_at=NOW()
		 WHERE id=$4`,
		s.Name, s.Description, s.Expression, s.ID)
	if err != nil {
		if conflict, ok := nameConflict(err); ok {
			return conflict
		}
		return fmt.Errorf("content_selectors update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("content selector not found: %s", s.ID)
	}
	return nil
}

// Delete removes the content selector with the given id.
func (r *ContentSelectorRepo) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM content_selectors WHERE id = $1`, id)
	return err
}

// ListForUser joins user_roles → role_privileges → privileges (where
// content_selector_id is set) → content_selectors. DISTINCT guards against
// multiple paths to the same selector (nested roles, duplicate grants).
func (r *ContentSelectorRepo) ListForUser(ctx context.Context, userID string) ([]domain.ContentSelector, error) {
	if userID == "" {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT cs.id, cs.name, cs.description, cs.expression,
		       cs.created_at, cs.updated_at
		FROM content_selectors cs
		JOIN privileges p      ON p.content_selector_id = cs.id
		JOIN role_privileges rp ON rp.privilege_id = p.id
		JOIN user_roles ur      ON ur.role_id = rp.role_id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("content_selectors list for user: %w", err)
	}
	defer rows.Close()
	var out []domain.ContentSelector
	for rows.Next() {
		var s domain.ContentSelector
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Expression,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AttachToPrivilege sets the named privilege's content_selector_id to selectorID.
func (r *ContentSelectorRepo) AttachToPrivilege(ctx context.Context, privilegeName, selectorID string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE privileges SET content_selector_id = $1 WHERE name = $2`,
		selectorID, privilegeName)
	if err != nil {
		return fmt.Errorf("attach selector: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("privilege not found: %s", privilegeName)
	}
	return nil
}

// DetachFromPrivilege clears the named privilege's content_selector_id.
func (r *ContentSelectorRepo) DetachFromPrivilege(ctx context.Context, privilegeName string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE privileges SET content_selector_id = NULL WHERE name = $1`,
		privilegeName)
	if err != nil {
		return fmt.Errorf("detach selector: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("privilege not found: %s", privilegeName)
	}
	return nil
}
