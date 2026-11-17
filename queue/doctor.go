package queue

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/natsx"
)

const (
	// minPostgres 是队列需要的 PostgreSQL 版本（server_version_num）：迁移创建可以随主备切换同步的复制槽。
	minPostgres = 170000
	// minNATSMajor、minNATSMinor 是队列需要的 NATS 版本：JetStream 的定时消息。
	minNATSMajor, minNATSMinor = 2, 15

	docPreparing = "docs/background/queue.md#preparing"
	docSettings  = "docs/installation/operations.md#queue"
	docRecovery  = "docs/installation/operations.md#queue-recovery"
)

// Doctor 返回检查队列运行环境的一组自检，组名为 queue。数据库一侧：
//
//   - postgres_version：PostgreSQL 17 以上；
//   - wal_level：logical；
//   - replication_limits：max_replication_slots 和 max_wal_senders 大于 0；
//   - replication_connection：用 Worker 的方式建立一条复制连接，确认用户有 REPLICATION 属性、
//     cfg.ReplicationDSN 可用（连接经过 PgBouncer 时需要它）；
//   - wal_keep_size：设置了 max_slot_wal_keep_size，否则提示：worker 停止时积压的 WAL 可能写满磁盘；
//   - slot：转发任务的复制槽存在并且没有失效。队列的迁移还没执行时跳过，所以迁移之前也可以运行这组检查。
//
// nc 不为 nil 时还检查 NATS：nats 连接可用，nats_version 为 2.15 以上，jetstream 已经开启。
// db 和 nc 在检查时才使用，可以在打开连接之前构造。
func Doctor(db *bun.DB, nc *nats.Conn, cfg Config) doctor.Group {
	checks := []doctor.Check{
		{Name: "postgres_version", Run: func(ctx context.Context) error {
			var num int
			var version string
			err := db.NewRaw("SELECT current_setting('server_version_num')::int, current_setting('server_version')").
				Scan(ctx, &num, &version)
			if err != nil {
				return err
			}
			return checkPostgresVersion(num, version)
		}},
		{Name: "wal_level", Run: func(ctx context.Context) error {
			level, err := setting(ctx, db, "wal_level")
			if err != nil {
				return err
			}
			return checkWALLevel(level)
		}},
		{Name: "replication_limits", Run: func(ctx context.Context) error {
			var slots, senders int
			err := db.NewRaw("SELECT current_setting('max_replication_slots')::int, current_setting('max_wal_senders')::int").
				Scan(ctx, &slots, &senders)
			if err != nil {
				return err
			}
			return checkReplicationLimits(slots, senders)
		}},
		{Name: "replication_connection", Run: func(ctx context.Context) error {
			return checkReplicationConnection(ctx, db, cfg.ReplicationDSN)
		}},
		{Name: "wal_keep_size", Run: func(ctx context.Context) error {
			size, err := setting(ctx, db, "max_slot_wal_keep_size")
			if err != nil {
				return err
			}
			return checkWALKeepSize(size)
		}},
		{Name: "slot", Run: func(ctx context.Context) error {
			var migrated bool
			if err := db.NewRaw("SELECT to_regclass('queue_failed_jobs') IS NOT NULL").Scan(ctx, &migrated); err != nil {
				return err
			}
			if !migrated {
				return doctor.Skip("the queue migrations have not been applied yet")
			}
			s, err := ReadStatus(ctx, db)
			if err != nil {
				return err
			}
			return checkSlotStatus(s)
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

// setting 读取 PostgreSQL 的一项设置。
func setting(ctx context.Context, db bun.IDB, name string) (string, error) {
	var v string
	err := db.NewRaw("SELECT current_setting(?)", name).Scan(ctx, &v)
	return v, errors.Wrapf(err, "queue: read %s", name)
}

// checkPostgresVersion 检查 PostgreSQL 的版本号 num 不低于 minPostgres。
func checkPostgresVersion(num int, version string) error {
	if num >= minPostgres {
		return nil
	}
	return doctor.Fail(
		fmt.Sprintf("PostgreSQL %s is too old; the queue needs 17 or later", version),
		"upgrade PostgreSQL to 17 or later (the framework is tested with 18)",
	).See(docPreparing)
}

// checkWALLevel 检查 wal_level 是 logical。
func checkWALLevel(level string) error {
	if level == "logical" {
		return nil
	}
	return doctor.Fail(
		fmt.Sprintf("wal_level is %s; the queue needs logical", level),
		"set wal_level = logical and restart PostgreSQL:\n"+
			"Docker: command: [postgres, -c, wal_level=logical]\n"+
			"managed databases: enable logical replication in the parameter group (RDS: rds.logical_replication = 1)",
	).See(docPreparing)
}

// checkReplicationLimits 检查 max_replication_slots 和 max_wal_senders 都大于 0。
func checkReplicationLimits(slots, senders int) error {
	if slots > 0 && senders > 0 {
		return nil
	}
	return doctor.Fail(
		fmt.Sprintf("max_replication_slots is %d and max_wal_senders is %d; the queue needs one of each", slots, senders),
		"set both to at least 1 (the default 10 is enough) and restart PostgreSQL",
	).See(docSettings)
}

// checkReplicationConnection 像 Worker 一样建立复制连接，执行 IDENTIFY_SYSTEM。
func checkReplicationConnection(ctx context.Context, db *bun.DB, dsn string) error {
	cfg, err := replicationConfig(ctx, db, dsn)
	if err != nil {
		return err
	}
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err == nil {
		_, err = pglogrepl.IdentifySystem(ctx, conn)
		_ = conn.Close(context.WithoutCancel(ctx))
	}
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42501" { // insufficient_privilege
		return doctor.Fail(
			"the database user cannot open a replication connection: "+pgErr.Message,
			"grant it the REPLICATION attribute: ALTER ROLE <user> REPLICATION\n"+
				"(RDS: GRANT rds_replication TO <user>)",
		).See(docSettings)
	}
	return doctor.Fail(
		"cannot open a replication connection: "+err.Error(),
		"replication connections must reach PostgreSQL directly: when the database connection goes through\n"+
			"PgBouncer, set queue.replication_dsn to a direct address; check pg_hba.conf allows replication",
	).See(docSettings)
}

// checkWALKeepSize 检查 max_slot_wal_keep_size 设置了上限。
func checkWALKeepSize(size string) error {
	if size != "-1" {
		return nil
	}
	return doctor.Warn(
		"max_slot_wal_keep_size is unlimited: while no worker relays jobs, WAL piles up until the disk is full",
		"set a limit, for example -c max_slot_wal_keep_size=10GB, and alert on the queue backlog",
	).See(docSettings)
}

// checkSlotStatus 检查复制槽存在、没有失效，保留的 WAL 没有即将超过上限（unreserved）。
func checkSlotStatus(s Status) error {
	switch {
	case !s.Exists:
		return doctor.Fail(
			fmt.Sprintf("replication slot %s does not exist, for example after restoring a backup or a failover", s.Slot),
			"run `queue slot recreate`; jobs published while it was missing are lost",
		).See(docRecovery)
	case s.WALStatus == "lost":
		return doctor.Fail(
			fmt.Sprintf("replication slot %s is lost: its backlog exceeded max_slot_wal_keep_size", s.Slot),
			"run `queue slot recreate`; jobs that were in the lost backlog are gone",
		).See(docRecovery)
	case s.WALStatus == "unreserved":
		return doctor.Warn(
			fmt.Sprintf("replication slot %s is about to exceed max_slot_wal_keep_size", s.Slot),
			"start a worker so that it relays the backlog, or raise max_slot_wal_keep_size",
		).See(docSettings)
	}
	return nil
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

// majorMinor 从 "2.15.1" 或 "v2.15.0-beta" 这样的版本中取出主版本号和次版本号。
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
