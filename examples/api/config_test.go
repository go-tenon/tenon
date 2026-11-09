package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryan053857/mygoravel/command"
	"github.com/ryan053857/mygoravel/config"
	"github.com/ryan053857/mygoravel/httpx/rest"
)

func writeConfig(t *testing.T, s string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestNoImplicitCredentialsAndRedactedShow(t *testing.T) {
	path := writeConfig(t, "database:\n  dsn: postgres://localhost/test\nauth:\n  credentials:\n    - token: private-custom-token\n      subject_id: custom\n")
	cfg, err := loadRuntime(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Auth.Credentials) != 1 || cfg.Auth.Credentials[0].SubjectID != "custom" {
		t.Fatal("default credential leaked")
	}
	api := buildAPI(cfg, nil, rest.ModeServe)
	req := httptest.NewRequest("GET", "/orders", nil)
	req.Header.Set("Authorization", "Bearer dev")
	rr := httptest.NewRecorder()
	api.Handler().ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatal(rr.Code)
	}
	for _, format := range []string{"yaml", "json"} {
		out, diag := &bytes.Buffer{}, &bytes.Buffer{}
		cmd := newCommand(command.IO{Out: out, Err: diag})
		cmd.SetArgs([]string{"--config", path, "config", "show", "--format", format})
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "private-custom-token") || !strings.Contains(out.String(), "******") || diag.Len() > 0 {
			t.Fatalf("unsafe output: %s %s", out, diag)
		}
	}
	if err := defaultConfig().Auth.Validate(); err == nil {
		t.Fatal("default auth accepted")
	}
}
func TestConfigCheckAggregatesAndMigrateIgnoresOtherSecrets(t *testing.T) {
	path := writeConfig(t, "http:\n  addr: ''\ndatabase:\n  dsn: ''\nauth:\n  credentials: []\ncache:\n  driver: invalid\n")
	err := configCheck(path)
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	for _, section := range []string{"http", "database", "auth", "cache"} {
		if !strings.Contains(err.Error(), section) {
			t.Fatalf("missing %s: %v", section, err)
		}
	}
	path = writeConfig(t, "database:\n  dsn: postgres://localhost/test\nauth:\n  credentials: ${NEVER_SET_AUTH_SECRET}\ncache:\n  addr: ${NEVER_SET_CACHE_SECRET}\n")
	if _, err := loadDBOnly(path); err != nil {
		t.Fatal(err)
	}
}
func TestOfflineCommandIOAndUsage(t *testing.T) {
	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	root := newCommand(command.IO{Out: out, Err: diag})
	root.SetArgs([]string{"--config", "/missing/config.yaml", "openapi"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "order-create") || diag.Len() != 0 {
		t.Fatal("offline output mismatch")
	}
	for _, args := range [][]string{{"serve", "unexpected"}, {"serve", "--bad-flag"}, {"unknown"}, {"migrate", "unknown"}} {
		root = newCommand(command.IO{Out: out, Err: diag})
		root.SetArgs(args)
		if err := root.Execute(); command.ExitCode(err) != command.ExitUsage {
			t.Fatal(err)
		}
	}
}
func TestCredentialValidation(t *testing.T) {
	for _, credentials := range [][]credential{{{Token: config.Secret("x")}}, {{Token: "x", SubjectID: "a"}, {Token: "x", SubjectID: "b"}}} {
		if (authConfig{Credentials: credentials}).Validate() == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
}
