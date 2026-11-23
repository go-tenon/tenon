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
	"slices"
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

func TestParseSingleHostAndMultiRoleTopology(t *testing.T) {
	t.Parallel()

	// 1. 单机简写：自动等价为单个 all 角色。
	single, err := deploy.Parse([]byte(`
app: blog
environments:
  production:
    hosts: [203.0.113.10]
`), "fallback", "")
	if err != nil {
		t.Fatalf("Parse single: %v", err)
	}
	if len(single.Hosts) != 1 || !slices.Equal(single.Hosts[0].Parts, []string{"all"}) || !single.Hosts[0].HasHTTP() {
		t.Fatalf("unexpected single host: %+v", single.Hosts)
	}

	// 2. 多服务多机 roles：同主机多角色自动归并去重，波次为后端优先 + 入口金丝雀后批量滚动。
	multi, err := deploy.Parse([]byte(`
app: blog
environments:
  production:
    roles:
      http:
        hosts: [10.0.1.11, 10.0.1.12, 10.0.1.13]
        max_parallel: 2
        stop_timeout: 30s
      worker:
        hosts: [10.0.1.21, 10.0.1.22]
        stop_timeout: 120s
        env:
          GOMAXPROCS: "4"
      scheduler:
        hosts: [10.0.1.21]
`), "blog", "")
	if err != nil {
		t.Fatalf("Parse multi: %v", err)
	}
	// 默认 migrate_host 优先选首个非入口节点 10.0.1.21。
	if multi.MigrateHost().Addr != "10.0.1.21" {
		t.Fatalf("migrate host = %s, want 10.0.1.21", multi.MigrateHost().Addr)
	}
	// 10.0.1.21 同时承担 scheduler 和 worker 角色，归并去重后只有一条主机记录。
	h21, ok := multi.HostByAddr("10.0.1.21")
	if !ok || !slices.Equal(h21.Parts, []string{"scheduler", "worker"}) || !slices.Equal(h21.Roles, []string{"scheduler", "worker"}) {
		t.Fatalf("merged host 10.0.1.21 = %+v", h21)
	}
	if di := h21.DropIns["worker"]; di.StopTimeout != "120s" || di.Env["GOMAXPROCS"] != "4" {
		t.Fatalf("worker drop-in = %+v", di)
	}

	waves := multi.RolloutWaves()
	if len(waves) != 4 {
		t.Fatalf("waves = %+v, want 4 waves", waves)
	}
	if waves[0].Name != "backend-canary" || !slices.Equal(waves[0].Hosts, []string{"10.0.1.21"}) {
		t.Errorf("wave[0] = %+v", waves[0])
	}
	if waves[1].Name != "backend" || !slices.Equal(waves[1].Hosts, []string{"10.0.1.22"}) {
		t.Errorf("wave[1] = %+v", waves[1])
	}
	if waves[2].Name != "http-canary" || !slices.Equal(waves[2].Hosts, []string{"10.0.1.11"}) || waves[2].MaxParallel != 1 {
		t.Errorf("wave[2] = %+v", waves[2])
	}
	if waves[3].Name != "http" || !slices.Equal(waves[3].Hosts, []string{"10.0.1.12", "10.0.1.13"}) || waves[3].MaxParallel != 2 {
		t.Errorf("wave[3] = %+v", waves[3])
	}

	// 非法配置均应报错。
	for name, bad := range map[string]string{
		"unknown field":    "unknown: 1\nenvironments:\n  production:\n    hosts: [a]\n",
		"both hosts roles": "environments:\n  production:\n    hosts: [a]\n    roles:\n      http:\n        hosts: [a]\n",
		"bad migrate_host": "environments:\n  production:\n    migrate_host: nope\n    hosts: [a]\n",
		"mix all and http": "environments:\n  production:\n    roles:\n      all:\n        hosts: [a]\n      http:\n        hosts: [a]\n",
		"bad env key":      "environments:\n  production:\n    roles:\n      worker:\n        hosts: [a]\n        env:\n          bad-key: '1'\n",
	} {
		if _, err := deploy.Parse([]byte(bad), "blog", ""); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRenderUnitAndDropIn(t *testing.T) {
	t.Parallel()
	target, err := deploy.Parse([]byte("app: blog\nenvironments:\n  production:\n    hosts: [10.0.0.1]\n"), "blog", "")
	if err != nil {
		t.Fatal(err)
	}
	unit := deploy.RenderUnit(target)
	for _, want := range []string{
		"Type=notify-reload",
		"RuntimeDirectory=blog/%i",
		"PIDFile=/run/blog/%i/daemon.pid",
		"ExecStart=/opt/blog/bin/blog serve %i",
		"RestartPreventExitStatus=2 3",
		"ReadWritePaths=/opt/blog/storage",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
	dropIn := deploy.RenderDropIn(deploy.PartDropIn{
		StopTimeout: "120s",
		Env:         map[string]string{"GOMAXPROCS": "4"},
	})
	if !strings.Contains(dropIn, "TimeoutStopSec=120s") || !strings.Contains(dropIn, `Environment="GOMAXPROCS=4"`) {
		t.Errorf("unexpected drop-in:\n%s", dropIn)
	}
}

func TestSetupWritesDropIns(t *testing.T) {
	t.Parallel()
	target, err := deploy.Parse([]byte(`
app: blog
environments:
  production:
    roles:
      worker:
        hosts: [10.0.0.1]
        stop_timeout: 120s
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	var out bytes.Buffer
	report, err := deploy.Setup(context.Background(), runner, target, deploy.SetupOptions{HasSSR: true}, &out)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(report.Hosts) != 1 || report.Hosts[0] != "10.0.0.1" {
		t.Fatalf("report = %+v", report)
	}
	wroteDropIn := false
	for _, c := range runner.calls {
		if strings.Contains(c.Script, "blog@worker.service.d/override.conf") && strings.Contains(string(c.Stdin), "TimeoutStopSec=120s") {
			wroteDropIn = true
		}
	}
	if !wroteDropIn {
		t.Errorf("expected worker drop-in override.conf to be written")
	}
}

func TestDeployMultiRoleWavesReconcileAndExcludeLocalYAML(t *testing.T) {
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
    roles:
      http:
        hosts: [web-1, web-2]
      worker:
        hosts: [worker-1]
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var uploadedTar []byte
	var migrateCount int
	var switchOrder []string
	var disabledUnits []string

	runner := &fakeRunner{
		onRun: func(host deploy.Host, script string, stdin []byte) (string, string, error) {
			switch {
			case script == "uname -m":
				return "x86_64\n", "", nil
			case strings.Contains(script, "tar -xzf"):
				mu.Lock()
				if uploadedTar == nil {
					uploadedTar = stdin
				}
				mu.Unlock()
			case strings.Contains(script, "--help"):
				return "yes\n", "", nil
			case strings.Contains(script, "'migrate' 'up'"):
				mu.Lock()
				migrateCount++
				mu.Unlock()
			case strings.Contains(script, "systemctl list-units"):
				// 模拟 web-1 之前跑的是 blog@all.service，如今拆分为 blog@http.service，应自动清理旧 unit。
				if host.Addr == "web-1" {
					return "blog@all.service\nblog@http.service\n", "", nil
				}
				return "", "", nil
			case strings.HasPrefix(script, "sudo systemctl disable --now "):
				mu.Lock()
				disabledUnits = append(disabledUnits, host.Addr+":"+script)
				mu.Unlock()
			case strings.Contains(script, "systemctl reload"):
				mu.Lock()
				switchOrder = append(switchOrder, host.Addr)
				mu.Unlock()
			case strings.HasPrefix(script, "ls -1 "):
				return "20261120-010000\n20261121-010000\n20261122-010000\n", "", nil
			}
			return "", "", nil
		},
	}

	var out, errOut bytes.Buffer
	report, err := deploy.Deploy(context.Background(), runner, target, deploy.ReleaseOptions{
		Binary:  binPath,
		Version: "20261122-010000",
		Now:     func() time.Time { return time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC) },
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("Deploy: %v\nout=%s\nerr=%s", err, out.String(), errOut.String())
	}
	if migrateCount != 1 || !report.Migrated {
		t.Errorf("migrateCount=%d migrated=%v", migrateCount, report.Migrated)
	}
	// 验证波次顺序：worker-1 先于 web-1（金丝雀），web-1 先于 web-2。
	if !slices.Equal(switchOrder, []string{"worker-1", "web-1", "web-2"}) {
		t.Errorf("switch order = %v, want [worker-1 web-1 web-2]", switchOrder)
	}
	// 验证声明式对账停用了 web-1 上的孤儿 blog@all.service。
	if len(disabledUnits) != 1 || !strings.Contains(disabledUnits[0], "blog@all.service") {
		t.Errorf("disabledUnits = %v", disabledUnits)
	}

	// 验证同步的配置归档排除 local.yaml。
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
	if !slices.Equal(names, []string{"app.yaml"}) {
		t.Errorf("config archive entries = %v, want [app.yaml]", names)
	}
}

func TestDeployAbortsAfterCanaryFailure(t *testing.T) {
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
    roles:
      http:
        hosts: [web-1, web-2]
        max_parallel: 2
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
	_, err = deploy.Deploy(context.Background(), runner, target, deploy.ReleaseOptions{
		Binary:  binPath,
		Version: "v2",
	}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "upgrade failed") {
		t.Fatalf("expected upgrade failed error, got %v", err)
	}
	if web2Switched {
		t.Errorf("web-2 should not be switched after canary web-1 failed")
	}
}

func TestStatusLogsAggregationAndOps(t *testing.T) {
	t.Parallel()
	target, err := deploy.Parse([]byte(`
app: blog
environments:
  production:
    maintenance_file: /etc/nginx/blog.maintenance
    roles:
      http:
        hosts: [web-1, web-2]
      worker:
        hosts: [worker-1]
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		onRun: func(host deploy.Host, script string, _ []byte) (string, string, error) {
			switch {
			case strings.Contains(script, "systemctl show -p ActiveState"):
				return "release=v1\n---unit=blog@http.service\nActiveState=active\nSubState=running\nStatusText=\nActiveEnterTimestamp=Sun 2026-11-22\n", "", nil
			case strings.Contains(script, "journalctl"):
				return "INF ok on " + host.Addr + "\nERR boom on " + host.Addr + "\n", "", nil
			case strings.Contains(script, "cat '/opt/blog/current_release'"):
				return "20261122-020000\n", "", nil
			case strings.Contains(script, "ls -1 '/opt/blog/releases'"):
				return "20261121-010000\n20261122-020000\n", "", nil
			case strings.Contains(script, "test -x"):
				return "cfg\n", "", nil
			case strings.Contains(script, "--help"):
				return "no\n", "", nil
			}
			return "", "", nil
		},
	}

	// 1. 多机状态表格与 JSON 结构。
	st, err := deploy.CollectStatus(context.Background(), runner, target, "http", "")
	if err != nil || len(st.Hosts) != 2 {
		t.Fatalf("CollectStatus: st=%+v err=%v", st, err)
	}
	var table bytes.Buffer
	deploy.FormatStatus(&table, st)
	if !strings.Contains(table.String(), "web-1") || !strings.Contains(table.String(), "active (running)") {
		t.Errorf("status table:\n%s", table.String())
	}

	// 2. 多机日志聚合（流式带 [host] 前缀 + 过滤 ERR）。
	var logBuf, errBuf bytes.Buffer
	err = deploy.Logs(context.Background(), runner, target, deploy.LogsOptions{
		Role:     "http",
		Contains: "ERR",
		Since:    "15m",
	}, &logBuf, &errBuf)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if !strings.Contains(logBuf.String(), "[web-1] ERR boom on web-1") || !strings.Contains(logBuf.String(), "[web-2] ERR boom on web-2") || strings.Contains(logBuf.String(), "INF") {
		t.Errorf("aggregated logs:\n%s", logBuf.String())
	}

	// 3. CollectLogs 结构化输出。
	lrep, err := deploy.CollectLogs(context.Background(), runner, target, deploy.LogsOptions{Role: "http", Contains: "ERR"})
	if err != nil || len(lrep.Hosts) != 2 || len(lrep.Hosts[0].Lines) != 1 {
		t.Fatalf("CollectLogs: %+v err=%v", lrep, err)
	}

	// 4. Maintenance 默认只操作入口节点（web-1、web-2），不动 worker-1。
	var out bytes.Buffer
	mrep, err := deploy.Maintenance(context.Background(), runner, target, "", "", true, &out)
	if err != nil {
		t.Fatalf("Maintenance: %v", err)
	}
	if !slices.Equal(mrep.Hosts, []string{"web-1", "web-2"}) {
		t.Errorf("maintenance hosts = %v, want [web-1 web-2]", mrep.Hosts)
	}

	// 5. Rollback 返回结构化报告。
	out.Reset()
	rrep, err := deploy.Rollback(context.Background(), runner, target, "", &out, &errBuf)
	if err != nil || rrep.Version != "20261121-010000" {
		t.Fatalf("Rollback: rep=%+v err=%v", rrep, err)
	}
}

func TestParseConfigFilesAndRenderUnit(t *testing.T) {
	t.Parallel()

	target, err := deploy.Parse([]byte(`
app: blog
environments:
  production:
    config_files:
      - config/app.yaml
      - config/prod.yaml
      - deploy/extra.yaml
    hosts: [203.0.113.10]
`), "blog", "")
	if err != nil {
		t.Fatalf("Parse config_files: %v", err)
	}
	wantRemote := []string{"config/app.yaml", "config/prod.yaml", "config/deploy/extra.yaml"}
	if !slices.Equal(target.RemoteConfigFiles(), wantRemote) {
		t.Fatalf("RemoteConfigFiles = %v, want %v", target.RemoteConfigFiles(), wantRemote)
	}
	if target.ConfigDir != "" {
		t.Fatalf("ConfigDir = %q, want empty when config_files is set", target.ConfigDir)
	}
	if got, want := target.ConfigFlagValue(), "config/app.yaml,config/prod.yaml,config/deploy/extra.yaml"; got != want {
		t.Fatalf("ConfigFlagValue = %q, want %q", got, want)
	}

	unit := deploy.RenderUnit(target)
	wantExec := "ExecStart=/opt/blog/bin/blog --config config/app.yaml,config/prod.yaml,config/deploy/extra.yaml serve %i"
	if !strings.Contains(unit, wantExec) {
		t.Errorf("unit missing %q:\n%s", wantExec, unit)
	}

	for name, bad := range map[string]string{
		"both config_dir and config_files": "environments:\n  production:\n    config_dir: config\n    config_files: [config/app.yaml]\n    hosts: [a]\n",
		"absolute path":                    "environments:\n  production:\n    config_files: [/etc/app.yaml]\n    hosts: [a]\n",
		"path escape":                      "environments:\n  production:\n    config_files: [../secret.yaml]\n    hosts: [a]\n",
		"comma in path":                    "environments:\n  production:\n    config_files: ['config/a,b.yaml']\n    hosts: [a]\n",
		"space in path":                    "environments:\n  production:\n    config_files: ['config/a b.yaml']\n    hosts: [a]\n",
		"duplicate remote path":            "environments:\n  production:\n    config_files: [config/prod.yaml, prod.yaml]\n    hosts: [a]\n",
		"bare config dir":                  "environments:\n  production:\n    config_files: [config]\n    hosts: [a]\n",
	} {
		if _, err := deploy.Parse([]byte(bad), "blog", ""); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestDeployConfigFilesOverlayPermissionsAndRunRemote(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"app.yaml":     "http:\n  addr: ':8080'\n",
		"prod.yaml":    "crypt:\n  key: 'prod-secret'\n",
		"staging.yaml": "crypt:\n  key: 'staging-secret'\n",
		"local.yaml":   "http:\n  addr: ':9999'\n",
	} {
		if err := os.WriteFile(filepath.Join(cfgDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	binPath := filepath.Join(dir, "blog-bin")
	if err := os.WriteFile(binPath, []byte("fake-elf-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	target, err := deploy.Parse([]byte(`
app: blog
arch: amd64
environments:
  production:
    config_files:
      - config/app.yaml
      - config/prod.yaml
    hosts: [web-1]
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Plan 与 LoadTopology 均包含 config_files。
	plan, err := deploy.Plan(context.Background(), target, deploy.ReleaseOptions{Version: "v1"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !slices.Equal(plan.ConfigFiles, []string{"config/app.yaml", "config/prod.yaml"}) {
		t.Fatalf("plan.ConfigFiles = %v", plan.ConfigFiles)
	}

	// 2. 执行完整发布：验证仅打包清单文件、权限 0640/0750、systemd-run 自动注入 --config。
	var mu sync.Mutex
	var uploadedTar []byte
	var unpackScript string
	var systemdRuns []string
	runner := &fakeRunner{
		onRun: func(_ deploy.Host, script string, stdin []byte) (string, string, error) {
			switch {
			case strings.Contains(script, "tar -xzf"):
				mu.Lock()
				uploadedTar = stdin
				unpackScript = script
				mu.Unlock()
			case strings.Contains(script, "--help"):
				return "yes\n", "", nil
			case strings.HasPrefix(script, "sudo systemd-run "):
				mu.Lock()
				systemdRuns = append(systemdRuns, script)
				mu.Unlock()
			}
			return "", "", nil
		},
	}

	var out, errOut bytes.Buffer
	report, err := deploy.Deploy(context.Background(), runner, target, deploy.ReleaseOptions{
		Binary:  binPath,
		Version: "20261123-010000",
		WorkDir: dir,
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("Deploy: %v\nout=%s\nerr=%s", err, out.String(), errOut.String())
	}
	if !slices.Equal(report.ConfigFiles, []string{"config/app.yaml", "config/prod.yaml"}) {
		t.Errorf("report.ConfigFiles = %v", report.ConfigFiles)
	}

	// 验证归档仅含 app.yaml 与 prod.yaml，排除 staging.yaml 与 local.yaml，且权限为 0640。
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
		if hdr.Mode != 0o640 {
			t.Errorf("tar entry %s mode = %o, want 640", hdr.Name, hdr.Mode)
		}
		names = append(names, hdr.Name)
	}
	if !slices.Equal(names, []string{"app.yaml", "prod.yaml"}) {
		t.Errorf("config archive entries = %v, want [app.yaml prod.yaml]", names)
	}
	for _, want := range []string{"-m 750 -o root -g 'blog'", "chown -R 'root:blog'", "chmod 750", "chmod 640"} {
		if !strings.Contains(unpackScript, want) {
			t.Errorf("unpackScript missing %q:\n%s", want, unpackScript)
		}
	}

	// 验证 config check、doctor、migrate up、health 均自动传入 --config。
	wantFlag := "'--config' 'config/app.yaml,config/prod.yaml'"
	if len(systemdRuns) < 4 {
		t.Fatalf("expected at least 4 systemd-run calls, got %d: %v", len(systemdRuns), systemdRuns)
	}
	for _, cmd := range systemdRuns {
		if !strings.Contains(cmd, wantFlag) {
			t.Errorf("systemd-run missing %s: %s", wantFlag, cmd)
		}
	}

	// 3. RunRemote 自动注入 --config；调用方显式传 --config 时不重复注入。
	systemdRuns = nil
	if err := deploy.RunRemote(context.Background(), runner, target, "", "", []string{"doctor"}, &out, &errOut); err != nil {
		t.Fatalf("RunRemote doctor: %v", err)
	}
	if err := deploy.RunRemote(context.Background(), runner, target, "", "", []string{"config", "check", "--config", "config/custom.yaml"}, &out, &errOut); err != nil {
		t.Fatalf("RunRemote custom config: %v", err)
	}
	if len(systemdRuns) != 2 {
		t.Fatalf("expected 2 RunRemote calls, got %d", len(systemdRuns))
	}
	if !strings.Contains(systemdRuns[0], wantFlag) {
		t.Errorf("RunRemote doctor missing %s: %s", wantFlag, systemdRuns[0])
	}
	if strings.Contains(systemdRuns[1], wantFlag) {
		t.Errorf("RunRemote with explicit --config should not duplicate flag: %s", systemdRuns[1])
	}

	// 4. 本地缺少声明的配置文件时，在触发 Build 之前立即失败。
	missingTarget, err := deploy.Parse([]byte(`
app: blog
arch: amd64
environments:
  production:
    config_files:
      - config/app.yaml
      - config/missing.yaml
    hosts: [web-1]
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}
	buildCalled := false
	_, err = deploy.Deploy(context.Background(), runner, missingTarget, deploy.ReleaseOptions{
		WorkDir: dir,
		Build: func(context.Context, string, string) error {
			buildCalled = true
			return nil
		},
	}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "config/missing.yaml") {
		t.Fatalf("expected missing config file error, got %v", err)
	}
	if buildCalled {
		t.Errorf("Build should not be called when a config_files entry is missing")
	}
}
