package database_test

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/uptrace/bun"
	"golang.org/x/sync/errgroup"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
)

type User struct {
	bun.BaseModel `bun:"table:users"`
	database.Model

	Email string `bun:"email,notnull"`
	Name  string `bun:"name,notnull"`
}

// 集成测试需要 Postgres 18+，见 dbtest 包。每个测试创建一个独立的临时数据库。
func newTestDB(t *testing.T) *bun.DB {
	t.Helper()
	cfg := database.DefaultConfig()
	cfg.DSN = dbtest.DSN(t)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func migrate(t *testing.T, db *bun.DB) {
	t.Helper()
	if err := database.Migrate(t.Context(), db.DB, os.DirFS("testdata/migrations")); err != nil {
		t.Fatal(err)
	}
}

func load(t *testing.T, db *bun.DB, id uuid.UUID) User {
	t.Helper()
	var u User
	if err := db.NewSelect().Model(&u).Where("id = ?", id).Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestModelHook(t *testing.T) {
	ctx := t.Context()

	var u User
	if err := u.BeforeAppendModel(ctx, &bun.InsertQuery{}); err != nil {
		t.Fatal(err)
	}
	if u.ID == uuid.Nil() || u.ID[6]>>4 != 7 {
		t.Errorf("ID = %v, want UUIDv7", u.ID)
	}
	if u.CreatedAt.IsZero() || !u.UpdatedAt.Equal(u.CreatedAt) {
		t.Errorf("CreatedAt = %v, UpdatedAt = %v", u.CreatedAt, u.UpdatedAt)
	}
	if u.CreatedAt.Nanosecond()%1000 != 0 {
		t.Errorf("CreatedAt = %v, want microsecond precision", u.CreatedAt)
	}

	// 已有的值不会被覆盖。
	id, created := u.ID, u.CreatedAt.Add(-time.Hour)
	u2 := User{Model: database.Model{ID: id, CreatedAt: created}}
	_ = u2.BeforeAppendModel(ctx, &bun.InsertQuery{})
	if u2.ID != id || !u2.CreatedAt.Equal(created) || !u2.UpdatedAt.Equal(created) {
		t.Errorf("preset values overwritten: %+v", u2.Model)
	}

	before := u.UpdatedAt
	time.Sleep(time.Millisecond)
	_ = u.BeforeAppendModel(ctx, &bun.UpdateQuery{})
	if !u.UpdatedAt.After(before) || u.ID != id {
		t.Errorf("after update: %+v", u.Model)
	}

	before = u.UpdatedAt
	_ = u.BeforeAppendModel(ctx, &bun.SelectQuery{})
	_ = u.BeforeAppendModel(ctx, &bun.DeleteQuery{})
	if !u.UpdatedAt.Equal(before) {
		t.Errorf("select/delete changed UpdatedAt")
	}
}

func TestOpenInvalidDSN(t *testing.T) {
	_, err := database.Open(t.Context(), database.Config{DSN: "postgres://u:secret@127.0.0.1:1/db"})
	if err == nil {
		t.Fatal("want error")
	}
	if s := err.Error(); strings.Contains(s, "secret") {
		t.Errorf("error leaks password: %s", s)
	}
}

func TestInsertAndUpdate(t *testing.T) {
	db := newTestDB(t)
	migrate(t, db)
	ctx := t.Context()

	u := &User{Email: "a@example.com", Name: "A"}
	if _, err := db.NewInsert().Model(u).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got := load(t, db, u.ID)
	if got.Model != u.Model {
		// 时间精度与时区可能不同，用 Equal 再比一次。
		if got.ID != u.ID || !got.CreatedAt.Equal(u.CreatedAt) || !got.UpdatedAt.Equal(u.UpdatedAt) {
			t.Fatalf("stored %+v, want %+v", got.Model, u.Model)
		}
	}

	// 通过模型更新全部列：hook 设置的值写入数据库，触发器不覆盖。
	time.Sleep(time.Millisecond)
	prev := u.UpdatedAt
	u.Name = "B"
	if _, err := db.NewUpdate().Model(u).WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got = load(t, db, u.ID)
	if !u.UpdatedAt.After(prev) || !got.UpdatedAt.Equal(u.UpdatedAt) || got.Name != "B" {
		t.Errorf("full update: stored %v, struct %v, prev %v", got.UpdatedAt, u.UpdatedAt, prev)
	}
	if !got.CreatedAt.Equal(u.CreatedAt) {
		t.Errorf("CreatedAt changed: %v -> %v", u.CreatedAt, got.CreatedAt)
	}

	// 只更新部分列：updated_at 不在 SET 中，由触发器维护。
	time.Sleep(time.Millisecond)
	prev = got.UpdatedAt
	u.Name = "C"
	if _, err := db.NewUpdate().Model(u).Column("name").WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got = load(t, db, u.ID); !got.UpdatedAt.After(prev) {
		t.Errorf("column update: updated_at %v not after %v", got.UpdatedAt, prev)
	}

	// 不经过模型的更新同样由触发器维护。
	time.Sleep(time.Millisecond)
	prev = got.UpdatedAt
	if _, err := db.NewUpdate().Table("users").Set("name = ?", "D").Where("id = ?", u.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got = load(t, db, u.ID); !got.UpdatedAt.After(prev) || got.Name != "D" {
		t.Errorf("table update: updated_at %v not after %v", got.UpdatedAt, prev)
	}

	// 手写 SQL 插入使用数据库默认值。
	var id uuid.UUID
	if err := db.QueryRowContext(ctx, "INSERT INTO users (email) VALUES ('raw@example.com') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id[6]>>4 != 7 {
		t.Errorf("default id %v is not UUIDv7", id)
	}
}

func TestBulkInsert(t *testing.T) {
	db := newTestDB(t)
	migrate(t, db)
	ctx := t.Context()

	users := []User{{Email: "1@example.com"}, {Email: "2@example.com"}, {Email: "3@example.com"}}
	if _, err := db.NewInsert().Model(&users).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for i, u := range users {
		if u.ID == uuid.Nil() || u.CreatedAt.IsZero() {
			t.Errorf("users[%d] not filled: %+v", i, u.Model)
		}
		if i > 0 && u.ID.Compare(users[i-1].ID) <= 0 {
			t.Errorf("UUIDv7 not increasing: %v <= %v", u.ID, users[i-1].ID)
		}
	}

	var got []User
	if err := db.NewSelect().Model(&got).Order("id").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != users[0].ID || got[2].Email != "3@example.com" {
		t.Errorf("got %+v", got)
	}
}

func TestErrors(t *testing.T) {
	db := newTestDB(t)
	migrate(t, db)
	ctx := t.Context()

	if _, err := db.NewInsert().Model(&User{Email: "dup@example.com"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := db.NewInsert().Model(&User{Email: "dup@example.com"}).Exec(ctx)
	if !database.IsUniqueViolation(err) {
		t.Errorf("IsUniqueViolation(%v) = false", err)
	}
	if !database.IsUniqueViolation(err, "users_pkey", "users_email_key") {
		t.Errorf("IsUniqueViolation with constraint = false: %v", err)
	}
	if database.IsUniqueViolation(err, "users_pkey") {
		t.Errorf("IsUniqueViolation matched wrong constraint")
	}
	if database.IsUniqueViolation(nil) || database.IsUniqueViolation(errors.New("x")) {
		t.Errorf("IsUniqueViolation matched non-pg error")
	}

	var u User
	err = db.NewSelect().Model(&u).Where("id = ?", uuid.NewV7()).Scan(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("not found: %v", err)
	}
}

func TestMigrateConcurrent(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()

	// 模拟多个实例同时启动。
	var g errgroup.Group
	for range 4 {
		g.Go(func() error {
			return database.Migrate(ctx, db.DB, os.DirFS("testdata/migrations"))
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("applied %d migrations, want 2", n)
	}
}
