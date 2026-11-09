package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/ryan053857/mygoravel/config"
	"github.com/ryan053857/mygoravel/database"
	"github.com/ryan053857/mygoravel/database/migrate"
)

// EnvDSN 是测试管理员连接的环境变量。
const EnvDSN = "DATABASE_TEST_DSN"

// RequireDSN 返回管理员 DSN；未设置时失败（用于 integration 测试，禁止静默跳过）。
func RequireDSN(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		t.Fatalf("%s is required for this test", EnvDSN)
	}
	return dsn
}

// Open 创建空临时库、可选执行 migrations，返回 *bun.DB；测试结束删除库。
func Open(t testing.TB, migrations fs.FS) *bun.DB {
	t.Helper()
	adminDSN := RequireDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := database.Open(ctx, database.Config{DSN: config.Secret(adminDSN)})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	name := "test_" + randomName()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		db, err := database.Open(cleanCtx, database.Config{DSN: config.Secret(adminDSN)})
		if err != nil {
			t.Errorf("dbtest: reopen admin: %v", err)
			return
		}
		defer db.Close()
		if _, err := db.ExecContext(cleanCtx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dbtest: drop database: %v", err)
		}
	})

	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	cfg := database.DefaultConfig()
	cfg.DSN = config.Secret(u.String())
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if migrations != nil {
		p, err := migrate.NewProvider(db.DB, migrations)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrate.Up(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func randomName() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
