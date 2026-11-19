package privacy_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/oauthserver"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/privacy"
	"github.com/go-tenon/tenon/push"
)

// 框架各包的 Erase、Export 都可以直接登记到 Source。
var _ = []privacy.Source{
	{Name: "login", Erase: (*login.Realm)(nil).Forget, Export: (*login.Realm)(nil).Export},
	{Name: "api_tokens", Erase: apitoken.Erase, Export: apitoken.Export},
	{Name: "roles", Erase: (*authz.Store)(nil).Erase, Export: (*authz.Store)(nil).Export},
	{Name: "push", Erase: push.Erase, Export: push.Export},
	{Name: "notifications", Erase: notification.Erase, Export: notification.Export},
	{Name: "activity", Export: activity.Export},
	{Name: "flags", Erase: feature.Erase},
	{Name: "operations", Erase: operation.Erase},
	{Name: "oauth_codes", Erase: oauthserver.Erase},
}

var errSoleOwner = errorx.Define(errorx.Conflict, "test.sole_owner")

func user(id string) identity.Ref { return identity.Ref{Type: "user", ID: id} }

// appMigrations 是应用自己的表。
var appMigrations = fstest.MapFS{
	"2026_01_01_000001_users.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE users (id text PRIMARY KEY);\n-- +goose Down\nDROP TABLE users;\n")},
}

// migrations 合并注销、通知和 token 的迁移，再加上应用自己的 users 表。
func migrations() []fs.FS {
	return []fs.FS{appMigrations, privacy.Migrations(), notification.Migrations(), apitoken.Migrations()}
}

// app 是测试中的应用：users 表、通知和 token，以及控制各项行为的开关。
type app struct {
	db     *bun.DB
	tokens *apitoken.Store
	owner  sync.Map   // owner 中的用户是团队唯一的所有者，不能注销。
	fail   sync.Map   // fail 中的用户删除时失败。
	mu     sync.Mutex // 保护 erased
	erased []string   // users 这一项的每次调用
}

func newApp(t *testing.T) *app {
	t.Helper()
	db := dbtest.Open(t, migrations()...)
	return &app{db: db, tokens: apitoken.NewStore(db, apitoken.Config{Lifetime: time.Hour})}
}

func (a *app) sources() []privacy.Source {
	return []privacy.Source{
		{
			Name: "users",
			Check: func(_ context.Context, ref identity.Ref) error {
				if _, ok := a.owner.Load(ref.ID); ok {
					return errSoleOwner.New()
				}
				return nil
			},
			Erase: func(ctx context.Context, db bun.IDB, ref identity.Ref) error {
				a.mu.Lock()
				a.erased = append(a.erased, ref.ID)
				a.mu.Unlock()
				_, err := db.NewDelete().Table("users").Where("id = ?", ref.ID).Exec(ctx)
				return err
			},
		},
		{Name: "notifications", Erase: notification.Erase},
		{Name: "api_tokens", Erase: apitoken.Erase},
		{
			Name: "search",
			Erase: func(_ context.Context, db bun.IDB, ref identity.Ref) error {
				if _, ok := a.fail.Load(ref.ID); ok {
					return errors.New("search index unavailable")
				}
				return nil
			},
		},
	}
}

func (a *app) service(grace time.Duration) *privacy.Service {
	return privacy.New(a.db, privacy.Options{Type: "user", Grace: grace, Sources: a.sources()})
}

// signUp 建一个用户，给他一条通知和一个 token，返回 token。
func (a *app) signUp(t *testing.T, id string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := a.db.NewRaw("INSERT INTO users (id) VALUES (?)", id).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	r := notification.Recipient{Tenant: "acme", Ref: user(id)}
	if _, err := notification.Send(ctx, a.db, notification.Notification{Event: "welcome"}, r); err != nil {
		t.Fatal(err)
	}
	token, err := a.tokens.Login(ctx, identity.Subject{Type: "user", ID: id}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// exists 报告用户的数据还在不在：users 行、通知和 token 要么都在，要么都不在。
func (a *app) exists(t *testing.T, id, token string) bool {
	t.Helper()
	ctx := context.Background()
	users, err := a.db.NewSelect().Table("users").Where("id = ?", id).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := notification.UnreadCount(ctx, a.db, notification.Recipient{Tenant: "acme", Ref: user(id)})
	if err != nil {
		t.Fatal(err)
	}
	_, tokenErr := a.tokens.Validate(ctx, token)
	if int(users) != notes || (users == 1) != (tokenErr == nil) {
		t.Fatalf("%s: partially erased: users=%d notifications=%d token error=%v", id, users, notes, tokenErr)
	}
	return users == 1
}

func (a *app) erasures(t *testing.T, id string) []privacy.Erasure {
	t.Helper()
	var list []privacy.Erasure
	err := a.db.NewSelect().Model(&list).Where("subject_type = 'user' AND subject_id = ?", id).OrderExpr("id").Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func request(t *testing.T, s *privacy.Service, db bun.IDB, id string) *privacy.Erasure {
	t.Helper()
	e, err := s.Request(context.Background(), db, user(id))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestNewPanics(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, privacy.Migrations())
	erase := func(context.Context, bun.IDB, identity.Ref) error { return nil }
	for name, tc := range map[string]struct {
		db   *bun.DB
		opts privacy.Options
	}{
		"nil db":      {nil, privacy.Options{Type: "user", Sources: []privacy.Source{{Name: "a", Erase: erase}}}},
		"no type":     {db, privacy.Options{Sources: []privacy.Source{{Name: "a", Erase: erase}}}},
		"bad type":    {db, privacy.Options{Type: "User", Sources: []privacy.Source{{Name: "a", Erase: erase}}}},
		"no sources":  {db, privacy.Options{Type: "user"}},
		"empty name":  {db, privacy.Options{Type: "user", Sources: []privacy.Source{{Erase: erase}}}},
		"duplicate":   {db, privacy.Options{Type: "user", Sources: []privacy.Source{{Name: "a", Erase: erase}, {Name: "a", Erase: erase}}}},
		"no function": {db, privacy.Options{Type: "user", Sources: []privacy.Source{{Name: "a"}}}},
		"negative":    {db, privacy.Options{Type: "user", Grace: -time.Second, Sources: []privacy.Source{{Name: "a", Erase: erase}}}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected panic", name)
				}
			}()
			privacy.New(tc.db, tc.opts)
		}()
	}
}

// 冷静期内可以查询和撤销，不会被删除。
func TestRequestAndCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newApp(t)
	s := a.service(24 * time.Hour)
	token := a.signUp(t, "alice")

	before := time.Now()
	e := request(t, s, a.db, "alice")
	if e.Subject() != user("alice") || e.Erased() || e.DueAt.Sub(e.CreatedAt) != 24*time.Hour || e.CreatedAt.Before(before.Add(-time.Second)) {
		t.Fatalf("request: %+v", e)
	}
	if _, err := s.Request(ctx, a.db, user("alice")); !errors.Is(err, privacy.ErrPending) {
		t.Fatalf("request again: %v", err)
	}
	got, err := s.Pending(ctx, user("alice"))
	if err != nil || got.ID != e.ID || !got.DueAt.Equal(e.DueAt) {
		t.Fatalf("pending: %+v, %v", got, err)
	}
	if n, err := s.EraseDue(ctx, 10); n != 0 || err != nil {
		t.Fatalf("erase due during grace period: %d, %v", n, err)
	}
	if !a.exists(t, "alice", token) {
		t.Fatal("erased during grace period")
	}

	if err := s.Cancel(ctx, a.db, user("alice")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pending(ctx, user("alice")); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("pending after cancel: %v", err)
	}
	if err := s.Cancel(ctx, a.db, user("alice")); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("cancel again: %v", err)
	}
	// 撤销之后可以再次申请。
	request(t, s, a.db, "alice")
}

// 申请与业务修改在同一个事务中，一起回滚。
func TestRequestInTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newApp(t)
	s := a.service(time.Hour)
	rollback := errors.New("rollback")
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		request(t, s, tx, "alice")
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := s.Pending(ctx, user("alice")); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("pending after rollback: %v", err)
	}
}

// Check 返回的业务错误原样传给调用方，什么都不登记。
func TestCheckBlocksRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newApp(t)
	s := a.service(0)
	token := a.signUp(t, "alice")
	a.owner.Store("alice", true)
	for name, err := range map[string]error{
		"check":   s.Check(ctx, user("alice")),
		"request": func() error { _, err := s.Request(ctx, a.db, user("alice")); return err }(),
		"erase":   s.Erase(ctx, user("alice")),
	} {
		if !errors.Is(err, errSoleOwner) || errorx.CodeOf(err) != "test.sole_owner" {
			t.Errorf("%s: %v (code %q)", name, err, errorx.CodeOf(err))
		}
	}
	if len(a.erasures(t, "alice")) != 0 || !a.exists(t, "alice", token) {
		t.Fatal("blocked erasure changed data")
	}
	if _, err := s.Request(ctx, a.db, identity.Ref{}); err == nil {
		t.Fatal("expected error for empty subject")
	}
}

// 冷静期过后，EraseDue 删除各项的数据，保留一行已删除的记录。
func TestEraseDue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newApp(t)
	s := a.service(0)
	tokens := map[string]string{}
	for _, id := range []string{"alice", "bob", "carol"} {
		tokens[id] = a.signUp(t, id)
	}
	request(t, s, a.db, "alice")
	request(t, s, a.db, "bob")

	if _, err := s.EraseDue(ctx, 0); err == nil {
		t.Fatal("expected error for non-positive limit")
	}
	if n, err := s.EraseDue(ctx, 1); n != 1 || err != nil {
		t.Fatalf("first batch: %d, %v", n, err)
	}
	if n, err := s.EraseDue(ctx, 10); n != 1 || err != nil {
		t.Fatalf("second batch: %d, %v", n, err)
	}
	if n, err := s.EraseDue(ctx, 10); n != 0 || err != nil {
		t.Fatalf("nothing left: %d, %v", n, err)
	}
	for id, want := range map[string]bool{"alice": false, "bob": false, "carol": true} {
		if got := a.exists(t, id, tokens[id]); got != want {
			t.Errorf("%s exists=%v, want %v", id, got, want)
		}
	}
	if got := strings.Join(a.erased, ","); got != "alice,bob" {
		t.Fatalf("erased in order %s", got)
	}
	list := a.erasures(t, "alice")
	if len(list) != 1 || !list[0].Erased() || list[0].Attempts != 1 || list[0].LastError != "" {
		t.Fatalf("erasure record: %+v", list)
	}
	if _, err := s.Pending(ctx, user("alice")); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("pending after erase: %v", err)
	}
	if err := s.Cancel(ctx, a.db, user("alice")); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("cancel after erase: %v", err)
	}
	// 已删除的记录不妨碍同一个 ID 再次申请，例如 ID 由外部系统分配。
	request(t, s, a.db, "alice")
	if got := a.erasures(t, "alice"); len(got) != 2 {
		t.Fatalf("records: %+v", got)
	}
}

// 一项失败时，这个主体的修改全部回滚，记下原因，推迟重试；其他主体照常删除。
func TestEraseDueRetriesFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newApp(t)
	s := a.service(0)
	alice, bob := a.signUp(t, "alice"), a.signUp(t, "bob")
	request(t, s, a.db, "alice")
	request(t, s, a.db, "bob")
	a.fail.Store("alice", true)

	n, err := s.EraseDue(ctx, 10)
	if n != 1 || err == nil || !strings.Contains(err.Error(), "search index unavailable") || !strings.Contains(err.Error(), "erase search for user:alice") {
		t.Fatalf("erase due: %d, %v", n, err)
	}
	if !a.exists(t, "alice", alice) || a.exists(t, "bob", bob) {
		t.Fatal("failure was not rolled back, or bob was not erased")
	}
	e, err := s.Pending(ctx, user("alice"))
	if err != nil || e.Attempts != 1 || !strings.Contains(e.LastError, "search index unavailable") || !e.DueAt.After(time.Now().Add(5*time.Minute)) {
		t.Fatalf("pending after failure: %+v, %v", e, err)
	}
	// 推迟期间不再执行。
	if n, err := s.EraseDue(ctx, 10); n != 0 || err != nil {
		t.Fatalf("erase due while delayed: %d, %v", n, err)
	}

	a.fail.Delete("alice")
	if _, err := a.db.NewUpdate().Model((*privacy.Erasure)(nil)).Set("due_at = ?", time.Now().UTC()).Where("id = ?", e.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := s.EraseDue(ctx, 10); n != 1 || err != nil {
		t.Fatalf("retry: %d, %v", n, err)
	}
	if a.exists(t, "alice", alice) {
		t.Fatal("alice not erased on retry")
	}
	if list := a.erasures(t, "alice"); len(list) != 1 || list[0].Attempts != 2 || list[0].LastError != "" || !list[0].Erased() {
		t.Fatalf("record after retry: %+v", list)
	}
}

// 失败的一次中登记的提交后回调被丢弃，成功的一次中登记的在提交之后执行。
func TestEraseDueAfterCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newApp(t)
	var mu sync.Mutex
	var notified []string
	notify := privacy.Source{Name: "realtime", Erase: func(_ context.Context, db bun.IDB, ref identity.Ref) error {
		db.(database.Tx).AfterCommit(func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			notified = append(notified, ref.ID)
			return nil
		})
		return nil
	}}
	// notify 在 search 之前：search 失败时，notify 已经登记了回调。
	sources := a.sources()
	sources = append(sources[:3:3], notify, sources[3])
	s := privacy.New(a.db, privacy.Options{Type: "user", Sources: sources})
	request(t, s, a.db, "alice")
	request(t, s, a.db, "bob")
	a.fail.Store("alice", true)
	if n, _ := s.EraseDue(ctx, 10); n != 1 {
		t.Fatalf("erased %d", n)
	}
	if got := strings.Join(notified, ","); got != "bob" {
		t.Fatalf("notified %q", got)
	}
}

// Erase 跳过冷静期：有待执行的注销时一并完成，失败时什么都不改变。
func TestEraseNow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newApp(t)
	s := a.service(24 * time.Hour)
	alice, bob, carol := a.signUp(t, "alice"), a.signUp(t, "bob"), a.signUp(t, "carol")
	pending := request(t, s, a.db, "alice")

	if err := s.Erase(ctx, user("alice")); err != nil {
		t.Fatal(err)
	}
	if err := s.Erase(ctx, user("bob")); err != nil {
		t.Fatal(err)
	}
	if a.exists(t, "alice", alice) || a.exists(t, "bob", bob) {
		t.Fatal("not erased")
	}
	if list := a.erasures(t, "alice"); len(list) != 1 || list[0].ID != pending.ID || !list[0].Erased() {
		t.Fatalf("alice records: %+v", list)
	}
	if list := a.erasures(t, "bob"); len(list) != 1 || !list[0].Erased() || !list[0].DueAt.Equal(list[0].CreatedAt) {
		t.Fatalf("bob records: %+v", list)
	}

	carolPending := request(t, s, a.db, "carol")
	a.fail.Store("carol", true)
	if err := s.Erase(ctx, user("carol")); err == nil || !strings.Contains(err.Error(), "search index unavailable") {
		t.Fatalf("erase carol: %v", err)
	}
	if !a.exists(t, "carol", carol) {
		t.Fatal("failed erase changed data")
	}
	if e, err := s.Pending(ctx, user("carol")); err != nil || e.ID != carolPending.ID || e.Attempts != 0 {
		t.Fatalf("carol pending: %+v, %v", e, err)
	}
}

// 多个实例同时执行 EraseDue，每个主体只删除一次。
func TestEraseDueConcurrent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newApp(t)
	s := a.service(0)
	const users = 20
	for i := range users {
		id := fmt.Sprintf("u%02d", i)
		a.signUp(t, id)
		request(t, s, a.db, id)
	}
	var total atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			n, err := s.EraseDue(ctx, users)
			if err != nil {
				t.Error(err)
			}
			total.Add(int64(n))
		})
	}
	wg.Wait()
	if total.Load() != users || len(a.erased) != users {
		t.Fatalf("erased %d subjects in %d calls", total.Load(), len(a.erased))
	}
}

// 每套账号体系一个服务：两个服务共用一张表，各自只处理自己的主体，不接受别的账号体系。
func TestAccountSystems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a := newApp(t)
	users := a.service(0)
	var staffErased []string
	staff := privacy.New(a.db, privacy.Options{Type: "staff", Sources: []privacy.Source{{
		Name: "staff",
		Erase: func(_ context.Context, _ bun.IDB, ref identity.Ref) error {
			if ref.Type != "staff" {
				return errors.New("staff source got " + ref.String())
			}
			staffErased = append(staffErased, ref.ID)
			return nil
		},
	}}})
	token := a.signUp(t, "alice")
	request(t, users, a.db, "alice")
	if _, err := staff.Request(ctx, a.db, identity.Ref{Type: "staff", ID: "alice"}); err != nil {
		t.Fatal(err)
	}

	// staff 的服务只删除 staff 的注销。
	if n, err := staff.EraseDue(ctx, 10); n != 1 || err != nil {
		t.Fatalf("staff erase due: %d, %v", n, err)
	}
	if !a.exists(t, "alice", token) || strings.Join(staffErased, ",") != "alice" {
		t.Fatalf("staff erased user data, or staff not erased: %v", staffErased)
	}
	if _, err := users.Pending(ctx, user("alice")); err != nil {
		t.Fatalf("user erasure no longer pending: %v", err)
	}
	if n, err := users.EraseDue(ctx, 10); n != 1 || err != nil {
		t.Fatalf("user erase due: %d, %v", n, err)
	}
	if a.exists(t, "alice", token) {
		t.Fatal("user not erased")
	}

	other := identity.Ref{Type: "staff", ID: "bob"}
	for name, err := range map[string]error{
		"check":   users.Check(ctx, other),
		"request": func() error { _, err := users.Request(ctx, a.db, other); return err }(),
		"pending": func() error { _, err := users.Pending(ctx, other); return err }(),
		"cancel":  users.Cancel(ctx, a.db, other),
		"erase":   users.Erase(ctx, other),
	} {
		if err == nil || !strings.Contains(err.Error(), "does not belong") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
