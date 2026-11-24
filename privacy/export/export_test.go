package export_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/filesystem/local"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/privacy"
	"github.com/go-tenon/tenon/privacy/export"
	"github.com/go-tenon/tenon/push"
	"github.com/go-tenon/tenon/queue"
)

var (
	alice = identity.Subject{ID: "u1", Type: "user", Tenant: "acme"}
	bob   = identity.Subject{ID: "u2", Type: "user", Tenant: "acme"}
)

var errLocked = errorx.Define(errorx.Conflict, "test.locked")

// migrations 是测试用到的迁移。
func migrations() []fs.FS {
	return []fs.FS{filesystem.Migrations(), queue.Migrations(), operation.Migrations(), export.Migrations(), notification.Migrations(), apitoken.Migrations(), push.Migrations(), activity.Migrations()}
}

type env struct {
	db       *bun.DB
	files    *filesystem.Service
	ops      *operation.Service
	tokens   *apitoken.Store
	personal *export.Service
	locked   sync.Map // 其中的用户在应用自己的 profile 一项中返回业务错误
	down     sync.Map // 其中的用户在应用自己的 profile 一项中返回其他错误
}

func newEnv(t *testing.T, extra ...privacy.Source) *env {
	t.Helper()
	db := dbtest.Open(t, migrations()...)
	disk, err := local.Open(filesystem.LocalConfig{Root: filepath.Join(t.TempDir(), "files"), PublicURL: "/storage"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disk.Close() })
	var cfg filesystem.Config
	cfg.SetDefaults()
	files, err := filesystem.New(db, cfg, map[string]filesystem.Disk{"local": disk})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{db: db, files: files, ops: operation.New(db), tokens: apitoken.NewStore(db, apitoken.Config{Lifetime: time.Hour})}
	sources := append([]privacy.Source{
		{Name: "profile", Export: func(_ context.Context, _ bun.IDB, ref identity.Ref) (any, error) {
			if _, ok := e.locked.Load(ref.ID); ok {
				return nil, errLocked.New()
			}
			if _, ok := e.down.Load(ref.ID); ok {
				return nil, errors.New("profile store down")
			}
			return map[string]string{"id": ref.ID, "nickname": "Alice"}, nil
		}},
		{Name: "login", Erase: func(context.Context, bun.IDB, identity.Ref) error { return nil }}, // 没有 Export，不导出
		{Name: "notifications", Erase: notification.Erase, Export: notification.Export},
		{Name: "api_tokens", Erase: apitoken.Erase, Export: apitoken.Export},
		{Name: "push", Erase: push.Erase, Export: push.Export},
		{Name: "activity", Export: activity.Export},
		{Name: "empty", Export: func(context.Context, bun.IDB, identity.Ref) (any, error) { return nil, nil }},
	}, extra...)
	deletions := privacy.New(db, privacy.Options{Type: "user", Sources: sources})
	e.personal = export.New(db, e.ops, files, deletions)
	return e
}

// seed 给 sub 写入一条通知、一个 token、一台设备和一条操作日志。
func (e *env) seed(t *testing.T, sub identity.Subject) {
	t.Helper()
	ctx := context.Background()
	if _, err := notification.Send(ctx, e.db, notification.Notification{Event: "welcome", Data: map[string]any{"name": sub.ID}}, notification.RecipientOf(sub)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tokens.Login(ctx, sub, apitoken.Device{IP: "203.0.113.7", UserAgent: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := push.Register(ctx, e.db, sub, push.IOS, "device-"+sub.ID); err != nil {
		t.Fatal(err)
	}
	if err := activity.Record(ctx, e.db, sub, activity.Activity{Event: "article.published", TargetType: "article", TargetID: "a1"}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) start(t *testing.T, sub identity.Subject) *export.Export {
	t.Helper()
	exp, err := e.personal.Start(context.Background(), e.db, sub)
	if err != nil {
		t.Fatal(err)
	}
	return exp
}

func (e *env) get(t *testing.T, sub identity.Subject, id uuid.UUID) *export.Export {
	t.Helper()
	exp, err := e.personal.Get(context.Background(), sub, id)
	if err != nil {
		t.Fatal(err)
	}
	return exp
}

// unzip 读出压缩包中的每个文件。
func (e *env) unzip(t *testing.T, sub identity.Subject, id uuid.UUID) map[string][]byte {
	t.Helper()
	body, _, err := e.personal.Open(context.Background(), sub, id)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name], err = io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

type recorder struct {
	mu    sync.Mutex
	saves []string
}

func (r *recorder) saved(_ context.Context, _ database.Tx, exp *export.Export) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saves = append(r.saves, fmt.Sprintf("%s:%d/%d", exp.Status, exp.Processed, exp.Total))
	return nil
}

func TestProcess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	e.seed(t, alice)
	e.seed(t, bob)
	exp := e.start(t, alice)
	if exp.Kind != export.Kind || exp.Status != operation.StatusPending {
		t.Fatalf("start: %+v", exp)
	}
	if _, _, err := e.personal.Open(ctx, alice, exp.ID); !errors.Is(err, export.ErrNotReady) {
		t.Fatalf("open before process: %v", err)
	}

	rec := &recorder{}
	if err := e.personal.Process(ctx, exp.ID, rec.saved); err != nil {
		t.Fatal(err)
	}
	got := e.get(t, alice, exp.ID)
	if got.Status != operation.StatusCompleted || got.Processed != 6 || got.Total != 6 ||
		!strings.HasPrefix(got.Filename, "personal-data-") || !strings.HasSuffix(got.Filename, ".zip") || got.ExpiresAt == nil {
		t.Fatalf("completed: %+v", got)
	}
	want := []string{"running:0/0", "running:0/6", "running:6/6", "completed:6/6"}
	if !slices.Equal(rec.saves, want) {
		t.Fatalf("saves: %v", rec.saves)
	}

	files := e.unzip(t, alice, exp.ID)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "activity.json,api_tokens.json,export.json,notifications.json,profile.json,push.json" {
		t.Fatalf("files: %s", got)
	}
	var manifest struct {
		Subject struct{ Type, ID string }
		Files   []string
	}
	if err := json.Unmarshal(files["export.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Subject.Type != "user" || manifest.Subject.ID != "u1" ||
		strings.Join(manifest.Files, ",") != "profile.json,notifications.json,api_tokens.json,push.json,activity.json" {
		t.Fatalf("manifest: %s", files["export.json"])
	}
	for name, want := range map[string]string{
		"profile.json":       `"nickname": "Alice"`,
		"notifications.json": `"event": "welcome"`,
		"api_tokens.json":    `"ip": "203.0.113.7"`,
		"push.json":          `"platform": "ios"`,
		"activity.json":      `"event": "article.published"`,
	} {
		if !strings.Contains(string(files[name]), want) {
			t.Errorf("%s does not contain %s:\n%s", name, want, files[name])
		}
	}
	// 只有本人的数据；设备 token 不导出。
	for name, data := range files {
		if strings.Contains(string(data), "u2") || strings.Contains(string(data), "device-") {
			t.Errorf("%s leaks other data:\n%s", name, data)
		}
	}

	// 已经结束的导出不再处理。
	if err := e.personal.Process(ctx, exp.ID, rec.saved); err != nil || len(rec.saves) != len(want) {
		t.Fatalf("process again: %v, %v", err, rec.saves)
	}
}

func TestStartAndOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	exp := e.start(t, alice)
	if _, err := e.personal.Start(ctx, e.db, alice); !errors.Is(err, export.ErrInProgress) {
		t.Fatalf("start again: %v", err)
	}
	// 在另一个租户中也不能同时导出：导出的是本人在所有租户中的数据。
	if _, err := e.personal.Start(ctx, e.db, identity.Subject{ID: alice.ID, Type: alice.Type, Tenant: "globex"}); !errors.Is(err, export.ErrInProgress) {
		t.Fatalf("start in another tenant: %v", err)
	}
	e.start(t, bob)
	if _, err := e.personal.Start(ctx, e.db, identity.Subject{ID: "s1", Type: "staff"}); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("start for staff: %v", err)
	}

	if err := e.personal.Process(ctx, exp.ID, nil); err != nil {
		t.Fatal(err)
	}
	// 完成之后可以再次导出。
	e.start(t, alice)

	for name, sub := range map[string]identity.Subject{"bob": bob, "other tenant": {ID: alice.ID, Type: alice.Type, Tenant: "globex"}} {
		if _, err := e.personal.Get(ctx, sub, exp.ID); !errors.Is(err, operation.ErrNotFound) {
			t.Errorf("get as %s: %v", name, err)
		}
		if _, _, err := e.personal.Open(ctx, sub, exp.ID); !errors.Is(err, operation.ErrNotFound) {
			t.Errorf("open as %s: %v", name, err)
		}
	}

	// 过期之后不能再下载。
	if _, err := e.db.NewUpdate().Model((*export.Export)(nil)).Set("expires_at = ?", time.Now().Add(-time.Second)).
		Where("operation_id = ?", exp.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.personal.Open(ctx, alice, exp.ID); !errors.Is(err, export.ErrExpired) {
		t.Fatalf("open expired: %v", err)
	}
}

func TestProcessErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)

	// 业务错误使导出失败。
	e.locked.Store(alice.ID, true)
	exp := e.start(t, alice)
	if err := e.personal.Process(ctx, exp.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, alice, exp.ID); got.Status != operation.StatusFailed || got.Error != "test.locked" {
		t.Fatalf("failed: %+v", got)
	}
	if _, _, err := e.personal.Open(ctx, alice, exp.ID); !errors.Is(err, export.ErrNotReady) {
		t.Fatalf("open failed export: %v", err)
	}

	// 其他错误返回给队列，重试时从头开始。
	e.locked.Delete(alice.ID)
	e.down.Store(alice.ID, true)
	exp = e.start(t, alice)
	if err := e.personal.Process(ctx, exp.ID, nil); err == nil || !strings.Contains(err.Error(), "export profile for user:u1") {
		t.Fatalf("first run: %v", err)
	}
	if got := e.get(t, alice, exp.ID); got.Status != operation.StatusRunning {
		t.Fatalf("after first run: %+v", got)
	}
	e.down.Delete(alice.ID)
	if err := e.personal.Process(ctx, exp.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, alice, exp.ID); got.Status != operation.StatusCompleted || got.Processed != 6 {
		t.Fatalf("retried: %+v", got)
	}
}

// 各项读到的是同一时刻的数据：导出过程中写入的数据不出现在之后导出的项中。
func TestProcessReadsSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	writer := privacy.Source{Name: "writer", Export: func(ctx context.Context, db bun.IDB, ref identity.Ref) (any, error) {
		var n int
		if err := db.NewSelect().ColumnExpr("count(*)").TableExpr("notifications").Scan(ctx, &n); err != nil {
			return nil, err
		}
		// 另一个连接写入一条通知。
		_, err := notification.Send(ctx, e.db, notification.Notification{Event: "late"}, notification.Recipient{Tenant: "acme", Ref: ref})
		return map[string]int{"notifications": n}, err
	}}
	// writer 排在 notifications 之前。
	sources := []privacy.Source{writer, {Name: "notifications", Export: notification.Export}}
	e.personal = export.New(e.db, e.ops, e.files, privacy.New(e.db, privacy.Options{Type: "user", Sources: sources}))
	e.seed(t, alice)
	exp := e.start(t, alice)
	if err := e.personal.Process(ctx, exp.ID, nil); err != nil {
		t.Fatal(err)
	}
	files := e.unzip(t, alice, exp.ID)
	if !strings.Contains(string(files["notifications.json"]), "welcome") || strings.Contains(string(files["notifications.json"]), "late") {
		t.Fatalf("notifications.json: %s", files["notifications.json"])
	}
}

func TestNewPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	export.New(nil, nil, nil, nil)
}
