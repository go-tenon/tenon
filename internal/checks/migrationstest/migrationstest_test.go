// Package migrationstest 检查框架各个包内嵌的迁移：每个包都登记在这里，能通过公开的 publish 命令原样发布，
// 全部放在一起时能按版本顺序执行。
package migrationstest

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/ai/chat"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/migratecmd"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/lock/pglock"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/login/passkey"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/oauthserver"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/privacy"
	privacyexport "github.com/go-tenon/tenon/privacy/export"
	"github.com/go-tenon/tenon/push"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/ratelimit/pglimit"
	"github.com/go-tenon/tenon/savedview"
	"github.com/go-tenon/tenon/schedule/scheduleapi"
	"github.com/go-tenon/tenon/search"
	"github.com/go-tenon/tenon/settings"
	"github.com/go-tenon/tenon/sheet/exports"
	"github.com/go-tenon/tenon/sheet/imports"
	"github.com/go-tenon/tenon/vector"
	"github.com/go-tenon/tenon/webhook"
)

// sources 是框架中带迁移的包，键是包相对模块根的目录。
// 新增带迁移的包时在这里登记，TestEveryPackageIsListed 检查没有遗漏。
var sources = map[string]fs.FS{
	"activity":             activity.Migrations(),
	"ai/chat":              chat.Migrations(),
	"apitoken":             apitoken.Migrations(),
	"authz":                authz.Migrations(),
	"sheet/exports":        exports.Migrations(),
	"feature":              feature.Migrations(),
	"filesystem":           filesystem.Migrations(),
	"sheet/imports":        imports.Migrations(),
	"lock/pglock":          pglock.Migrations(),
	"login":                login.Migrations(),
	"login/passkey":        passkey.Migrations(),
	"notification":         notification.Migrations(),
	"oauthserver":          oauthserver.Migrations(),
	"operation":            operation.Migrations(),
	"privacy":              privacy.Migrations(),
	"privacy/export":       privacyexport.Migrations(),
	"push":                 push.Migrations(),
	"queue":                queue.Migrations(),
	"ratelimit/pglimit":    pglimit.Migrations(),
	"savedview":            savedview.Migrations(),
	"schedule/scheduleapi": scheduleapi.Migrations(),
	"search":               search.Migrations(),
	"vector":               vector.Migrations(),
	"settings":             settings.Migrations(),
	"webhook":              webhook.Migrations(),
}

// TestEveryPackageIsListed 确保仓库中每个 migrations 目录都登记在 sources 中。
func TestEveryPackageIsListed(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	root := strings.TrimSpace(string(out))
	var found []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		switch d.Name() {
		case ".git", "testdata":
			return filepath.SkipDir
		case "migrations":
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			found = append(found, filepath.ToSlash(rel))
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range found {
		if _, ok := sources[dir]; !ok {
			t.Errorf("%s has migrations but is not listed in sources", dir)
		}
	}
	for dir := range sources {
		if !slices.Contains(found, dir) {
			t.Errorf("%s is listed in sources but has no migrations directory", dir)
		}
	}
}

// TestPublishThroughPublicCommand 确保内嵌的 SQL 能通过公开的 publish 命令原样发布，重复发布时什么也不写。
func TestPublishThroughPublicCommand(t *testing.T) {
	all := slices.Collect(maps.Values(sources))
	target := filepath.Join(t.TempDir(), "migrations")
	run := func() []string {
		t.Helper()
		root := command.Root("api", "test")
		root.AddCommand(migratecmd.Publish(all...))
		root.SetArgs([]string{"publish", "--dir", target, "--config", filepath.Join(target, "absent.yaml")})
		var output bytes.Buffer
		root.SetOut(&output)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return strings.Fields(output.String())
	}
	names := run()
	var count int
	for dir, src := range sources {
		entries, err := fs.ReadDir(src, ".")
		if err != nil || len(entries) == 0 {
			t.Fatalf("%s: embedded migrations=%v err=%v", dir, entries, err)
		}
		for _, entry := range entries {
			count++
			original, err := fs.ReadFile(src, entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			published, err := os.ReadFile(filepath.Join(target, entry.Name()))
			if err != nil {
				t.Fatalf("%s: %v", dir, err)
			}
			if !bytes.Equal(published, original) {
				t.Fatalf("published content differs from embedded SQL: %s", entry.Name())
			}
		}
	}
	if len(names) != count {
		t.Fatalf("published %d files, embedded %d", len(names), count)
	}
	if again := run(); len(again) != 0 {
		t.Fatalf("repeat published=%v", again)
	}
}

// TestAllMigrationsRun 把全部迁移放在一起执行：版本不重复，引用其他包的表的迁移排在那个包之后；
// 执行之后检查实体表的时间列以及全部表和字段的 COMMENT。
func TestAllMigrationsRun(t *testing.T) {
	db := dbtest.Open(t, slices.Collect(maps.Values(sources))...)
	dbtest.CheckSchema(t, db, timestampExempt...)
	dbtest.CheckComments(t, db)
}

// timestampExempt 是不检查时间列的表：ai/chat 中 trpc-agent-go 的表，它们的结构由上游决定。
var timestampExempt = []string{
	"agent_session_states", "agent_session_events", "agent_session_track_events", "agent_session_summaries",
	"agent_app_states", "agent_user_states",
}
