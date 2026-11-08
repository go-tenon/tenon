// Command service shows how a product wires the config and logger modules
// together in main.
//
// Run from the repository root:
//
//	go run ./examples/service -config examples/service/config.yaml
//	LOG_LEVEL=debug LOG_FORMAT=text go run ./examples/service -config examples/service/config.yaml
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"github.com/shellphy-kit/tools/config"
	"github.com/shellphy-kit/tools/logger"
)

const (
	serviceName = "example-service"
	version     = "dev" // normally injected with -ldflags
)

// Config is owned by the product. The log section uses slog.Level directly:
// it decodes from "debug", "info", "warn", "error".
type Config struct {
	Log struct {
		Level  slog.Level `yaml:"level"`
		Format string     `yaml:"format"`
	} `yaml:"log"`
	DB struct {
		DSN config.Secret `yaml:"dsn"`
	} `yaml:"db"`
}

func DefaultConfig() Config {
	var c Config
	c.Log.Level = slog.LevelInfo
	c.Log.Format = logger.FormatJSON
	return c
}

func main() {
	path := flag.String("config", "config.yaml", "path to the configuration file")
	flag.Parse()

	cfg := DefaultConfig()
	if err := config.Load(*path, &cfg); err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	// LevelVar allows changing the level at runtime (e.g. on config reload).
	var level slog.LevelVar
	level.Set(cfg.Log.Level)
	slog.SetDefault(logger.New(logger.Options{
		Level:  &level,
		Format: cfg.Log.Format,
	}).With("service", serviceName, "version", version))

	// Secret fields are masked, so the whole config can be logged safely.
	slog.Info("config loaded", "config", cfg)

	// Request-scoped fields are attached to the context once (typically in
	// a middleware); every slog.*Context call below includes them.
	ctx := logger.WithAttrs(context.Background(), slog.String("request_id", "req-1"))
	createOrder(ctx, 42)
}

// createOrder is ordinary business code: it uses only the standard library slog.
func createOrder(ctx context.Context, id int) {
	slog.DebugContext(ctx, "creating order", "order_id", id)
	slog.InfoContext(ctx, "order created", "order_id", id)
}
