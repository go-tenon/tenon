package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// ReleaseOptions 控制 tenon deploy 单次发布的行为。
type ReleaseOptions struct {
	Binary     string                                                // 现成的 Linux 二进制路径；留空时调用 Build 构建
	Version    string                                                // 发布版本名；留空时生成时间戳加短提交号
	WorkDir    string                                                // 本地项目根目录；留空时使用当前工作目录
	SkipDoctor bool                                                  // 为真时跳过上线前 doctor 自检
	DryRun     bool                                                  // 为真时只输出发布计划，不修改远端
	Now        func() time.Time                                      // 当前时间函数，测试可注入；为空时取 time.Now
	Build      func(ctx context.Context, arch, outPath string) error // 交叉构建回调
}

// DeployReport 是 tenon deploy 与 tenon_deploy_plan 的结构化结果。
type DeployReport struct {
	App         string           `json:"app"`                    // 应用名
	Env         string           `json:"env"`                    // 环境名
	Version     string           `json:"version"`                // 发布版本号
	Arch        string           `json:"arch"`                   // 目标架构
	DryRun      bool             `json:"dry_run"`                // 是否为预演模式
	ConfigFiles []string         `json:"config_files,omitempty"` // 按清单叠加加载的配置文件列表
	MigrateHost string           `json:"migrate_host"`           // 执行预检与迁移的主节点
	Migrated    bool             `json:"migrated,omitempty"`     // 是否执行了 migrate up
	Waves       []Wave           `json:"waves"`                  // 滚动发布波次
	Hosts       []HostSwitchInfo `json:"hosts"`                  // 各主机期望角色、parts 与切换结果
}

// HostSwitchInfo 记录单台主机的切换详情（含声明式清理掉的孤儿 unit）。
type HostSwitchInfo struct {
	Addr         string   `json:"addr"`                    // 主机地址
	Roles        []string `json:"roles"`                   // 承担的角色
	Parts        []string `json:"parts"`                   // 期望运行的 parts
	RemovedUnits []string `json:"removed_units,omitempty"` // 声明式对账时停用并禁用的旧 unit
	Status       string   `json:"status"`                  // planned / active
}

// Plan 生成单次发布的只读预演计划，不触发构建或远端写操作。
func Plan(ctx context.Context, t Target, opts ReleaseOptions) (*DeployReport, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	arch := t.Arch
	if arch == "" {
		arch = "auto"
	}
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = defaultVersion(ctx, now().UTC())
	}
	if !isSafeVersion(version) {
		return nil, fmt.Errorf("deploy: version %q contains invalid characters", version)
	}
	hosts := make([]HostSwitchInfo, 0, len(t.Hosts))
	for _, h := range t.Hosts {
		hosts = append(hosts, HostSwitchInfo{
			Addr:   h.Addr,
			Roles:  h.Roles,
			Parts:  h.Parts,
			Status: "planned",
		})
	}
	return &DeployReport{
		App:         t.App,
		Env:         t.EnvName,
		Version:     version,
		Arch:        arch,
		DryRun:      true,
		ConfigFiles: t.RemoteConfigFiles(),
		MigrateHost: t.MigrateHost().Addr,
		Waves:       t.RolloutWaves(),
		Hosts:       hosts,
	}, nil
}

// Deploy 执行完整的零停机发布流水线：
// 探测架构与构建 → 主节点加锁 → 全集群并行上传校验 → config check → 单节点 migrate up → doctor →
// 按波次（后端优先、入口金丝雀后批量滚动）原子切换、声明式清理孤儿 unit 与 reload → 并行清理旧版本。
func Deploy(ctx context.Context, r Runner, t Target, opts ReleaseOptions, out, errOut io.Writer) (*DeployReport, error) {
	if opts.DryRun {
		plan, err := Plan(ctx, t, opts)
		if err != nil {
			return nil, err
		}
		printPlan(out, plan)
		return plan, nil
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	arch, err := resolveArch(ctx, r, t)
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = defaultVersion(ctx, now().UTC())
	}
	if !isSafeVersion(version) {
		return nil, fmt.Errorf("deploy: version %q contains invalid characters", version)
	}

	// 1. 先打包配置文件，再准备待发布的二进制文件，本地缺少配置时在构建之前立即报错。
	cfgArchive, hasConfig, err := packConfig(opts.WorkDir, t)
	if err != nil {
		return nil, err
	}
	binPath := opts.Binary
	if binPath == "" {
		if opts.Build == nil {
			return nil, fmt.Errorf("deploy: neither --binary nor Build callback is set")
		}
		tmpDir, err := os.MkdirTemp("", "tenon-deploy-*")
		if err != nil {
			return nil, fmt.Errorf("deploy: create temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()
		binPath = filepath.Join(tmpDir, t.App)
		fmt.Fprintf(out, "构建 linux/%s 二进制...\n", arch)
		if err := opts.Build(ctx, arch, binPath); err != nil {
			return nil, fmt.Errorf("deploy: build binary: %w", err)
		}
	}
	sum, err := fileSHA256(binPath)
	if err != nil {
		return nil, fmt.Errorf("deploy: hash binary: %w", err)
	}

	// 2. 在主节点获取原子发布锁，防止并发发布互相覆盖。
	primary := t.MigrateHost()
	if err := acquireLock(ctx, r, t, primary, version); err != nil {
		return nil, err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = releaseLock(releaseCtx, r, t, primary)
	}()

	// 3. 并行上传二进制与配置文件到全部主机并校验 SHA-256。
	fmt.Fprintf(out, "上传版本 %s 到 %d 台主机...\n", version, len(t.Hosts))
	releaseDir := path.Join(t.Dir, "releases", version)
	remoteBin := path.Join(releaseDir, t.App)
	if err := uploadAll(ctx, r, t, releaseDir, remoteBin, binPath, sum, cfgArchive, hasConfig); err != nil {
		return nil, err
	}

	// 4. 配置检查（在主节点使用新版本运行，不影响正在服务的旧进程）。
	fmt.Fprintf(out, "[%s] 检查配置...\n", primary.Addr)
	if _, err := RunCapture(ctx, r, primary, systemdRunCommand(t, releaseDir, remoteBin, "config", "check")); err != nil {
		return nil, fmt.Errorf("[%s] config check 失败: %w", primary.Addr, err)
	}

	// 5. 数据库迁移（仅在主节点执行一次，先于 doctor 以使 migrate.Doctor 与队列复制槽检查就绪）。
	migrated := false
	hasMigrate, err := remoteHasSubcommand(ctx, r, primary, remoteBin, "migrate")
	if err != nil {
		return nil, fmt.Errorf("[%s] 检查 migrate 命令失败: %w", primary.Addr, err)
	}
	if hasMigrate {
		fmt.Fprintf(out, "[%s] 执行数据库迁移...\n", primary.Addr)
		if err := r.Run(ctx, primary, systemdRunCommand(t, releaseDir, remoteBin, "migrate", "up"), nil, out, errOut); err != nil {
			return nil, fmt.Errorf("[%s] migrate up 失败（旧版本仍正常运行）: %w", primary.Addr, err)
		}
		migrated = true
	}

	// 6. 上线前全量自检 doctor（确认迁移、复制槽与全部依赖均就绪后再切换服务）。
	if !opts.SkipDoctor {
		hasDoc, err := remoteHasSubcommand(ctx, r, primary, remoteBin, "doctor")
		if err != nil {
			return nil, fmt.Errorf("[%s] 检查 doctor 命令失败: %w", primary.Addr, err)
		}
		if hasDoc {
			fmt.Fprintf(out, "[%s] 运行自检 doctor...\n", primary.Addr)
			if err := r.Run(ctx, primary, systemdRunCommand(t, releaseDir, remoteBin, "doctor"), nil, out, errOut); err != nil {
				return nil, fmt.Errorf("[%s] doctor 自检未通过: %w", primary.Addr, err)
			}
		}
	}

	// 7. 按波次（先后端，后入口金丝雀再批量滚动）切换主机，任一波次失败立即熔断。
	waves := t.RolloutWaves()
	switchResults, err := rolloutWaves(ctx, r, t, waves, version, hasConfig, out, errOut)
	if err != nil {
		return nil, err
	}

	// 8. 并行清理超出 keep_releases 的旧版本目录。
	var pruneGroup errgroup.Group
	for _, h := range t.Hosts {
		pruneGroup.Go(func() error {
			if perr := pruneReleases(ctx, r, t, h, version); perr != nil {
				fmt.Fprintf(errOut, "[%s] 警告：清理旧版本失败: %v\n", h.Addr, perr)
			}
			return nil
		})
	}
	_ = pruneGroup.Wait()

	fmt.Fprintf(out, "发布完成：%s (%s) -> %s\n", t.App, t.EnvName, version)
	return &DeployReport{
		App:         t.App,
		Env:         t.EnvName,
		Version:     version,
		Arch:        arch,
		DryRun:      false,
		ConfigFiles: t.RemoteConfigFiles(),
		MigrateHost: primary.Addr,
		Migrated:    migrated,
		Waves:       waves,
		Hosts:       switchResults,
	}, nil
}

// printPlan 以人类可读文本打印发布预演计划。
func printPlan(out io.Writer, plan *DeployReport) {
	fmt.Fprintf(out, "[dry-run] 环境 %s，应用 %s，版本 %s，架构 linux/%s\n", plan.Env, plan.App, plan.Version, plan.Arch)
	if len(plan.ConfigFiles) > 0 {
		fmt.Fprintf(out, "[dry-run] 配置文件：%s\n", strings.Join(plan.ConfigFiles, ", "))
	}
	fmt.Fprintf(out, "[dry-run] 主迁移节点：%s；主机总数：%d\n", plan.MigrateHost, len(plan.Hosts))
	for i, w := range plan.Waves {
		fmt.Fprintf(out, "[dry-run] 波次 %d (%s, max_parallel=%d): %s\n", i+1, w.Name, w.MaxParallel, strings.Join(w.Hosts, ", "))
	}
	for _, h := range plan.Hosts {
		fmt.Fprintf(out, "[dry-run]   - %s (roles: %s; parts: %s)\n", h.Addr, strings.Join(h.Roles, ","), strings.Join(h.Parts, ","))
	}
}

// rolloutWaves 按波次执行主机切换，波次内按 MaxParallel 控制并发度；任一主机失败立即中止后续波次。
func rolloutWaves(ctx context.Context, r Runner, t Target, waves []Wave, version string, hasConfig bool, out, errOut io.Writer) ([]HostSwitchInfo, error) {
	var mu sync.Mutex
	results := make(map[string]HostSwitchInfo, len(t.Hosts))

	for i, w := range waves {
		fmt.Fprintf(out, "波次 %d/%d [%s]（并发 %d）：%s\n", i+1, len(waves), w.Name, w.MaxParallel, strings.Join(w.Hosts, ", "))
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(max(w.MaxParallel, 1))
		for _, addr := range w.Hosts {
			h, ok := t.HostByAddr(addr)
			if !ok {
				return nil, fmt.Errorf("deploy: unknown host %q in wave %q", addr, w.Name)
			}
			g.Go(func() error {
				removed, err := switchHost(gctx, r, t, h, version, hasConfig, out, errOut)
				if err != nil {
					return err
				}
				mu.Lock()
				results[h.Addr] = HostSwitchInfo{
					Addr:         h.Addr,
					Roles:        h.Roles,
					Parts:        h.Parts,
					RemovedUnits: removed,
					Status:       "active",
				}
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
	}

	ordered := make([]HostSwitchInfo, 0, len(t.Hosts))
	for _, h := range t.Hosts {
		ordered = append(ordered, results[h.Addr])
	}
	return ordered, nil
}

// resolveArch 返回构建架构；配置未指定时通过 SSH 探测所有主机架构并确认一致。
func resolveArch(ctx context.Context, r Runner, t Target) (string, error) {
	if t.Arch != "" {
		return t.Arch, nil
	}
	var detected string
	for _, h := range t.Hosts {
		raw, err := RunCapture(ctx, r, h, "uname -m")
		if err != nil {
			return "", fmt.Errorf("[%s] 探测目标架构失败: %w", h.Addr, err)
		}
		a, err := normalizeArch(strings.TrimSpace(raw))
		if err != nil {
			return "", fmt.Errorf("[%s] %w", h.Addr, err)
		}
		if detected == "" {
			detected = a
		} else if detected != a {
			return "", fmt.Errorf("deploy: mixed host architectures (%s and %s); deploy them in separate environments", detected, a)
		}
	}
	return detected, nil
}

// normalizeArch 将 uname -m 的输出映射为 Go/Tenon 支持的 amd64 或 arm64。
func normalizeArch(machine string) (string, error) {
	switch machine {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported target architecture %q", machine)
	}
}

// defaultVersion 生成发布版本名：YYYYMMDD-HHMMSS-<git短提交号>（不在 git 仓库时省略提交号）。
func defaultVersion(ctx context.Context, now time.Time) string {
	ts := now.Format("20060102-150405")
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ts
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return ts
	}
	return ts + "-" + sha
}

// isSafeVersion 检查发布版本名只包含字母、数字、点、下划线和连字符。
func isSafeVersion(v string) bool {
	if v == "" || strings.HasPrefix(v, ".") {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// fileSHA256 返回本地文件的十六进制 SHA-256 摘要。
func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// packConfig 根据目标环境配置打包待同步的配置文件：配置了 config_files 时按清单精确打包，
// 否则打包 config_dir 目录并自动跳过 local.yaml 与隐藏文件。
func packConfig(workDir string, t Target) ([]byte, bool, error) {
	if len(t.ConfigFiles) > 0 {
		archive, err := archiveConfigFiles(workDir, t.ConfigFiles)
		if err != nil {
			return nil, false, err
		}
		return archive, true, nil
	}
	return archiveConfigDir(resolveLocalPath(workDir, t.ConfigDir))
}

// resolveLocalPath 将相对路径 p 锚定到 workDir；workDir 为空或 p 为绝对路径时直接返回 p。
func resolveLocalPath(workDir, p string) string {
	if workDir == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, filepath.FromSlash(p))
}

// archiveConfigFiles 按 config_files 清单打包本地配置文件为 tar.gz，权限设为 0640；任一文件缺失或为目录时返回错误。
func archiveConfigFiles(workDir string, files []ConfigFile) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		localPath := resolveLocalPath(workDir, f.LocalPath)
		info, err := os.Stat(localPath)
		if err != nil {
			return nil, fmt.Errorf("deploy: config file %s: %w", f.LocalPath, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("deploy: config file %s is a directory", f.LocalPath)
		}
		data, err := os.ReadFile(localPath)
		if err != nil {
			return nil, fmt.Errorf("deploy: read config file %s: %w", f.LocalPath, err)
		}
		hdr := &tar.Header{
			Name: f.ArchiveRel,
			Mode: 0o640,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// archiveConfigDir 把本地配置目录打包为 tar.gz 字节流，自动跳过 local.yaml 与隐藏文件；
// 目录不存在时返回 has=false。
func archiveConfigDir(dir string) (archive []byte, has bool, err error) {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("deploy: stat config_dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, false, fmt.Errorf("deploy: config_dir %s is not a directory", dir)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	count := 0
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		base := d.Name()
		if strings.HasPrefix(base, ".") || filepath.ToSlash(rel) == "local.yaml" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name: filepath.ToSlash(rel),
			Mode: 0o640,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
		count++
		return nil
	})
	if walkErr != nil {
		return nil, false, fmt.Errorf("deploy: pack config_dir %s: %w", dir, walkErr)
	}
	if err := tw.Close(); err != nil {
		return nil, false, err
	}
	if err := gz.Close(); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), count > 0, nil
}

// lockPath 返回发布锁目录路径。
func lockPath(t Target) string {
	return path.Join(t.Dir, ".deploy.lock")
}

// acquireLock 在主节点用原子 mkdir 获取发布锁，记录当前版本，已有锁时返回明确提示。
func acquireLock(ctx context.Context, r Runner, t Target, host Host, version string) error {
	lp := lockPath(t)
	script := fmt.Sprintf(`if ! sudo mkdir %s 2>/dev/null; then
  holder=$(sudo cat %s 2>/dev/null || true)
  echo "another deploy is in progress ($holder); run 'tenon deploy unlock' if a previous deploy crashed" >&2
  exit 1
fi
printf '%%s\n' %s | sudo tee %s >/dev/null`,
		shellQuote(lp),
		shellQuote(path.Join(lp, "info")),
		shellQuote(version),
		shellQuote(path.Join(lp, "info")),
	)
	if _, err := RunCapture(ctx, r, host, script); err != nil {
		return fmt.Errorf("[%s] 获取发布锁失败: %w", host.Addr, err)
	}
	return nil
}

// releaseLock 释放主节点上的发布锁。
func releaseLock(ctx context.Context, r Runner, t Target, host Host) error {
	lp := lockPath(t)
	_, err := RunCapture(ctx, r, host, "sudo rm -rf "+shellQuote(lp))
	return err
}

// uploadAll 并行向全部主机上传二进制和配置目录，并校验二进制的 SHA-256。
func uploadAll(ctx context.Context, r Runner, t Target, releaseDir, remoteBin, localBin, sha string, cfgArchive []byte, hasConfig bool) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, h := range t.Hosts {
		g.Go(func() error {
			tmpRemote := fmt.Sprintf("/tmp/%s-%s.bin", t.App, path.Base(releaseDir))
			if err := r.Upload(gctx, h, localBin, tmpRemote); err != nil {
				return fmt.Errorf("[%s] 上传二进制失败: %w", h.Addr, err)
			}
			installScript := fmt.Sprintf(`set -e
trap 'rm -f %s' EXIT
got=$(sha256sum %s | awk '{print $1}')
test "$got" = %s || { echo "sha256 mismatch: got $got want %s" >&2; exit 1; }
sudo install -d -m 755 %s
sudo install -m 755 -o root -g root %s %s`,
				shellQuote(tmpRemote),
				shellQuote(tmpRemote),
				shellQuote(sha), sha,
				shellQuote(releaseDir),
				shellQuote(tmpRemote), shellQuote(remoteBin),
			)
			if _, err := RunCapture(gctx, r, h, installScript); err != nil {
				return fmt.Errorf("[%s] 安装发布二进制失败: %w", h.Addr, err)
			}
			if hasConfig {
				cfgDir := path.Join(releaseDir, "config")
				owner := "root:" + t.ServiceUser
				unpack := fmt.Sprintf(
					"set -e; sudo rm -rf %s; sudo install -d -m 750 -o root -g %s %s; sudo tar -xzf - --no-same-owner -C %s; sudo chown -R %s %s; sudo find %s -type d -exec chmod 750 {} +; sudo find %s -type f -exec chmod 640 {} +",
					shellQuote(cfgDir),
					shellQuote(t.ServiceUser), shellQuote(cfgDir),
					shellQuote(cfgDir),
					shellQuote(owner), shellQuote(cfgDir),
					shellQuote(cfgDir),
					shellQuote(cfgDir),
				)
				if err := r.Run(gctx, h, unpack, bytes.NewReader(cfgArchive), io.Discard, io.Discard); err != nil {
					return fmt.Errorf("[%s] 同步配置目录失败: %w", h.Addr, err)
				}
			}
			return nil
		})
	}
	return g.Wait()
}

// remoteHasSubcommand 检查远端二进制是否提供指定一级子命令（如 doctor、migrate）。
func remoteHasSubcommand(ctx context.Context, r Runner, host Host, remoteBin, sub string) (bool, error) {
	script := fmt.Sprintf("if %s %s --help >/dev/null 2>&1; then echo yes; else echo no; fi",
		shellQuote(remoteBin), shellQuote(sub))
	out, err := RunCapture(ctx, r, host, script)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "yes", nil
}

// switchHost 将单台主机原子切换到 version 对应的二进制与配置，先对账并清理不再属于该主机的孤儿 unit，
// 再启动或热重载期望的全部 unit；返回被清理掉的孤儿 unit 名称列表。
func switchHost(ctx context.Context, r Runner, t Target, h Host, version string, hasConfig bool, out, errOut io.Writer) ([]string, error) {
	releaseDir := path.Join(t.Dir, "releases", version)
	releaseBin := path.Join(releaseDir, t.App)
	liveBin := path.Join(t.Dir, "bin", t.App)
	liveBinNew := liveBin + ".new"

	// 1. 按需同步模板 unit 与各 part 的 systemd drop-in 覆盖配置。
	unitUpdated, err := syncUnit(ctx, r, t, h)
	if err != nil {
		return nil, err
	}
	if err := syncDropIns(ctx, r, t, h); err != nil {
		return nil, err
	}

	// 2. 使用硬链接 + 原子 mv 替换二进制，既不占双倍空间、不触发 Text file busy，也避免 /proc/self/exe 解析软链。
	var swap strings.Builder
	fmt.Fprintf(&swap, "set -e\n")
	fmt.Fprintf(&swap, "sudo ln -f %s %s\n", shellQuote(releaseBin), shellQuote(liveBinNew))
	fmt.Fprintf(&swap, "sudo mv -f %s %s\n", shellQuote(liveBinNew), shellQuote(liveBin))
	if hasConfig {
		liveCfg := path.Join(t.Dir, "config")
		liveCfgNew := liveCfg + ".new"
		fmt.Fprintf(&swap, "sudo ln -sfn %s %s\n", shellQuote(path.Join(releaseDir, "config")), shellQuote(liveCfgNew))
		fmt.Fprintf(&swap, "sudo mv -Tf %s %s\n", shellQuote(liveCfgNew), shellQuote(liveCfg))
	}
	fmt.Fprintf(&swap, "printf '%%s\\n' %s | sudo tee %s >/dev/null\n",
		shellQuote(version), shellQuote(path.Join(t.Dir, "current_release")))
	if _, err := RunCapture(ctx, r, h, swap.String()); err != nil {
		return nil, fmt.Errorf("[%s] 切换二进制与配置失败: %w", h.Addr, err)
	}

	// 3. 声明式对账：停用并禁用不再属于当前主机期望 parts 的旧 unit（例如从 all 拆分为 http/worker 时）。
	removed, err := reconcileOrphanUnits(ctx, r, t, h, out)
	if err != nil {
		return nil, err
	}

	// 4. 启动或零停机热重载期望的每个 unit。
	for _, part := range h.Parts {
		unit := UnitName(t.App, part)
		reloadScript := fmt.Sprintf(`set -e
if sudo systemctl is-active --quiet %s; then
  sudo systemctl reload %s
  status=$(systemctl show -p StatusText --value %s)
  case "$status" in
    "upgrade failed"*)
      echo "$status" >&2
      exit 1
      ;;
  esac
else
  sudo systemctl enable --now %s
fi`, shellQuote(unit), shellQuote(unit), shellQuote(unit), shellQuote(unit))
		if _, err := RunCapture(ctx, r, h, reloadScript); err != nil {
			_, _ = fmt.Fprintf(errOut, "[%s] %s 切换失败，最近日志：\n", h.Addr, unit)
			_ = r.Run(ctx, h, fmt.Sprintf("sudo journalctl -u %s -n 30 --no-pager", shellQuote(unit)), nil, errOut, errOut)
			return removed, fmt.Errorf("[%s] %s 启动或热重载失败: %w", h.Addr, unit, err)
		}
		fmt.Fprintf(out, "[%s] %s 已就绪\n", h.Addr, unit)
		if unitUpdated {
			fmt.Fprintf(out, "[%s] 提示：模板 unit 已更新，如修改了 config_files 列表需执行 sudo systemctl restart %s 使新启动参数生效\n", h.Addr, unit)
		}
	}

	// 5. 含入口服务的节点执行本机 health 检查确认 /livez 通过。
	if h.HasHTTP() {
		hasHealth, err := remoteHasSubcommand(ctx, r, h, liveBin, "health")
		if err == nil && hasHealth {
			if _, err := RunCapture(ctx, r, h, systemdRunCommand(t, t.Dir, liveBin, "health")); err != nil {
				return removed, fmt.Errorf("[%s] health 检查失败: %w", h.Addr, err)
			}
		}
	}
	return removed, nil
}

// reconcileOrphanUnits 查询主机上当前运行中或已启用的 <app>@*.service，停止并禁用不在期望列表中的旧 unit。
func reconcileOrphanUnits(ctx context.Context, r Runner, t Target, h Host, out io.Writer) ([]string, error) {
	pattern := t.App + "@*.service"
	listCmd := fmt.Sprintf("systemctl list-units %s --state=active,activating,failed --plain --no-legend 2>/dev/null | awk '{print $1}'", shellQuote(pattern))
	raw, err := RunCapture(ctx, r, h, listCmd)
	if err != nil {
		return nil, fmt.Errorf("[%s] 查询运行中 unit 失败: %w", h.Addr, err)
	}
	desired := make(map[string]bool, len(h.Parts))
	for _, p := range h.Parts {
		desired[UnitName(t.App, p)] = true
	}
	var orphans []string
	prefix := t.App + "@"
	for line := range strings.Lines(raw) {
		unit := strings.TrimSpace(line)
		if unit == "" || !strings.HasPrefix(unit, prefix) || !strings.HasSuffix(unit, ".service") {
			continue
		}
		if !desired[unit] && !slices.Contains(orphans, unit) {
			orphans = append(orphans, unit)
		}
	}
	slices.Sort(orphans)
	for _, unit := range orphans {
		fmt.Fprintf(out, "[%s] 停用已移除的旧服务 %s...\n", h.Addr, unit)
		if _, err := RunCapture(ctx, r, h, "sudo systemctl disable --now "+shellQuote(unit)); err != nil {
			return orphans, fmt.Errorf("[%s] 停用旧服务 %s 失败: %w", h.Addr, unit, err)
		}
	}
	return orphans, nil
}

// pruneReleases 清理主机上超出 keep_releases 的历史发布目录，始终保留 current。
func pruneReleases(ctx context.Context, r Runner, t Target, h Host, current string) error {
	releasesDir := path.Join(t.Dir, "releases")
	raw, err := RunCapture(ctx, r, h, fmt.Sprintf("ls -1 %s 2>/dev/null || true", shellQuote(releasesDir)))
	if err != nil {
		return err
	}
	var versions []string
	for line := range strings.Lines(raw) {
		if v := strings.TrimSpace(line); v != "" {
			versions = append(versions, v)
		}
	}
	slices.Sort(versions)
	if len(versions) <= t.KeepReleases {
		return nil
	}
	dropCount := len(versions) - t.KeepReleases
	var toDelete []string
	for _, v := range versions {
		if len(toDelete) >= dropCount {
			break
		}
		if v == current || !isSafeVersion(v) {
			continue
		}
		toDelete = append(toDelete, shellQuote(path.Join(releasesDir, v)))
	}
	if len(toDelete) == 0 {
		return nil
	}
	_, err = RunCapture(ctx, r, h, "sudo rm -rf "+strings.Join(toDelete, " "))
	return err
}
