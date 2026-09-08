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

// RoutingRuleRepo is a postgres-backed implementation of repository.RoutingRuleRepo.
type RoutingRuleRepo struct{ pool *pgxpool.Pool }

// NewRoutingRuleRepo returns a postgres-backed RoutingRuleRepo.
func NewRoutingRuleRepo(pool *pgxpool.Pool) *RoutingRuleRepo {
	return &RoutingRuleRepo{pool: pool}
}

// List returns all routing rules ordered by name.
func (r *RoutingRuleRepo) List(ctx context.Context) ([]domain.RoutingRule, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, description, mode, matchers, created_at, updated_at
		 FROM routing_rules ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("routing_rules list: %w", err)
	}
	defer rows.Close()
	var rules []domain.RoutingRule
	for rows.Next() {
		var rr domain.RoutingRule
		if err := rows.Scan(&rr.ID, &rr.Name, &rr.Description, &rr.Mode,
			&rr.Matchers, &rr.CreatedAt, &rr.UpdatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, rr)
	}
	return rules, rows.Err()
}

// Get returns the routing rule with the given id, or repository.ErrNotFound.
func (r *RoutingRuleRepo) Get(ctx context.Context, id string) (*domain.RoutingRule, error) {
	var rr domain.RoutingRule
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, description, mode, matchers, created_at, updated_at
		 FROM routing_rules WHERE id = $1`, id).
		Scan(&rr.ID, &rr.Name, &rr.Description, &rr.Mode,
			&rr.Matchers, &rr.CreatedAt, &rr.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("routing_rules get: %w", err)
	}
	return &rr, nil
}

// GetByName returns the routing rule with the given name, or repository.ErrNotFound.
func (r *RoutingRuleRepo) GetByName(ctx context.Context, name string) (*domain.RoutingRule, error) {
	var rr domain.RoutingRule
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, description, mode, matchers, created_at, updated_at
		 FROM routing_rules WHERE name = $1`, name).
		Scan(&rr.ID, &rr.Name, &rr.Description, &rr.Mode,
			&rr.Matchers, &rr.CreatedAt, &rr.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("routing_rules get by name: %w", err)
	}
	return &rr, nil
}

// Create inserts a new routing rule and populates its generated fields.
func (r *RoutingRuleRepo) Create(ctx context.Context, rr *domain.RoutingRule) error {
	return translateNameUnique(r.pool.QueryRow(ctx,
		`INSERT INTO routing_rules (name, description, mode, matchers)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at, updated_at`,
		rr.Name, rr.Description, rr.Mode, rr.Matchers).
		Scan(&rr.ID, &rr.CreatedAt, &rr.UpdatedAt))
}

// Update overwrites the routing rule identified by rr.ID.
func (r *RoutingRuleRepo) Update(ctx context.Context, rr *domain.RoutingRule) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE routing_rules
		 SET name=$1, description=$2, mode=$3, matchers=$4, updated_at=NOW()
		 WHERE id=$5`,
		rr.Name, rr.Description, rr.Mode, rr.Matchers, rr.ID)
	if err != nil {
		if conflict := translateNameUnique(err); conflict != err {
			return conflict
		}
		return fmt.Errorf("routing_rules update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("routing rule not found: %s", rr.ID)
	}
	return nil
}

// Delete removes the routing rule with the given id.
func (r *RoutingRuleRepo) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM routing_rules WHERE id = $1`, id)
	return err
}
