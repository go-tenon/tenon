package migratecmd_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/migratecmd"
	"github.com/go-tenon/tenon/doctor"
)

// TestCheck 检查 Check 设置的自检在 up 之前运行：失败时不执行迁移，status 不受影响。
func TestCheck(t *testing.T) {
	path := serverConfig(t)
	migrations := fstest.MapFS{
		"2026_01_01_000001_create_notes.sql": {Data: []byte("-- +goose Up\nCREATE TABLE notes (id int);\n-- +goose Down\nDROP TABLE notes;\n")},
	}
	var ready bool
	var gotDSN string
	check := migratecmd.Check(func(cfg settings, db *bun.DB) []doctor.Group {
		gotDSN = cfg.Database.DSN
		return []doctor.Group{{Name: "postgres", Checks: []doctor.Check{
			{Name: "wal_level", Run: func(ctx context.Context) error {
				if err := db.PingContext(ctx); err != nil {
					return err
				}
				if !ready {
					return doctor.Fail("wal_level is replica", "set wal_level=logical")
				}
				return nil
			}},
		}}}
	})
	run := func(args ...string) (string, error) {
		root := newRoot(migratecmd.New(migrations, dbConfig, check))
		out, _, err := execute(root, append(args, "--config", path)...)
		return out, err
	}

	_, err := run("migrate", "up")
	if command.ExitCode(err) != command.ExitUnavailable || !strings.Contains(err.Error(), "postgres/wal_level: wal_level is replica\n    fix: set wal_level=logical") {
		t.Fatalf("up with a failing check: %v (exit %d)", err, command.ExitCode(err))
	}
	if gotDSN == "" {
		t.Error("Check did not receive the loaded configuration")
	}
	if out, err := run("migrate", "status"); err != nil || !strings.Contains(out, "pending") {
		t.Fatalf("status after the refused up: %v\n%s", err, out)
	}
	ready = true
	if out, err := run("migrate", "up"); err != nil || !strings.Contains(out, "2026_01_01_000001_create_notes.sql") {
		t.Fatalf("up with passing checks: %v\n%s", err, out)
	}
}
