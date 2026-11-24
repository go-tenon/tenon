package httpcmd_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/httpcmd"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/httpx"
)

func TestServeOptionsAndServe(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if opts := httpcmd.ServeOptions(nil); opts != nil {
		t.Fatalf("ServeOptions(nil) = %v, want nil", opts)
	}

	d, err := command.NewDaemon(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	cfg := config.Default[httpx.Config]()
	cfg.Addr = "127.0.0.1:0"
	cfg.DrainDelay = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := false
	run, err := httpcmd.Serve(d, cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), httpx.OnReady(func() {
		served = true
		cancel()
	}))
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if err := d.Run(ctx, run); err != nil {
		t.Fatalf("d.Run: %v", err)
	}
	if !served {
		t.Fatal("OnReady callback was not invoked")
	}
}
