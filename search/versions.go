package search

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/internal/indexversion"
)

// indexes 是 search 的索引版本表，版本的规则见 indexversion。
var indexes = indexversion.Table{Name: "search_indexes", Keys: "search_keys", Superseded: errSuperseded}

// dropBatch 是 dropStale 一个事务删除的文档数。
const dropBatch = 1000

// dropStale 删除已经废弃的版本中的文档，见 indexversion 的说明。
func dropStale(ctx context.Context, db bun.IDB, index string) error {
	st, err := indexes.Read(ctx, db, index, false)
	if err != nil {
		return err
	}
	below := max(st.Version, int(st.Building.Int64))
	for {
		var ids []int64
		err := db.NewRaw(`SELECT id FROM search_entries WHERE index_name = ? AND version < ? AND version <> ? LIMIT ?`,
			index, below, st.Version, dropBatch).Scan(ctx, &ids)
		if err != nil {
			return errors.Wrap(err, "search: find stale entries")
		}
		if len(ids) == 0 {
			return nil
		}
		err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			for _, q := range []string{`DELETE FROM search_values WHERE entry_id IN (?)`, `DELETE FROM search_entries WHERE id IN (?)`} {
				if _, err := tx.NewRaw(q, bun.List(ids)).Exec(ctx); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return errors.Wrap(err, "search: delete stale entries")
		}
	}
}
