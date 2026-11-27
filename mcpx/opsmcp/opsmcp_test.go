package opsmcp_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/mcpx"
	"github.com/go-tenon/tenon/mcpx/mcpxtest"
	"github.com/go-tenon/tenon/mcpx/opsmcp"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/schedule"
)

// fakeScheduleStore 是用于测试 ops_schedule_status 的内存调度状态存储。
type fakeScheduleStore struct {
	states map[string]schedule.TaskState
}

func (f fakeScheduleStore) Claim(context.Context, string, time.Time, string, time.Duration, bool) (schedule.ClaimResult, error) {
	return schedule.Claimed, nil
}
func (f fakeScheduleStore) AcquireRunning(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (f fakeScheduleStore) Refresh(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (f fakeScheduleStore) Release(context.Context, string, string) error { return nil }
func (f fakeScheduleStore) States(context.Context) (map[string]schedule.TaskState, error) {
	return f.states, nil
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}

func TestPermissionsAndOptionalRegistration(t *testing.T) {
	t.Parallel()
	srv := mcpx.New(mcpx.Options{Name: "ops-test"})
	opsmcp.Register(srv, opsmcp.Options{
		Permissions: []string{"ops:read"},
		Doctor: func() []doctor.Group {
			return []doctor.Group{{Name: "app", Checks: []doctor.Check{{Name: "ok", Run: func(context.Context) error { return nil }}}}}
		},
		Schedule: fakeScheduleStore{states: map[string]schedule.TaskState{
			"billing:daily": {LastTick: time.Date(2026, 11, 27, 1, 0, 0, 0, time.UTC), Running: false},
			"cleanup:old":   {LastTick: time.Date(2026, 11, 27, 2, 0, 0, 0, time.UTC), Running: true},
		}},
	})

	// 无权限主体看不到任何运维工具。
	noPerm := mcpxtest.Connect(t, srv, identity.Subject{Type: "user", ID: "u1"})
	if len(noPerm.Tools()) != 0 {
		t.Fatalf("expected 0 tools without ops:read, got %v", noPerm.Tools())
	}

	// 有 ops:read 权限的主体只看到已传入依赖对应的工具。
	ops := mcpxtest.Connect(t, srv, identity.Subject{Type: "user", ID: "admin1", Permissions: []string{"ops:read"}})
	gotTools := ops.Tools()
	if !slices.Equal(gotTools, []string{"ops_doctor", "ops_schedule_status"}) {
		t.Fatalf("unexpected tools: %v", gotTools)
	}

	res, err := ops.Call("ops_doctor", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("ops_doctor failed: err=%v text=%s", err, res.Text)
	}
	var report doctor.DoctorReport
	if err := res.Decode(&report); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Status != doctor.Passed {
		t.Fatalf("unexpected doctor report: %+v", report)
	}

	res, err = ops.Call("ops_schedule_status", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("ops_schedule_status failed: err=%v text=%s", err, res.Text)
	}
	var sched struct {
		Tasks []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
		} `json:"tasks"`
	}
	if err := res.Decode(&sched); err != nil {
		t.Fatal(err)
	}
	if len(sched.Tasks) != 2 || sched.Tasks[0].Name != "billing:daily" || !sched.Tasks[1].Running {
		t.Fatalf("unexpected schedule tasks: %+v", sched.Tasks)
	}
}

func TestDBToolsAndGuardrails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.Open(t)

	_, err := db.ExecContext(ctx, `
CREATE TABLE demo_users (
	id int PRIMARY KEY,
	email text NOT NULL,
	password_hash text NOT NULL,
	api_key text NOT NULL,
	key text NOT NULL,
	note text NOT NULL
);
COMMENT ON TABLE demo_users IS '演示用户表';
COMMENT ON COLUMN demo_users.email IS '邮箱地址';
INSERT INTO demo_users VALUES
	(1, 'a@example.com', 'hash-secret-1', 'sk-live-1', 'normal-key-1', repeat('x', 80)),
	(2, 'b@example.com', 'hash-secret-2', 'sk-live-2', 'normal-key-2', 'short'),
	(3, 'c@example.com', 'hash-secret-3', 'sk-live-3', 'normal-key-3', 'short');
`)
	if err != nil {
		t.Fatal(err)
	}

	srv := mcpx.New(mcpx.Options{Name: "ops-db-test"})
	opsmcp.Register(srv, opsmcp.Options{
		Permissions:   []string{"ops:read"},
		DB:            db,
		QueryTimeout:  300 * time.Millisecond,
		MaxRows:       2,
		MaxCellLen:    20,
		RedactColumns: []string{"email"},
	})
	c := mcpxtest.Connect(t, srv, identity.Subject{Type: "user", ID: "ops", Permissions: []string{"ops:read"}})

	// 1. ops_db_tables 查看表与列详情。
	res, err := c.Call("ops_db_tables", map[string]any{"table": "demo_users"})
	if err != nil || res.IsError {
		t.Fatalf("ops_db_tables failed: err=%v text=%s", err, res.Text)
	}
	var tables struct {
		Tables  []struct{ Name, Comment string } `json:"tables"`
		Columns []struct{ Name, Comment string } `json:"columns"`
		Indexes []struct{ Name string }          `json:"indexes"`
	}
	if err := res.Decode(&tables); err != nil {
		t.Fatal(err)
	}
	if len(tables.Tables) != 1 || tables.Tables[0].Name != "demo_users" || tables.Tables[0].Comment != "演示用户表" {
		t.Fatalf("unexpected tables: %+v", tables.Tables)
	}
	if len(tables.Columns) != 6 || len(tables.Indexes) == 0 {
		t.Fatalf("unexpected columns/indexes: %+v", tables)
	}

	// 2. ops_db_query 行数截断、文本截断、敏感列脱敏且普通 key 列不误伤。
	res, err = c.Call("ops_db_query", map[string]any{
		"sql": "/* header */ SELECT id, email, password_hash, api_key, key, note FROM demo_users ORDER BY id;",
	})
	if err != nil || res.IsError {
		t.Fatalf("ops_db_query failed: err=%v text=%s", err, res.Text)
	}
	var qr struct {
		Columns         []string `json:"columns"`
		Rows            [][]any  `json:"rows"`
		RowCount        int      `json:"row_count"`
		Truncated       bool     `json:"truncated"`
		RedactedColumns []string `json:"redacted_columns"`
	}
	if err := res.Decode(&qr); err != nil {
		t.Fatal(err)
	}
	if qr.RowCount != 2 || !qr.Truncated {
		t.Fatalf("expected 2 rows truncated, got %+v", qr)
	}
	if !slices.Equal(qr.RedactedColumns, []string{"email", "password_hash", "api_key"}) {
		t.Fatalf("unexpected redacted columns: %v", qr.RedactedColumns)
	}
	if qr.Rows[0][1] != "[REDACTED]" || qr.Rows[0][2] != "[REDACTED]" || qr.Rows[0][3] != "[REDACTED]" {
		t.Fatalf("sensitive columns not redacted: %v", qr.Rows[0])
	}
	if qr.Rows[0][4] != "normal-key-1" {
		t.Fatalf("benign column key should not be redacted, got %v", qr.Rows[0][4])
	}
	if note, _ := qr.Rows[0][5].(string); !strings.HasSuffix(note, "…(truncated)") {
		t.Fatalf("long cell should be truncated, got %q", note)
	}

	// 3. 字符串中的分号允许执行。
	res, err = c.Call("ops_db_query", map[string]any{"sql": "SELECT 'a;b' AS semi, $$c;d$$ AS dollar"})
	if err != nil || res.IsError {
		t.Fatalf("semicolon inside string literal should pass: err=%v text=%s", err, res.Text)
	}

	// 4. 写操作、多语句与 CTE 写绕过全部被拦截。
	rejected := []string{
		"DELETE FROM demo_users",
		"DROP TABLE demo_users",
		"SELECT 1; DROP TABLE demo_users",
		"WITH d AS (DELETE FROM demo_users RETURNING *) SELECT * FROM d",
		"EXPLAIN ANALYZE DELETE FROM demo_users",
		"   ",
	}
	for _, stmt := range rejected {
		res, err = c.Call("ops_db_query", map[string]any{"sql": stmt})
		if err != nil {
			t.Fatalf("unexpected transport error for %q: %v", stmt, err)
		}
		if !res.IsError {
			t.Fatalf("expected error for unsafe SQL %q, got success: %s", stmt, res.Text)
		}
	}

	// 5. 语句超时熔断生效。
	res, err = c.Call("ops_db_query", map[string]any{"sql": "SELECT pg_sleep(2)"})
	if err != nil || !res.IsError {
		t.Fatalf("expected timeout error for pg_sleep, err=%v text=%s", err, res.Text)
	}

	// 6. 确认表数据完好无损。
	var count int
	if err := db.NewRaw("SELECT count(*) FROM demo_users").Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("demo_users mutated, count=%d", count)
	}
}

func TestDBDoctorAndStrictReadOnlyRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.Open(t)

	_, err := db.ExecContext(ctx, "CREATE TABLE items (id int PRIMARY KEY, name text)")
	if err != nil {
		t.Fatal(err)
	}

	// 主库连接通常是 superuser/读写账号：Doctor 返回 Warning 并附带 pg_read_all_data 修复提示。
	rep := doctor.Run(ctx, opsmcp.Doctor(db))
	if rep.Count(doctor.Warning) != 1 {
		t.Fatalf("expected 1 warning on writable role, got %+v", rep.Results)
	}
	if !strings.Contains(rep.Results[1].Fix, "pg_read_all_data") {
		t.Fatalf("expected pg_read_all_data hint in fix, got %q", rep.Results[1].Fix)
	}

	// RequireReadOnlyRole 为 true 时，读写账号直接被 ops_db_query 拒绝。
	strictSrv := mcpx.New(mcpx.Options{Name: "strict-ops"})
	opsmcp.Register(strictSrv, opsmcp.Options{DB: db, RequireReadOnlyRole: true})
	sc := mcpxtest.Connect(t, strictSrv, identity.Subject{Type: "user", ID: "ops"})
	res, err := sc.Call("ops_db_query", map[string]any{"sql": "SELECT 1"})
	if err != nil || !res.IsError || !strings.Contains(res.Text, "ops.query_rejected") {
		t.Fatalf("expected ops.query_rejected on writable role, err=%v text=%s", err, res.Text)
	}

	// 创建真实的 pg_read_all_data 只读角色，校验 Doctor 通过且严格模式允许查询。
	roleName := "tenon_ro_" + randomSuffix(t)
	rolePass := "ro_" + randomSuffix(t)
	if _, err := db.ExecContext(ctx, "CREATE ROLE "+roleName+" LOGIN PASSWORD '"+rolePass+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+roleName)
	})
	if _, err := db.ExecContext(ctx, "GRANT pg_read_all_data TO "+roleName); err != nil {
		t.Fatal(err)
	}

	var dbName string
	if err := db.NewRaw("SELECT current_database()").Scan(ctx, &dbName); err != nil {
		t.Fatal(err)
	}
	baseDSN := os.Getenv("POSTGRES_TEST_URL")
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(roleName, rolePass)
	u.Path = "/" + dbName

	roDB, err := database.Open(ctx, database.Config{DSN: u.String(), MaxConns: 2}, database.ReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = roDB.Close() })

	roRep := doctor.Run(ctx, opsmcp.Doctor(roDB))
	if roRep.Count(doctor.Warning) != 0 || roRep.Count(doctor.Failed) != 0 {
		t.Fatalf("expected all passed on pg_read_all_data role, got %+v", roRep.Results)
	}

	roSrv := mcpx.New(mcpx.Options{Name: "strict-ro-ops"})
	opsmcp.Register(roSrv, opsmcp.Options{DB: roDB, RequireReadOnlyRole: true})
	rc := mcpxtest.Connect(t, roSrv, identity.Subject{Type: "user", ID: "ops"})
	res, err = rc.Call("ops_db_query", map[string]any{"sql": "SELECT count(*) FROM items"})
	if err != nil || res.IsError {
		t.Fatalf("expected query to succeed on read-only role, err=%v text=%s", err, res.Text)
	}
}

func TestRedisNATSandQueueTools(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Redis
	redisURL := os.Getenv("REDIS_TEST_URL")
	if redisURL == "" {
		t.Fatal("REDIS_TEST_URL is required")
	}
	ropts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(ropts)
	t.Cleanup(func() { _ = rdb.Close() })

	prefix := "opsmcp:test:" + randomSuffix(t) + ":"
	pubKey := prefix + "profile"
	secKey := prefix + "user_session_token"
	hashKey := prefix + "meta"
	if err := rdb.Set(ctx, pubKey, "hello-world", 5*time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.Set(ctx, secKey, "super-secret-value", 5*time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.HSet(ctx, hashKey, "name", "alice", "api_secret", "top-secret").Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Del(context.Background(), pubKey, secKey, hashKey).Err() })

	// NATS + JetStream
	natsURL := os.Getenv("NATS_TEST_URL")
	if natsURL == "" {
		t.Fatal("NATS_TEST_URL is required")
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	streamName := "OPSMCP_" + strings.ToUpper(randomSuffix(t))
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	st, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     streamName,
		Subjects: []string{streamName + ".job.>"},
		Storage:  jetstream.MemoryStorage,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = js.DeleteStream(context.Background(), streamName) })
	if _, err := st.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "c1",
		FilterSubject: streamName + ".job.demo",
	}); err != nil {
		t.Fatal(err)
	}

	// Queue DB
	qdb := dbtest.Open(t, queue.Migrations())
	qcfg := queue.DefaultConfig()
	qcfg.Stream = streamName
	qadmin := queue.NewAdmin(nc, qcfg)

	srv := mcpx.New(mcpx.Options{Name: "ops-infra-test"})
	opsmcp.Register(srv, opsmcp.Options{
		DB:         qdb,
		QueueAdmin: qadmin,
		Redis:      rdb,
		NATS:       nc,
	})
	c := mcpxtest.Connect(t, srv, identity.Subject{Type: "user", ID: "ops"})

	// Redis INFO
	res, err := c.Call("ops_redis_info", map[string]any{"section": "memory"})
	if err != nil || res.IsError || !strings.Contains(res.Text, "used_memory") {
		t.Fatalf("ops_redis_info failed: err=%v text=%s", err, res.Text)
	}
	res, err = c.Call("ops_redis_info", map[string]any{"section": "all"})
	if err != nil || !res.IsError {
		t.Fatalf("disallowed info section should be rejected: err=%v text=%s", err, res.Text)
	}

	// Redis SCAN
	res, err = c.Call("ops_redis_scan", map[string]any{"pattern": prefix + "*"})
	if err != nil || res.IsError {
		t.Fatalf("ops_redis_scan failed: err=%v text=%s", err, res.Text)
	}
	var scanRes struct {
		Keys []struct {
			Key  string `json:"key"`
			Type string `json:"type"`
		} `json:"keys"`
	}
	if err := res.Decode(&scanRes); err != nil {
		t.Fatal(err)
	}
	if len(scanRes.Keys) != 3 {
		t.Fatalf("expected 3 keys, got %+v", scanRes.Keys)
	}

	// Redis GET: 普通键返回原文，敏感键脱敏，Hash 内部敏感字段脱敏。
	res, err = c.Call("ops_redis_get", map[string]any{"key": pubKey})
	if err != nil || res.IsError || !strings.Contains(res.Text, "hello-world") {
		t.Fatalf("ops_redis_get public failed: err=%v text=%s", err, res.Text)
	}
	res, err = c.Call("ops_redis_get", map[string]any{"key": secKey})
	if err != nil || res.IsError || strings.Contains(res.Text, "super-secret-value") || !strings.Contains(res.Text, "[REDACTED]") {
		t.Fatalf("ops_redis_get secret should be redacted: err=%v text=%s", err, res.Text)
	}
	res, err = c.Call("ops_redis_get", map[string]any{"key": hashKey})
	if err != nil || res.IsError || strings.Contains(res.Text, "top-secret") || !strings.Contains(res.Text, "alice") {
		t.Fatalf("ops_redis_get hash field should be redacted: err=%v text=%s", err, res.Text)
	}

	// NATS status + stream consumer listing
	res, err = c.Call("ops_nats_status", map[string]any{"stream": streamName})
	if err != nil || res.IsError || !strings.Contains(res.Text, streamName) || !strings.Contains(res.Text, `"c1"`) {
		t.Fatalf("ops_nats_status failed: err=%v text=%s", err, res.Text)
	}

	// Queue status + failed
	res, err = c.Call("ops_queue_status", map[string]any{})
	if err != nil || res.IsError || !strings.Contains(res.Text, streamName) {
		t.Fatalf("ops_queue_status failed: err=%v text=%s", err, res.Text)
	}
	res, err = c.Call("ops_queue_failed", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("ops_queue_failed failed: err=%v text=%s", err, res.Text)
	}
}
