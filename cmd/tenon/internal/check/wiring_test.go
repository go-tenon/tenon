package check_test

import (
	"strings"
	"testing"

	"github.com/go-tenon/tenon/cmd/tenon/internal/check"
	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
)

// TestCheckWiring 验证框架装配检查能识别常见遗漏与反模式，并支持 wiring-ignore 豁免。
func TestCheckWiring(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"cmd/app/main.go": `package main

import (
	"context"
	"net"
	"net/http"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/lock/pglock"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/ratelimit/pglimit"
	"github.com/go-tenon/tenon/schedule"
)

func run(ctx context.Context, cfg httpx.Config, ln net.Listener, h http.Handler, db *database.DB, sched *schedule.Scheduler) {
	_ = httpx.Serve(ctx, cfg, ln, h)
	_ = httpx.Serve(ctx, cfg, ln, h) // wiring-ignore
	_ = database.RunInTx(ctx, db, func(ctx context.Context, tx database.Tx) error {
		_ = authz.NewStore(tx, nil)
		_ = apitoken.NewStore(tx)
		return nil
	})
	_ = queue.Topic[string]("search.sync.articles")
	sched.Add("rl", "@hourly", schedule.Cleanup(pglimit.New(db, "x", cfg).Cleanup, 1000))
	sched.Add("lk", "@hourly", schedule.Cleanup(pglock.New(db).Cleanup, 1000))
	op := struct{ MaxBodyBytes int64 }{}
	op.MaxBodyBytes = 10 << 20
}
`,
		"apps/web/src/start.ts": `import { createStart } from "@tanstack/react-start"
export const startInstance = createStart(() => ({}))
`,
	})
	cfg := &project.Config{
		Frontends: []project.Frontend{{Name: "web", Dir: "apps/web", Base: "/", SSR: true}},
	}
	found, err := check.CheckWiring(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantSubstrings := []string{
		"httpx.Serve is missing",
		"authz.NewStore(tx, ...)",
		"apitoken.NewStore(tx, ...)",
		`hardcoded search queue topic "search.sync.articles"`,
		"throwaway pglimit.New(...).Cleanup",
		"throwaway pglock.New(...).Cleanup",
		"MaxBodyBytes (10485760) exceeds default 1 MiB",
		"must install requestMiddleware",
	}
	if len(found) != len(wantSubstrings) {
		t.Fatalf("got %d findings, want %d:\n%+v", len(found), len(wantSubstrings), found)
	}
	for _, want := range wantSubstrings {
		matched := false
		for _, f := range found {
			if f.Check == "wiring" && strings.Contains(f.Message, want) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("missing finding containing %q in %+v", want, found)
		}
	}
}

// TestCheckWiringClean 验证正确装配的代码不产生误报。
func TestCheckWiringClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"cmd/app/main.go": `package main

import (
	"context"
	"net"
	"net/http"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/httpcmd"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/ratelimit/pglimit"
	"github.com/go-tenon/tenon/schedule"
)

func run(ctx context.Context, d *command.Daemon, cfg httpx.Config, ln net.Listener, h http.Handler, db *database.DB, sched *schedule.Scheduler, roles interface{ WithDB(any) any }) {
	_ = httpx.Defaults(cfg, httpx.BodyLimitFor(func(*http.Request) int64 { return 20 << 20 }))
	_ = httpx.Serve(ctx, cfg, ln, h, httpcmd.ServeOptions(d)...)
	_ = database.RunInTx(ctx, db, func(ctx context.Context, tx database.Tx) error {
		_ = roles.WithDB(tx)
		return nil
	})
	sched.Add("rl", "@hourly", schedule.Cleanup(pglimit.Cleanup(db), 1000))
	op := struct{ MaxBodyBytes int64 }{MaxBodyBytes: 10 << 20}
	_ = op
}
`,
		"apps/web/src/start.ts": `import { createStart } from "@tanstack/react-start"
import { requestMiddleware } from "@go-tenon/start"
export const startInstance = createStart(() => ({ requestMiddleware: [requestMiddleware] }))
`,
	})
	cfg := &project.Config{
		Frontends: []project.Frontend{{Name: "web", Dir: "apps/web", Base: "/", SSR: true}},
	}
	found, err := check.CheckWiring(root, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("clean project reported findings: %+v", found)
	}
}
