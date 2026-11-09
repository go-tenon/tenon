package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/cockroachdb/errors"
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
	"github.com/ryan053857/mygoravel/i18n"
	"github.com/ryan053857/mygoravel/identity"
	"github.com/ryan053857/mygoravel/logging"
)

func main() {
	os.Exit(run())
}

func newCommand(streams command.IO) *cobra.Command {
	cfgPath := "config.yaml"
	root := command.Root("orders", "mygoravel API example", streams)
	root.PersistentFlags().StringVar(&cfgPath, "config", cfgPath, "配置文件路径")

	root.AddCommand(command.NewServe(func(ctx context.Context, cmd *cobra.Command, args []string) error {
		return serveCmd(ctx, cfgPath, logging.New(logging.Options{Writer: cmd.ErrOrStderr()}))
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

	root.Version = "orders 0.1.0; mygoravel " + frameworkVersion()
	if info, ok := debug.ReadBuildInfo(); ok {
		root.Version += "; " + info.GoVersion
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" || setting.Key == "vcs.modified" {
				root.Version += "; " + setting.Key + "=" + setting.Value
			}
		}
	}
	return root
}
func frameworkVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/ryan053857/mygoravel" {
				return dep.Version
			}
		}
		return info.Main.Version
	}
	return "(devel)"
}
func run() int {
	root := newCommand(command.IO{Out: os.Stdout, Err: os.Stderr})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		return command.ExitCode(err)
	}
	return command.ExitOK
}

func serveCmd(ctx context.Context, cfgPath string, logger *slog.Logger) (err error) {
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

	c, err := openCache(ctx, cfg.Cache, &stack)
	if err != nil {
		return command.AsExitError(command.ExitUnavailable, err)
	}
	svc := order.New(db, order.Options{Cache: c, Namespace: cfg.Cache.Namespace, TTL: cfg.Cache.TTL, Logger: logger})
	api := buildAPI(cfg, svc, rest.ModeServe, logger)
	var draining httpx.Draining
	api.Router().Get("/livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	api.Router().Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		probe, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if draining.Load() || db.PingContext(probe) != nil {
			httpx.WriteProblem(w, 503, "error.unavailable", httpx.RequestID(r.Context()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ln, err := net.Listen("tcp", cfg.HTTP.Addr)
	if err != nil {
		return command.AsExitError(command.ExitUnavailable, err)
	}
	srv := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: cfg.HTTP.RequestTimeout, WriteTimeout: cfg.HTTP.RequestTimeout, IdleTimeout: time.Minute}
	return httpx.Serve(ctx, srv, ln, httpx.ServeOptions{ShutdownTimeout: cfg.HTTP.ShutdownTimeout, Logger: logger, OnDraining: draining.Set})
}

func buildAPI(cfg appConfig, svc *order.Service, mode rest.Mode, loggers ...*slog.Logger) *rest.API {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	languages, err := i18n.New(i18n.Options{Default: cfg.HTTP.DefaultLanguage, Layers: []i18n.Layer{i18n.FrameworkLayer()}})
	if err != nil {
		panic(err)
	}
	var proxies []netip.Prefix
	for _, p := range cfg.HTTP.TrustedProxies {
		proxies = append(proxies, netip.MustParsePrefix(p))
	}
	api := rest.New(rest.Options{Title: "orders", Version: "0.1.0", Mode: mode, Middleware: httpx.MiddlewareOptions{Logger: logger, Languages: languages, MaxBodyBytes: cfg.HTTP.MaxBodyBytes, RequestTimeout: cfg.HTTP.RequestTimeout, TrustedProxies: proxies}})
	validate := func(ctx huma.Context, token string) (identity.Subject, error) {
		id := ""
		for _, c := range cfg.Auth.Credentials {
			if subtle.ConstantTimeCompare([]byte(c.Token.Reveal()), []byte(token)) == 1 {
				id = c.SubjectID
			}
		}
		if id == "" {
			return identity.Subject{}, fault.Define(fault.Unauthenticated, "auth.unauthenticated").New()
		}
		return identity.Subject{ID: id, Type: identity.SubjectUser, Method: "bearer", Scopes: []string{"orders"}}, nil
	}
	orderhttp.Register(api, orderhttp.Dependencies{Orders: svc, Validate: validate})
	return api
}

func loadRuntime(path string) (appConfig, error) {
	doc, err := readDoc(path, "http", "database", "auth", "cache")
	if err != nil {
		return appConfig{}, err
	}
	cfg := defaultConfig()
	err = errors.Join(doc.Decode("http", &cfg.HTTP), doc.Decode("database", &cfg.Database), doc.Decode("auth", &cfg.Auth), doc.Decode("cache", &cfg.Cache))
	if err != nil {
		return appConfig{}, err
	}
	return cfg, nil
}

func loadDBOnly(path string) (database.Config, error) {
	doc, err := readDoc(path, "http", "database", "auth", "cache")
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
	format, _ := cmd.Flags().GetString("format")
	if format == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(cfg)
	}
	if format != "" && format != "yaml" {
		return command.AsExitError(command.ExitUsage, errors.New("format must be json or yaml"))
	}
	enc := yaml.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	return enc.Close()
}

func doctorCmd(ctx context.Context, path string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	doc, err := readDoc(path, "http", "database", "auth", "cache")
	if err != nil {
		return command.AsExitError(command.ExitConfig, err)
	}
	cfg := defaultConfig()
	err = errors.Join(doc.Decode("database", &cfg.Database), doc.Decode("cache", &cfg.Cache))
	if err != nil {
		return command.AsExitError(command.ExitConfig, err)
	}
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return command.AsExitError(command.ExitUnavailable, err)
	}
	var stack cleanup.Stack
	defer func() { err = stack.CloseWith(context.Background(), err) }()
	_ = stack.Add("db", func(context.Context) error { return db.Close() })
	_, err = openCache(ctx, cfg.Cache, &stack)
	return command.AsExitError(command.ExitUnavailable, err)
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
	statuses, err := p.Status(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(statuses)
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
