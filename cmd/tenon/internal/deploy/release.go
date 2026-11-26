package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// RunnerFactory 为指定目标实例创建远端执行器与清理函数，供多目标发布按目标隔离 SSH 配置。
type RunnerFactory func(t Target) (Runner, func() error, error)

// ReleaseOptions 控制 tenon deploy 单次发布的行为。
type ReleaseOptions struct {
	Binary         string                                                                   // 现成的 Linux 二进制路径；留空时按 Tag 或 Build 准备
	Tag            string                                                                   // GitHub Release 标签（如 v1.2.0）；非空时直接下载对应架构二进制
	Version        string                                                                   // 显式指定构建版本号；留空时取 Tag 或 git describe
	WorkDir        string                                                                   // 本地项目根目录；留空时使用当前工作目录
	AllowDirty     bool                                                                     // 为真时允许在存在未提交改动的工作区执行本地构建发布
	Force          bool                                                                     // 为真时跳过 No-op 检测，强制创建新发布并切换
	SkipDoctor     bool                                                                     // 为真时跳过上线前 doctor 自检
	AutoRollback   bool                                                                     // 波次滚动中途失败时自动将已切换主机回退到上一发布
	DryRun         bool                                                                     // 为真时只输出发布计划，不修改远端
	Now            func() time.Time                                                         // 当前时间函数，测试可注入；为空时取 time.Now
	Build          func(ctx context.Context, arch, outPath string, meta GitBuildInfo) error // 交叉构建回调
	ReleaseBaseURL string                                                                   // GitHub Release 基础 URL，留空时为 https://github.com（测试可注入）
	HTTPClient     *http.Client                                                             // 下载 Release 产物使用的 HTTP 客户端
}

// ConfigReleaseOptions 控制 tenon deploy config 仅配置热发布的行为。
type ConfigReleaseOptions struct {
	WorkDir      string           // 本地项目根目录；留空时使用当前工作目录
	Force        bool             // 为真时跳过配置未变化的 No-op 检测
	SkipDoctor   bool             // 为真时跳过上线前 doctor 自检
	AutoRollback bool             // 波次滚动中途失败时自动将已切换主机回退到上一发布
	DryRun       bool             // 为真时只输出配置发布计划，不修改远端
	Now          func() time.Time // 当前时间函数，测试可注入；为空时取 time.Now
}

// DeployReport 是 tenon deploy、tenon deploy config 与 tenon_deploy_plan 的结构化结果。
type DeployReport struct {
	App            string           `json:"app"`                        // 构建产物名
	Target         string           `json:"target"`                     // 目标名
	Instance       string           `json:"instance"`                   // 远端实例名
	Kind           string           `json:"kind"`                       // full / config
	ReleaseID      int              `json:"release_id,omitempty"`       // 分配的单调发布序号
	Version        string           `json:"version"`                    // 构建版本号（如 v1.2.0）
	Commit         string           `json:"commit,omitempty"`           // Git 短提交号
	Dirty          bool             `json:"dirty,omitempty"`            // 构建时工作区存在未提交改动
	BinSHA256      string           `json:"bin_sha256,omitempty"`       // 二进制 SHA-256
	ConfigSHA256   string           `json:"config_sha256,omitempty"`    // 配置内容确定性 SHA-256
	Arch           string           `json:"arch,omitempty"`             // 目标架构
	DryRun         bool             `json:"dry_run"`                    // 是否为预演模式
	Skipped        bool             `json:"skipped,omitempty"`          // 与线上完全一致时短路跳过
	UploadedHosts  int              `json:"uploaded_hosts,omitempty"`   // 实际上传了二进制的主机数（命中内容寻址缓存的主机不计入）
	ConfigFiles    []string         `json:"config_files,omitempty"`     // 按清单叠加加载的配置文件列表
	MigrateHost    string           `json:"migrate_host"`               // 执行迁移与自检的主节点
	Migrated       bool             `json:"migrated,omitempty"`         // 是否执行了 migrate up
	AutoRolledBack bool             `json:"auto_rolled_back,omitempty"` // 波次失败后是否已自动回退已切换节点
	Waves          []Wave           `json:"waves"`                      // 滚动发布波次
	Hosts          []HostSwitchInfo `json:"hosts"`                      // 各主机期望角色、parts 与切换结果
}

// BatchDeployReport 是多目标发布（DeployMany / DeployConfigMany）的结构化结果。
type BatchDeployReport struct {
	App     string          `json:"app"`     // 构建产物名
	Kind    string          `json:"kind"`    // full / config
	DryRun  bool            `json:"dry_run"` // 是否为预演模式
	Targets []*DeployReport `json:"targets"` // 按顺序执行的各目标发布报告
}

// HostSwitchInfo 记录单台主机的切换详情（含声明式清理掉的孤儿 unit 与切换方式）。
type HostSwitchInfo struct {
	Addr         string   `json:"addr"`                    // 主机地址
	Roles        []string `json:"roles"`                   // 承担的角色
	Parts        []string `json:"parts"`                   // 期望运行的 parts
	Action       string   `json:"action,omitempty"`        // reload / restart / start / unchanged
	RemovedUnits []string `json:"removed_units,omitempty"` // 声明式对账时停用并禁用的旧 unit
	Status       string   `json:"status"`                  // planned / active / skipped
}

// preparedBinary 是多目标发布中按架构缓存的已构建或已下载二进制。
type preparedBinary struct {
	arch    string
	binPath string
	binSum  string
	meta    GitBuildInfo
}

// Plan 生成单次发布的只读预演计划，不触发构建或远端写操作。
func Plan(ctx context.Context, t Target, opts ReleaseOptions) (*DeployReport, error) {
	arch := t.Arch
	if arch == "" {
		arch = "auto"
	}
	gitInfo := InspectGit(ctx, opts.WorkDir)
	version := resolveBuildVersion(opts, gitInfo)
	if !isSafeVersion(version) {
		return nil, fmt.Errorf("deploy: version %q contains invalid characters", version)
	}
	hosts := plannedHosts(t)
	return &DeployReport{
		App:         t.App,
		Target:      t.Name,
		Instance:    t.Instance,
		Kind:        ReleaseKindFull,
		Version:     version,
		Commit:      gitInfo.Commit,
		Dirty:       gitInfo.Dirty && opts.Binary == "" && opts.Tag == "",
		Arch:        arch,
		DryRun:      true,
		ConfigFiles: t.RemoteConfigFiles(),
		MigrateHost: t.MigrateHost().Addr,
		Waves:       t.RolloutWaves(),
		Hosts:       hosts,
	}, nil
}

// Deploy 执行单个目标实例的完整零停机发布流水线：
// Git 工作区校验与架构并发探测 → 本地构建或从 GitHub Release 拉取 → 会话级 flock 加锁 →
// 内容寻址去重上传（gzip 流式）与 No-op 检测 → 全机并行 config check → 单节点 migrate up → doctor →
// 按波次切换（自动识别 unit/drop-in 变更走 restart，其余走 tableflip reload）→ 清理旧 release 与无引用 artifacts。
func Deploy(ctx context.Context, r Runner, t Target, opts ReleaseOptions, out, errOut io.Writer) (*DeployReport, error) {
	return deployOne(ctx, r, t, opts, nil, out, errOut)
}

// DeployMany 面向多目标实例执行“一次构建、按序串行发布”：
// 先校验全部目标的配置文件并探测各自架构，按唯一架构只构建或下载一次二进制，再按顺序逐个实例发布；
// 任一实例失败立即熔断，不影响后续目标实例。
func DeployMany(ctx context.Context, targets []Target, newRunner RunnerFactory, opts ReleaseOptions, out, errOut io.Writer) (*BatchDeployReport, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("deploy: at least one target is required")
	}
	if opts.DryRun {
		batch := &BatchDeployReport{
			App:     targets[0].App,
			Kind:    ReleaseKindFull,
			DryRun:  true,
			Targets: make([]*DeployReport, 0, len(targets)),
		}
		for i, t := range targets {
			if i > 0 {
				fmt.Fprintln(out)
			}
			plan, err := Plan(ctx, t, opts)
			if err != nil {
				return nil, err
			}
			printPlan(out, plan)
			batch.Targets = append(batch.Targets, plan)
		}
		return batch, nil
	}
	if opts.Binary != "" && opts.Tag != "" {
		return nil, fmt.Errorf("deploy: --binary and --tag cannot be used together")
	}

	gitInfo := InspectGit(ctx, opts.WorkDir)
	if opts.Binary == "" && opts.Tag == "" && gitInfo.InRepo && gitInfo.Dirty && !opts.AllowDirty {
		return nil, fmt.Errorf("deploy: git working tree has uncommitted changes; commit them first or pass --allow-dirty")
	}
	version := resolveBuildVersion(opts, gitInfo)
	if !isSafeVersion(version) {
		return nil, fmt.Errorf("deploy: version %q contains invalid characters", version)
	}
	buildMeta := GitBuildInfo{
		InRepo:     gitInfo.InRepo,
		Version:    version,
		Commit:     gitInfo.Commit,
		CommitTime: gitInfo.CommitTime,
		Dirty:      gitInfo.Dirty && opts.Binary == "" && opts.Tag == "",
		Repo:       gitInfo.Repo,
	}

	// 1. 先校验全部目标的本地配置文件，缺文件在编译前立即暴露。
	for _, t := range targets {
		if _, _, _, err := packConfig(opts.WorkDir, t); err != nil {
			return nil, fmt.Errorf("[%s] %w", t.Name, err)
		}
	}

	// 2. 为各目标建立 SSH 会话并并发探测架构（复用同一会话供后续上传与切换）。
	runners := make([]Runner, len(targets))
	closers := make([]func() error, len(targets))
	defer func() {
		for _, closeFn := range closers {
			if closeFn != nil {
				_ = closeFn()
			}
		}
	}()
	targetArches := make([]string, len(targets))
	for i, t := range targets {
		r, closeFn, err := newRunner(t)
		if err != nil {
			return nil, fmt.Errorf("[%s] 创建 SSH 会话失败: %w", t.Name, err)
		}
		runners[i] = r
		closers[i] = closeFn
		arch, err := resolveArch(ctx, r, t)
		if err != nil {
			return nil, fmt.Errorf("[%s] %w", t.Name, err)
		}
		targetArches[i] = arch
	}

	// 3. 按唯一架构准备二进制（同一架构只构建或下载一次，保证所有目标跑同一份产物）。
	tmpDir, err := os.MkdirTemp("", "tenon-deploy-batch-*")
	if err != nil {
		return nil, fmt.Errorf("deploy: create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	byArch := make(map[string]*preparedBinary)
	var preBinSum string
	if opts.Binary != "" {
		preBinSum, err = fileSHA256(opts.Binary)
		if err != nil {
			return nil, fmt.Errorf("deploy: hash binary: %w", err)
		}
	}
	for i, t := range targets {
		arch := targetArches[i]
		if _, ok := byArch[arch]; ok {
			continue
		}
		if opts.Binary != "" {
			byArch[arch] = &preparedBinary{arch: arch, binPath: opts.Binary, binSum: preBinSum, meta: buildMeta}
			continue
		}
		binPath := filepath.Join(tmpDir, fmt.Sprintf("%s-linux-%s", t.App, arch))
		var binSum string
		if opts.Tag != "" {
			repo := t.Repo
			if repo == "" {
				repo = gitInfo.Repo
			}
			fmt.Fprintf(out, "从 GitHub Release (%s %s) 下载 linux/%s 二进制...\n", repo, opts.Tag, arch)
			sum, err := DownloadReleaseBinary(ctx, opts.HTTPClient, opts.ReleaseBaseURL, repo, opts.Tag, t.App, arch, binPath)
			if err != nil {
				return nil, err
			}
			binSum = sum
		} else {
			if opts.Build == nil {
				return nil, fmt.Errorf("deploy: neither --binary, --tag nor Build callback is set")
			}
			fmt.Fprintf(out, "构建 %s (linux/%s)...\n", version, arch)
			if err := opts.Build(ctx, arch, binPath, buildMeta); err != nil {
				return nil, fmt.Errorf("deploy: build binary: %w", err)
			}
			binSum, err = fileSHA256(binPath)
			if err != nil {
				return nil, fmt.Errorf("deploy: hash binary: %w", err)
			}
		}
		byArch[arch] = &preparedBinary{arch: arch, binPath: binPath, binSum: binSum, meta: buildMeta}
	}

	// 4. 按顺序串行发布各目标实例，任一目标失败立即熔断。
	batch := &BatchDeployReport{
		App:     targets[0].App,
		Kind:    ReleaseKindFull,
		Targets: make([]*DeployReport, 0, len(targets)),
	}
	for i, t := range targets {
		if len(targets) > 1 {
			fmt.Fprintf(out, "\n=== 发布目标 %d/%d: %s ===\n", i+1, len(targets), t.DisplayLabel())
		}
		rep, err := deployOne(ctx, runners[i], t, opts, byArch[targetArches[i]], out, errOut)
		if err != nil {
			return nil, fmt.Errorf("目标 %s 发布失败: %w", t.Name, err)
		}
		batch.Targets = append(batch.Targets, rep)
	}
	return batch, nil
}

// deployOne 执行单个目标的发布流水线；pre 非空时复用已准备好的二进制与架构探测结果。
func deployOne(ctx context.Context, r Runner, t Target, opts ReleaseOptions, pre *preparedBinary, out, errOut io.Writer) (*DeployReport, error) {
	if opts.DryRun {
		plan, err := Plan(ctx, t, opts)
		if err != nil {
			return nil, err
		}
		printPlan(out, plan)
		return plan, nil
	}
	if opts.Binary != "" && opts.Tag != "" {
		return nil, fmt.Errorf("deploy: --binary and --tag cannot be used together")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	// 1. 先打包配置文件并计算确定性哈希，本地缺少配置时在构建之前立即报错。
	cfgArchive, cfgSum, hasConfig, err := packConfig(opts.WorkDir, t)
	if err != nil {
		return nil, err
	}

	var arch, binPath, binSum, version string
	var buildMeta GitBuildInfo
	if pre != nil {
		arch = pre.arch
		binPath = pre.binPath
		binSum = pre.binSum
		buildMeta = pre.meta
		version = pre.meta.Version
	} else {
		gitInfo := InspectGit(ctx, opts.WorkDir)
		if opts.Binary == "" && opts.Tag == "" && gitInfo.InRepo && gitInfo.Dirty && !opts.AllowDirty {
			return nil, fmt.Errorf("deploy: git working tree has uncommitted changes; commit them first or pass --allow-dirty")
		}
		version = resolveBuildVersion(opts, gitInfo)
		if !isSafeVersion(version) {
			return nil, fmt.Errorf("deploy: version %q contains invalid characters", version)
		}
		buildMeta = GitBuildInfo{
			InRepo:     gitInfo.InRepo,
			Version:    version,
			Commit:     gitInfo.Commit,
			CommitTime: gitInfo.CommitTime,
			Dirty:      gitInfo.Dirty && opts.Binary == "" && opts.Tag == "",
			Repo:       gitInfo.Repo,
		}

		// 2. 并发探测全部目标主机架构并确认一致。
		arch, err = resolveArch(ctx, r, t)
		if err != nil {
			return nil, err
		}

		// 3. 准备待发布的二进制：--binary 直接使用，--tag 从 GitHub Release 拉取并校验，否则调用 Build。
		binPath = opts.Binary
		if opts.Tag != "" {
			tmpDir, err := os.MkdirTemp("", "tenon-release-*")
			if err != nil {
				return nil, fmt.Errorf("deploy: create temp dir: %w", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()
			binPath = filepath.Join(tmpDir, t.App)
			repo := t.Repo
			if repo == "" {
				repo = gitInfo.Repo
			}
			fmt.Fprintf(out, "从 GitHub Release (%s %s) 下载 linux/%s 二进制...\n", repo, opts.Tag, arch)
			sum, err := DownloadReleaseBinary(ctx, opts.HTTPClient, opts.ReleaseBaseURL, repo, opts.Tag, t.App, arch, binPath)
			if err != nil {
				return nil, err
			}
			binSum = sum
		} else if binPath == "" {
			if opts.Build == nil {
				return nil, fmt.Errorf("deploy: neither --binary, --tag nor Build callback is set")
			}
			tmpDir, err := os.MkdirTemp("", "tenon-deploy-*")
			if err != nil {
				return nil, fmt.Errorf("deploy: create temp dir: %w", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()
			binPath = filepath.Join(tmpDir, t.App)
			fmt.Fprintf(out, "构建 %s (linux/%s)...\n", version, arch)
			if err := opts.Build(ctx, arch, binPath, buildMeta); err != nil {
				return nil, fmt.Errorf("deploy: build binary: %w", err)
			}
		}
		if binSum == "" {
			binSum, err = fileSHA256(binPath)
			if err != nil {
				return nil, fmt.Errorf("deploy: hash binary: %w", err)
			}
		}
	}

	// 4. 在主节点获取绑定 SSH 会话生命周期的 flock 发布锁，防止并发发布。
	primary := t.MigrateHost()
	operator := CurrentOperator()
	releaseLock, err := acquireLock(ctx, r, t, primary, fmt.Sprintf("%s (%s)", version, operator))
	if err != nil {
		return nil, err
	}
	defer releaseLock()

	// 5. 读取集群当前状态，执行 No-op 检测（二进制、配置与 systemd 定义均未变化时直接短路）。
	curManifests, err := readClusterCurrentManifests(ctx, r, t)
	if err != nil {
		return nil, err
	}
	fromRelease := 0
	if pm := curManifests[primary.Addr]; pm != nil {
		fromRelease = pm.ReleaseID
	}
	if !opts.Force && clusterMatchesDesired(t, curManifests, binSum, cfgSum) {
		runtimeChanged, err := checkAnyRuntimeChanged(ctx, r, t)
		if err != nil {
			return nil, err
		}
		if !runtimeChanged {
			fmt.Fprintf(out, "%s 已是最新状态（#%d %s，bin:%s，cfg:%s），跳过发布（使用 --force 强制发布）。\n",
				t.DisplayLabel(), fromRelease, version, ShortSHA(binSum), ShortSHA(cfgSum))
			hosts := make([]HostSwitchInfo, 0, len(t.Hosts))
			for _, h := range t.Hosts {
				hosts = append(hosts, HostSwitchInfo{
					Addr:   h.Addr,
					Roles:  h.Roles,
					Parts:  h.Parts,
					Action: "unchanged",
					Status: "skipped",
				})
			}
			return &DeployReport{
				App:          t.App,
				Target:       t.Name,
				Instance:     t.Instance,
				Kind:         ReleaseKindFull,
				ReleaseID:    fromRelease,
				Version:      version,
				Commit:       buildMeta.Commit,
				Dirty:        buildMeta.Dirty,
				BinSHA256:    binSum,
				ConfigSHA256: cfgSum,
				Arch:         arch,
				Skipped:      true,
				ConfigFiles:  t.RemoteConfigFiles(),
				MigrateHost:  primary.Addr,
				Waves:        t.RolloutWaves(),
				Hosts:        hosts,
			}, nil
		}
	}

	// 6. 分配严格单调的下一个发布序号，组装不可变 manifest.json。
	releaseID, err := nextReleaseID(ctx, r, t, primary)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{
		ReleaseID:    releaseID,
		Version:      version,
		Commit:       buildMeta.Commit,
		Dirty:        buildMeta.Dirty,
		BinSHA256:    binSum,
		ConfigSHA256: cfgSum,
		Kind:         ReleaseKindFull,
		FromRelease:  fromRelease,
		DeployedAt:   now().UTC(),
		DeployedBy:   operator,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestBytes = append(manifestBytes, '\n')

	// 7. 并行向全部主机准备发布目录：二进制按 SHA-256 去重后 gzip 流式上传，硬链接为 releases/<id>/<instance>。
	relName := FormatReleaseID(releaseID)
	releaseDir := path.Join(t.Dir, "releases", relName)
	remoteBin := path.Join(releaseDir, t.Instance)
	fmt.Fprintf(out, "准备发布 %s #%d (%s，bin:%s) 到 %d 台主机...\n", t.DisplayLabel(), releaseID, version, ShortSHA(binSum), len(t.Hosts))

	switched := false
	defer func() {
		if switched {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		removeReleaseAll(cleanupCtx, r, t, releaseDir, errOut)
	}()
	uploadedCount, err := prepareReleaseAll(ctx, r, t, releaseDir, remoteBin, binPath, binSum, cfgArchive, hasConfig, manifestBytes, out)
	if err != nil {
		return nil, err
	}

	// 8. 全机并行运行离线 config check，提前发现任意节点的 env_file 或配置缺失。
	fmt.Fprintf(out, "并行校验 %d 台主机配置...\n", len(t.Hosts))
	if err := configCheckAll(ctx, r, t, releaseDir, remoteBin); err != nil {
		return nil, err
	}

	// 9. 数据库迁移（仅在主节点执行一次，先于 doctor 以使数据表就绪）。
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

	// 10. 上线前全量自检 doctor（确认迁移与全部依赖就绪后再切换服务）。
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

	// 11. 按波次切换主机，任一波次失败立即熔断；开启 AutoRollback 时自动回退已切换主机。
	switched = true
	waves := t.RolloutWaves()
	histEntry := HistoryEntry{
		ReleaseID: releaseID,
		Version:   version,
		Kind:      ReleaseKindFull,
		At:        manifest.DeployedAt,
		By:        operator,
	}
	switchResults, switchedAddrs, err := rolloutWaves(ctx, r, t, waves, releaseID, hasConfig, histEntry, out, errOut)
	if err != nil {
		if opts.AutoRollback && fromRelease > 0 && len(switchedAddrs) > 0 {
			autoRollbackHosts(ctx, r, t, switchedAddrs, fromRelease, operator, now().UTC(), out, errOut)
		}
		return nil, err
	}

	// 12. 并行清理超出 keep_releases 的旧发布目录并回收无引用的 artifacts 对象。
	pruneCluster(ctx, r, t, releaseID, errOut)

	fmt.Fprintf(out, "发布完成：%s -> #%d (%s)\n", t.DisplayLabel(), releaseID, version)
	return &DeployReport{
		App:           t.App,
		Target:        t.Name,
		Instance:      t.Instance,
		Kind:          ReleaseKindFull,
		ReleaseID:     releaseID,
		Version:       version,
		Commit:        buildMeta.Commit,
		Dirty:         buildMeta.Dirty,
		BinSHA256:     binSum,
		ConfigSHA256:  cfgSum,
		Arch:          arch,
		UploadedHosts: uploadedCount,
		ConfigFiles:   t.RemoteConfigFiles(),
		MigrateHost:   primary.Addr,
		Migrated:      migrated,
		Waves:         waves,
		Hosts:         switchResults,
	}, nil
}

// DeployConfigMany 对多个目标实例先统一校验本地配置，再按顺序串行执行仅配置热发布，任一目标失败立即熔断。
func DeployConfigMany(ctx context.Context, targets []Target, newRunner RunnerFactory, opts ConfigReleaseOptions, out, errOut io.Writer) (*BatchDeployReport, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("deploy: at least one target is required")
	}
	for _, t := range targets {
		_, _, has, err := packConfig(opts.WorkDir, t)
		if err != nil {
			return nil, fmt.Errorf("[%s] %w", t.Name, err)
		}
		if !has {
			return nil, fmt.Errorf("[%s] deploy: no configuration files found to deploy", t.Name)
		}
	}
	batch := &BatchDeployReport{
		App:     targets[0].App,
		Kind:    ReleaseKindConfig,
		DryRun:  opts.DryRun,
		Targets: make([]*DeployReport, 0, len(targets)),
	}
	for i, t := range targets {
		if i > 0 {
			fmt.Fprintln(out)
		}
		if opts.DryRun {
			rep, err := DeployConfig(ctx, nil, t, opts, out, errOut)
			if err != nil {
				return nil, fmt.Errorf("目标 %s 配置预演失败: %w", t.Name, err)
			}
			batch.Targets = append(batch.Targets, rep)
			continue
		}
		if len(targets) > 1 {
			fmt.Fprintf(out, "=== 配置发布目标 %d/%d: %s ===\n", i+1, len(targets), t.DisplayLabel())
		}
		r, closeFn, err := newRunner(t)
		if err != nil {
			return nil, fmt.Errorf("[%s] 创建 SSH 会话失败: %w", t.Name, err)
		}
		rep, err := DeployConfig(ctx, r, t, opts, out, errOut)
		if closeFn != nil {
			_ = closeFn()
		}
		if err != nil {
			return nil, fmt.Errorf("目标 %s 配置发布失败: %w", t.Name, err)
		}
		batch.Targets = append(batch.Targets, rep)
	}
	return batch, nil
}

// DeployConfig 仅同步配置文件到远端并零停机热重载：复用集群当前运行的二进制，不编译、不上传二进制、不执行迁移，
// 同时生成完整的不可变发布快照以支持随时回滚。
func DeployConfig(ctx context.Context, r Runner, t Target, opts ConfigReleaseOptions, out, errOut io.Writer) (*DeployReport, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cfgArchive, cfgSum, hasConfig, err := packConfig(opts.WorkDir, t)
	if err != nil {
		return nil, err
	}
	if !hasConfig {
		return nil, fmt.Errorf("deploy: no configuration files found to deploy")
	}
	if opts.DryRun {
		report := &DeployReport{
			App:          t.App,
			Target:       t.Name,
			Instance:     t.Instance,
			Kind:         ReleaseKindConfig,
			ConfigSHA256: cfgSum,
			DryRun:       true,
			ConfigFiles:  t.RemoteConfigFiles(),
			MigrateHost:  t.MigrateHost().Addr,
			Waves:        t.RolloutWaves(),
			Hosts:        plannedHosts(t),
		}
		printPlan(out, report)
		return report, nil
	}

	primary := t.MigrateHost()
	operator := CurrentOperator()
	releaseLock, err := acquireLock(ctx, r, t, primary, fmt.Sprintf("config (%s)", operator))
	if err != nil {
		return nil, err
	}
	defer releaseLock()

	// 读取当前集群运行的 manifest，确认线上已有版本且二进制 SHA-256 存在。
	curManifests, err := readClusterCurrentManifests(ctx, r, t)
	if err != nil {
		return nil, err
	}
	cur := curManifests[primary.Addr]
	if cur == nil || cur.BinSHA256 == "" {
		return nil, fmt.Errorf("[%s] 未检测到已部署的发布版本，请先执行一次完整的 tenon deploy", primary.Addr)
	}
	if !opts.Force && clusterMatchesDesired(t, curManifests, cur.BinSHA256, cfgSum) {
		runtimeChanged, err := checkAnyRuntimeChanged(ctx, r, t)
		if err != nil {
			return nil, err
		}
		if !runtimeChanged {
			fmt.Fprintf(out, "%s 配置文件未变化（#%d %s，cfg:%s），跳过发布（使用 --force 强制重载）。\n",
				t.DisplayLabel(), cur.ReleaseID, cur.Version, ShortSHA(cfgSum))
			hosts := make([]HostSwitchInfo, 0, len(t.Hosts))
			for _, h := range t.Hosts {
				hosts = append(hosts, HostSwitchInfo{
					Addr:   h.Addr,
					Roles:  h.Roles,
					Parts:  h.Parts,
					Action: "unchanged",
					Status: "skipped",
				})
			}
			return &DeployReport{
				App:          t.App,
				Target:       t.Name,
				Instance:     t.Instance,
				Kind:         ReleaseKindConfig,
				ReleaseID:    cur.ReleaseID,
				Version:      cur.Version,
				Commit:       cur.Commit,
				Dirty:        cur.Dirty,
				BinSHA256:    cur.BinSHA256,
				ConfigSHA256: cfgSum,
				Skipped:      true,
				ConfigFiles:  t.RemoteConfigFiles(),
				MigrateHost:  primary.Addr,
				Waves:        t.RolloutWaves(),
				Hosts:        hosts,
			}, nil
		}
	}

	// 分配新 releaseID，复用当前二进制对象创建轻量不可变配置发布。
	releaseID, err := nextReleaseID(ctx, r, t, primary)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{
		ReleaseID:    releaseID,
		Version:      cur.Version,
		Commit:       cur.Commit,
		Dirty:        cur.Dirty,
		BinSHA256:    cur.BinSHA256,
		ConfigSHA256: cfgSum,
		Kind:         ReleaseKindConfig,
		FromRelease:  cur.ReleaseID,
		DeployedAt:   now().UTC(),
		DeployedBy:   operator,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestBytes = append(manifestBytes, '\n')

	relName := FormatReleaseID(releaseID)
	releaseDir := path.Join(t.Dir, "releases", relName)
	remoteBin := path.Join(releaseDir, t.Instance)
	fmt.Fprintf(out, "准备仅配置发布 %s #%d（复用 %s / bin:%s，新配置 cfg:%s）到 %d 台主机...\n",
		t.DisplayLabel(), releaseID, cur.Version, ShortSHA(cur.BinSHA256), ShortSHA(cfgSum), len(t.Hosts))

	switched := false
	defer func() {
		if switched {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		removeReleaseAll(cleanupCtx, r, t, releaseDir, errOut)
	}()
	if _, err := prepareReleaseAll(ctx, r, t, releaseDir, remoteBin, "", cur.BinSHA256, cfgArchive, true, manifestBytes, out); err != nil {
		return nil, err
	}

	// 全机并行 config check，不执行数据库迁移。
	fmt.Fprintf(out, "并行校验 %d 台主机配置...\n", len(t.Hosts))
	if err := configCheckAll(ctx, r, t, releaseDir, remoteBin); err != nil {
		return nil, err
	}
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

	switched = true
	waves := t.RolloutWaves()
	histEntry := HistoryEntry{
		ReleaseID: releaseID,
		Version:   cur.Version,
		Kind:      ReleaseKindConfig,
		At:        manifest.DeployedAt,
		By:        operator,
	}
	switchResults, switchedAddrs, err := rolloutWaves(ctx, r, t, waves, releaseID, true, histEntry, out, errOut)
	if err != nil {
		if opts.AutoRollback && cur.ReleaseID > 0 && len(switchedAddrs) > 0 {
			autoRollbackHosts(ctx, r, t, switchedAddrs, cur.ReleaseID, operator, now().UTC(), out, errOut)
		}
		return nil, err
	}

	pruneCluster(ctx, r, t, releaseID, errOut)
	fmt.Fprintf(out, "配置发布完成：%s -> #%d（%s，cfg:%s）\n", t.DisplayLabel(), releaseID, cur.Version, ShortSHA(cfgSum))
	return &DeployReport{
		App:          t.App,
		Target:       t.Name,
		Instance:     t.Instance,
		Kind:         ReleaseKindConfig,
		ReleaseID:    releaseID,
		Version:      cur.Version,
		Commit:       cur.Commit,
		Dirty:        cur.Dirty,
		BinSHA256:    cur.BinSHA256,
		ConfigSHA256: cfgSum,
		ConfigFiles:  t.RemoteConfigFiles(),
		MigrateHost:  primary.Addr,
		Waves:        waves,
		Hosts:        switchResults,
	}, nil
}

// plannedHosts 构造用于预演报告的主机列表。
func plannedHosts(t Target) []HostSwitchInfo {
	hosts := make([]HostSwitchInfo, 0, len(t.Hosts))
	for _, h := range t.Hosts {
		hosts = append(hosts, HostSwitchInfo{
			Addr:   h.Addr,
			Roles:  h.Roles,
			Parts:  h.Parts,
			Status: "planned",
		})
	}
	return hosts
}

// resolveBuildVersion 按优先级推导构建版本号：显式 Version > Tag > Git describe。
func resolveBuildVersion(opts ReleaseOptions, gitInfo GitBuildInfo) string {
	if v := strings.TrimSpace(opts.Version); v != "" {
		return v
	}
	if tag := strings.TrimSpace(opts.Tag); tag != "" {
		return tag
	}
	if gitInfo.Version != "" {
		return gitInfo.Version
	}
	return "dev"
}

// printPlan 以人类可读文本打印发布预演计划。
func printPlan(out io.Writer, plan *DeployReport) {
	label := fmt.Sprintf("目标 %s，实例 %s", plan.Target, plan.Instance)
	if plan.Instance != plan.App {
		label += fmt.Sprintf("（产物 %s）", plan.App)
	}
	if plan.Kind == ReleaseKindConfig {
		fmt.Fprintf(out, "[dry-run] 配置发布：%s，配置摘要 %s\n", label, ShortSHA(plan.ConfigSHA256))
	} else {
		fmt.Fprintf(out, "[dry-run] %s，版本 %s，架构 linux/%s\n", label, plan.Version, plan.Arch)
	}
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

// readClusterCurrentManifests 并发读取集群全部主机当前激活的 current/manifest.json；尚未部署的主机对应值为 nil。
func readClusterCurrentManifests(ctx context.Context, r Runner, t Target) (map[string]*Manifest, error) {
	var mu sync.Mutex
	out := make(map[string]*Manifest, len(t.Hosts))
	manifestPath := path.Join(t.Dir, "current", "manifest.json")
	cmd := fmt.Sprintf("if test -f %s; then cat %s; fi", shellQuote(manifestPath), shellQuote(manifestPath))
	g, gctx := errgroup.WithContext(ctx)
	for _, h := range t.Hosts {
		g.Go(func() error {
			raw, err := RunCapture(gctx, r, h, cmd)
			if err != nil {
				return fmt.Errorf("[%s] 读取当前发布清单失败: %w", h.Addr, err)
			}
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				return nil
			}
			var m Manifest
			if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
				return fmt.Errorf("[%s] 解析 %s 失败: %w", h.Addr, manifestPath, err)
			}
			mu.Lock()
			out[h.Addr] = &m
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// clusterMatchesDesired 报告集群所有主机当前运行的二进制与配置哈希是否均已与期望值一致。
func clusterMatchesDesired(t Target, manifests map[string]*Manifest, binSum, cfgSum string) bool {
	for _, h := range t.Hosts {
		m := manifests[h.Addr]
		if m == nil || m.BinSHA256 != binSum || m.ConfigSHA256 != cfgSum {
			return false
		}
	}
	return true
}

// checkAnyRuntimeChanged 并发检查任意目标主机的 systemd 模板 unit、drop-in 或运维包装脚本是否与期望内容存在差异。
func checkAnyRuntimeChanged(ctx context.Context, r Runner, t Target) (bool, error) {
	unitContent := RenderUnit(t)
	wrapperContent := RenderWrapper(t)
	unitPath := TemplateUnitPath(t.Instance)
	wrapperPath := WrapperPath(t.Instance)

	var mu sync.Mutex
	changed := false
	g, gctx := errgroup.WithContext(ctx)
	for _, h := range t.Hosts {
		g.Go(func() error {
			uDiff, err := remoteFileDiffers(gctx, r, h, unitPath, unitContent)
			if err != nil {
				return err
			}
			if uDiff {
				mu.Lock()
				changed = true
				mu.Unlock()
				return nil
			}
			wDiff, err := remoteFileDiffers(gctx, r, h, wrapperPath, wrapperContent)
			if err != nil {
				return err
			}
			if wDiff {
				mu.Lock()
				changed = true
				mu.Unlock()
				return nil
			}
			for _, part := range h.Parts {
				dp := DropInPath(t.Instance, part)
				di := h.DropIns[part]
				if di.Empty() {
					res, err := RunCapture(gctx, r, h, fmt.Sprintf("if sudo test -f %s; then echo exists; fi", shellQuote(dp)))
					if err != nil {
						return err
					}
					if strings.TrimSpace(res) == "exists" {
						mu.Lock()
						changed = true
						mu.Unlock()
						return nil
					}
					continue
				}
				dDiff, err := remoteFileDiffers(gctx, r, h, dp, RenderDropIn(di))
				if err != nil {
					return err
				}
				if dDiff {
					mu.Lock()
					changed = true
					mu.Unlock()
					return nil
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return false, err
	}
	return changed, nil
}

// remoteFileDiffers 检查远端文件 targetPath 的内容是否与 want 不同（不存在视为不同）。
func remoteFileDiffers(ctx context.Context, r Runner, h Host, targetPath, want string) (bool, error) {
	script := fmt.Sprintf(`set -e
umask 077
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
if ! sudo cmp -s "$tmp" %s; then
  echo diff
fi`, shellQuote(targetPath))
	var out, errBuf strings.Builder
	if err := r.Run(ctx, h, script, strings.NewReader(want), &out, &errBuf); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return false, fmt.Errorf("[%s] 比对 %s 失败: %w: %s", h.Addr, targetPath, err, msg)
		}
		return false, fmt.Errorf("[%s] 比对 %s 失败: %w", h.Addr, targetPath, err)
	}
	return strings.TrimSpace(out.String()) == "diff", nil
}

// nextReleaseID 读取主节点 releases/ 目录中已有的最大整数发布序号并返回 max+1。
func nextReleaseID(ctx context.Context, r Runner, t Target, primary Host) (int, error) {
	releasesDir := path.Join(t.Dir, "releases")
	raw, err := RunCapture(ctx, r, primary, fmt.Sprintf("ls -1 %s 2>/dev/null || true", shellQuote(releasesDir)))
	if err != nil {
		return 0, fmt.Errorf("[%s] 读取历史发布列表失败: %w", primary.Addr, err)
	}
	maxID := 0
	for line := range strings.Lines(raw) {
		if id, ok := ParseReleaseID(line); ok && id > maxID {
			maxID = id
		}
	}
	return maxID + 1, nil
}

// prepareReleaseAll 并发在全部主机上准备发布目录：
// 先检查内容寻址对象池 artifacts/<binSum> 是否已存在，缺失时以 gzip 压缩流式上传并校验 SHA-256，
// 再创建 releases/<id>/、硬链接二进制、解压配置文件并写入 manifest.json；返回实际上传二进制的主机数。
func prepareReleaseAll(ctx context.Context, r Runner, t Target, releaseDir, remoteBin, localBin, binSum string, cfgArchive []byte, hasConfig bool, manifestBytes []byte, out io.Writer) (int, error) {
	artifactsDir := path.Join(t.Dir, "artifacts")
	artifactPath := path.Join(artifactsDir, binSum)
	var mu sync.Mutex
	uploadedCount := 0

	g, gctx := errgroup.WithContext(ctx)
	for _, h := range t.Hosts {
		g.Go(func() error {
			// 1. 内容寻址存在性检查：远端已有相同 SHA-256 二进制时跳过网络传输。
			res, err := RunCapture(gctx, r, h, fmt.Sprintf("if test -x %s; then echo hit; else echo miss; fi", shellQuote(artifactPath)))
			if err != nil {
				return fmt.Errorf("[%s] 检查二进制缓存失败: %w", h.Addr, err)
			}
			if strings.TrimSpace(res) != "hit" {
				if localBin == "" {
					return fmt.Errorf("[%s] 远端缺少二进制对象 %s，请执行完整 tenon deploy", h.Addr, ShortSHA(binSum))
				}
				if err := streamUploadArtifact(gctx, r, h, localBin, artifactsDir, artifactPath, binSum); err != nil {
					return fmt.Errorf("[%s] 上传二进制失败: %w", h.Addr, err)
				}
				mu.Lock()
				uploadedCount++
				fmt.Fprintf(out, "[%s] 已压缩上传二进制 (%s)\n", h.Addr, ShortSHA(binSum))
				mu.Unlock()
			} else {
				mu.Lock()
				fmt.Fprintf(out, "[%s] 命中远端二进制缓存 (%s)，跳过上传\n", h.Addr, ShortSHA(binSum))
				mu.Unlock()
			}

			// 2. 创建发布目录并从对象池硬链接二进制（零额外磁盘占用）。
			linkScript := fmt.Sprintf("set -e; sudo install -d -m 755 %s; sudo ln -f %s %s",
				shellQuote(releaseDir), shellQuote(artifactPath), shellQuote(remoteBin))
			if _, err := RunCapture(gctx, r, h, linkScript); err != nil {
				return fmt.Errorf("[%s] 链接发布二进制失败: %w", h.Addr, err)
			}

			// 3. 同步不可变配置快照（目录 0750、文件 0640，属主 root:<service_user>）。
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

			// 4. 写入不可变 manifest.json。
			manifestPath := path.Join(releaseDir, "manifest.json")
			if err := r.Run(gctx, h, installFromStdinCommand("644", manifestPath), bytes.NewReader(manifestBytes), io.Discard, io.Discard); err != nil {
				return fmt.Errorf("[%s] 写入 manifest.json 失败: %w", h.Addr, err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return uploadedCount, err
	}
	return uploadedCount, nil
}

// streamUploadArtifact 以 gzip 压缩流式传输 localBin 到远端临时文件，解压并校验 SHA-256 后原子安装到 artifactPath。
func streamUploadArtifact(ctx context.Context, r Runner, h Host, localBin, artifactsDir, artifactPath, wantSHA string) error {
	pr, pw := io.Pipe()
	go func() {
		f, err := os.Open(localBin)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		defer func() { _ = f.Close() }()
		gz := gzip.NewWriter(pw)
		if _, err := io.Copy(gz, f); err != nil {
			_ = gz.Close()
			_ = pw.CloseWithError(err)
			return
		}
		if err := gz.Close(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()

	script := fmt.Sprintf(`set -e
umask 077
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
gzip -dc > "$tmp"
got=$(sha256sum "$tmp" | awk '{print $1}')
test "$got" = %s || { echo "sha256 mismatch: got $got want %s" >&2; exit 1; }
sudo install -d -m 755 %s
sudo install -m 755 -o root -g root "$tmp" %s`,
		shellQuote(wantSHA), wantSHA,
		shellQuote(artifactsDir),
		shellQuote(artifactPath),
	)
	var errBuf bytes.Buffer
	err := r.Run(ctx, h, script, pr, io.Discard, &errBuf)
	_ = pr.Close()
	if err != nil {
		if msg := strings.TrimSpace(errBuf.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// configCheckAll 在全部目标主机并行执行新版本的离线 config check，提前发现单机环境变量或配置错误。
func configCheckAll(ctx context.Context, r Runner, t Target, releaseDir, remoteBin string) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, h := range t.Hosts {
		g.Go(func() error {
			if _, err := RunCapture(gctx, r, h, systemdRunCommand(t, releaseDir, remoteBin, "config", "check")); err != nil {
				return fmt.Errorf("[%s] config check 失败: %w", h.Addr, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// rolloutWaves 按波次执行主机切换，波次内按 MaxParallel 控制并发度；任一主机失败立即中止后续波次，
// 返回已完成切换的主机详情和地址列表（供失败时自动回滚）。
func rolloutWaves(ctx context.Context, r Runner, t Target, waves []Wave, releaseID int, hasConfig bool, hist HistoryEntry, out, errOut io.Writer) ([]HostSwitchInfo, []string, error) {
	var mu sync.Mutex
	results := make(map[string]HostSwitchInfo, len(t.Hosts))
	var switchedAddrs []string

	for i, w := range waves {
		fmt.Fprintf(out, "波次 %d/%d [%s]（并发 %d）：%s\n", i+1, len(waves), w.Name, w.MaxParallel, strings.Join(w.Hosts, ", "))
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(max(w.MaxParallel, 1))
		for _, addr := range w.Hosts {
			h, ok := t.HostByAddr(addr)
			if !ok {
				return nil, switchedAddrs, fmt.Errorf("deploy: unknown host %q in wave %q", addr, w.Name)
			}
			g.Go(func() error {
				action, removed, err := switchHost(gctx, r, t, h, releaseID, hasConfig, hist, out, errOut)
				if err != nil {
					return err
				}
				mu.Lock()
				results[h.Addr] = HostSwitchInfo{
					Addr:         h.Addr,
					Roles:        h.Roles,
					Parts:        h.Parts,
					Action:       action,
					RemovedUnits: removed,
					Status:       "active",
				}
				switchedAddrs = append(switchedAddrs, h.Addr)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, switchedAddrs, err
		}
	}

	ordered := make([]HostSwitchInfo, 0, len(t.Hosts))
	for _, h := range t.Hosts {
		ordered = append(ordered, results[h.Addr])
	}
	return ordered, switchedAddrs, nil
}

// autoRollbackHosts 在波次中途失败后，尽力将已切换成功的主机回退到 fromRelease。
func autoRollbackHosts(ctx context.Context, r Runner, t Target, addrs []string, fromRelease int, operator string, now time.Time, out, errOut io.Writer) {
	fmt.Fprintf(errOut, "检测到波次失败，自动将已切换的 %d 台主机回退到 #%d...\n", len(addrs), fromRelease)
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	relDir := path.Join(t.Dir, "releases", FormatReleaseID(fromRelease))
	hist := HistoryEntry{
		ReleaseID: fromRelease,
		Kind:      ReleaseKindRollback,
		At:        now,
		By:        operator,
	}
	for _, addr := range addrs {
		h, ok := t.HostByAddr(addr)
		if !ok {
			continue
		}
		hasCfgRaw, _ := RunCapture(rbCtx, r, h, fmt.Sprintf("if test -d %s; then echo yes; fi", shellQuote(path.Join(relDir, "config"))))
		hasCfg := strings.TrimSpace(hasCfgRaw) == "yes"
		if _, _, err := switchHost(rbCtx, r, t, h, fromRelease, hasCfg, hist, out, errOut); err != nil {
			fmt.Fprintf(errOut, "[%s] 自动回退失败: %v\n", h.Addr, err)
		} else {
			fmt.Fprintf(out, "[%s] 已自动回退到 #%d\n", h.Addr, fromRelease)
		}
	}
}

// resolveArch 并发探测全部目标主机架构并确认一致；配置已指定时直接返回。
func resolveArch(ctx context.Context, r Runner, t Target) (string, error) {
	if t.Arch != "" {
		return t.Arch, nil
	}
	arches := make([]string, len(t.Hosts))
	g, gctx := errgroup.WithContext(ctx)
	for i, h := range t.Hosts {
		g.Go(func() error {
			raw, err := RunCapture(gctx, r, h, "uname -m")
			if err != nil {
				return fmt.Errorf("[%s] 探测目标架构失败: %w", h.Addr, err)
			}
			a, err := normalizeArch(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("[%s] %w", h.Addr, err)
			}
			arches[i] = a
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}
	detected := arches[0]
	for _, a := range arches[1:] {
		if a != detected {
			return "", fmt.Errorf("deploy: mixed host architectures (%s and %s); deploy them in separate targets", detected, a)
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

// isSafeVersion 检查构建版本号只包含字母、数字、点、下划线、加号和连字符。
func isSafeVersion(v string) bool {
	if v == "" || strings.HasPrefix(v, ".") {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '+':
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

// configEntry 是用于打包与确定性哈希计算的单条配置文件记录。
type configEntry struct {
	rel  string
	data []byte
}

// packConfig 根据目标配置打包待同步的配置文件并计算确定性内容哈希：
// 配置了 config_files 时按清单精确打包；配置了 config_dir 时打包该目录并跳过 local.yaml 与隐藏文件；均未配置时返回 has=false。
func packConfig(workDir string, t Target) (archive []byte, sum string, has bool, err error) {
	var entries []configEntry
	switch {
	case len(t.ConfigFiles) > 0:
		entries, err = collectConfigFiles(workDir, t.ConfigFiles)
		if err != nil {
			return nil, "", false, err
		}
	case t.ConfigDir != "":
		entries, err = collectConfigDir(resolveLocalPath(workDir, t.ConfigDir))
		if err != nil {
			return nil, "", false, err
		}
	default:
		return nil, "", false, nil
	}
	if len(entries) == 0 {
		return nil, "", false, nil
	}
	// 按路径排序以计算确定性内容哈希并生成可复现 tar.gz。
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b configEntry) int { return strings.Compare(a.rel, b.rel) })

	h := sha256.New()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range sorted {
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", e.rel, len(e.data))
		_, _ = h.Write(e.data)
		hdr := &tar.Header{
			Name:    e.rel,
			Mode:    0o640,
			Size:    int64(len(e.data)),
			ModTime: time.Unix(0, 0).UTC(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, "", false, err
		}
		if _, err := tw.Write(e.data); err != nil {
			return nil, "", false, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", false, err
	}
	if err := gz.Close(); err != nil {
		return nil, "", false, err
	}
	return buf.Bytes(), hex.EncodeToString(h.Sum(nil)), true, nil
}

// resolveLocalPath 将相对路径 p 锚定到 workDir；workDir 为空或 p 为绝对路径时直接返回 p。
func resolveLocalPath(workDir, p string) string {
	if workDir == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, filepath.FromSlash(p))
}

// collectConfigFiles 读取 config_files 清单中的全部本地配置文件；任一文件缺失或为目录时返回错误。
func collectConfigFiles(workDir string, files []ConfigFile) ([]configEntry, error) {
	entries := make([]configEntry, 0, len(files))
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
		entries = append(entries, configEntry{rel: f.ArchiveRel, data: data})
	}
	return entries, nil
}

// collectConfigDir 遍历本地配置目录并收集全部普通文件，自动跳过 local.yaml 与隐藏文件；目录不存在时返回空切片。
func collectConfigDir(dir string) ([]configEntry, error) {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("deploy: stat config_dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("deploy: config_dir %s is not a directory", dir)
	}
	var entries []configEntry
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
		entries = append(entries, configEntry{rel: filepath.ToSlash(rel), data: data})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("deploy: pack config_dir %s: %w", dir, walkErr)
	}
	return entries, nil
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

// switchHost 将单台主机原子切换到 releaseID 对应的二进制与配置，同步 unit/drop-in/wrapper，
// 清理孤儿 unit，自动按变更类型执行 reload 或 restart，记录历史并校验入口 health。
func switchHost(ctx context.Context, r Runner, t Target, h Host, releaseID int, hasConfig bool, hist HistoryEntry, out, errOut io.Writer) (string, []string, error) {
	relName := FormatReleaseID(releaseID)
	releaseDir := path.Join(t.Dir, "releases", relName)
	releaseBin := path.Join(releaseDir, t.Instance)
	liveBin := path.Join(t.Dir, "bin", t.Instance)
	liveBinNew := liveBin + ".new"

	// 1. 幂等同步模板 unit、角色级 drop-in 与 /usr/local/bin/<instance> 运维包装脚本。
	unitUpdated, err := syncUnit(ctx, r, t, h)
	if err != nil {
		return "", nil, err
	}
	dropInUpdated, err := syncDropIns(ctx, r, t, h)
	if err != nil {
		return "", nil, err
	}
	if _, err := syncWrapper(ctx, r, t, h); err != nil {
		return "", nil, err
	}
	needRestart := unitUpdated || dropInUpdated

	// 2. 原子替换二进制（硬链+mv，兼顾 tableflip 的 /proc/self/exe）、config 软链与 current 软链，并追加 history.jsonl。
	histLine, err := json.Marshal(hist)
	if err != nil {
		return "", nil, err
	}
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
	liveCur := path.Join(t.Dir, "current")
	liveCurNew := liveCur + ".new"
	fmt.Fprintf(&swap, "sudo ln -sfn %s %s\n", shellQuote(releaseDir), shellQuote(liveCurNew))
	fmt.Fprintf(&swap, "sudo mv -Tf %s %s\n", shellQuote(liveCurNew), shellQuote(liveCur))
	fmt.Fprintf(&swap, "printf '%%s\\n' %s | sudo tee -a %s >/dev/null\n",
		shellQuote(string(histLine)), shellQuote(path.Join(t.Dir, "history.jsonl")))
	if _, err := RunCapture(ctx, r, h, swap.String()); err != nil {
		return "", nil, fmt.Errorf("[%s] 切换二进制与配置失败: %w", h.Addr, err)
	}

	// 3. 声明式对账：停用并禁用不再属于当前主机期望 parts 的旧 unit。
	removed, err := reconcileOrphanUnits(ctx, r, t, h, out)
	if err != nil {
		return "", nil, err
	}

	// 4. 启动或切换期望的每个 unit：unit 或 drop-in 变更时自动执行 restart 使新参数/环境变量生效，其余执行零停机 reload。
	action := "reload"
	if needRestart {
		action = "restart"
	}
	for _, part := range h.Parts {
		unit := UnitName(t.Instance, part)
		var unitScript string
		if needRestart {
			unitScript = fmt.Sprintf(`set -e
if sudo systemctl is-active --quiet %s; then
  sudo systemctl restart %s
else
  sudo systemctl enable --now %s
fi`, shellQuote(unit), shellQuote(unit), shellQuote(unit))
		} else {
			unitScript = fmt.Sprintf(`set -e
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
		}
		if _, err := RunCapture(ctx, r, h, unitScript); err != nil {
			_, _ = fmt.Fprintf(errOut, "[%s] %s 切换失败，最近日志：\n", h.Addr, unit)
			_ = r.Run(ctx, h, fmt.Sprintf("sudo journalctl -u %s -n 30 --no-pager", shellQuote(unit)), nil, errOut, errOut)
			return action, removed, fmt.Errorf("[%s] %s 启动或切换失败: %w", h.Addr, unit, err)
		}
		if needRestart {
			fmt.Fprintf(out, "[%s] %s 已重启就绪（因 unit/drop-in 变更）\n", h.Addr, unit)
		} else {
			fmt.Fprintf(out, "[%s] %s 已热重载就绪\n", h.Addr, unit)
		}
	}

	// 5. 含入口服务的节点执行本机 health 检查确认 /livez 通过。
	if h.HasHTTP() {
		hasHealth, err := remoteHasSubcommand(ctx, r, h, liveBin, "health")
		if err == nil && hasHealth {
			if _, err := RunCapture(ctx, r, h, systemdRunCommand(t, t.Dir, liveBin, "health")); err != nil {
				return action, removed, fmt.Errorf("[%s] health 检查失败: %w", h.Addr, err)
			}
		}
	}
	return action, removed, nil
}

// reconcileOrphanUnits 查询主机上当前运行中或已启用的 <instance>@*.service，停止并禁用不在期望列表中的旧 unit。
func reconcileOrphanUnits(ctx context.Context, r Runner, t Target, h Host, out io.Writer) ([]string, error) {
	pattern := t.Instance + "@*.service"
	listCmd := fmt.Sprintf("systemctl list-units %s --state=active,activating,failed --plain --no-legend 2>/dev/null | awk '{print $1}'", shellQuote(pattern))
	raw, err := RunCapture(ctx, r, h, listCmd)
	if err != nil {
		return nil, fmt.Errorf("[%s] 查询运行中 unit 失败: %w", h.Addr, err)
	}
	desired := make(map[string]bool, len(h.Parts))
	for _, p := range h.Parts {
		desired[UnitName(t.Instance, p)] = true
	}
	var orphans []string
	prefix := t.Instance + "@"
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

// pruneCluster 并行清理全部主机上超出 keep_releases 的历史发布目录，并回收不再被引用的 artifacts 二进制对象。
func pruneCluster(ctx context.Context, r Runner, t Target, currentID int, errOut io.Writer) {
	var g errgroup.Group
	for _, h := range t.Hosts {
		g.Go(func() error {
			if err := pruneReleases(ctx, r, t, h, currentID); err != nil {
				fmt.Fprintf(errOut, "[%s] 警告：清理旧版本失败: %v\n", h.Addr, err)
			}
			return nil
		})
	}
	_ = g.Wait()
}

// pruneReleases 按数值序号清理单台主机上超出 keep_releases 的历史发布目录（始终保留 currentID），
// 再对 artifacts/ 目录执行引用计数垃圾回收。
func pruneReleases(ctx context.Context, r Runner, t Target, h Host, currentID int) error {
	releasesDir := path.Join(t.Dir, "releases")
	raw, err := RunCapture(ctx, r, h, fmt.Sprintf("ls -1 %s 2>/dev/null || true", shellQuote(releasesDir)))
	if err != nil {
		return err
	}
	type relDir struct {
		id   int
		name string
	}
	var items []relDir
	for line := range strings.Lines(raw) {
		name := strings.TrimSpace(line)
		if id, ok := ParseReleaseID(name); ok {
			items = append(items, relDir{id: id, name: name})
		}
	}
	slices.SortFunc(items, func(a, b relDir) int { return a.id - b.id })
	if len(items) > t.KeepReleases {
		dropCount := len(items) - t.KeepReleases
		var toDelete []string
		for _, item := range items {
			if len(toDelete) >= dropCount {
				break
			}
			if item.id == currentID {
				continue
			}
			toDelete = append(toDelete, shellQuote(path.Join(releasesDir, item.name)))
		}
		if len(toDelete) > 0 {
			if _, err := RunCapture(ctx, r, h, "sudo rm -rf "+strings.Join(toDelete, " ")); err != nil {
				return err
			}
		}
	}

	// 读取留存 release 的 manifest.json，回收 artifacts/ 下不再被引用的旧二进制对象。
	artifactsDir := path.Join(t.Dir, "artifacts")
	gcScript := fmt.Sprintf(`set -e
if [ ! -d %s ]; then exit 0; fi
keep=$(grep -h '"bin_sha256"' %s/*/manifest.json 2>/dev/null | sed -E 's/.*"bin_sha256"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/' | sort -u || true)
for f in %s/*; do
  [ -f "$f" ] || continue
  base=$(basename "$f")
  if ! printf '%%s\n' "$keep" | grep -Fxq "$base"; then
    sudo rm -f "$f"
  fi
done`, shellQuote(artifactsDir), shellQuote(releasesDir), shellQuote(artifactsDir))
	_, err = RunCapture(ctx, r, h, gcScript)
	return err
}

// removeReleaseAll 并行清理全部主机上本次未切换成功的发布目录，单机清理失败时输出警告。
func removeReleaseAll(ctx context.Context, r Runner, t Target, releaseDir string, errOut io.Writer) {
	var g errgroup.Group
	cmd := "sudo rm -rf " + shellQuote(releaseDir)
	for _, h := range t.Hosts {
		g.Go(func() error {
			if _, err := RunCapture(ctx, r, h, cmd); err != nil {
				fmt.Fprintf(errOut, "[%s] 警告：清理失败发布目录 %s 失败: %v\n", h.Addr, releaseDir, err)
			}
			return nil
		})
	}
	_ = g.Wait()
}
