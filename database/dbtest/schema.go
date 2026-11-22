package dbtest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/uptrace/bun"
)

// CheckSchema 检查数据库 db 中的表是否符合框架的结构约定：
//  1. 以 id 为主键的表，前三列依次是 id、created_at、updated_at，两个时间列都是 timestamptz NOT NULL；
//     有软删除的 deleted_at 列时，它是第四列，timestamptz 允许为空；关联表、锁、计数这类以其他列为主键的表不检查时间列。
//  2. 所有表和字段都有非空的 COMMENT（COMMENT ON TABLE / COMMENT ON COLUMN）。
//
// exempt 列出不检查的表名（例如上游库的表），goose_db_version 默认跳过。
func CheckSchema(t testing.TB, db *bun.DB, exempt ...string) {
	t.Helper()
	checkTimestamps(t, db, exempt)
	CheckComments(t, db, exempt...)
}

// CheckComments 检查数据库 db 的 public 模式中所有表和字段是否都有非空的 COMMENT（COMMENT ON TABLE / COMMENT ON COLUMN）。
// exempt 列出不检查的表名，goose_db_version 默认跳过。
func CheckComments(t testing.TB, db *bun.DB, exempt ...string) {
	t.Helper()
	var tables []struct {
		Name        string   `bun:"name"`
		Comment     string   `bun:"comment"`
		Columns     []string `bun:"columns,array"`
		ColComments []string `bun:"col_comments,array"`
	}
	err := db.NewRaw(`
SELECT c.relname::text AS name,
       COALESCE(pg_catalog.obj_description(c.oid, 'pg_class'), '') AS comment,
       COALESCE(array_agg(a.attname::text ORDER BY a.attnum) FILTER (WHERE a.attnum IS NOT NULL), ARRAY[]::text[]) AS columns,
       COALESCE(array_agg(COALESCE(pg_catalog.col_description(c.oid, a.attnum), '') ORDER BY a.attnum) FILTER (WHERE a.attnum IS NOT NULL), ARRAY[]::text[]) AS col_comments
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_catalog.pg_attribute a
  ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
WHERE n.nspname = 'public'
  AND c.relkind IN ('r', 'p')
GROUP BY c.oid, c.relname
ORDER BY c.relname`).Scan(context.Background(), &tables)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if table.Name == "goose_db_version" || slices.Contains(exempt, table.Name) {
			continue
		}
		if strings.TrimSpace(table.Comment) == "" {
			t.Errorf("%s: missing table comment (COMMENT ON TABLE)", table.Name)
		}
		for i, col := range table.Columns {
			if strings.TrimSpace(table.ColComments[i]) == "" {
				t.Errorf("%s.%s: missing column comment (COMMENT ON COLUMN)", table.Name, col)
			}
		}
	}
}

// checkTimestamps 检查数据库 db 中以 id 为主键的表的时间列顺序、类型与可空性。
func checkTimestamps(t testing.TB, db *bun.DB, exempt []string) {
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
