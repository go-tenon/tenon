package database

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/doctor"
)

// MaxClockSkew 是 Doctor 允许的应用与数据库时钟之差，超过时提示。
const MaxClockSkew = time.Second

// Doctor 返回检查 PostgreSQL 的一组自检，组名为 postgres：
//
//   - connection：连接可用；
//   - clock：本机时钟与数据库时钟相差不超过 MaxClockSkew，否则提示。框架把 Go 算出的时间写进 SQL，
//     锁的过期、限流的窗口、任务的执行时间都按应用实例的时钟计算；各实例都与数据库对时，它们之间也就一致。
//
// db 在检查时才使用，可以在打开连接之前构造。
func Doctor(db *bun.DB) doctor.Group {
	return doctor.Group{Name: "postgres", Checks: []doctor.Check{
		{Name: "connection", Run: func(ctx context.Context) error { return db.PingContext(ctx) }},
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
