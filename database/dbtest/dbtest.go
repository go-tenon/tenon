// Package dbtest 为集成测试创建临时数据库。
//
// 测试需要 Postgres 18+，通过环境变量 DATABASE_TEST_DSN 指定一个有建库权限的连接，例如：
//
//	docker run -d --name pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 postgres:18
//	export DATABASE_TEST_DSN='postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable'
//	go test ./...
//
// 未设置时调用 DSN 的测试会被跳过。
//
// 大多数测试直接用 New：建临时库、发布并执行模块迁移，返回连接：
//
//	db := dbtest.New(t, account.Migrations, audit.Migrations)
package dbtest

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/config"
	"github.com/ryan053857/go-kit/database"
)

// EnvDSN 是指定测试数据库连接的环境变量名。
const EnvDSN = "DATABASE_TEST_DSN"

// DSN 创建一个空的临时数据库并返回其连接串，测试结束时删除。未设置 DATABASE_TEST_DSN 时跳过测试。
func DSN(t testing.TB) config.Secret {
	t.Helper()
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		t.Skip(EnvDSN + " not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("dbtest: %s must be a URL: %v", EnvDSN, err)
	}

	admin, err := database.Open(t.Context(), database.Config{DSN: config.Secret(dsn)})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "test_" + strings.ToLower(rand.Text()[:12])
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db, err := database.Open(context.Background(), database.Config{DSN: config.Secret(dsn)})
		if err != nil {
			t.Logf("dbtest: drop %s: %v", name, err)
			return
		}
		defer db.Close()
		if _, err := db.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Logf("dbtest: drop %s: %v", name, err)
		}
	})

	u.Path = "/" + name
	return config.Secret(u.String())
}

// New 创建临时数据库，把 sources 的迁移发布到临时目录并按顺序执行，返回连接（测试结束时关闭）。
// 没有设置 DATABASE_TEST_DSN 时跳过测试。
func New(t testing.TB, sources ...database.MigrationSource) *bun.DB {
	t.Helper()
	cfg := database.DefaultConfig()
	cfg.DSN = DSN(t)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if len(sources) > 0 {
		dir := t.TempDir()
		if _, err := database.PublishMigrations(dir, sources...); err != nil {
			t.Fatal(err)
		}
		if err := database.Migrate(t.Context(), db.DB, os.DirFS(dir)); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
