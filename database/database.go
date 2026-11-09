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

	"github.com/ryan053857/mygoravel/config"
)

// Config 是数据库连接配置。
type Config struct {
	DSN             config.Secret `yaml:"dsn"`
	MaxOpenConns    int           `yaml:"max_open_conns"`
	MaxIdleConns    int           `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time"`
}

// DefaultConfig 返回默认连接池参数；DSN 必须由应用配置。
func DefaultConfig() Config {
	n := 4 * runtime.GOMAXPROCS(0)
	return Config{
		MaxOpenConns:    n,
		MaxIdleConns:    n,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 30 * time.Minute,
	}
}

// Validate 检查必填项。
func (c Config) Validate() error {
	if c.DSN.Reveal() == "" {
		return errors.New("database: dsn is required")
	}
	if c.MaxOpenConns < 0 || c.MaxIdleConns < 0 {
		return errors.New("database: connection pool sizes must be >= 0")
	}
	if c.ConnMaxLifetime < 0 || c.ConnMaxIdleTime < 0 {
		return errors.New("database: connection lifetimes must be >= 0")
	}
	if _, err := pgx.ParseConfig(c.DSN.Reveal()); err != nil {
		return errors.New("database: invalid dsn")
	}
	return nil
}

// Open 创建原生连接池并 Ping；任何失败都关闭已打开的资源。
func Open(ctx context.Context, cfg Config) (*bun.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	pgxCfg, err := pgx.ParseConfig(cfg.DSN.Reveal())
	if err != nil {
		return nil, errors.New("database: invalid dsn")
	}
	sqldb := stdlib.OpenDB(*pgxCfg)
	sqldb.SetMaxOpenConns(cfg.MaxOpenConns)
	sqldb.SetMaxIdleConns(cfg.MaxIdleConns)
	sqldb.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqldb.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	db := bun.NewDB(sqldb, pgdialect.New())
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, errors.Wrap(err, "database: ping")
	}
	return db, nil
}
