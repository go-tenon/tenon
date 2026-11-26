package deploy_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-tenon/tenon/cmd/tenon/internal/deploy"
)

// callRecord 记录一次远端命令调用。
type callRecord struct {
	Host   string
	Script string
	Stdin  []byte
}

// fakeRunner 是用于测试部署编排的内存 Runner，内置 flock 会话锁模拟。
type fakeRunner struct {
	mu       sync.Mutex
	calls    []callRecord
	lockBusy bool
	onRun    func(host deploy.Host, script string, stdin []byte) (stdout, stderr string, err error)
}

// Run 模拟远端脚本执行：识别 flock 锁脚本并在释放 stdin 前保持阻塞，其余命令交给 onRun 处理。
func (f *fakeRunner) Run(ctx context.Context, host deploy.Host, script string, stdin io.Reader, stdout, stderr io.Writer) error {
	if strings.Contains(script, "flock -n 9") {
		f.mu.Lock()
		busy := f.lockBusy
		f.calls = append(f.calls, callRecord{Host: host.Addr, Script: script})
		f.mu.Unlock()
		if busy {
			if stderr != nil {
				_, _ = io.WriteString(stderr, "another deploy is in progress (v1.0.0)\n")
			}
			return errors.New("exit status 1")
		}
		if stdout != nil {
			_, _ = io.WriteString(stdout, "LOCKED\n")
		}
		if stdin != nil {
			_, _ = io.Copy(io.Discard, stdin)
		} else {
			<-ctx.Done()
		}
		return nil
	}
	var in []byte
	if stdin != nil {
		in, _ = io.ReadAll(stdin)
	}
	f.mu.Lock()
	f.calls = append(f.calls, callRecord{Host: host.Addr, Script: script, Stdin: in})
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

// writeFiles 在根目录 root 下批量写入测试文件。
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadTargetsMultiInstanceDefaultsAndGuardrails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	deployDir := filepath.Join(root, "deploy")
	writeFiles(t, root, map[string]string{
		"config/app.yaml": "http:\n  addr: ':8080'\n",
		"deploy/app.yaml": `app: omirouter
repo: acme/omirouter
keep_releases: 7
defaults:
  user: ecs-user
  port: 2222
`,
		"deploy/targets/prod/target.yaml": `hosts: [10.0.0.1]
`,
		"deploy/targets/prod/config.yaml": "logs:\n  level: info\n",
		"deploy/targets/partner-acme/target.yaml": `instance: acme-router
user: acme-ops
config_files:
  - ../../../config/app.yaml
  - ./config.yaml
hosts: [203.0.113.20]
`,
		"deploy/targets/partner-acme/config.yaml": "logs:\n  level: warn\n",
	})

	opts := deploy.LoadOptions{Dir: deployDir}

	// 1. 存在多个目标时，未显式指定 target 必须报错并列出全部可用目标。
	if _, err := deploy.Load(opts, ""); err == nil || !strings.Contains(err.Error(), "partner-acme") || !strings.Contains(err.Error(), "prod") {
		t.Fatalf("expected guardrail error for multiple targets, got %v", err)
	}

	// 2. 加载默认实例名的 prod：继承 defaults.user/port，自动发现同目录 config.yaml，env_file 默认非严格模式。
	prod, err := deploy.Load(opts, "prod")
	if err != nil {
		t.Fatalf("Load prod: %v", err)
	}
	if prod.App != "omirouter" || prod.Instance != "omirouter" || prod.User != "ecs-user" || prod.Port != 2222 || prod.KeepReleases != 7 {
		t.Fatalf("unexpected prod target: %+v", prod)
	}
	if prod.Dir != "/opt/omirouter" || prod.ServiceUser != "omirouter" || prod.EnvFileSpec() != "-/etc/omirouter/env" {
		t.Fatalf("unexpected prod paths: dir=%s user=%s env=%s", prod.Dir, prod.ServiceUser, prod.EnvFileSpec())
	}
	if !slices.Equal(prod.RemoteConfigFiles(), []string{"config/config.yaml"}) {
		t.Fatalf("prod auto config_files = %v, want [config/config.yaml]", prod.RemoteConfigFiles())
	}

	// 3. 加载自定义实例名的 partner-acme：app 仍为 omirouter，远端全部资源按 acme-router 隔离，支持跨目录相对 config_files。
	partner, err := deploy.Load(opts, "partner-acme")
	if err != nil {
		t.Fatalf("Load partner-acme: %v", err)
	}
	if partner.App != "omirouter" || partner.Name != "partner-acme" || partner.Instance != "acme-router" || partner.User != "acme-ops" {
		t.Fatalf("unexpected partner target: %+v", partner)
	}
	if partner.Dir != "/opt/acme-router" || partner.ServiceUser != "acme-router" || partner.EnvFile != "/etc/acme-router/env" {
		t.Fatalf("partner isolation paths = dir:%s user:%s env:%s", partner.Dir, partner.ServiceUser, partner.EnvFile)
	}
	if !slices.Equal(partner.RemoteConfigFiles(), []string{"config/app.yaml", "config/config.yaml"}) {
		t.Fatalf("partner config_files = %v", partner.RemoteConfigFiles())
	}

	// 4. LoadTopology 汇总所有目标。
	topo, err := deploy.LoadTopology(opts)
	if err != nil || len(topo.Targets) != 2 || topo.Targets["partner-acme"].Instance != "acme-router" {
		t.Fatalf("LoadTopology: topo=%+v err=%v", topo, err)
	}

	// 5. LoadMany(--all) 按名称排序返回全部目标。
	all, err := deploy.LoadMany(opts, nil, true)
	if err != nil || len(all) != 2 || all[0].Name != "partner-acme" || all[1].Name != "prod" {
		t.Fatalf("LoadMany all: %+v err=%v", all, err)
	}
}

func TestBuildTargetMultiRoleAndValidationErrors(t *testing.T) {
	t.Parallel()
	af := deploy.AppFile{App: "blog"}
	multi, err := deploy.BuildTarget(af, "", "blog", "prod", deploy.TargetFile{
		Roles: map[string]deploy.Role{
			"http": {
				Hosts:       []string{"10.0.1.11", "10.0.1.12", "10.0.1.13"},
				MaxParallel: 2,
				StopTimeout: "30s",
			},
			"worker": {
				Hosts:       []string{"10.0.1.21", "10.0.1.22"},
				StopTimeout: "120s",
				Env:         map[string]string{"GOMAXPROCS": "4"},
			},
			"scheduler": {
				Hosts: []string{"10.0.1.21"},
			},
		},
	}, "")
	if err != nil {
		t.Fatalf("BuildTarget multi: %v", err)
	}
	if multi.MigrateHost().Addr != "10.0.1.21" {
		t.Fatalf("migrate host = %s, want 10.0.1.21", multi.MigrateHost().Addr)
	}
	h21, ok := multi.HostByAddr("10.0.1.21")
	if !ok || !slices.Equal(h21.Parts, []string{"scheduler", "worker"}) || !slices.Equal(h21.Roles, []string{"scheduler", "worker"}) {
		t.Fatalf("merged host 10.0.1.21 = %+v", h21)
	}
	if di := h21.DropIns["worker"]; di.StopTimeout != "120s" || di.Env["GOMAXPROCS"] != "4" {
		t.Fatalf("worker drop-in = %+v", di)
	}
	waves := multi.RolloutWaves()
	if len(waves) != 4 || waves[0].Name != "backend-canary" || waves[2].Name != "http-canary" || waves[3].MaxParallel != 2 {
		t.Fatalf("waves = %+v", waves)
	}

	// 非法配置均应报错。
	for name, tc := range map[string]struct {
		af deploy.AppFile
		tf deploy.TargetFile
	}{
		"bad repo":         {deploy.AppFile{App: "blog", Repo: "invalid"}, deploy.TargetFile{Hosts: []string{"a"}}},
		"bad instance":     {af, deploy.TargetFile{Instance: "Bad_Name", Hosts: []string{"a"}}},
		"both hosts roles": {af, deploy.TargetFile{Hosts: []string{"a"}, Roles: map[string]deploy.Role{"http": {Hosts: []string{"a"}}}}},
		"bad migrate_host": {af, deploy.TargetFile{MigrateHost: "nope", Hosts: []string{"a"}}},
		"mix all and http": {af, deploy.TargetFile{Roles: map[string]deploy.Role{"all": {Hosts: []string{"a"}}, "http": {Hosts: []string{"a"}}}}},
		"bad env key":      {af, deploy.TargetFile{Roles: map[string]deploy.Role{"worker": {Hosts: []string{"a"}, Env: map[string]string{"bad-key": "1"}}}}},
		"dup remote cfg":   {af, deploy.TargetFile{Hosts: []string{"a"}, ConfigFiles: []string{"../a/config.yaml", "./config.yaml"}}},
	} {
		if _, err := deploy.BuildTarget(tc.af, "", "blog", "prod", tc.tf, ""); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseGitHubRepoAndReleaseID(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"https://github.com/go-tenon/tenon.git":   "go-tenon/tenon",
		"https://github.com/go-tenon/tenon":       "go-tenon/tenon",
		"git@github.com:go-tenon/tenon.git":       "go-tenon/tenon",
		"ssh://git@github.com/go-tenon/tenon.git": "go-tenon/tenon",
		"https://gitlab.com/group/project.git":    "",
		"https://github.com/only-owner":           "",
	} {
		if got := deploy.ParseGitHubRepo(url); got != want {
			t.Errorf("ParseGitHubRepo(%q) = %q, want %q", url, got, want)
		}
	}
	if id, ok := deploy.ParseReleaseID("0042"); !ok || id != 42 {
		t.Errorf("ParseReleaseID(0042) = %d, %v", id, ok)
	}
	if id, ok := deploy.ParseReleaseID("#7"); !ok || id != 7 {
		t.Errorf("ParseReleaseID(#7) = %d, %v", id, ok)
	}
	if _, ok := deploy.ParseReleaseID("v1.2.0"); ok {
		t.Errorf("ParseReleaseID(v1.2.0) should be false")
	}
}

func TestRenderUnitDropInAndWrapperWithCustomInstance(t *testing.T) {
	t.Parallel()
	target, err := deploy.BuildTarget(deploy.AppFile{App: "omirouter"}, "", "omirouter", "partner-acme", deploy.TargetFile{
		Instance:    "acme-router",
		ConfigFiles: []string{"config.yaml"},
		Hosts:       []string{"10.0.0.1"},
	}, "/tmp/acme")
	if err != nil {
		t.Fatal(err)
	}
	unit := deploy.RenderUnit(target)
	for _, want := range []string{
		"Description=acme-router %i",
		"Type=notify-reload",
		"RuntimeDirectory=acme-router/%i",
		"PIDFile=/run/acme-router/%i/daemon.pid",
		"User=acme-router",
		"WorkingDirectory=/opt/acme-router",
		"EnvironmentFile=-/etc/acme-router/env",
		"ExecStart=/opt/acme-router/bin/acme-router --config config/config.yaml serve %i",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "omirouter") {
		t.Errorf("custom instance unit leaked product app name:\n%s", unit)
	}
	wrapper := deploy.RenderWrapper(target)
	for _, want := range []string{
		"'Environment=APP_CONFIG=config/config.yaml'",
		"'EnvironmentFile=-/etc/acme-router/env'",
		"'/opt/acme-router/bin/acme-router'",
	} {
		if !strings.Contains(wrapper, want) {
			t.Errorf("wrapper missing %q:\n%s", want, wrapper)
		}
	}

	// 显式指定 env_file 时使用严格模式（不带前导 -）。
	strict, err := deploy.BuildTarget(deploy.AppFile{App: "blog"}, "", "blog", "prod", deploy.TargetFile{
		EnvFile: "/etc/blog/env",
		Hosts:   []string{"10.0.0.1"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := deploy.RenderUnit(strict); !strings.Contains(got, "EnvironmentFile=/etc/blog/env\n") {
		t.Errorf("strict env_file unit:\n%s", got)
	}

	dropIn := deploy.RenderDropIn(deploy.PartDropIn{StopTimeout: "120s", Env: map[string]string{"GOMAXPROCS": "4"}})
	if !strings.Contains(dropIn, "TimeoutStopSec=120s") || !strings.Contains(dropIn, `Environment="GOMAXPROCS=4"`) {
		t.Errorf("unexpected drop-in:\n%s", dropIn)
	}
}

func TestSetupCreatesArtifactsAndDropInsByInstance(t *testing.T) {
	t.Parallel()
	target, err := deploy.BuildTarget(deploy.AppFile{App: "omirouter"}, "", "omirouter", "partner-acme", deploy.TargetFile{
		Instance: "acme-router",
		EnvFile:  "/etc/acme-router/env",
		Roles: map[string]deploy.Role{
			"worker": {Hosts: []string{"10.0.0.1"}, StopTimeout: "120s"},
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	var out bytes.Buffer
	report, err := deploy.Setup(context.Background(), runner, target, deploy.SetupOptions{HasSSR: true}, &out)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if report.Instance != "acme-router" || report.UnitPath != "/etc/systemd/system/acme-router@.service" || report.WrapperPath != "/usr/local/bin/acme-router" {
		t.Fatalf("report = %+v", report)
	}
	var hasArtifactsDir, wroteDropIn, wroteWrapper, createdEnv bool
	for _, c := range runner.calls {
		if strings.Contains(c.Script, "'/opt/acme-router/artifacts'") {
			hasArtifactsDir = true
		}
		if strings.Contains(c.Script, "acme-router@worker.service.d/override.conf") && strings.Contains(string(c.Stdin), "TimeoutStopSec=120s") {
			wroteDropIn = true
		}
		if strings.Contains(c.Script, "/usr/local/bin/acme-router") {
			wroteWrapper = true
		}
		if strings.Contains(c.Script, "'/etc/acme-router/env'") {
			createdEnv = true
		}
	}
	if !hasArtifactsDir || !wroteDropIn || !wroteWrapper || !createdEnv {
		t.Errorf("artifacts=%v dropIn=%v wrapper=%v env=%v", hasArtifactsDir, wroteDropIn, wroteWrapper, createdEnv)
	}
}

func TestDeployManyBuildsOnceAndFailsFast(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"deploy/app.yaml":                         "app: omirouter\narch: amd64\n",
		"deploy/targets/prod/target.yaml":         "hosts: [prod-1]\n",
		"deploy/targets/prod/config.yaml":         "logs:\n  level: info\n",
		"deploy/targets/partner-acme/target.yaml": "instance: acme-router\nhosts: [acme-1]\n",
		"deploy/targets/partner-acme/config.yaml": "logs:\n  level: warn\n",
	})
	targets, err := deploy.LoadMany(deploy.LoadOptions{Dir: filepath.Join(root, "deploy")}, []string{"prod", "partner-acme"}, false)
	if err != nil {
		t.Fatalf("LoadMany: %v", err)
	}

	buildCalls := 0
	var seenHosts []string
	var sawAcmeUnit bool
	factory := func(tgt deploy.Target) (deploy.Runner, func() error, error) {
		r := &fakeRunner{
			onRun: func(host deploy.Host, script string, _ []byte) (string, string, error) {
				if strings.Contains(script, "systemctl reload") {
					seenHosts = append(seenHosts, host.Addr)
				}
				if strings.Contains(script, "acme-router@all.service") {
					sawAcmeUnit = true
				}
				if strings.Contains(script, "--help") {
					return "no\n", "", nil
				}
				return "", "", nil
			},
		}
		return r, nil, nil
	}

	var out, errOut bytes.Buffer
	batch, err := deploy.DeployMany(context.Background(), targets, factory, deploy.ReleaseOptions{
		Version: "v1.5.0",
		WorkDir: root,
		Build: func(_ context.Context, arch, outPath string, _ deploy.GitBuildInfo) error {
			buildCalls++
			return os.WriteFile(outPath, []byte("built-"+arch), 0o755)
		},
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("DeployMany: %v\nout=%s\nerr=%s", err, out.String(), errOut.String())
	}
	if buildCalls != 1 {
		t.Fatalf("buildCalls = %d, want 1 (build once, deploy many)", buildCalls)
	}
	if len(batch.Targets) != 2 || batch.Targets[0].BinSHA256 != batch.Targets[1].BinSHA256 || batch.Targets[1].Instance != "acme-router" {
		t.Fatalf("batch = %+v", batch)
	}
	if !slices.Equal(seenHosts, []string{"prod-1", "acme-1"}) || !sawAcmeUnit {
		t.Errorf("seenHosts=%v sawAcmeUnit=%v", seenHosts, sawAcmeUnit)
	}

	// 验证失败熔断：第一个目标失败时，绝不触碰第二个目标。
	touchedSecond := false
	failFactory := func(tgt deploy.Target) (deploy.Runner, func() error, error) {
		r := &fakeRunner{
			onRun: func(host deploy.Host, script string, _ []byte) (string, string, error) {
				if tgt.Name == "partner-acme" && strings.Contains(script, "systemctl reload") {
					touchedSecond = true
				}
				if tgt.Name == "prod" && strings.Contains(script, "systemctl reload") {
					return "", "boom", errors.New("exit status 1")
				}
				if strings.Contains(script, "--help") {
					return "no\n", "", nil
				}
				return "", "", nil
			},
		}
		return r, nil, nil
	}
	out.Reset()
	errOut.Reset()
	if _, err := deploy.DeployMany(context.Background(), targets, failFactory, deploy.ReleaseOptions{
		Version: "v1.5.0",
		WorkDir: root,
		Build: func(_ context.Context, _ string, outPath string, _ deploy.GitBuildInfo) error {
			return os.WriteFile(outPath, []byte("built"), 0o755)
		},
	}, &out, &errOut); err == nil {
		t.Fatalf("expected DeployMany failure on first target")
	}
	if touchedSecond {
		t.Errorf("second target should not be switched after first target failed")
	}
}

func TestDeployMultiRoleContentDedupConfigCheckAllAndPruneNumeric(t *testing.T) {
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
	binBytes := []byte("fake-elf-binary-payload")
	binPath := filepath.Join(dir, "blog-bin")
	if err := os.WriteFile(binPath, binBytes, 0o755); err != nil {
		t.Fatal(err)
	}

	target, err := deploy.BuildTarget(deploy.AppFile{App: "blog", KeepReleases: 2}, dir, "blog", "prod", deploy.TargetFile{
		ConfigDir: cfgDir,
		Roles: map[string]deploy.Role{
			"http":   {Hosts: []string{"web-1", "web-2"}},
			"worker": {Hosts: []string{"worker-1"}},
		},
	}, dir)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var uploadedTar []byte
	var uploadedBinHosts []string
	var configCheckHosts []string
	var migrateCount int
	var switchOrder []string
	var disabledUnits []string
	var wroteManifests int

	runner := &fakeRunner{
		onRun: func(host deploy.Host, script string, stdin []byte) (string, string, error) {
			switch {
			case script == "uname -m":
				return "x86_64\n", "", nil
			case strings.Contains(script, "if test -x '/opt/blog/artifacts/"):
				if host.Addr == "worker-1" {
					return "hit\n", "", nil
				}
				return "miss\n", "", nil
			case strings.Contains(script, "gzip -dc >"):
				gz, err := gzip.NewReader(bytes.NewReader(stdin))
				if err != nil {
					return "", err.Error(), err
				}
				got, err := io.ReadAll(gz)
				_ = gz.Close()
				if err != nil || !bytes.Equal(got, binBytes) {
					return "", "corrupt stream", errors.New("corrupt stream")
				}
				mu.Lock()
				uploadedBinHosts = append(uploadedBinHosts, host.Addr)
				mu.Unlock()
			case strings.Contains(script, "tar -xzf"):
				mu.Lock()
				if uploadedTar == nil {
					uploadedTar = stdin
				}
				mu.Unlock()
			case strings.Contains(script, "manifest.json") && strings.Contains(script, "sudo install -m 644"):
				mu.Lock()
				wroteManifests++
				mu.Unlock()
			case strings.Contains(script, "'config' 'check'"):
				mu.Lock()
				configCheckHosts = append(configCheckHosts, host.Addr)
				mu.Unlock()
			case strings.Contains(script, "--help"):
				return "yes\n", "", nil
			case strings.Contains(script, "'migrate' 'up'"):
				mu.Lock()
				migrateCount++
				mu.Unlock()
			case strings.Contains(script, "systemctl list-units"):
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
				return "0009\n0010\n", "", nil
			}
			return "", "", nil
		},
	}

	var out, errOut bytes.Buffer
	report, err := deploy.Deploy(context.Background(), runner, target, deploy.ReleaseOptions{
		Binary:  binPath,
		Version: "v1.10.0",
		Now:     func() time.Time { return time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC) },
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("Deploy: %v\nout=%s\nerr=%s", err, out.String(), errOut.String())
	}
	if report.ReleaseID != 11 || report.Version != "v1.10.0" || report.UploadedHosts != 2 {
		t.Fatalf("report = %+v", report)
	}
	slices.Sort(uploadedBinHosts)
	if !slices.Equal(uploadedBinHosts, []string{"web-1", "web-2"}) {
		t.Errorf("uploadedBinHosts = %v, want [web-1 web-2]", uploadedBinHosts)
	}
	slices.Sort(configCheckHosts)
	if !slices.Equal(configCheckHosts, []string{"web-1", "web-2", "worker-1"}) {
		t.Errorf("configCheckHosts = %v, want all 3 hosts", configCheckHosts)
	}
	if migrateCount != 1 || !report.Migrated || wroteManifests != 3 {
		t.Errorf("migrateCount=%d migrated=%v wroteManifests=%d", migrateCount, report.Migrated, wroteManifests)
	}
	if !slices.Equal(switchOrder, []string{"worker-1", "web-1", "web-2"}) {
		t.Errorf("switchOrder = %v, want [worker-1 web-1 web-2]", switchOrder)
	}
	if len(disabledUnits) != 1 || !strings.Contains(disabledUnits[0], "blog@all.service") {
		t.Errorf("disabledUnits = %v", disabledUnits)
	}
	gz, err := gzip.NewReader(bytes.NewReader(uploadedTar))
	if err != nil {
		t.Fatal(err)
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
		t.Errorf("config archive = %v, want [app.yaml]", names)
	}
}

func TestDeployNoOpAndUnitRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"deploy/app.yaml":                 "app: blog\narch: amd64\n",
		"deploy/targets/prod/target.yaml": "hosts: [web-1]\n",
		"deploy/targets/prod/config.yaml": "http:\n  addr: ':8080'\n",
	})
	binPath := filepath.Join(dir, "blog-bin")
	binBytes := []byte("same-binary")
	if err := os.WriteFile(binPath, binBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	binSumArr := sha256.Sum256(binBytes)
	binSum := hex.EncodeToString(binSumArr[:])

	target, err := deploy.Load(deploy.LoadOptions{Dir: filepath.Join(dir, "deploy")}, "prod")
	if err != nil {
		t.Fatal(err)
	}

	var capturedManifest deploy.Manifest
	runner1 := &fakeRunner{
		onRun: func(_ deploy.Host, script string, stdin []byte) (string, string, error) {
			if strings.Contains(script, "manifest.json") && strings.Contains(script, "sudo install -m 644") {
				_ = json.Unmarshal(stdin, &capturedManifest)
			}
			if strings.Contains(script, "--help") {
				return "no\n", "", nil
			}
			return "", "", nil
		},
	}
	var out, errOut bytes.Buffer
	rep1, err := deploy.Deploy(context.Background(), runner1, target, deploy.ReleaseOptions{
		Binary:  binPath,
		Version: "v1.0.0",
		WorkDir: dir,
	}, &out, &errOut)
	if err != nil || rep1.Skipped {
		t.Fatalf("first deploy: rep=%+v err=%v", rep1, err)
	}

	manifestJSON, _ := json.Marshal(deploy.Manifest{
		ReleaseID:    1,
		Version:      "v1.0.0",
		BinSHA256:    binSum,
		ConfigSHA256: capturedManifest.ConfigSHA256,
		Kind:         deploy.ReleaseKindFull,
	})
	runner2 := &fakeRunner{
		onRun: func(_ deploy.Host, script string, _ []byte) (string, string, error) {
			if strings.Contains(script, "/opt/blog/current/manifest.json") {
				return string(manifestJSON) + "\n", "", nil
			}
			return "", "", nil
		},
	}
	out.Reset()
	rep2, err := deploy.Deploy(context.Background(), runner2, target, deploy.ReleaseOptions{
		Binary:  binPath,
		Version: "v1.0.0",
		WorkDir: dir,
	}, &out, &errOut)
	if err != nil || !rep2.Skipped {
		t.Fatalf("expected skipped no-op deploy, got rep=%+v err=%v", rep2, err)
	}

	var restarted bool
	runner3 := &fakeRunner{
		onRun: func(_ deploy.Host, script string, _ []byte) (string, string, error) {
			switch {
			case strings.Contains(script, "/opt/blog/current/manifest.json"):
				return string(manifestJSON) + "\n", "", nil
			case strings.Contains(script, "/etc/systemd/system/blog@.service") && strings.Contains(script, "echo diff"):
				return "diff\n", "", nil
			case strings.Contains(script, "/etc/systemd/system/blog@.service") && strings.Contains(script, "echo updated"):
				return "updated\n", "", nil
			case strings.Contains(script, "if test -x '/opt/blog/artifacts/"):
				return "hit\n", "", nil
			case strings.Contains(script, "systemctl restart"):
				restarted = true
			case strings.Contains(script, "--help"):
				return "no\n", "", nil
			}
			return "", "", nil
		},
	}
	out.Reset()
	rep3, err := deploy.Deploy(context.Background(), runner3, target, deploy.ReleaseOptions{
		Binary:  binPath,
		Version: "v1.0.0",
		WorkDir: dir,
	}, &out, &errOut)
	if err != nil || rep3.Skipped || !restarted || rep3.Hosts[0].Action != "restart" {
		t.Fatalf("expected restart on unit change: rep=%+v restarted=%v err=%v", rep3, restarted, err)
	}
}

func TestDeployConfigOnlySkipsBuildUploadAndMigrate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"deploy/app.yaml":                 "app: blog\n",
		"deploy/targets/prod/target.yaml": "hosts: [web-1]\n",
		"deploy/targets/prod/config.yaml": "logs:\n  level: debug\n",
	})
	target, err := deploy.Load(deploy.LoadOptions{Dir: filepath.Join(dir, "deploy")}, "prod")
	if err != nil {
		t.Fatal(err)
	}

	curManifest, _ := json.Marshal(deploy.Manifest{
		ReleaseID:    5,
		Version:      "v1.2.0",
		Commit:       "abc1234",
		BinSHA256:    "deadbeef",
		ConfigSHA256: "oldcfg",
		Kind:         deploy.ReleaseKindFull,
	})
	var uploadedBin, ranMigrate, ranConfigCheck bool
	var wroteManifest deploy.Manifest
	runner := &fakeRunner{
		onRun: func(_ deploy.Host, script string, stdin []byte) (string, string, error) {
			switch {
			case strings.Contains(script, "/opt/blog/current/manifest.json"):
				return string(curManifest) + "\n", "", nil
			case strings.HasPrefix(script, "ls -1 "):
				return "0005\n", "", nil
			case strings.Contains(script, "if test -x '/opt/blog/artifacts/deadbeef'"):
				return "hit\n", "", nil
			case strings.Contains(script, "gzip -dc >"):
				uploadedBin = true
			case strings.Contains(script, "'migrate' 'up'"):
				ranMigrate = true
			case strings.Contains(script, "'config' 'check'"):
				ranConfigCheck = true
			case strings.Contains(script, "manifest.json") && strings.Contains(script, "sudo install -m 644"):
				_ = json.Unmarshal(stdin, &wroteManifest)
			case strings.Contains(script, "--help"):
				return "no\n", "", nil
			}
			return "", "", nil
		},
	}

	var out, errOut bytes.Buffer
	rep, err := deploy.DeployConfig(context.Background(), runner, target, deploy.ConfigReleaseOptions{
		WorkDir: dir,
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("DeployConfig: %v\nout=%s\nerr=%s", err, out.String(), errOut.String())
	}
	if rep.ReleaseID != 6 || rep.Kind != deploy.ReleaseKindConfig || rep.Version != "v1.2.0" || rep.BinSHA256 != "deadbeef" {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if uploadedBin || ranMigrate || !ranConfigCheck {
		t.Errorf("uploadedBin=%v ranMigrate=%v ranConfigCheck=%v", uploadedBin, ranMigrate, ranConfigCheck)
	}
	if wroteManifest.ReleaseID != 6 || wroteManifest.Kind != deploy.ReleaseKindConfig || wroteManifest.FromRelease != 5 {
		t.Errorf("manifest = %+v", wroteManifest)
	}
}

func TestDeployFromGitHubTagWithCustomInstance(t *testing.T) {
	t.Parallel()
	payload := []byte("compiled-linux-amd64-binary")
	sumArr := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sumArr[:])

	// Release 资产名始终使用 app（omirouter），即使目标实例名为 acme-router。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/omirouter/releases/download/v2.0.0/omirouter-linux-amd64":
			_, _ = w.Write(payload)
		case "/acme/omirouter/releases/download/v2.0.0/checksums.txt":
			_, _ = fmt.Fprintf(w, "%s  omirouter-linux-amd64\n", sumHex)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	target, err := deploy.BuildTarget(deploy.AppFile{
		App:  "omirouter",
		Repo: "acme/omirouter",
		Arch: "amd64",
	}, dir, "omirouter", "partner-acme", deploy.TargetFile{
		Instance: "acme-router",
		Hosts:    []string{"web-1"},
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	var linkedInstanceBin bool
	runner := &fakeRunner{
		onRun: func(_ deploy.Host, script string, _ []byte) (string, string, error) {
			if strings.Contains(script, "/opt/acme-router/bin/acme-router") {
				linkedInstanceBin = true
			}
			if strings.Contains(script, "--help") {
				return "no\n", "", nil
			}
			return "", "", nil
		},
	}
	var out, errOut bytes.Buffer
	rep, err := deploy.Deploy(context.Background(), runner, target, deploy.ReleaseOptions{
		Tag:            "v2.0.0",
		WorkDir:        dir,
		ReleaseBaseURL: srv.URL,
		HTTPClient:     srv.Client(),
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("Deploy --tag: %v", err)
	}
	if rep.Version != "v2.0.0" || rep.BinSHA256 != sumHex || rep.Instance != "acme-router" || !linkedInstanceBin {
		t.Fatalf("report = %+v linkedInstanceBin=%v", rep, linkedInstanceBin)
	}
}

func TestDeployAutoRollbackAndLockBusy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "blog-bin")
	if err := os.WriteFile(binPath, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	target, err := deploy.BuildTarget(deploy.AppFile{App: "blog", Arch: "amd64"}, dir, "blog", "prod", deploy.TargetFile{
		Roles: map[string]deploy.Role{
			"worker": {Hosts: []string{"worker-1"}},
			"http":   {Hosts: []string{"web-1"}},
		},
	}, dir)
	if err != nil {
		t.Fatal(err)
	}

	busyRunner := &fakeRunner{lockBusy: true}
	var out, errOut bytes.Buffer
	if _, err := deploy.Deploy(context.Background(), busyRunner, target, deploy.ReleaseOptions{Binary: binPath, Version: "v2"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "another deploy is in progress") {
		t.Fatalf("expected lock busy error, got %v", err)
	}

	cur1, _ := json.Marshal(deploy.Manifest{ReleaseID: 1, Version: "v1", BinSHA256: "old"})
	var rolledBackWorker bool
	runner := &fakeRunner{
		onRun: func(host deploy.Host, script string, _ []byte) (string, string, error) {
			switch {
			case strings.Contains(script, "/opt/blog/current/manifest.json"):
				return string(cur1) + "\n", "", nil
			case strings.HasPrefix(script, "ls -1 "):
				return "0001\n", "", nil
			case strings.Contains(script, "--help"):
				return "no\n", "", nil
			case host.Addr == "web-1" && strings.Contains(script, "systemctl reload"):
				return "", "upgrade failed: port error", errors.New("exit status 1")
			case host.Addr == "worker-1" && strings.Contains(script, "/opt/blog/releases/0001/blog"):
				rolledBackWorker = true
			}
			return "", "", nil
		},
	}
	out.Reset()
	errOut.Reset()
	_, err = deploy.Deploy(context.Background(), runner, target, deploy.ReleaseOptions{
		Binary:       binPath,
		Version:      "v2",
		AutoRollback: true,
	}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "upgrade failed") {
		t.Fatalf("expected upgrade failed, got %v", err)
	}
	if !rolledBackWorker {
		t.Errorf("expected worker-1 to be automatically rolled back to #1")
	}
}

func TestRollbackByHistoryAndByTagAndListReleases(t *testing.T) {
	t.Parallel()
	target, err := deploy.BuildTarget(deploy.AppFile{App: "blog"}, "", "blog", "prod", deploy.TargetFile{
		Hosts: []string{"web-1"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	m1, _ := json.Marshal(deploy.Manifest{ReleaseID: 1, Version: "v1.0.0", BinSHA256: "aaa", Kind: deploy.ReleaseKindFull})
	m2, _ := json.Marshal(deploy.Manifest{ReleaseID: 2, Version: "v1.1.0", BinSHA256: "bbb", Kind: deploy.ReleaseKindFull})
	m3, _ := json.Marshal(deploy.Manifest{ReleaseID: 3, Version: "v1.1.0", BinSHA256: "bbb", ConfigSHA256: "ccc", Kind: deploy.ReleaseKindConfig})
	history := `{"release_id":1,"version":"v1.0.0","kind":"full"}
{"release_id":3,"version":"v1.1.0","kind":"config"}
`
	var configChecked bool
	runner := &fakeRunner{
		onRun: func(_ deploy.Host, script string, _ []byte) (string, string, error) {
			switch {
			case strings.Contains(script, "---current"):
				return fmt.Sprintf("---current\n%s\n---manifest\n%s\n---manifest\n%s\n---manifest\n%s\n", m3, m1, m2, m3), "", nil
			case strings.Contains(script, "history.jsonl"):
				return history, "", nil
			case strings.Contains(script, "test -x"):
				return "cfg\n", "", nil
			case strings.Contains(script, "'config' 'check'"):
				configChecked = true
			case strings.Contains(script, "--help"):
				return "no\n", "", nil
			}
			return "", "", nil
		},
	}

	listRep, err := deploy.ListReleases(context.Background(), runner, target)
	if err != nil || listRep.CurrentRelease != 3 || len(listRep.Releases) != 3 || listRep.Releases[0].ReleaseID != 3 || listRep.Releases[2].ReleaseID != 1 {
		t.Fatalf("ListReleases: rep=%+v err=%v", listRep, err)
	}
	var table bytes.Buffer
	deploy.FormatReleases(&table, listRep)
	if !strings.Contains(table.String(), "#3") || !strings.Contains(table.String(), "v1.1.0") {
		t.Errorf("FormatReleases:\n%s", table.String())
	}

	var out, errOut bytes.Buffer
	rb1, err := deploy.Rollback(context.Background(), runner, target, "", &out, &errOut)
	if err != nil || rb1.ReleaseID != 1 || rb1.Version != "v1.0.0" || !configChecked {
		t.Fatalf("Rollback history: rep=%+v configChecked=%v err=%v", rb1, configChecked, err)
	}

	rbTag, err := deploy.Rollback(context.Background(), runner, target, "v1.1.0", &out, &errOut)
	if err != nil || rbTag.ReleaseID != 3 || rbTag.Version != "v1.1.0" {
		t.Fatalf("Rollback by tag: rep=%+v err=%v", rbTag, err)
	}

	rbNum, err := deploy.Rollback(context.Background(), runner, target, "#2", &out, &errOut)
	if err != nil || rbNum.ReleaseID != 2 || rbNum.Version != "v1.1.0" {
		t.Fatalf("Rollback by num: rep=%+v err=%v", rbNum, err)
	}
}

func TestStatusLogsAndMaintenanceWithCustomInstance(t *testing.T) {
	t.Parallel()
	target, err := deploy.BuildTarget(deploy.AppFile{App: "omirouter"}, "", "omirouter", "partner-acme", deploy.TargetFile{
		Instance:        "acme-router",
		MaintenanceFile: "/etc/nginx/acme-router.maintenance",
		Roles: map[string]deploy.Role{
			"http":   {Hosts: []string{"web-1", "web-2"}},
			"worker": {Hosts: []string{"worker-1"}},
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	var sawInstanceJournal atomic.Bool
	runner := &fakeRunner{
		onRun: func(host deploy.Host, script string, _ []byte) (string, string, error) {
			switch {
			case strings.Contains(script, "systemctl show -p ActiveState"):
				return "---manifest\n{\"release_id\":12,\"version\":\"v1.2.0\",\"config_sha256\":\"abcdef012345\"}\n---unit=acme-router@http.service\nActiveState=active\nSubState=running\nStatusText=\nActiveEnterTimestamp=Sun 2026-11-22\n", "", nil
			case strings.Contains(script, "journalctl"):
				if strings.Contains(script, "'acme-router@*'") {
					sawInstanceJournal.Store(true)
				}
				return "INF ok on " + host.Addr + "\nERR boom on " + host.Addr + "\n", "", nil
			}
			return "", "", nil
		},
	}

	st, err := deploy.CollectStatus(context.Background(), runner, target, "http", "")
	if err != nil || len(st.Hosts) != 2 || st.Instance != "acme-router" || st.Hosts[0].ReleaseID != 12 {
		t.Fatalf("CollectStatus: st=%+v err=%v", st, err)
	}
	var table bytes.Buffer
	deploy.FormatStatus(&table, st)
	if !strings.Contains(table.String(), "实例: acme-router, 产物: omirouter") || !strings.Contains(table.String(), "#12") {
		t.Errorf("status table:\n%s", table.String())
	}

	var logBuf, errBuf bytes.Buffer
	if err := deploy.Logs(context.Background(), runner, target, deploy.LogsOptions{Role: "http", Contains: "ERR", Since: "15m"}, &logBuf, &errBuf); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if !sawInstanceJournal.Load() || !strings.Contains(logBuf.String(), "[web-1] ERR boom on web-1") || strings.Contains(logBuf.String(), "INF") {
		t.Errorf("logs (sawInstanceJournal=%v):\n%s", sawInstanceJournal.Load(), logBuf.String())
	}

	var out bytes.Buffer
	mrep, err := deploy.Maintenance(context.Background(), runner, target, "", "", true, &out)
	if err != nil || !slices.Equal(mrep.Hosts, []string{"web-1", "web-2"}) || mrep.Instance != "acme-router" {
		t.Fatalf("Maintenance: rep=%+v err=%v", mrep, err)
	}
}
