package database

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"runtime"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/go-tenon/tenon/errorx"
)

// ErrUnavailable 表示 Open 时数据库连不上。它属于 errorx.Unavailable 分类，
// 命令因此以 command.ExitUnavailable 退出，部署脚本可以据此重试。
var ErrUnavailable = errorx.Define(errorx.Unavailable, "database.unavailable")

// Config 是数据库连接配置。连接池由 pgxpool 管理；MaxConns、MaxConnLifetime、MaxConnIdleTime 为 0 时使用 DefaultConfig 的值。
type Config struct {
	DSN             string        `yaml:"dsn"`                // pgx 支持的连接串，例如 postgres://user:pass@localhost:5432/app?sslmode=disable。
	MaxConns        int           `yaml:"max_conns"`          // 连接池的最大连接数。所有实例、所有进程的连接数之和要控制在数据库或 PgBouncer 的上限以内。
	MinConns        int           `yaml:"min_conns"`          // 连接池保持的最少连接数，空闲时也不关闭。
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime"`  // 连接的最长存活时间，到期后在空闲时关闭，例如让数据库的配置变更和负载均衡生效。
	MaxConnIdleTime time.Duration `yaml:"max_conn_idle_time"` // 空闲连接的最长保留时间，超过 MinConns 的空闲连接到期后关闭。
	SlowQuery       time.Duration `yaml:"slow_query"`         // 慢查询的阈值：执行时间达到它的语句记录一条 Warn 日志，带着把字面量换成 ? 之后的 SQL；0 表示不记录。
	Replicas        []string      `yaml:"replicas"`           // 读库的连接串。事务之外的 SELECT 在读库之间轮流执行，其余语句在 DSN 指向的主库执行；为空时全部在主库。
}

// DefaultConfig 返回默认连接池参数；DSN 必须由应用配置。
func DefaultConfig() Config {
	return Config{
		MaxConns:        4 * runtime.GOMAXPROCS(0),
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: 30 * time.Minute,
		SlowQuery:       time.Second,
	}
}

// SetDefaults 实现 config.Defaulter，使 config.Load 自动填充默认值。
func (c *Config) SetDefaults() { *c = DefaultConfig() }

// withDefaults 返回把为 0 的连接池参数换成默认值之后的配置。
func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.MaxConns == 0 {
		c.MaxConns = d.MaxConns
	}
	if c.MaxConnLifetime == 0 {
		c.MaxConnLifetime = d.MaxConnLifetime
	}
	if c.MaxConnIdleTime == 0 {
		c.MaxConnIdleTime = d.MaxConnIdleTime
	}
	return c
}

// Validate 检查 DSN 非空与连接池参数；DSN 的格式在 Open 时检查。
func (c Config) Validate() error {
	if c.DSN == "" {
		return errors.New("dsn is required")
	}
	if slices.Contains(c.Replicas, "") {
		return errors.New("replicas must not contain an empty dsn")
	}
	c = c.withDefaults()
	if c.MaxConns < 1 || c.MaxConns > math.MaxInt32 {
		return errors.New("max_conns must be positive")
	}
	if c.MinConns < 0 || c.MinConns > c.MaxConns {
		return errors.New("min_conns must be between 0 and max_conns")
	}
	if c.MaxConnLifetime < 0 || c.MaxConnIdleTime < 0 {
		return errors.New("max_conn_lifetime and max_conn_idle_time must not be negative")
	}
	if c.SlowQuery < 0 {
		return errors.New("slow_query must be >= 0")
	}
	return nil
}

// Open 创建 PostgreSQL 连接池并 Ping；任何失败都关闭已打开的资源。Ping 失败时返回 ErrUnavailable。
// cfg.SlowQuery 大于 0 时记录慢查询；WithTelemetry 让每条语句报告指标和链路，并报告连接池的指标。
//
// 连接池是 pgxpool，返回的 *bun.DB 每次执行语句或开始事务时从它取一个连接，用完立即放回。
// 取连接时等待的时长由 ctx 决定，acquire.WithTimeout 可以只给这一步设一个较短的时限，超过时返回 ErrBusy。
//
// cfg.Replicas 非空时，每个读库另有一个同样参数的连接池，会话默认只读（与 ReadOnly 相同），
// 池名是连接池的名字加 -replica-1、-replica-2……。返回的 *bun.DB 按语句选择连接池：
// 事务之外的 SELECT 在读库之间轮流执行；写操作、原始 SQL（NewRaw、ExecContext 等）、事务中的语句，
// 以及 Primary 返回的 ctx 中的语句在主库执行。
func Open(ctx context.Context, cfg Config, opts ...Option) (*bun.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, errors.Wrap(err, "database")
	}
	cfg = cfg.withDefaults()
	o := options{name: "primary"}
	for _, opt := range opts {
		opt(&o)
	}
	primary, conn, err := openPool(ctx, cfg, cfg.DSN, o)
	if err != nil {
		return nil, err
	}
	servers := []*pgconn.Config{conn}
	var resolver *replicas
	if len(cfg.Replicas) > 0 {
		resolver = &replicas{}
		for i, dsn := range cfg.Replicas {
			ro := o
			ro.name, ro.readOnly = fmt.Sprintf("%s-replica-%d", o.name, i+1), true
			r, conn, err := openPool(ctx, cfg, dsn, ro)
			if err != nil {
				_ = primary.Close()
				_ = resolver.Close()
				return nil, errors.Wrapf(err, "replica %d", i+1)
			}
			resolver.dbs = append(resolver.dbs, r)
			servers = append(servers, conn)
		}
	}
	var db *bun.DB
	if resolver != nil {
		db = bun.NewDB(primary, pgdialect.New(), bun.WithConnResolver(resolver))
	} else {
		db = bun.NewDB(primary, pgdialect.New())
	}
	if h := newQueryHook(o, cfg, servers); h != nil {
		db = db.WithQueryHook(h)
	}
	return db, nil
}

// openPool 打开 dsn 的连接池并 Ping，返回交给 Bun 的 *sql.DB 和解析后的连接参数。
// Ping 失败时关闭连接池，返回 ErrUnavailable。
func openPool(ctx context.Context, cfg Config, dsn string, o options) (*sql.DB, *pgconn.Config, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, errors.Wrap(err, "database: invalid dsn")
	}
	poolCfg.MaxConns, poolCfg.MinConns = int32(cfg.MaxConns), int32(cfg.MinConns)
	poolCfg.MaxConnLifetime, poolCfg.MaxConnIdleTime = cfg.MaxConnLifetime, cfg.MaxConnIdleTime
	if o.readOnly {
		poolCfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, nil, errors.Wrap(err, "database: create pool")
	}
	sqlDB := sql.OpenDB(newConnector(pool, o)) // Close 时关闭 connector，从而关闭 pool
	// 连接由 pool 管理：database/sql 不保留空闲连接，用完立即放回 pool，也不限制数量。
	sqlDB.SetMaxIdleConns(0)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, nil, ErrUnavailable.Wrap(errors.Wrap(err, "database: ping"))
	}
	return sqlDB, &poolCfg.ConnConfig.Config, nil
}
