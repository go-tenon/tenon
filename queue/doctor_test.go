package queue_test

import (
	"context"
	"strings"
	"testing"

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
	for _, name := range []string{"tables", "nats", "nats_version", "jetstream"} {
		if res := got[name]; res.Status != doctor.Passed {
			t.Errorf("%s = %+v, want passed on the test services", name, res)
		}
	}
}

func TestDoctorBeforeMigrations(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, nil)
	got := results(doctor.Run(context.Background(), queue.Doctor(db, nil, queue.DefaultConfig())))
	if res := got["tables"]; res.Status != doctor.Skipped || !strings.Contains(res.Summary, "migrations") {
		t.Errorf("tables before migrations = %+v, want skipped", res)
	}
	if _, ok := got["jetstream"]; ok {
		t.Error("NATS checks ran without a connection")
	}
}
