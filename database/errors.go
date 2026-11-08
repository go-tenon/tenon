package database

import (
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsUniqueViolation 报告 err 是否为唯一约束冲突（SQLSTATE 23505），
// 例如注册时邮箱已存在。constraints 非空时，还要求冲突的约束名是其中之一，
// 用于一张表有多个唯一约束、需要区分是哪一个冲突的情况：
//
//	if database.IsUniqueViolation(err, "users_email_key") {
//		return huma.Error409Conflict("邮箱已被注册")
//	}
//
// 其他错误码直接用 errors.As 取出 *pgconn.PgError 判断。
// 查询不到记录时 bun 返回 sql.ErrNoRows，用 errors.Is 判断。
func IsUniqueViolation(err error, constraints ...string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return len(constraints) == 0 || slices.Contains(constraints, pgErr.ConstraintName)
}
