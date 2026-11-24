package queue

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/natsx"
)

const (
	// minNATSMajor、minNATSMinor 是框架要求的最低 NATS 版本：支持 JetStream Pull、consumer 暂停与 KV 过期标记。
	minNATSMajor, minNATSMinor = 2, 15

	docPreparing = "docs/background/queue.md#preparing"
)

// Doctor 返回检查队列运行环境的一组自检，组名为 queue。
//
//   - tables：queue_outbox 和 queue_failed_jobs 表是否存在；迁移还没执行时跳过；
//   - nats（nc 不为 nil）：NATS 连接可用；
//   - nats_version（nc 不为 nil）：NATS 服务器版本为 2.15 以上；
//   - jetstream（nc 不为 nil）：NATS 已开启 JetStream。
//
// db 和 nc 在检查时才使用，可以在打开连接之前构造。
func Doctor(db *bun.DB, nc *nats.Conn, _ Config) doctor.Group {
	checks := []doctor.Check{
		{Name: "tables", Run: func(ctx context.Context) error {
			var outbox, failed bool
			err := db.NewRaw("SELECT to_regclass('queue_outbox') IS NOT NULL, to_regclass('queue_failed_jobs') IS NOT NULL").
				Scan(ctx, &outbox, &failed)
			if err != nil {
				return err
			}
			if !outbox && !failed {
				return doctor.Skip("the queue migrations have not been applied yet")
			}
			if !outbox || !failed {
				return doctor.Fail(
					"queue tables are incomplete",
					"publish and run queue.Migrations() so both queue_outbox and queue_failed_jobs exist",
				).See(docPreparing)
			}
			return nil
		}},
	}
	if nc != nil {
		checks = append(checks,
			doctor.Check{Name: "nats", Run: natsx.Ping(nc)},
			doctor.Check{Name: "nats_version", Run: func(context.Context) error {
				return checkNATSVersion(nc.ConnectedServerVersion())
			}},
			doctor.Check{Name: "jetstream", Run: func(ctx context.Context) error {
				js, err := jetstream.New(nc)
				if err != nil {
					return err
				}
				_, err = js.AccountInfo(ctx)
				return checkJetStream(err)
			}},
		)
	}
	return doctor.Group{Name: "queue", Checks: checks}
}

// checkNATSVersion 检查 NATS 服务器的版本不低于要求。
func checkNATSVersion(version string) error {
	major, minor, ok := majorMinor(version)
	if ok && (major > minNATSMajor || major == minNATSMajor && minor >= minNATSMinor) {
		return nil
	}
	return doctor.Fail(
		fmt.Sprintf("NATS server %q is too old; the queue needs %d.%d or later", version, minNATSMajor, minNATSMinor),
		fmt.Sprintf("upgrade nats-server to %d.%d or later", minNATSMajor, minNATSMinor),
	).See(docPreparing)
}

// majorMinor 从 "2.10.1" 或 "v2.15.0-beta" 这样的版本中取出主版本号和次版本号。
func majorMinor(version string) (major, minor int, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(version, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	return major, minor, err1 == nil && err2 == nil
}

// checkJetStream 把访问 JetStream 的错误 err 转换为检查结果。
func checkJetStream(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, jetstream.ErrJetStreamNotEnabled), errors.Is(err, jetstream.ErrJetStreamNotEnabledForAccount):
		return doctor.Fail(
			"JetStream is not enabled on the NATS server; the queue keeps its jobs there",
			"start nats-server with -js and a storage directory, for example: nats-server -js -sd /data",
		).See(docPreparing)
	}
	return err
}
