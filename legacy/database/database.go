package database

import (
	"context"
	"runtime"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/ryan053857/go-kit/config"
)

// Config 是数据库连接配置，产品将其嵌入自己的配置结构体，
// 先用 DefaultConfig 填好默认值，再由 config.Load 读取 YAML 覆盖：
//
//	database:
//	  dsn: ${DATABASE_URL}
//	  max_open_conns: 20
type Config struct {
	// DSN 为 pgx 支持的连接串，URL 或 key=value 形式均可，例如
	// postgres://user:pass@localhost:5432/app?sslmode=disable。
	DSN config.Secret `yaml:"dsn"`

	// 以下与 sql.DB 的同名设置含义相同。
	MaxOpenConns    int           `yaml:"max_open_conns"`     // 默认 4 × GOMAXPROCS
	MaxIdleConns    int           `yaml:"max_idle_conns"`     // 默认与 MaxOpenConns 相同
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`  // 默认 1h
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time"` // 默认 30m
}

// DefaultConfig 返回带默认值的 Config（DSN 为空，必须由产品配置）。
func DefaultConfig() Config {
	n := 4 * runtime.GOMAXPROCS(0)
	return Config{
		MaxOpenConns:    n,
		MaxIdleConns:    n,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 30 * time.Minute,
	}
}

// Open 与 New 相同，并用 ctx Ping 一次以尽早发现配置错误。调用方负责 Close。
//
// 本函数不挂载查询日志等 hook，由产品按需显式添加，例如：
//
//	db.AddQueryHook(bunslog.NewQueryHook(bunslog.WithSlowQueryThreshold(200 * time.Millisecond)))
func Open(ctx context.Context, cfg Config) (*bun.DB, error) {
	db, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, errors.Wrap(err, "database: ping")
	}
	return db, nil
}

// New 通过 pgx 创建连接 Postgres 的 *bun.DB，但不建立连接：首次查询时才连接。
// 用于不需要访问数据库的场景，例如导出 OpenAPI 文档。调用方负责 Close。
func New(cfg Config) (*bun.DB, error) {
	pgxCfg, err := pgx.ParseConfig(string(cfg.DSN))
	if err != nil {
		// pgx 的解析错误会对密码打码，可以直接返回。
		return nil, errors.Wrap(err, "database")
	}

	sqldb := stdlib.OpenDB(*pgxCfg)
	sqldb.SetMaxOpenConns(cfg.MaxOpenConns)
	sqldb.SetMaxIdleConns(cfg.MaxIdleConns)
	sqldb.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqldb.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	return bun.NewDB(sqldb, pgdialect.New()), nil
}
