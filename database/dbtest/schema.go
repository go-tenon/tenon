package dbtest

import (
	"context"
	"slices"
	"testing"

	"github.com/uptrace/bun"
)

// CheckSchema 检查数据库 db 中以 id 为主键的表是否符合框架的时间列约定：
// 前三列依次是 id、created_at、updated_at，两个时间列都是 timestamptz NOT NULL；
// 有软删除的 deleted_at 列时，它是第四列，timestamptz 允许为空。
// 关联表、锁、计数这类以其他列为主键的表不检查；exempt 列出不检查的表名（例如上游库的表），goose_db_version 默认跳过。
func CheckSchema(t testing.TB, db *bun.DB, exempt ...string) {
	t.Helper()
	var tables []struct {
		Name    string   `bun:"name"`
		Columns []string `bun:"columns,array"`
		Types   []string `bun:"types,array"`
		Nulls   []string `bun:"nulls,array"`
	}
	err := db.NewRaw(`
SELECT c.table_name AS name,
       array_agg(c.column_name::text ORDER BY c.ordinal_position) AS columns,
       array_agg(c.data_type::text ORDER BY c.ordinal_position) AS types,
       array_agg(c.is_nullable::text ORDER BY c.ordinal_position) AS nulls
FROM information_schema.columns c
WHERE c.table_schema = 'public'
  AND c.table_name IN (
    SELECT tc.table_name
    FROM information_schema.table_constraints tc
    JOIN information_schema.key_column_usage k
      ON k.constraint_name = tc.constraint_name AND k.table_schema = tc.table_schema
    WHERE tc.table_schema = 'public' AND tc.constraint_type = 'PRIMARY KEY'
    GROUP BY tc.table_name
    HAVING array_agg(k.column_name::text) = ARRAY['id'])
GROUP BY c.table_name
ORDER BY c.table_name`).Scan(context.Background(), &tables)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if table.Name == "goose_db_version" || slices.Contains(exempt, table.Name) {
			continue
		}
		if len(table.Columns) < 3 || !slices.Equal(table.Columns[:3], []string{"id", "created_at", "updated_at"}) {
			t.Errorf("%s: columns start with %v, want id, created_at, updated_at", table.Name, table.Columns[:min(3, len(table.Columns))])
			continue
		}
		for i := 1; i < 3; i++ {
			if table.Types[i] != "timestamp with time zone" || table.Nulls[i] != "NO" {
				t.Errorf("%s.%s: %s nullable=%s, want timestamptz NOT NULL", table.Name, table.Columns[i], table.Types[i], table.Nulls[i])
			}
		}
		if i := slices.Index(table.Columns, "deleted_at"); i >= 0 &&
			(i != 3 || table.Types[i] != "timestamp with time zone" || table.Nulls[i] != "YES") {
			t.Errorf("%s.deleted_at: column %d, %s nullable=%s, want column 4, nullable timestamptz", table.Name, i+1, table.Types[i], table.Nulls[i])
		}
	}
}
