package postgres

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexspence-oss/nexspence/internal/repository"
)

// pgerrUniqueViolation is Postgres' SQLSTATE for a unique-constraint violation.
const pgerrUniqueViolation = "23505"

// uniqueViolation reports whether err is a unique-constraint violation and, if
// so, which constraint raised it. A violation is a client-visible conflict, not
// an internal failure, so repositories translate it instead of letting a raw
// driver error — constraint name, SQLSTATE and all — reach the caller.
func uniqueViolation(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrUniqueViolation {
		return "", false
	}
	return pgErr.ConstraintName, true
}

// nameConstraintSuffix is how Postgres names the implicit constraint behind a
// `name TEXT NOT NULL UNIQUE` column: "<table>_name_key".
const nameConstraintSuffix = "_name_key"

// translateNameUnique converts a unique-violation on a catalog table's name
// column into repository.ErrAlreadyExists naming that field, so a duplicate
// name answers as the conflict it is instead of leaking the constraint name,
// table name and SQLSTATE of the raw driver error. A violation of any other
// constraint passes through untouched rather than being reported as a name
// conflict it is not.
func translateNameUnique(err error) error {
	constraint, ok := uniqueViolation(err)
	if !ok || !strings.HasSuffix(constraint, nameConstraintSuffix) {
		return err
	}
	return &repository.UniqueViolationError{Field: "name"}
}
