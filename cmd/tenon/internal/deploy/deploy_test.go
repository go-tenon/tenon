package deploy_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-tenon/tenon/cmd/tenon/internal/deploy"
)

// callRecord 记录一次远端命令或上传调用。
type callRecord struct {
	Host       string
	Kind       string // "run" 或 "upload"
	Script     string
	LocalPath  string
	RemotePath string
	Stdin      []byte
}

// fakeRunner 是用于测试部署编排的内存 Runner。
type fakeRunner struct {
	mu    sync.Mutex
	calls []callRecord
	onRun func(host deploy.Host, script string, stdin []byte) (stdout, stderr string, err error)
	onUp  func(host deploy.Host, localPath, remotePath string) error
}

// Run 记录命令调用并交给 onRun 处理。
func (f *fakeRunner) Run(_ context.Context, host deploy.Host, script string, stdin io.Reader, stdout, stderr io.Writer) error {
	var in []byte
	if stdin != nil {
		in, _ = io.ReadAll(stdin)
	}
	f.mu.Lock()
	f.calls = append(f.calls, callRecord{Host: host.Addr, Kind: "run", Script: script, Stdin: in})
	handler := f.onRun
	f.mu.Unlock()
	if handler == nil {
		return nil
	}
	out, errText, err := handler(host, script, in)
	if out != "" && stdout != nil {
		_, _ = io.WriteString(stdout, out)
	}
	if errText != "" && stderr != nil {
		_, _ = io.WriteString(stderr, errText)
	}
	return err
}

// Upload 记录上传调用并交给 onUp 处理。
func (f *fakeRunner) Upload(_ context.Context, host deploy.Host, localPath, remotePath string) error {
	f.mu.Lock()
	f.calls = append(f.calls, callRecord{Host: host.Addr, Kind: "upload", LocalPath: localPath, RemotePath: remotePath})
	handler := f.onUp
	f.mu.Unlock()
	if handler != nil {
		return handler(host, localPath, remotePath)
	}
	return nil
}

func TestParseConfig(t *testing.T) {
	t.Parallel()

	raw := `
app: blog
environments:
  production:
    hosts:
      - addr: 203.0.113.10
      - addr: 203.0.113.11
        parts: [worker, scheduler]
        migrate: true
`
	target, err := deploy.Parse([]byte(raw), "fallback", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if target.App != "blog" || target.EnvName != "production" || target.KeepReleases != 5 {
		t.Fatalf("unexpected defaults: %+v", target)
	}
	if target.User != "deploy" || target.ServiceUser != "blog" || target.Dir != "/opt/blog" || target.EnvFile != "/etc/blog/env" {
		t.Fatalf("unexpected env defaults: %+v", target.Environment)
	}
	if target.MigrateHost().Addr != "203.0.113.11" {
		t.Fatalf("migrate host = %s, want 203.0.113.11", target.MigrateHost().Addr)
	}
	if !target.Hosts[0].HasHTTP() || target.Hosts[1].HasHTTP() {
		t.Fatalf("HasHTTP mismatch: %+v", target.Hosts)
	}

	// 未知字段、非法架构、多台 migrate、重复主机均应报错。
	for name, bad := range map[string]string{
		"unknown field":    "unknown: 1\nenvironments:\n  production:\n    hosts: [{addr: a}]\n",
		"bad arch":         "arch: 386\nenvironments:\n  production:\n    hosts: [{addr: a}]\n",
		"multiple migrate": "environments:\n  production:\n    hosts: [{addr: a, migrate: true}, {addr: b, migrate: true}]\n",
		"duplicate host":   "environments:\n  production:\n    hosts: [{addr: a}, {addr: a}]\n",
		"relative dir":     "environments:\n  production:\n    dir: opt/blog\n    hosts: [{addr: a}]\n",
	} {
		if _, err := deploy.Parse([]byte(bad), "blog", ""); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRenderUnit(t *testing.T) {
	t.Parallel()
	target, err := deploy.Parse([]byte("app: blog\nenvironments:\n  production:\n    hosts: [{addr: 10.0.0.1}]\n"), "blog", "")
	if err != nil {
		t.Fatal(err)
	}
	unit := deploy.RenderUnit(target)
	for _, want := range []string{
		"Type=notify-reload",
		"RuntimeDirectory=blog/%i",
		"PIDFile=/run/blog/%i/daemon.pid",
		"User=blog",
		"WorkingDirectory=/opt/blog",
		"EnvironmentFile=/etc/blog/env",
		"ExecStart=/opt/blog/bin/blog serve %i",
		"RestartPreventExitStatus=2 3",
		"ReadWritePaths=/opt/blog/storage",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
}

func TestSetup(t *testing.T) {
	t.Parallel()
	target, err := deploy.Parse([]byte("app: blog\nenvironments:\n  production:\n    hosts: [{addr: 10.0.0.1}]\n"), "blog", "")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	var out bytes.Buffer
	if err := deploy.Setup(context.Background(), runner, target, deploy.SetupOptions{HasSSR: true}, &out); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 ssh calls, got %d", len(runner.calls))
	}
	if !strings.Contains(runner.calls[0].Script, "glibc") {
		t.Errorf("SSR setup should check glibc:\n%s", runner.calls[0].Script)
	}
	if !strings.Contains(string(runner.calls[2].Stdin), "Type=notify-reload") {
		t.Errorf("unit stdin missing Type=notify-reload:\n%s", runner.calls[2].Stdin)
	}
}

func TestDeployFullPipelineAndExcludesLocalYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "app.yaml"), []byte("http:\n  addr: ':8080'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "local.yaml"), []byte("http:\n  addr: ':9999'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "blog-bin")
	if err := os.WriteFile(binPath, []byte("fake-elf-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	target, err := deploy.Parse([]byte(`
app: blog
keep_releases: 2
environments:
  production:
    config_dir: `+cfgDir+`
    hosts:
      - addr: web-1
        parts: [http]
        migrate: true
      - addr: worker-1
        parts: [worker]
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}

	var uploadedTar []byte
	var migrateCount int
	var pruned []string
	runner := &fakeRunner{
		onRun: func(host deploy.Host, script string, stdin []byte) (string, string, error) {
			switch {
			case script == "uname -m":
				return "x86_64\n", "", nil
			case strings.Contains(script, "tar -xzf"):
				if host.Addr == "web-1" {
					uploadedTar = stdin
				}
			case strings.Contains(script, "--help"):
				return "yes\n", "", nil
			case strings.Contains(script, "'migrate' 'up'"):
				migrateCount++
			case strings.HasPrefix(script, "ls -1 "):
				return "20261120-010000\n20261121-010000\n20261122-010000\n", "", nil
			case strings.HasPrefix(script, "sudo rm -rf '/opt/blog/releases/"):
				pruned = append(pruned, host.Addr+":"+script)
			}
			return "", "", nil
		},
	}

	var out, errOut bytes.Buffer
	err = deploy.Deploy(context.Background(), runner, target, deploy.ReleaseOptions{
		Binary:  binPath,
		Version: "20261122-010000",
		Now:     func() time.Time { return time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC) },
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("Deploy: %v\nout=%s\nerr=%s", err, out.String(), errOut.String())
	}
	if migrateCount != 1 {
		t.Errorf("migrate up ran %d times, want 1", migrateCount)
	}
	if len(pruned) != 2 || !strings.Contains(pruned[0], "20261120-010000") {
		t.Errorf("expected oldest release pruned on both hosts, got %v", pruned)
	}

	// 验证同步的配置归档包含 app.yaml 且排除了 local.yaml。
	gz, err := gzip.NewReader(bytes.NewReader(uploadedTar))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	if len(names) != 1 || names[0] != "app.yaml" {
		t.Errorf("config archive entries = %v, want [app.yaml]", names)
	}
}

func TestDeployAbortsOnReloadUpgradeFailed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "blog-bin")
	if err := os.WriteFile(binPath, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	target, err := deploy.Parse([]byte(`
app: blog
arch: amd64
environments:
  production:
    config_dir: `+filepath.Join(dir, "no-config")+`
    hosts:
      - addr: web-1
      - addr: web-2
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}

	web2Switched := false
	runner := &fakeRunner{
		onRun: func(host deploy.Host, script string, _ []byte) (string, string, error) {
			if strings.Contains(script, "--help") {
				return "no\n", "", nil
			}
			if host.Addr == "web-1" && strings.Contains(script, "systemctl reload") {
				return "", "upgrade failed: listen error", errors.New("exit status 1")
			}
			if host.Addr == "web-2" && strings.Contains(script, "systemctl reload") {
				web2Switched = true
			}
			return "", "", nil
		},
	}

	var out, errOut bytes.Buffer
	err = deploy.Deploy(context.Background(), runner, target, deploy.ReleaseOptions{
		Binary:  binPath,
		Version: "v2",
	}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "upgrade failed") {
		t.Fatalf("expected upgrade failed error, got %v", err)
	}
	if web2Switched {
		t.Errorf("web-2 should not be switched after web-1 failed")
	}
}

func TestRollbackAndOps(t *testing.T) {
	t.Parallel()
	target, err := deploy.Parse([]byte(`
app: blog
environments:
  production:
    maintenance_file: /etc/nginx/blog.maintenance
    hosts:
      - addr: web-1
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		onRun: func(_ deploy.Host, script string, _ []byte) (string, string, error) {
			switch {
			case strings.Contains(script, "cat '/opt/blog/current_release'"):
				return "20261122-020000\n", "", nil
			case strings.Contains(script, "ls -1 '/opt/blog/releases'"):
				return "20261121-010000\n20261122-020000\n", "", nil
			case strings.Contains(script, "test -x"):
				return "cfg\n", "", nil
			case strings.Contains(script, "--help"):
				return "no\n", "", nil
			case strings.Contains(script, "sudo cat '/etc/blog/env'"):
				return "APP_URL=https://example.com\n", "", nil
			}
			return "", "", nil
		},
	}

	var out, errOut bytes.Buffer
	if err := deploy.Rollback(context.Background(), runner, target, "", &out, &errOut); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if !strings.Contains(out.String(), "20261121-010000") {
		t.Errorf("rollback output:\n%s", out.String())
	}

	// EnvPush 经标准输入写入远端 env 文件，并可选择重启服务。
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envPath, []byte("APP_KEY=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := deploy.EnvPush(context.Background(), runner, target, envPath, "", true, &out, &errOut); err != nil {
		t.Fatalf("EnvPush: %v", err)
	}
	if !strings.Contains(out.String(), "已重启 blog@all.service") {
		t.Errorf("EnvPush output:\n%s", out.String())
	}

	out.Reset()
	if err := deploy.EnvPull(context.Background(), runner, target, "", &out); err != nil {
		t.Fatalf("EnvPull: %v", err)
	}
	if !strings.Contains(out.String(), "APP_URL=https://example.com") {
		t.Errorf("EnvPull output: %q", out.String())
	}

	out.Reset()
	if err := deploy.Maintenance(context.Background(), runner, target, true, &out); err != nil {
		t.Fatalf("Maintenance on: %v", err)
	}
	if !strings.Contains(out.String(), "已开启维护模式") {
		t.Errorf("Maintenance output: %q", out.String())
	}
}
