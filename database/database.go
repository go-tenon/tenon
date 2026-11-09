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
	return nil
}

// New 创建 *bun.DB，不主动连接。
func New(cfg Config) (*bun.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	pgxCfg, err := pgx.ParseConfig(cfg.DSN.Reveal())
	if err != nil {
		return nil, errors.Wrap(err, "database")
	}
	sqldb := stdlib.OpenDB(*pgxCfg)
	sqldb.SetMaxOpenConns(cfg.MaxOpenConns)
	sqldb.SetMaxIdleConns(cfg.MaxIdleConns)
	sqldb.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqldb.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	return bun.NewDB(sqldb, pgdialect.New()), nil
}

// Open 创建并 Ping；失败时关闭连接。
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
