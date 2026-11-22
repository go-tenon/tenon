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
	"time"

	"golang.org/x/sync/errgroup"
)

// ReleaseOptions 控制 tenon deploy 单次发布的行为。
type ReleaseOptions struct {
	Binary     string                                                // 现成的 Linux 二进制路径；留空时调用 Build 构建
	Version    string                                                // 发布版本名；留空时生成时间戳加短提交号
	SkipDoctor bool                                                  // 为真时跳过上线前 doctor 自检
	DryRun     bool                                                  // 为真时只打印发布计划，不修改远端
	Now        func() time.Time                                      // 当前时间函数，测试可注入；为空时取 time.Now
	Build      func(ctx context.Context, arch, outPath string) error // 交叉构建回调
}

// Deploy 执行完整的零停机发布流水线：
// 探测架构与构建 → 主节点加锁 → 并行上传与校验 → config check 与 doctor → 单节点 migrate up → 逐台原子切换与 reload → 清理旧版本。
func Deploy(ctx context.Context, r Runner, t Target, opts ReleaseOptions, out, errOut io.Writer) error {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	arch, err := resolveArch(ctx, r, t, opts.DryRun)
	if err != nil {
		return err
	}
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = defaultVersion(ctx, now().UTC())
	}
	if !isSafeVersion(version) {
		return fmt.Errorf("deploy: version %q contains invalid characters", version)
	}

	if opts.DryRun {
		fmt.Fprintf(out, "[dry-run] 环境 %s，应用 %s，版本 %s，架构 linux/%s\n", t.EnvName, t.App, version, arch)
		fmt.Fprintf(out, "[dry-run] 主迁移节点：%s；全部主机：%d 台\n", t.MigrateHost().Addr, len(t.Hosts))
		for _, h := range t.Hosts {
			fmt.Fprintf(out, "[dry-run]   - %s (parts: %s)\n", h.Addr, strings.Join(h.Parts, ", "))
		}
		return nil
	}

	// 1. 准备待发布的二进制文件与配置归档。
	binPath := opts.Binary
	if binPath == "" {
		if opts.Build == nil {
			return fmt.Errorf("deploy: neither --binary nor Build callback is set")
		}
		tmpDir, err := os.MkdirTemp("", "tenon-deploy-*")
		if err != nil {
			return fmt.Errorf("deploy: create temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()
		binPath = filepath.Join(tmpDir, t.App)
		fmt.Fprintf(out, "构建 linux/%s 二进制...\n", arch)
		if err := opts.Build(ctx, arch, binPath); err != nil {
			return fmt.Errorf("deploy: build binary: %w", err)
		}
	}
	sum, err := fileSHA256(binPath)
	if err != nil {
		return fmt.Errorf("deploy: hash binary: %w", err)
	}
	cfgArchive, hasConfig, err := archiveConfigDir(t.ConfigDir)
	if err != nil {
		return err
	}

	// 2. 在主节点获取原子发布锁，防止并发发布互相覆盖。
	primary := t.MigrateHost()
	if err := acquireLock(ctx, r, t, primary, version); err != nil {
		return err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = releaseLock(releaseCtx, r, t, primary)
	}()

	// 3. 并行上传二进制与配置文件到全部主机并校验 SHA256。
	fmt.Fprintf(out, "上传版本 %s 到 %d 台主机...\n", version, len(t.Hosts))
	releaseDir := path.Join(t.Dir, "releases", version)
	remoteBin := path.Join(releaseDir, t.App)
	if err := uploadAll(ctx, r, t, releaseDir, remoteBin, binPath, sum, cfgArchive, hasConfig); err != nil {
		return err
	}

	// 4. 上线前预检（在主节点使用新版本运行，不影响正在服务的旧进程）。
	fmt.Fprintf(out, "[%s] 检查配置...\n", primary.Addr)
	if _, err := RunCapture(ctx, r, primary, systemdRunCommand(t, releaseDir, remoteBin, "config", "check")); err != nil {
		return fmt.Errorf("[%s] config check 失败: %w", primary.Addr, err)
	}
	if !opts.SkipDoctor {
		hasDoc, err := remoteHasSubcommand(ctx, r, primary, remoteBin, "doctor")
		if err != nil {
			return fmt.Errorf("[%s] 检查 doctor 命令失败: %w", primary.Addr, err)
		}
		if hasDoc {
			fmt.Fprintf(out, "[%s] 运行自检 doctor...\n", primary.Addr)
			if err := r.Run(ctx, primary, systemdRunCommand(t, releaseDir, remoteBin, "doctor"), nil, out, errOut); err != nil {
				return fmt.Errorf("[%s] doctor 自检未通过: %w", primary.Addr, err)
			}
		}
	}

	// 5. 数据库迁移（仅在主节点执行一次）。
	hasMigrate, err := remoteHasSubcommand(ctx, r, primary, remoteBin, "migrate")
	if err != nil {
		return fmt.Errorf("[%s] 检查 migrate 命令失败: %w", primary.Addr, err)
	}
	if hasMigrate {
		fmt.Fprintf(out, "[%s] 执行数据库迁移...\n", primary.Addr)
		if err := r.Run(ctx, primary, systemdRunCommand(t, releaseDir, remoteBin, "migrate", "up"), nil, out, errOut); err != nil {
			return fmt.Errorf("[%s] migrate up 失败（旧版本仍正常运行）: %w", primary.Addr, err)
		}
	}

	// 6. 逐台滚动零停机切换，单台失败立即熔断。
	for _, h := range t.Hosts {
		fmt.Fprintf(out, "[%s] 切换到版本 %s...\n", h.Addr, version)
		if err := switchHost(ctx, r, t, h, version, hasConfig, out, errOut); err != nil {
			return err
		}
	}

	// 7. 清理超出 keep_releases 的旧版本目录。
	for _, h := range t.Hosts {
		if err := pruneReleases(ctx, r, t, h, version); err != nil {
			fmt.Fprintf(errOut, "[%s] 警告：清理旧版本失败: %v\n", h.Addr, err)
		}
	}
	fmt.Fprintf(out, "发布完成：%s (%s) -> %s\n", t.App, t.EnvName, version)
	return nil
}

// resolveArch 返回构建架构；配置未指定且非 dry-run 时通过 SSH 探测目标机架构，并确认各台机器架构一致。
func resolveArch(ctx context.Context, r Runner, t Target, dryRun bool) (string, error) {
	if t.Arch != "" {
		return t.Arch, nil
	}
	if dryRun {
		return "auto", nil
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
		// 跳过隐藏文件与本机开发覆盖 local.yaml，避免开发配置进入生产。
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
			Mode: 0o644,
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
			// 先建目录并允许当前 SSH 用户写入，上传完成后统一改回 root 所有和 755 权限。
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
				unpack := fmt.Sprintf("set -e; sudo rm -rf %s; sudo install -d -m 755 %s; sudo tar -xzf - -C %s",
					shellQuote(cfgDir), shellQuote(cfgDir), shellQuote(cfgDir))
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

// switchHost 将单台主机原子切换到 version 对应的二进制与配置，并启动或热重载全部 systemd unit。
// 新进程升级失败时拉取最近日志并返回错误。
func switchHost(ctx context.Context, r Runner, t Target, h Host, version string, hasConfig bool, out, errOut io.Writer) error {
	releaseDir := path.Join(t.Dir, "releases", version)
	releaseBin := path.Join(releaseDir, t.App)
	liveBin := path.Join(t.Dir, "bin", t.App)
	liveBinNew := liveBin + ".new"

	// 使用硬链接 + 原子 mv 替换二进制，既不占双倍空间、不触发 Text file busy，也避免 /proc/self/exe 解析软链。
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
		return fmt.Errorf("[%s] 切换二进制与配置失败: %w", h.Addr, err)
	}

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
			// 拉取最近日志帮助定位失败原因，再中止滚动发布。
			_, _ = fmt.Fprintf(errOut, "[%s] %s 切换失败，最近日志：\n", h.Addr, unit)
			_ = r.Run(ctx, h, fmt.Sprintf("sudo journalctl -u %s -n 30 --no-pager", shellQuote(unit)), nil, errOut, errOut)
			return fmt.Errorf("[%s] %s 启动或热重载失败: %w", h.Addr, unit, err)
		}
		fmt.Fprintf(out, "[%s] %s 已就绪\n", h.Addr, unit)
	}

	if h.HasHTTP() {
		hasHealth, err := remoteHasSubcommand(ctx, r, h, liveBin, "health")
		if err == nil && hasHealth {
			if _, err := RunCapture(ctx, r, h, systemdRunCommand(t, t.Dir, liveBin, "health")); err != nil {
				return fmt.Errorf("[%s] health 检查失败: %w", h.Addr, err)
			}
		}
	}
	return nil
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
