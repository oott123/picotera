package server

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation returns true iff err carries Postgres SQLSTATE 23505.
// Centralized here so every handler's "is this a duplicate-key collision?"
// branch shares one canonical check.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
