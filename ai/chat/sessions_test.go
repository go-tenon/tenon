package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	tpg "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/go-tenon/tenon/ai/chat"
	"github.com/go-tenon/tenon/ai/chat/chattest"
	"github.com/go-tenon/tenon/database/dbtest"
)

// newSessions 创建会话存储；initSchema 为 true 时让 trpc-agent-go 自己建表。
func newSessions(t *testing.T, db *bun.DB, initSchema bool) session.Service {
	t.Helper()
	s, err := chat.NewSessions(db, tpg.WithSkipDBInit(!initSchema))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSessionMigrationsMatchTrpc 确保迁移建出的表与 trpc-agent-go 自己建的完全一致，升级 trpc-agent-go 时发现表结构的变化。
func TestSessionMigrationsMatchTrpc(t *testing.T) {
	t.Parallel()
	auto := dbtest.Open(t, nil)
	s := newSessions(t, auto, true)
	_ = s.Close()
	migrated := dbtest.Open(t, chat.Migrations())
	want, got := schema(t, auto), schema(t, migrated)
	if len(want) == 0 {
		t.Fatal("trpc-agent-go created no tables")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("migrations differ from trpc-agent-go schema\nmigrations:\n%s\n\ntrpc-agent-go:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func schema(t *testing.T, db *bun.DB) []string {
	t.Helper()
	ctx := context.Background()
	var out []string
	query := func(q string, args ...any) {
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]string, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			out = append(out, strings.Join(vals, " | "))
		}
	}
	query(`SELECT table_name, column_name, data_type, is_nullable, coalesce(column_default, ''), coalesce(character_maximum_length::text, '')
		FROM information_schema.columns WHERE table_schema = 'public' AND table_name LIKE 'agent\_%' ORDER BY 1, 2`)
	query(`SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename LIKE 'agent\_%' ORDER BY 1`)
	return out
}

// TestSessionsAcrossRestart 在迁移建出的表上运行到等待审批，再用新的 Runs（相当于新进程）批准并继续。
func TestSessionsAcrossRestart(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, chat.Migrations())
	ctx := context.Background()
	var refunded refunds
	refund := refunded.tool()
	m := chattest.NewModel(
		chattest.Calls(chattest.Call{ID: "r1", Name: "refund_order", Args: orderArgs{"A1"}}),
		chattest.Text("已退款"),
	)
	start := func() (*chat.Runs, func()) {
		sessions := newSessions(t, db, false)
		ag := llmagent.New("support", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{refund}),
			llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}))
		rs := chat.NewRuns("test", ag, sessions, chat.RequireApproval(refund))
		return rs, func() { _ = rs.Close(); _ = sessions.Close() }
	}

	first, stop := start()
	r, err := first.Start(ctx, "u1", "c1", model.NewUserMessage("退款 A1"))
	if err != nil {
		t.Fatal(err)
	}
	wait(t, r)
	stop()
	// 会话存储关闭后，应用的连接池仍然可用。
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("app database closed with sessions: %v", err)
	}

	second, stop := start()
	defer stop()
	pending, err := second.Pending(ctx, "u1", "c1")
	if err != nil || len(pending) != 1 || pending[0].ID != "r1" {
		t.Fatalf("pending %+v, %v", pending, err)
	}
	r, err = second.Approve(ctx, "u1", "c1", "r1")
	if err != nil || r == nil {
		t.Fatalf("approve: %v %v", r, err)
	}
	wait(t, r)
	if len(refunded.done) != 1 {
		t.Fatalf("refunded %v", refunded.done)
	}
	if got := messagesOf(m.LastRequest()); !strings.HasSuffix(got, "user:退款 A1 | assistant:call refund_order | tool:refunded A1") {
		t.Fatalf("history sent to model: %s", got)
	}
	history, err := second.UIMessages(ctx, "u1", "c1")
	if err != nil || len(history) != 2 || history[1].Parts[len(history[1].Parts)-1].Text != "已退款" {
		t.Fatalf("history %+v, %v", history, err)
	}
}

func wait(t *testing.T, r *chat.Run) {
	t.Helper()
	select {
	case <-r.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("run did not finish")
	}
}

// TestEraseUser 删除一个用户在全部应用中的会话和用户状态，不影响其他用户。
func TestEraseUser(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, chat.Migrations())
	ctx := context.Background()
	s := newSessions(t, db, false)
	t.Cleanup(func() { _ = s.Close() })
	for _, key := range []session.Key{
		{AppName: "blog", UserID: "u1", SessionID: "s1"},
		{AppName: "shop", UserID: "u1", SessionID: "s2"},
		{AppName: "blog", UserID: "u2", SessionID: "s3"},
	} {
		if _, err := s.CreateSession(ctx, key, session.StateMap{"k": []byte("v")}); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateUserState(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID}, session.StateMap{"name": []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	rows := func(userID string) int {
		total := 0
		for _, table := range []string{"agent_session_states", "agent_user_states"} {
			n, err := db.NewSelect().TableExpr(table).Where("user_id = ?", userID).Count(ctx)
			if err != nil {
				t.Fatal(err)
			}
			total += int(n)
		}
		return total
	}
	if rows("u1") != 4 || rows("u2") != 2 {
		t.Fatalf("before: u1=%d u2=%d", rows("u1"), rows("u2"))
	}
	if err := chat.EraseUser(ctx, db, "u1"); err != nil {
		t.Fatal(err)
	}
	if rows("u1") != 0 || rows("u2") != 2 {
		t.Fatalf("after: u1=%d u2=%d", rows("u1"), rows("u2"))
	}
	if err := chat.EraseUser(ctx, db, ""); err == nil {
		t.Fatal("expected error for empty user ID")
	}
}

// TestExportUser 导出一个用户在全部应用中的会话、事件和用户状态，不包括其他用户的。
func TestExportUser(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, chat.Migrations())
	ctx := context.Background()
	s := newSessions(t, db, false)
	t.Cleanup(func() { _ = s.Close() })
	for _, key := range []session.Key{
		{AppName: "blog", UserID: "u1", SessionID: "s1"},
		{AppName: "blog", UserID: "u2", SessionID: "s2"},
	} {
		sess, err := s.CreateSession(ctx, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		ev := event.NewResponseEvent("inv", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("hello from " + key.UserID)}}})
		if err := s.AppendEvent(ctx, sess, ev); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateUserState(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID}, session.StateMap{"nickname": []byte("nick-" + key.UserID)}); err != nil {
			t.Fatal(err)
		}
	}
	v, err := chat.ExportUser(ctx, db, "u1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{`"sessions":[{"app":"blog","session_id":"s1","created_at":`, "hello from u1", `"key":"nickname","value":"nick-u1"`, `"summaries":[]`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s does not contain %s", got, want)
		}
	}
	if strings.Contains(got, "u2") || strings.Contains(got, "deleted_at") {
		t.Errorf("export contains other data: %s", got)
	}
	if _, err := chat.ExportUser(ctx, db, ""); err == nil {
		t.Fatal("expected error for empty user ID")
	}
}
