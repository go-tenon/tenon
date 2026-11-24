package natsx_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/internal/natstest"
	"github.com/go-tenon/tenon/logging/logtest"
	"github.com/go-tenon/tenon/natsx"
)

func TestConnect(t *testing.T) {
	t.Parallel()
	rec := logtest.New()
	nc, err := natsx.Connect(natsx.Config{URL: natstest.URL(t)}, "natsx-test", natsx.WithLogger(rec.Logger()))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if nc.Opts.MaxReconnect != -1 || nc.Opts.PingInterval != natsx.PingInterval || nc.Opts.Name != "natsx-test" || nc.Opts.NoEcho {
		t.Fatalf("options %+v", nc.Opts)
	}
	ping := natsx.Ping(nc)
	if err := ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// 断开后记录日志、自动重连；重连期间 Ping 失败。
	if err := nc.ForceReconnect(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !rec.Has(slog.LevelInfo, "nats reconnected") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !rec.Has(slog.LevelInfo, "nats reconnected") {
		t.Fatalf("records %+v", rec.Records())
	}
	if err := ping(context.Background()); err != nil {
		t.Fatalf("ping after reconnect: %v", err)
	}
	nc.Close()
	if err := ping(context.Background()); err == nil || !strings.Contains(err.Error(), "CLOSED") {
		t.Fatalf("ping on a closed connection: %v", err)
	}
}

func TestConnectFails(t *testing.T) {
	t.Parallel()
	if _, err := natsx.Connect(natsx.Config{}, "x"); err == nil {
		t.Fatal("empty url accepted")
	}
	_, err := natsx.Connect(natsx.Config{URL: "nats://user:secret@127.0.0.1:1"}, "x")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unreachable server: %v", err)
	}
}
