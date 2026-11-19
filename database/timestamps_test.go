package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
)

// stamped 是测试用的模型，嵌入 database.Timestamps。
type stamped struct {
	bun.BaseModel `bun:"table:stamped"`

	ID int64 `bun:"id,pk"`
	database.Timestamps
	Name string `bun:"name,notnull"`
}

// openStamped 返回建好 stamped 表的临时数据库。
func openStamped(t *testing.T) *bun.DB {
	t.Helper()
	db := dbtest.Open(t)
	_, err := db.ExecContext(context.Background(), `CREATE TABLE stamped (
    id bigint PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    name text NOT NULL
)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// load 读出 id 这一行。
func load(t *testing.T, db bun.IDB, id int64) stamped {
	t.Helper()
	var s stamped
	if err := db.NewSelect().Model(&s).Where("id = ?", id).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTimestampsInsert(t *testing.T) {
	t.Parallel()
	db, ctx := openStamped(t), context.Background()
	before := time.Now().UTC().Add(-time.Second)

	fresh := &stamped{ID: 1, Name: "fresh"}
	if _, err := db.NewInsert().Model(fresh).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if fresh.CreatedAt.Before(before) || !fresh.UpdatedAt.Equal(fresh.CreatedAt) || fresh.CreatedAt.Location() != time.UTC {
		t.Fatalf("created_at=%v updated_at=%v", fresh.CreatedAt, fresh.UpdatedAt)
	}
	if got := load(t, db, 1); got != *fresh {
		t.Fatalf("stored %+v, model %+v", got, *fresh)
	}

	// 调用方给出的创建时间保留，例如导入旧数据。
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	imported := &stamped{ID: 2, Timestamps: database.Timestamps{CreatedAt: old}, Name: "imported"}
	if _, err := db.NewInsert().Model(imported).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got := load(t, db, 2); !got.CreatedAt.Equal(old) || !got.UpdatedAt.Equal(old) {
		t.Fatalf("imported %+v", got)
	}

	// 批量插入时每个元素都经过钩子。
	batch := []stamped{{ID: 3, Name: "a"}, {ID: 4, Name: "b"}}
	if _, err := db.NewInsert().Model(&batch).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, s := range batch {
		if got := load(t, db, s.ID); got.CreatedAt.Before(before) || !got.UpdatedAt.Equal(got.CreatedAt) {
			t.Fatalf("batch %+v", got)
		}
	}
}

func TestTimestampsUpdate(t *testing.T) {
	t.Parallel()
	db, ctx := openStamped(t), context.Background()
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name   string
		update func(s *stamped) *bun.UpdateQuery
	}{
		{"model, all columns", func(s *stamped) *bun.UpdateQuery { return db.NewUpdate().Model(s).WherePK() }},
		{"model, columns with updated_at", func(s *stamped) *bun.UpdateQuery {
			return db.NewUpdate().Model(s).Column("name", "updated_at").WherePK()
		}},
		{"nil model with Set", func(s *stamped) *bun.UpdateQuery {
			return db.NewUpdate().Model((*stamped)(nil)).Set("name = ?", s.Name).Where("id = ?", s.ID)
		}},
	}
	for i, c := range cases {
		id := int64(i + 1)
		row := &stamped{ID: id, Timestamps: database.Timestamps{CreatedAt: old, UpdatedAt: old}, Name: "before"}
		if _, err := db.NewInsert().Model(row).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		row.Name = "after"
		if _, err := c.update(row).Exec(ctx); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := load(t, db, id)
		if got.Name != "after" || !got.CreatedAt.Equal(old) || !got.UpdatedAt.After(old) {
			t.Fatalf("%s: %+v", c.name, got)
		}
	}
}

// view 用 bun:"-" 嵌入 stamped，Go 把 Timestamps 的钩子提升到 view 上，但 views 表没有时间列。
type view struct {
	bun.BaseModel `bun:"table:views"`
	stamped       `bun:"-"`

	Key  int64  `bun:"key,pk"`
	Note string `bun:"note,notnull"`
}

func TestTimestampsSkipTablesWithoutColumns(t *testing.T) {
	t.Parallel()
	db, ctx := dbtest.Open(t), context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE views (key bigint PRIMARY KEY, note text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	v := &view{Key: 1, Note: "a"}
	if _, err := db.NewInsert().Model(v).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Model((*view)(nil)).Set("note = ?", "b").Where("key = 1").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Model(v).WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !v.CreatedAt.IsZero() || !v.UpdatedAt.IsZero() {
		t.Fatalf("hook touched the embedded model: %+v", v.stamped)
	}
}
