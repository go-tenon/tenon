package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/ryan053857/mygoravel/cleanup"
	"github.com/ryan053857/mygoravel/command"
	"github.com/ryan053857/mygoravel/database"
	"github.com/ryan053857/mygoravel/database/migrate"
	"github.com/ryan053857/mygoravel/examples/api/internal/order"
	"github.com/ryan053857/mygoravel/examples/api/internal/order/orderhttp"
	"github.com/ryan053857/mygoravel/fault"
	"github.com/ryan053857/mygoravel/httpx"
	"github.com/ryan053857/mygoravel/httpx/rest"
	"github.com/ryan053857/mygoravel/identity"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfgPath := "config.yaml"
	root := command.Root("orders", "mygoravel API example", command.IO{Out: os.Stdout, Err: os.Stderr})
	root.PersistentFlags().StringVar(&cfgPath, "config", cfgPath, "配置文件路径")

	root.AddCommand(command.NewServe(func(ctx context.Context, cmd *cobra.Command, args []string) error {
		return serveCmd(ctx, cfgPath)
	}))
	root.AddCommand(command.NewConfig(
		func(ctx context.Context, cmd *cobra.Command, args []string) error { return configCheck(cfgPath) },
		func(ctx context.Context, cmd *cobra.Command, args []string) error { return configShow(cmd, cfgPath) },
	))
	root.AddCommand(command.NewDoctor(func(ctx context.Context, cmd *cobra.Command, args []string) error {
		return doctorCmd(ctx, cfgPath)
	}))
	root.AddCommand(command.NewMigrate(
		func(ctx context.Context, cmd *cobra.Command, args []string) error { return migrateUp(ctx, cfgPath) },
		func(ctx context.Context, cmd *cobra.Command, args []string) error {
			return migrateStatus(ctx, cmd, cfgPath)
		},
		func(ctx context.Context, cmd *cobra.Command, args []string) error { return migrateDown(ctx, cfgPath) },
	))
	root.AddCommand(command.NewOpenAPI(func(ctx context.Context, cmd *cobra.Command, args []string) error {
		return openAPICmd(cmd)
	}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		return command.ExitCode(err)
	}
	return command.ExitOK
}

func serveCmd(ctx context.Context, cfgPath string) (err error) {
	var stack cleanup.Stack
	defer func() { err = stack.CloseWith(context.Background(), err) }()

	cfg, err := loadRuntime(cfgPath)
	if err != nil {
		return command.AsExitError(command.ExitConfig, err)
	}
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return command.AsExitError(command.ExitUnavailable, err)
	}
	_ = stack.Add("db", func(context.Context) error { return db.Close() })

	api := buildAPI(cfg, order.New(db), rest.ModeServe)
	ln, err := net.Listen("tcp", cfg.HTTP.Addr)
	if err != nil {
		return command.AsExitError(command.ExitUnavailable, err)
	}
	srv := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	return httpx.Serve(ctx, srv, ln, httpx.ServeOptions{ShutdownTimeout: 10 * time.Second})
}

func buildAPI(cfg appConfig, svc *order.Service, mode rest.Mode) *rest.API {
	api := rest.New(rest.Options{Title: "orders", Version: "0.1.0", Mode: mode})
	validate := func(ctx huma.Context, token string) (identity.Subject, error) {
		id, ok := cfg.Auth.Tokens[token]
		if !ok {
			return identity.Subject{}, fault.Define(fault.Unauthenticated, "auth.unauthenticated").New()
		}
		return identity.Subject{ID: id, Type: identity.SubjectUser, Method: "bearer", Scopes: []string{"orders"}}, nil
	}
	orderhttp.Register(api, orderhttp.Dependencies{Orders: svc, Validate: validate})
	return api
}

func loadRuntime(path string) (appConfig, error) {
	doc, err := readDoc(path, "http", "database", "auth")
	if err != nil {
		return appConfig{}, err
	}
	cfg := defaultConfig()
	if err := doc.Decode("http", &cfg.HTTP); err != nil {
		return appConfig{}, err
	}
	if err := doc.Decode("database", &cfg.Database); err != nil {
		return appConfig{}, err
	}
	if err := doc.Decode("auth", &cfg.Auth); err != nil {
		return appConfig{}, err
	}
	return cfg, nil
}

func loadDBOnly(path string) (database.Config, error) {
	doc, err := readDoc(path, "http", "database", "auth")
	if err != nil {
		return database.Config{}, err
	}
	cfg := database.DefaultConfig()
	if err := doc.Decode("database", &cfg); err != nil {
		return database.Config{}, err
	}
	return cfg, nil
}

func configCheck(path string) error {
	cfg, err := loadRuntime(path)
	if err != nil {
		return command.AsExitError(command.ExitConfig, err)
	}
	_ = cfg
	return nil
}

func configShow(cmd *cobra.Command, path string) error {
	cfg, err := loadRuntime(path)
	if err != nil {
		return command.AsExitError(command.ExitConfig, err)
	}
	enc := yaml.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	return enc.Close()
}

func doctorCmd(ctx context.Context, path string) error {
	cfg, err := loadRuntime(path)
	if err != nil {
		return command.AsExitError(command.ExitConfig, err)
	}
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return command.AsExitError(command.ExitUnavailable, err)
	}
	_ = db.Close()
	return nil
}

func migrateFS() (fs.FS, error) {
	return migrate.Sub(migrationFiles, "migrations")
}

func migrateUp(ctx context.Context, path string) error {
	cfg, err := loadDBOnly(path)
	if err != nil {
		return command.AsExitError(command.ExitConfig, err)
	}
	db, err := database.Open(ctx, cfg)
	if err != nil {
		return command.AsExitError(command.ExitUnavailable, err)
	}
	defer db.Close()
	fsys, err := migrateFS()
	if err != nil {
		return err
	}
	p, err := migrate.NewProvider(db.DB, fsys)
	if err != nil {
		return err
	}
	return migrate.Up(ctx, p)
}

func migrateStatus(ctx context.Context, cmd *cobra.Command, path string) error {
	cfg, err := loadDBOnly(path)
	if err != nil {
		return command.AsExitError(command.ExitConfig, err)
	}
	db, err := database.Open(ctx, cfg)
	if err != nil {
		return command.AsExitError(command.ExitUnavailable, err)
	}
	defer db.Close()
	fsys, err := migrateFS()
	if err != nil {
		return err
	}
	p, err := migrate.NewProvider(db.DB, fsys)
	if err != nil {
		return err
	}
	ver, err := p.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	_, err = cmd.OutOrStdout().Write([]byte("version " + strconv.FormatInt(ver, 10) + "\n"))
	return err
}

func migrateDown(ctx context.Context, path string) error {
	cfg, err := loadDBOnly(path)
	if err != nil {
		return command.AsExitError(command.ExitConfig, err)
	}
	db, err := database.Open(ctx, cfg)
	if err != nil {
		return command.AsExitError(command.ExitUnavailable, err)
	}
	defer db.Close()
	fsys, err := migrateFS()
	if err != nil {
		return err
	}
	p, err := migrate.NewProvider(db.DB, fsys)
	if err != nil {
		return err
	}
	_, err = p.Down(ctx)
	return err
}

func openAPICmd(cmd *cobra.Command) error {
	// 离线：不读运行密钥、不打开数据库。
	api := buildAPI(defaultConfig(), nil, rest.ModeOpenAPI)
	data, err := api.OpenAPIJSON()
	if err != nil {
		return err
	}
	var pretty any
	if err := json.Unmarshal(data, &pretty); err != nil {
		return err
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(pretty)
}
