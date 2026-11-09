package database

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// RunInTx 在显式事务中执行 fn。不支持隐式嵌套：已在事务中应直接使用当前句柄。
// 取消遵循 ctx；fn 返回错误则回滚。
func RunInTx(ctx context.Context, db *bun.DB, opts *sql.TxOptions, fn func(ctx context.Context, tx bun.Tx) error) error {
	if db == nil {
		return errors.New("database: nil db")
	}
	if fn == nil {
		return errors.New("database: nil tx func")
	}
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return errors.Wrap(err, "database: begin")
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "database: commit")
	}
	return nil
}
