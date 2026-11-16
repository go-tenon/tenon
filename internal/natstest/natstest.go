// Package natstest 为框架的测试连接测试用的 NATS 服务器，地址来自 NATS_TEST_URL，服务器需要开启 JetStream。
//
// 测试应给自己的 subject、stream 和 bucket 加随机的名字，多个测试可以共用同一个服务器。
package natstest

import (
	"os"
	"testing"

	"github.com/nats-io/nats.go"
)

// URL 返回 NATS_TEST_URL。未设置时使测试失败。
func URL(t testing.TB) string {
	t.Helper()
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Fatal("NATS_TEST_URL is required for this test")
	}
	return url
}

// Connect 返回连接测试服务器的新连接，测试结束时关闭。连接断开后一直重连，与生产环境的建议设置相同。
func Connect(t testing.TB, opts ...nats.Option) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(URL(t), append([]nats.Option{nats.MaxReconnects(-1)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}
