package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/doctor"
)

const (
	// minPGMajor 与 minPGVersionNum 是框架要求的最低 PostgreSQL 主版本与 server_version_num。
	minPGMajor      = 18
	minPGVersionNum = minPGMajor * 10000

	docDatabase = "docs/database/database.md#connecting"
)

// MaxClockSkew 是 Doctor 允许的应用与数据库时钟之差，超过时提示。
const MaxClockSkew = time.Second

// Doctor 返回检查 PostgreSQL 的一组自检，组名为 postgres：
//
//   - connection：连接可用；
//   - version：PostgreSQL 主版本为 18 以上；
//   - clock：本机时钟与数据库时钟相差不超过 MaxClockSkew，否则提示。框架把 Go 算出的时间写进 SQL，
//     定时任务触发等按应用实例的时钟计算；各实例都与数据库对时，它们之间也就一致。
//
// db 在检查时才使用，可以在打开连接之前构造。
func Doctor(db *bun.DB) doctor.Group {
	return doctor.Group{Name: "postgres", Checks: []doctor.Check{
		{Name: "connection", Run: func(ctx context.Context) error { return db.PingContext(ctx) }},
		{Name: "version", Run: func(ctx context.Context) error {
			var raw string
			if err := db.NewRaw("SHOW server_version_num").Scan(ctx, &raw); err != nil {
				return err
			}
			return checkPGVersion(raw)
		}},
		{Name: "clock", Run: func(ctx context.Context) error {
			before := time.Now()
			var server time.Time
			if err := db.NewRaw("SELECT clock_timestamp()").Scan(ctx, &server); err != nil {
				return err
			}
			return checkClock(before, time.Now(), server)
		}},
	}}
}

// checkPGVersion 检查 PostgreSQL 的 server_version_num 不低于最低版本要求。
func checkPGVersion(raw string) error {
	num, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || num <= 0 {
		return doctor.Fail(
			fmt.Sprintf("PostgreSQL version %q is invalid; Tenon requires PostgreSQL %d or later", raw, minPGMajor),
			fmt.Sprintf("upgrade PostgreSQL to %d or later", minPGMajor),
		).See(docDatabase)
	}
	if num >= minPGVersionNum {
		return nil
	}
	return doctor.Fail(
		fmt.Sprintf("PostgreSQL %d.%d is too old; Tenon requires PostgreSQL %d or later", num/10000, num%10000, minPGMajor),
		fmt.Sprintf("upgrade PostgreSQL to %d or later", minPGMajor),
	).See(docDatabase)
}

// checkClock 比较数据库在 [before, after] 之间读到的时间 server 与本机时钟：落在这段时间之外超过 MaxClockSkew 时提示。
func checkClock(before, after, server time.Time) error {
	var skew time.Duration
	switch {
	case server.Before(before):
		skew = before.Sub(server)
	case server.After(after):
		skew = server.Sub(after)
	}
	if skew <= MaxClockSkew {
		return nil
	}
	direction := "ahead of"
	if server.Before(before) {
		direction = "behind"
	}
	return doctor.Warn(
		fmt.Sprintf("the database clock is %s %s this host", skew.Round(time.Millisecond), direction),
		"synchronize the clocks of the database and every application host with NTP (chrony, systemd-timesyncd)",
	)
}
