package database

import (
	"slices"

	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5/pgconn"
)

// IsUniqueViolation 报告是否为唯一约束冲突（SQLSTATE 23505）。
// constraints 非空时还要求约束名匹配。
func IsUniqueViolation(err error, constraints ...string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return len(constraints) == 0 || slices.Contains(constraints, pgErr.ConstraintName)
}

// IsForeignKeyViolation 报告是否为外键冲突（SQLSTATE 23503）。
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// IsCheckViolation 报告是否为检查约束冲突（SQLSTATE 23514）。
func IsCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
