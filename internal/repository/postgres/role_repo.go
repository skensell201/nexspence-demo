package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type roleRepo struct {
	db *pgxpool.Pool
}

// NewRoleRepo returns a postgres-backed RoleRepo.
func NewRoleRepo(db *pgxpool.Pool) *roleRepo {
	return &roleRepo{db: db}
}

func (r *roleRepo) List(ctx context.Context) ([]domain.Role, error) {
	rows, err := r.db.Query(ctx, `
		SELECT r.id, r.name, r.description, r.source, r.builtin, r.created_at, r.updated_at,
		       COALESCE(array_agg(rp.privilege_id::text) FILTER (WHERE rp.privilege_id IS NOT NULL), '{}') AS privilege_ids
		FROM roles r
		LEFT JOIN role_privileges rp ON rp.role_id = r.id
		GROUP BY r.id
		ORDER BY r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		var ro domain.Role
		var privIDs []string
		err := rows.Scan(&ro.ID, &ro.Name, &ro.Description, &ro.Source, &ro.ReadOnly,
			&ro.CreatedAt, &ro.UpdatedAt, &privIDs)
		if err != nil {
			return nil, err
		}
		ro.Privileges = privIDs
		ro.Roles = []string{}
		roles = append(roles, ro)
	}
	return roles, rows.Err()
}

func (r *roleRepo) Get(ctx context.Context, id string) (*domain.Role, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, description, source, builtin, created_at, updated_at
		FROM roles WHERE id = $1`, id)
	ro, err := scanRole(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return ro, err
}

func (r *roleRepo) GetByName(ctx context.Context, name string) (*domain.Role, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, description, source, builtin, created_at, updated_at
		FROM roles WHERE name = $1`, name)
	ro, err := scanRole(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return ro, err
}

func (r *roleRepo) Create(ctx context.Context, ro *domain.Role) error {
	return translateNameUnique(r.db.QueryRow(ctx, `
		INSERT INTO roles (name, description, source, builtin)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		ro.Name, ro.Description, ro.Source, ro.ReadOnly,
	).Scan(&ro.ID, &ro.CreatedAt, &ro.UpdatedAt))
}

func (r *roleRepo) Update(ctx context.Context, ro *domain.Role) error {
	_, err := r.db.Exec(ctx, `
		UPDATE roles SET name=$1, description=$2, updated_at=NOW()
		WHERE id=$3`,
		ro.Name, ro.Description, ro.ID,
	)
	return translateNameUnique(err)
}

func (r *roleRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM roles WHERE id=$1`, id)
	return err
}

func (r *roleRepo) GetUserRoles(ctx context.Context, userID string) ([]domain.Role, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ro.id, ro.name, ro.description, ro.source, ro.builtin, ro.created_at, ro.updated_at
		FROM roles ro
		JOIN user_roles ur ON ur.role_id = ro.id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		ro, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, *ro)
	}
	return roles, rows.Err()
}

func (r *roleRepo) SetUserRoles(ctx context.Context, userID string, roleIDs []string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, roleID := range roleIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			userID, roleID,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *roleRepo) SetPrivileges(ctx context.Context, roleID string, privilegeIDs []string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM role_privileges WHERE role_id = $1`, roleID); err != nil {
		return err
	}
	for _, pid := range privilegeIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO role_privileges (role_id, privilege_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			roleID, pid,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *roleRepo) ListPrivilegeIDsByRole(ctx context.Context, roleID string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT privilege_id FROM role_privileges WHERE role_id = $1`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, rows.Err()
}

func scanRole(row scanner) (*domain.Role, error) {
	var ro domain.Role
	err := row.Scan(&ro.ID, &ro.Name, &ro.Description, &ro.Source, &ro.ReadOnly,
		&ro.CreatedAt, &ro.UpdatedAt)
	if err != nil {
		return nil, err
	}
	ro.Privileges = []string{}
	ro.Roles = []string{}
	return &ro, nil
}
