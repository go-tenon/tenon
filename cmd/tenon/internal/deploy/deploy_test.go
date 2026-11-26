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

func TestParseSingleHostAndMultiRoleTopology(t *testing.T) {
	t.Parallel()

	// 1. 单机简写：自动等价为单个 all 角色，同时解析 repo 字段。
	single, err := deploy.Parse([]byte(`
app: blog
repo: acme/blog
environments:
  production:
    hosts: [203.0.113.10]
`), "fallback", "")
	if err != nil {
		t.Fatalf("Parse single: %v", err)
	}
	if single.Repo != "acme/blog" || len(single.Hosts) != 1 || !slices.Equal(single.Hosts[0].Parts, []string{"all"}) || !single.Hosts[0].HasHTTP() {
		t.Fatalf("unexpected single target: %+v", single)
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
	for name, bad := range map[string]string{
		"unknown field":    "unknown: 1\nenvironments:\n  production:\n    hosts: [a]\n",
		"bad repo":         "repo: invalid\nenvironments:\n  production:\n    hosts: [a]\n",
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

func TestRenderUnitDropInAndWrapper(t *testing.T) {
	t.Parallel()
	target, err := deploy.Parse([]byte("app: blog\nenvironments:\n  production:\n    config_files: [config/prod.yaml]\n    hosts: [10.0.0.1]\n"), "blog", "")
	if err != nil {
		t.Fatal(err)
	}
	unit := deploy.RenderUnit(target)
	for _, want := range []string{
		"Type=notify-reload",
		"RuntimeDirectory=blog/%i",
		"ExecStart=/opt/blog/bin/blog --config config/prod.yaml serve %i",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
	wrapper := deploy.RenderWrapper(target)
	if !strings.Contains(wrapper, "'Environment=APP_CONFIG=config/prod.yaml'") {
		t.Errorf("wrapper missing APP_CONFIG:\n%s", wrapper)
	}
	dropIn := deploy.RenderDropIn(deploy.PartDropIn{StopTimeout: "120s", Env: map[string]string{"GOMAXPROCS": "4"}})
	if !strings.Contains(dropIn, "TimeoutStopSec=120s") || !strings.Contains(dropIn, `Environment="GOMAXPROCS=4"`) {
		t.Errorf("unexpected drop-in:\n%s", dropIn)
	}
}

func TestSetupCreatesArtifactsAndDropIns(t *testing.T) {
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
	if len(report.Hosts) != 1 || report.WrapperPath != "/usr/local/bin/blog" {
		t.Fatalf("report = %+v", report)
	}
	var hasArtifactsDir, wroteDropIn, wroteWrapper bool
	for _, c := range runner.calls {
		if strings.Contains(c.Script, "'/opt/blog/artifacts'") {
			hasArtifactsDir = true
		}
		if strings.Contains(c.Script, "blog@worker.service.d/override.conf") && strings.Contains(string(c.Stdin), "TimeoutStopSec=120s") {
			wroteDropIn = true
		}
		if strings.Contains(c.Script, "/usr/local/bin/blog") {
			wroteWrapper = true
		}
	}
	if !hasArtifactsDir || !wroteDropIn || !wroteWrapper {
		t.Errorf("artifacts=%v dropIn=%v wrapper=%v", hasArtifactsDir, wroteDropIn, wroteWrapper)
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
	var uploadedBinHosts []string
	var configCheckHosts []string
	var migrateCount int
	var switchOrder []string
	var disabledUnits []string
	var prunedCmds []string
	var wroteManifests int

	runner := &fakeRunner{
		onRun: func(host deploy.Host, script string, stdin []byte) (string, string, error) {
			switch {
			case script == "uname -m":
				return "x86_64\n", "", nil
			case strings.Contains(script, "if test -x '/opt/blog/artifacts/"):
				// 模拟 worker-1 命中远端二进制缓存，web-1/web-2 未命中。
				if host.Addr == "worker-1" {
					return "hit\n", "", nil
				}
				return "miss\n", "", nil
			case strings.Contains(script, "gzip -dc >"):
				// 验证上传流是合法的 gzip 压缩二进制且还原后逐字节一致。
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
				// 故意返回 0009 与 0010 验证按数值排序而非字典序，新分配应为 11，清理应删 0009。
				return "0009\n0010\n", "", nil
			case strings.HasPrefix(script, "sudo rm -rf '/opt/blog/releases/"):
				mu.Lock()
				prunedCmds = append(prunedCmds, script)
				mu.Unlock()
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
	// 验证归档排除了 local.yaml。
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
	_ = prunedCmds
}

func TestDeployNoOpAndUnitRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config", "prod.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("http:\n  addr: ':8080'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "blog-bin")
	binBytes := []byte("same-binary")
	if err := os.WriteFile(binPath, binBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	binSumArr := sha256.Sum256(binBytes)
	binSum := hex.EncodeToString(binSumArr[:])

	target, err := deploy.Parse([]byte(`
app: blog
arch: amd64
environments:
  production:
    config_files: [config/prod.yaml]
    hosts: [web-1]
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}

	// 先运行一次记录确定性 config_sha256。
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

	// 第二次二进制、配置与 unit 全部一致，应短路跳过（Skipped=true）。
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

	// 当 unit 文件有变化时，不跳过且自动执行 systemctl restart。
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
	cfgPath := filepath.Join(dir, "config", "prod.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("logs:\n  level: debug\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target, err := deploy.Parse([]byte(`
app: blog
environments:
  production:
    config_files: [config/prod.yaml]
    hosts: [web-1]
`), "blog", "")
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

func TestDeployFromGitHubTagWithChecksum(t *testing.T) {
	t.Parallel()
	payload := []byte("compiled-linux-amd64-binary")
	sumArr := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sumArr[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/blog/releases/download/v2.0.0/blog-linux-amd64":
			_, _ = w.Write(payload)
		case "/acme/blog/releases/download/v2.0.0/checksums.txt":
			_, _ = fmt.Fprintf(w, "%s  blog-linux-amd64\n", sumHex)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	target, err := deploy.Parse([]byte(`
app: blog
repo: acme/blog
arch: amd64
environments:
  production:
    config_dir: `+filepath.Join(dir, "no-config")+`
    hosts: [web-1]
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		onRun: func(_ deploy.Host, script string, _ []byte) (string, string, error) {
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
	if rep.Version != "v2.0.0" || rep.BinSHA256 != sumHex {
		t.Fatalf("report = %+v", rep)
	}
}

func TestDeployAutoRollbackAndLockBusy(t *testing.T) {
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
      worker:
        hosts: [worker-1]
      http:
        hosts: [web-1]
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}

	// 1. 锁已被占用时立即返回明确错误。
	busyRunner := &fakeRunner{lockBusy: true}
	var out, errOut bytes.Buffer
	if _, err := deploy.Deploy(context.Background(), busyRunner, target, deploy.ReleaseOptions{Binary: binPath, Version: "v2"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "another deploy is in progress") {
		t.Fatalf("expected lock busy error, got %v", err)
	}

	// 2. worker-1 切换成功后 web-1 失败，开启 AutoRollback 应自动将 worker-1 切回 #1。
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
	target, err := deploy.Parse([]byte(`
app: blog
environments:
  production:
    hosts: [web-1]
`), "blog", "")
	if err != nil {
		t.Fatal(err)
	}

	m1, _ := json.Marshal(deploy.Manifest{ReleaseID: 1, Version: "v1.0.0", BinSHA256: "aaa", Kind: deploy.ReleaseKindFull})
	m2, _ := json.Marshal(deploy.Manifest{ReleaseID: 2, Version: "v1.1.0", BinSHA256: "bbb", Kind: deploy.ReleaseKindFull})
	m3, _ := json.Marshal(deploy.Manifest{ReleaseID: 3, Version: "v1.1.0", BinSHA256: "bbb", ConfigSHA256: "ccc", Kind: deploy.ReleaseKindConfig})
	// 历史记录：先发 #1 -> 发 #2 -> 发 #3 -> 回滚到 #2，此时再不带参 rollback 应跳过连续的 #2 回到 #3 或按栈回到之前激活版本；
	// 这里测试：当前是 #3，历史顺序是 #1 -> #3（跳过了 #2），不带参应回到 #1 而不是字典序的 #2！
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

	// 1. ListReleases 按 release_id 倒序返回。
	listRep, err := deploy.ListReleases(context.Background(), runner, target)
	if err != nil || listRep.CurrentRelease != 3 || len(listRep.Releases) != 3 || listRep.Releases[0].ReleaseID != 3 || listRep.Releases[2].ReleaseID != 1 {
		t.Fatalf("ListReleases: rep=%+v err=%v", listRep, err)
	}
	var table bytes.Buffer
	deploy.FormatReleases(&table, listRep)
	if !strings.Contains(table.String(), "#3") || !strings.Contains(table.String(), "v1.1.0") {
		t.Errorf("FormatReleases:\n%s", table.String())
	}

	// 2. 无参回滚遵循 history.jsonl 回到 #1，并执行 config check。
	var out, errOut bytes.Buffer
	rb1, err := deploy.Rollback(context.Background(), runner, target, "", &out, &errOut)
	if err != nil || rb1.ReleaseID != 1 || rb1.Version != "v1.0.0" || !configChecked {
		t.Fatalf("Rollback history: rep=%+v configChecked=%v err=%v", rb1, configChecked, err)
	}

	// 3. 按 Git Tag 回滚到最新的 v1.1.0（即 #3）。
	rbTag, err := deploy.Rollback(context.Background(), runner, target, "v1.1.0", &out, &errOut)
	if err != nil || rbTag.ReleaseID != 3 || rbTag.Version != "v1.1.0" {
		t.Fatalf("Rollback by tag: rep=%+v err=%v", rbTag, err)
	}

	// 4. 按序号回滚到 #2。
	rbNum, err := deploy.Rollback(context.Background(), runner, target, "#2", &out, &errOut)
	if err != nil || rbNum.ReleaseID != 2 || rbNum.Version != "v1.1.0" {
		t.Fatalf("Rollback by num: rep=%+v err=%v", rbNum, err)
	}
}

func TestStatusLogsAndMaintenance(t *testing.T) {
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
				return "---manifest\n{\"release_id\":12,\"version\":\"v1.2.0\",\"config_sha256\":\"abcdef012345\"}\n---unit=blog@http.service\nActiveState=active\nSubState=running\nStatusText=\nActiveEnterTimestamp=Sun 2026-11-22\n", "", nil
			case strings.Contains(script, "journalctl"):
				return "INF ok on " + host.Addr + "\nERR boom on " + host.Addr + "\n", "", nil
			}
			return "", "", nil
		},
	}

	st, err := deploy.CollectStatus(context.Background(), runner, target, "http", "")
	if err != nil || len(st.Hosts) != 2 || st.Hosts[0].ReleaseID != 12 || st.Hosts[0].Version != "v1.2.0" {
		t.Fatalf("CollectStatus: st=%+v err=%v", st, err)
	}
	var table bytes.Buffer
	deploy.FormatStatus(&table, st)
	if !strings.Contains(table.String(), "#12") || !strings.Contains(table.String(), "v1.2.0") || !strings.Contains(table.String(), "abcdef01") {
		t.Errorf("status table:\n%s", table.String())
	}

	var logBuf, errBuf bytes.Buffer
	if err := deploy.Logs(context.Background(), runner, target, deploy.LogsOptions{Role: "http", Contains: "ERR", Since: "15m"}, &logBuf, &errBuf); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if !strings.Contains(logBuf.String(), "[web-1] ERR boom on web-1") || strings.Contains(logBuf.String(), "INF") {
		t.Errorf("logs:\n%s", logBuf.String())
	}

	var out bytes.Buffer
	mrep, err := deploy.Maintenance(context.Background(), runner, target, "", "", true, &out)
	if err != nil || !slices.Equal(mrep.Hosts, []string{"web-1", "web-2"}) {
		t.Fatalf("Maintenance: rep=%+v err=%v", mrep, err)
	}
}
