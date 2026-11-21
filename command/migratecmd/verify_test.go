package migratecmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/migratecmd"
)

func TestVerifyWithoutConfigurationOrDatabase(t *testing.T) {
	t.Parallel()
	const file = "2026_11_10_114451_create_files.sql"
	sql := []byte("-- +goose Up\nCREATE TABLE files (id integer);\n-- +goose Down\nDROP TABLE files;\n")
	source := fstest.MapFS{file: {Data: sql}}
	dir := filepath.Join(t.TempDir(), "migrations")
	absentCfg := filepath.Join(t.TempDir(), "absent.yaml")

	run := func(args ...string) (string, error) {
		mig := migratecmd.New(fstest.MapFS{}, dbConfig)
		mig.AddCommand(migratecmd.Publish(source), migratecmd.Verify(source))
		out, _, err := execute(newRoot(mig), args...)
		return out, err
	}

	// 尚未发布时 verify 失败并报告 missing。
	out, err := run("migrate", "verify", "--dir", dir, "--config", absentCfg)
	if err == nil || !strings.Contains(out, "missing") || !strings.Contains(out, file) {
		t.Fatalf("before publish: out=%q err=%v", out, err)
	}

	// 发布之后 verify 通过。
	if _, err := run("migrate", "publish", "--dir", dir, "--config", absentCfg); err != nil {
		t.Fatal(err)
	}
	if out, err := run("migrate", "verify", "--dir", dir, "--config", absentCfg); err != nil || out != "" {
		t.Fatalf("after publish: out=%q err=%v", out, err)
	}

	// 本地改写已发布的框架迁移后 verify 失败并报告 modified。
	if err := os.WriteFile(filepath.Join(dir, file), append(sql, []byte("-- drift\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run("migrate", "verify", "--dir", dir, "--config", absentCfg)
	if err == nil || !strings.Contains(out, "modified") || !strings.Contains(out, file) {
		t.Fatalf("after modify: out=%q err=%v", out, err)
	}
}

func TestVerifyRejectsExtraArguments(t *testing.T) {
	t.Parallel()
	root := newRoot(migratecmd.Verify())
	_, _, err := execute(root, "verify", "extra")
	if command.ExitCode(err) != command.ExitUsage {
		t.Fatalf("err=%v exit=%d", err, command.ExitCode(err))
	}
}
