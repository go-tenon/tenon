// Package queuetest 在测试中运行连接测试 NATS 的 queue.Worker。
//
//	db := dbtest.Open(t, migrations.FS) // 包含已发布的 queue 迁移
//	w := queue.NewWorker(db, queuetest.Conn(t), queuetest.Config(t), nil)
//	article.PublishJob.Handle(w, publish)
//	queuetest.Run(t, w)
package queuetest

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/go-tenon/tenon/natsx"
	"github.com/go-tenon/tenon/queue"
)

// EnvURL 是测试 NATS 的地址所在的环境变量，例如 nats://127.0.0.1:4222。服务器需要开启 JetStream。
const EnvURL = "NATS_TEST_URL"

// url 返回测试 NATS 的地址，环境变量未设置时使测试失败。
func url(t testing.TB) string {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Fatalf("%s is required for this test", EnvURL)
	}
	return url
}

// Conn 返回连接测试 NATS 的连接，环境变量未设置时使测试失败，测试结束时关闭。
// 交给 queue.NewWorker 和 queue.NewAdmin；先于 Run 调用，连接就在 Worker 停止之后才关闭。
func Conn(t testing.TB) *nats.Conn {
	t.Helper()
	nc, err := natsx.Connect(natsx.Config{URL: url(t)}, "queuetest", nil)
	if err != nil {
		t.Fatalf("queuetest: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// Config 返回测试用的 Worker 配置，环境变量未设置时使测试失败。
// 每次调用使用随机的 stream，并行的测试和反复运行的测试互不干扰，测试结束时删除它；
// 租约和停止等待较短，任务很快重试，测试结束时很快停下。
func Config(t testing.TB) queue.Config {
	t.Helper()
	url := url(t)
	cfg := queue.DefaultConfig()
	cfg.Stream = "t" + strings.ToLower(rand.Text()[:10])
	cfg.Lease = 5 * time.Second
	cfg.ShutdownTimeout = time.Second
	// 在 Run 注册的清理之后执行：Worker 停止之后才删除 stream。
	t.Cleanup(func() { deleteStream(t, url, cfg.Stream) })
	return cfg
}

// Run 在后台运行 w，测试结束时停止它并等待 Run 返回。Run 返回错误时使测试失败。
func Run(t testing.TB, w *queue.Worker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("queuetest: worker: %v", err)
		}
	})
}

// deleteStream 删除测试用的 stream，它不存在（Worker 没有运行过）时什么也不做。
func deleteStream(t testing.TB, url, stream string) {
	nc, err := nats.Connect(url)
	if err != nil {
		t.Errorf("queuetest: connect to nats: %v", err)
		return
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Errorf("queuetest: jetstream: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := js.DeleteStream(ctx, stream); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Errorf("queuetest: delete stream %s: %v", stream, err)
	}
}
