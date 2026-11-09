// Package dbtest 为集成测试创建临时数据库。
//
// 测试需要 Postgres 18+，通过环境变量 DATABASE_TEST_DSN 指定一个有建库权限的连接，例如：
//
//	docker run -d --name pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 postgres:18
//	export DATABASE_TEST_DSN='postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable'
//	go test ./...
//
// 未设置时调用 DSN 的测试会被跳过。
package dbtest

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"strings"
	"testing"

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
