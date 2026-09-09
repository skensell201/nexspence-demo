package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type privilegeRepo struct{ db *pgxpool.Pool }

// NewPrivilegeRepo returns a postgres-backed PrivilegeRepo.
func NewPrivilegeRepo(db *pgxpool.Pool) *privilegeRepo {
	return &privilegeRepo{db: db}
}

func (r *privilegeRepo) List(ctx context.Context) ([]domain.Privilege, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, description, type, attrs, content_selector_id, builtin, created_at
		FROM privileges ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Privilege
	for rows.Next() {
		p, err := scanPrivilege(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *privilegeRepo) Get(ctx context.Context, id string) (*domain.Privilege, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, description, type, attrs, content_selector_id, builtin, created_at
		FROM privileges WHERE id = $1`, id)
	p, err := scanPrivilege(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return p, err
}

func (r *privilegeRepo) GetByName(ctx context.Context, name string) (*domain.Privilege, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, description, type, attrs, content_selector_id, builtin, created_at
		FROM privileges WHERE name = $1`, name)
	p, err := scanPrivilege(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return p, err
}

func (r *privilegeRepo) Create(ctx context.Context, p *domain.Privilege) error {
	attrsJSON, err := json.Marshal(p.Attrs)
	if err != nil {
		return err
	}
	return translateNameUnique(r.db.QueryRow(ctx, `
		INSERT INTO privileges (name, description, type, attrs, content_selector_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`,
		p.Name, p.Description, string(p.Type), attrsJSON, p.ContentSelectorID,
	).Scan(&p.ID, &p.CreatedAt))
}

func (r *privilegeRepo) Update(ctx context.Context, p *domain.Privilege) error {
	attrsJSON, err := json.Marshal(p.Attrs)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `
		UPDATE privileges SET name=$1, description=$2, type=$3, attrs=$4, content_selector_id=$5
		WHERE id=$6`,
		p.Name, p.Description, string(p.Type), attrsJSON, p.ContentSelectorID, p.ID,
	)
	return translateNameUnique(err)
}

func (r *privilegeRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM privileges WHERE id=$1 AND builtin=false`, id)
	return err
}

func (r *privilegeRepo) ListByRole(ctx context.Context, roleID string) ([]domain.Privilege, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.id, p.name, p.description, p.type, p.attrs, p.content_selector_id, p.builtin, p.created_at
		FROM privileges p
		JOIN role_privileges rp ON rp.privilege_id = p.id
		WHERE rp.role_id = $1
		ORDER BY p.name`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Privilege
	for rows.Next() {
		p, err := scanPrivilege(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *privilegeRepo) PrivilegeRoleMap(ctx context.Context) (map[string][]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT rp.privilege_id, ro.name
		 FROM role_privileges rp
		 JOIN roles ro ON ro.id = rp.role_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string][]string)
	for rows.Next() {
		var privID, roleName string
		if err := rows.Scan(&privID, &roleName); err != nil {
			return nil, err
		}
		m[privID] = append(m[privID], roleName)
	}
	return m, rows.Err()
}

func scanPrivilege(row scanner) (*domain.Privilege, error) {
	var p domain.Privilege
	var attrsRaw []byte
	var ptype string
	err := row.Scan(&p.ID, &p.Name, &p.Description, &ptype, &attrsRaw,
		&p.ContentSelectorID, &p.Builtin, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	p.Type = domain.PrivilegeType(ptype)
	// Attrs carries the privilege's match criteria, so a silent decode failure
	// here is the same class of problem as a corrupt rbac `actions` array: the
	// privilege degrades to one that matches nothing, with no trace.
	unmarshalJSONB(attrsRaw, &p.Attrs, "privileges", p.ID, "attrs")
	if p.Attrs == nil {
		p.Attrs = map[string]any{}
	}
	return &p, nil
}
