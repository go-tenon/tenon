package queue_test

import (
	"context"
	"crypto/rand"
	"net/url"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/internal/natstest"
	"github.com/go-tenon/tenon/queue"
)

// results 按检查名返回结果。
func results(r doctor.DoctorReport) map[string]doctor.DoctorResult {
	m := map[string]doctor.DoctorResult{}
	for _, res := range r.Results {
		m[res.Check] = res
	}
	return m
}

func TestDoctor(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, queue.Migrations())
	report := doctor.Run(context.Background(), queue.Doctor(db, natstest.Connect(t), queue.DefaultConfig()))
	got := results(report)
	for _, name := range []string{
		"postgres_version", "wal_level", "replication_limits", "replication_connection", "slot",
		"nats", "nats_version", "jetstream",
	} {
		if res := got[name]; res.Status != doctor.Passed {
			t.Errorf("%s = %+v, want passed on the test services", name, res)
		}
	}
	// 测试服务器没有设置 max_slot_wal_keep_size。
	if res := got["wal_keep_size"]; res.Status != doctor.Passed && res.Status != doctor.Warning {
		t.Errorf("wal_keep_size = %+v", res)
	}
}

func TestDoctorBeforeMigrations(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, nil)
	got := results(doctor.Run(context.Background(), queue.Doctor(db, nil, queue.DefaultConfig())))
	if res := got["slot"]; res.Status != doctor.Skipped || !strings.Contains(res.Summary, "migrations") {
		t.Errorf("slot before migrations = %+v, want skipped", res)
	}
	if _, ok := got["jetstream"]; ok {
		t.Error("NATS checks ran without a connection")
	}
}

func TestDoctorWithoutReplicationAttribute(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, nil)
	ctx := context.Background()
	admin, err := database.Open(ctx, database.Config{DSN: dbtest.URL(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() }) // 在删除角色之后关闭
	role, password := "doctor_"+strings.ToLower(rand.Text()[:12]), rand.Text()
	if _, err := admin.ExecContext(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD '"+password+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP ROLE "+role); err != nil {
			t.Error(err)
		}
	})
	var name string
	if err := db.NewRaw("SELECT current_database()").Scan(ctx, &name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	u.User, u.Path = url.UserPassword(role, password), "/"+name

	cfg := queue.DefaultConfig()
	cfg.ReplicationDSN = u.String()
	res := results(doctor.Run(ctx, queue.Doctor(db, nil, cfg)))["replication_connection"]
	if res.Status != doctor.Failed || !strings.Contains(res.Fix, "REPLICATION") {
		t.Errorf("replication_connection = %+v, want failed with the REPLICATION fix", res)
	}
}
