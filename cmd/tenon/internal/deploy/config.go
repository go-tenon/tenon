// Package deploy 实现 tenon deploy：基于 SSH 和 systemd 的原生单二进制零停机部署与远端运维，
// 以 deploy/app.yaml（产物与公共默认值）+ deploy/targets/<name>/（每套独立实例）分层组织，
// 支持任意实例命名（app 与 instance 分离）、多服务角色拓扑、一次构建多目标安全滚动以及人类和 AI 双模输出。
package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

const (
	// DefaultDir 是项目根目录默认的部署配置目录名。
	DefaultDir = "deploy"
	// AppFileName 是应用级公共部署声明文件名（位于部署根目录下，提交到仓库）。
	AppFileName = "app.yaml"
	// TargetsDirName 是存放各独立部署实例目录的子目录名（默认整体加入 .gitignore）。
	TargetsDirName = "targets"
	// TargetFileName 是单个部署实例的配置文件名。
	TargetFileName = "target.yaml"
	// DefaultTargetConfig 是单个部署实例目录下自动识别的默认应用配置文件名。
	DefaultTargetConfig = "config.yaml"
	// EnvTargetsDir 是覆盖部署实例目录路径的环境变量名，用于指向外部私有运维仓库。
	EnvTargetsDir = "TENON_DEPLOY_TARGETS"
	// DefaultKeepReleases 是各节点默认保留的历史发布目录数量。
	DefaultKeepReleases = 5
	// DefaultSSHUser 是未填写 SSH 登录用户时的默认值。
	DefaultSSHUser = "deploy"
)

// safeName 限制产物名、实例名、目标名、服务用户名、角色名和服务部分名只包含小写字母、数字和连字符。
var safeName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// safeUserName 限制只读观测用户名符合 Linux 系统用户名规范。
var safeUserName = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

// safeEnvKey 限制角色级 systemd drop-in 环境变量键为标准 POSIX 环境变量名。
var safeEnvKey = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// AppFile 是 deploy/app.yaml 的顶层结构，描述构建产物信息与各部署实例共享的默认值（进仓库，不含主机和密钥）。
type AppFile struct {
	App          string         `yaml:"app"`           // 构建产物与 Release 资产名，留空时取项目入口包目录名
	Repo         string         `yaml:"repo"`          // GitHub 仓库（owner/repo），供 --tag 拉取 Release 产物；留空时从 git origin 推导
	Arch         string         `yaml:"arch"`          // 默认目标 Linux 架构（amd64、arm64）；留空时自动探测
	KeepReleases int            `yaml:"keep_releases"` // 默认保留的历史发布目录数量，默认 5
	HasSSR       *bool          `yaml:"has_ssr"`       // 是否包含服务端渲染前端（setup 时检查 glibc）；留空时由项目配置推导
	Defaults     TargetDefaults `yaml:"defaults"`      // 所有部署实例继承的默认参数
}

// TargetDefaults 是 deploy/app.yaml 中供各实例继承的公共默认值，可在 target.yaml 中逐项覆盖。
type TargetDefaults struct {
	User                string   `yaml:"user"`                  // 默认 SSH 登录用户，留空时为 deploy
	Port                int      `yaml:"port"`                  // 默认 SSH 端口，0 表示沿用 ssh 配置
	IdentityFile        string   `yaml:"identity_file"`         // 默认 SSH 私钥路径（相对路径以 app.yaml 目录为基准）
	ObserveUser         string   `yaml:"observe_user"`          // 可选：默认只读观测 SSH 用户（用于 status/logs/releases 与 tenon mcp），留空时沿用 user
	ObserveIdentityFile string   `yaml:"observe_identity_file"` // 可选：默认只读观测 SSH 私钥路径，留空时沿用 identity_file
	ConfigFiles         []string `yaml:"config_files"`          // 默认叠加的配置文件路径（以 app.yaml 目录为基准）
}

// TargetFile 是 deploy/targets/<name>/target.yaml 的结构，描述一套独立部署实例的身份、拓扑与运行参数。
type TargetFile struct {
	Instance            string          `yaml:"instance"`              // 远端实例名（驱动 systemd unit、用户、目录、二进制和运维命令），留空时默认等于 app
	Arch                string          `yaml:"arch"`                  // 覆盖默认目标 Linux 架构（amd64、arm64）
	KeepReleases        int             `yaml:"keep_releases"`         // 覆盖默认保留发布数
	User                string          `yaml:"user"`                  // SSH 登录用户，留空继承 defaults.user
	Port                int             `yaml:"port"`                  // SSH 端口，0 继承 defaults.port
	IdentityFile        string          `yaml:"identity_file"`         // SSH 私钥路径，留空继承 defaults.identity_file
	ObserveUser         string          `yaml:"observe_user"`          // 可选：只读观测 SSH 用户，留空继承 defaults.observe_user，仍为空时沿用 user
	ObserveIdentityFile string          `yaml:"observe_identity_file"` // 可选：只读观测 SSH 私钥路径，留空继承 defaults.observe_identity_file
	ServiceUser         string          `yaml:"service_user"`          // 运行应用的 Linux 系统用户，默认与 instance 相同
	Dir                 string          `yaml:"dir"`                   // 远端部署目录，默认 /opt/<instance>
	EnvFile             string          `yaml:"env_file"`              // 可选：远端环境变量文件路径；显式填写时严格要求存在，省略时默认 /etc/<instance>/env（systemd 允许缺失）
	ConfigDir           string          `yaml:"config_dir"`            // 要同步的本地配置目录（相对于 target.yaml 所在目录），与 config_files 互斥
	ConfigFiles         []string        `yaml:"config_files"`          // 按顺序叠加加载的本地配置文件列表（相对于 target.yaml 所在目录）；留空时自动识别同目录 config.yaml
	MaintenanceFile     string          `yaml:"maintenance_file"`      // 反向代理维护页标记文件路径，可选
	MigrateHost         string          `yaml:"migrate_host"`          // 执行预检与数据库迁移的主机地址，可选
	Hosts               []string        `yaml:"hosts"`                 // 单机或同构简写：所有主机运行 all
	Roles               map[string]Role `yaml:"roles"`                 // 多服务角色拓扑：键为角色名（如 http、worker、scheduler）
}

// LoadOptions 控制部署配置的读取位置与默认产物名。
type LoadOptions struct {
	Dir        string // 部署根目录，留空时默认 deploy
	TargetsDir string // 目标实例目录，留空时依次取 TENON_DEPLOY_TARGETS 环境变量和 <Dir>/targets
	DefaultApp string // app.yaml 未写 app 时的默认产物名（通常取项目入口目录名）
}

// Role 定义一个服务角色运行哪些 serve 部分、落在哪些主机上，以及该角色专用的滚动和 systemd 参数。
type Role struct {
	Parts       []string          `yaml:"parts" json:"parts"`                         // 该角色运行的 serve 部分名，省略时默认为 [角色名]
	Hosts       []string          `yaml:"hosts" json:"hosts"`                         // 承担该角色的主机地址列表
	MaxParallel int               `yaml:"max_parallel" json:"max_parallel,omitempty"` // 金丝雀之后每批并发滚动的主机数，默认 1
	StopTimeout string            `yaml:"stop_timeout" json:"stop_timeout,omitempty"` // systemd TimeoutStopSec 覆盖值（例如 120s）
	Env         map[string]string `yaml:"env" json:"env,omitempty"`                   // 角色级非密钥环境变量，写入 systemd drop-in
}

// PartDropIn 是单个服务部分（如 worker）需要写入 systemd drop-in 的覆盖配置。
type PartDropIn struct {
	StopTimeout string            `json:"stop_timeout,omitempty"` // TimeoutStopSec 覆盖值
	Env         map[string]string `json:"env,omitempty"`          // 附加环境变量
}

// Empty 报告该 drop-in 是否为空（无需生成 override.conf）。
func (d PartDropIn) Empty() bool {
	return d.StopTimeout == "" && len(d.Env) == 0
}

// Host 是一台去重合并后的目标主机：当同一台主机出现在多个角色下时，它的角色与 parts 会自动归并。
type Host struct {
	Addr        string                `json:"addr"`         // 主机地址或 ~/.ssh/config 中的别名
	Roles       []string              `json:"roles"`        // 该主机承担的角色名列表
	Parts       []string              `json:"parts"`        // 该主机期望运行的 serve 部分列表
	DropIns     map[string]PartDropIn `json:"-"`            // 各 part 对应的 systemd drop-in 覆盖项
	MaxParallel int                   `json:"max_parallel"` // 该主机所在角色允许的批次并发度
	Migrate     bool                  `json:"migrate"`      // 是否为执行预检与数据库迁移的主节点
}

// HasHTTP 报告该主机是否运行入口 HTTP 服务（all 或 http）。
func (h Host) HasHTTP() bool {
	return slices.Contains(h.Parts, "all") || slices.Contains(h.Parts, "http")
}

// Wave 描述滚动发布中的一个波次。
type Wave struct {
	Name        string   `json:"name"`         // 波次名称，如 backend-canary、backend、http-canary、http
	Hosts       []string `json:"hosts"`        // 本波次切换的主机地址
	MaxParallel int      `json:"max_parallel"` // 本波次最大并发切换数
}

// ConfigFile 描述一个按清单同步的本地配置文件及其在远端发布目录中的映射路径。
type ConfigFile struct {
	LocalPath  string `json:"local_path"`  // 本地真实文件路径（已锚定到声明它的 YAML 所在目录）
	ArchiveRel string `json:"archive_rel"` // 远端 releases/<id>/config 内的相对路径，例如 config.yaml
	RemotePath string `json:"remote_path"` // 远端工作目录下的相对路径，例如 config/config.yaml
}

// Target 是补齐默认值、按主机归并角色并通过校验的单个部署目标实例。
type Target struct {
	App                 string          `json:"app"`                    // 构建产物与 Release 资产名
	Name                string          `json:"target"`                 // 目标名（deploy/targets/<target> 目录名）
	Instance            string          `json:"instance"`               // 远端实例名（用于 systemd unit、目录、用户、二进制与运维命令）
	Repo                string          `json:"repo,omitempty"`         // GitHub 仓库（owner/repo）
	Arch                string          `json:"arch,omitempty"`         // 目标架构，为空表示由远端 uname -m 探测
	KeepReleases        int             `json:"keep_releases"`          // 保留的历史发布目录数量
	HasSSR              bool            `json:"has_ssr,omitempty"`      // 目标应用是否包含服务端渲染前端
	User                string          `json:"user"`                   // SSH 登录用户
	Port                int             `json:"port,omitempty"`         // SSH 端口
	IdentityFile        string          `json:"-"`                      // SSH 私钥路径（不输出到结构化拓扑）
	ObserveUser         string          `json:"observe_user,omitempty"` // 可选：只读观测 SSH 用户
	ObserveIdentityFile string          `json:"-"`                      // 可选：只读观测 SSH 私钥路径（不输出到结构化拓扑）
	ServiceUser         string          `json:"service_user"`           // 运行应用的系统用户
	Dir                 string          `json:"dir"`                    // 远端部署目录
	EnvFile             string          `json:"env_file"`               // 远端环境变量文件路径
	EnvFileStrict       bool            `json:"-"`                      // 为真时强制要求 EnvFile 存在，为假时 systemd 以 - 前缀忽略缺失
	ConfigDir           string          `json:"config_dir,omitempty"`   // 本地配置目录（未配置 config_files 时生效）
	ConfigFiles         []ConfigFile    `json:"config_files,omitempty"` // 按顺序叠加加载的配置文件清单
	MaintenanceFile     string          `json:"maintenance_file"`       // 维护页标记文件路径
	Roles               map[string]Role `json:"roles"`                  // 标准化后的角色拓扑
	Hosts               []Host          `json:"hosts"`                  // 按主机去重归并后的主机列表
	MigrateHostIndex    int             `json:"-"`                      // 主迁移节点在 Hosts 中的下标
}

// EnvFileSpec 返回写入 systemd EnvironmentFile 的路径值；非严格模式下附加前导 - 允许文件缺失。
func (t Target) EnvFileSpec() string {
	if t.EnvFileStrict {
		return t.EnvFile
	}
	return "-" + t.EnvFile
}

// HasObserveUser 报告是否配置了独立于发布用户的只读观测 SSH 用户。
func (t Target) HasObserveUser() bool {
	return t.ObserveUser != "" && t.ObserveUser != t.User
}

// ObserveTarget 返回切换到只读观测 SSH 身份后的目标副本；未配置 observe_user 时保持原样。
func (t Target) ObserveTarget() Target {
	if t.ObserveUser == "" {
		return t
	}
	cp := t
	cp.User = t.ObserveUser
	if t.ObserveIdentityFile != "" {
		cp.IdentityFile = t.ObserveIdentityFile
	}
	return cp
}

// DisplayLabel 返回人类可读的目标身份描述，实例名与产物名不同时同时标出产物名。
func (t Target) DisplayLabel() string {
	if t.Instance != t.App {
		return fmt.Sprintf("%s (实例: %s, 产物: %s)", t.Name, t.Instance, t.App)
	}
	return fmt.Sprintf("%s (实例: %s)", t.Name, t.Instance)
}

// RemoteConfigFiles 返回远端工作目录下按顺序加载的配置文件路径列表。
func (t Target) RemoteConfigFiles() []string {
	if len(t.ConfigFiles) == 0 {
		return nil
	}
	out := make([]string, len(t.ConfigFiles))
	for i, f := range t.ConfigFiles {
		out[i] = f.RemotePath
	}
	return out
}

// ConfigFlagValue 返回传给应用 --config 选项的逗号分隔路径列表；未配置 config_files 时返回空字符串。
func (t Target) ConfigFlagValue() string {
	return strings.Join(t.RemoteConfigFiles(), ",")
}

// MigrateHost 返回执行预检与数据库迁移的主节点。
func (t Target) MigrateHost() Host {
	return t.Hosts[t.MigrateHostIndex]
}

// FilterHosts 按角色 role 和主机地址 addr 筛选目标主机；都为空时返回全部主机。
func (t Target) FilterHosts(role, addr string) ([]Host, error) {
	var allowed map[string]bool
	if role != "" {
		r, ok := t.Roles[role]
		if !ok {
			return nil, fmt.Errorf("deploy: role %q not found in target %q (available: %s)", role, t.Name, strings.Join(sortedRoleNames(t.Roles), ", "))
		}
		allowed = make(map[string]bool, len(r.Hosts))
		for _, a := range r.Hosts {
			allowed[a] = true
		}
	}
	var out []Host
	for _, h := range t.Hosts {
		if allowed != nil && !allowed[h.Addr] {
			continue
		}
		if addr != "" && h.Addr != addr {
			continue
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("deploy: no matching host in target %q (role=%q host=%q)", t.Name, role, addr)
	}
	return out, nil
}

// HTTPHosts 返回运行入口 HTTP 服务（all 或 http）的主机列表；若均无入口服务则返回全部主机。
func (t Target) HTTPHosts() []Host {
	var out []Host
	for _, h := range t.Hosts {
		if h.HasHTTP() {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		return t.Hosts
	}
	return out
}

// RolloutWaves 返回按安全顺序划分的发布波次：
// 先升级纯后端节点（rpc/worker/scheduler 等），再升级入口节点（http/all）；
// 每一类超过 1 台时先单独切换首台作为金丝雀，其余再按 max_parallel 分批并发切换。
func (t Target) RolloutWaves() []Wave {
	var backend, ingress []Host
	for _, h := range t.Hosts {
		if h.HasHTTP() {
			ingress = append(ingress, h)
		} else {
			backend = append(backend, h)
		}
	}
	var waves []Wave
	waves = append(waves, buildGroupWaves("backend", backend)...)
	waves = append(waves, buildGroupWaves("http", ingress)...)
	return waves
}

// buildGroupWaves 将同一类主机拆为金丝雀波次与剩余批量波次。
func buildGroupWaves(group string, hosts []Host) []Wave {
	if len(hosts) == 0 {
		return nil
	}
	if len(hosts) == 1 {
		return []Wave{{Name: group, Hosts: []string{hosts[0].Addr}, MaxParallel: 1}}
	}
	rest := make([]string, 0, len(hosts)-1)
	maxPar := 0
	for _, h := range hosts[1:] {
		rest = append(rest, h.Addr)
		if maxPar == 0 || h.MaxParallel < maxPar {
			maxPar = h.MaxParallel
		}
	}
	if maxPar < 1 {
		maxPar = 1
	}
	return []Wave{
		{Name: group + "-canary", Hosts: []string{hosts[0].Addr}, MaxParallel: 1},
		{Name: group, Hosts: rest, MaxParallel: maxPar},
	}
}

// HostByAddr 按地址查找 Target 中的主机。
func (t Target) HostByAddr(addr string) (Host, bool) {
	for _, h := range t.Hosts {
		if h.Addr == addr {
			return h, true
		}
	}
	return Host{}, false
}

// Topology 是部署目录的安全只读摘要，供 MCP 工具 tenon_deploy_info 和 JSON 输出使用，不含密钥。
type Topology struct {
	App          string                    `json:"app"`            // 构建产物名
	Repo         string                    `json:"repo,omitempty"` // GitHub 仓库（owner/repo）
	Arch         string                    `json:"arch"`           // 默认目标架构，auto 表示自动探测
	KeepReleases int                       `json:"keep_releases"`  // 默认保留版本数
	TargetsDir   string                    `json:"targets_dir"`    // 目标实例所在目录
	Targets      map[string]TargetTopology `json:"targets"`        // 各目标实例拓扑摘要
}

// TargetTopology 是单个目标实例的拓扑摘要。
type TargetTopology struct {
	Instance        string          `json:"instance"`                   // 远端实例名
	User            string          `json:"user"`                       // SSH 用户
	ObserveUser     string          `json:"observe_user,omitempty"`     // 可选：只读观测 SSH 用户
	ServiceUser     string          `json:"service_user"`               // 服务用户
	Dir             string          `json:"dir"`                        // 部署目录
	EnvFile         string          `json:"env_file"`                   // 远端环境变量路径
	ConfigFiles     []string        `json:"config_files,omitempty"`     // 按顺序叠加加载的配置文件列表
	MigrateHost     string          `json:"migrate_host"`               // 主迁移节点
	MaintenanceFile string          `json:"maintenance_file,omitempty"` // 维护页标记文件路径
	Roles           map[string]Role `json:"roles"`                      // 角色定义
	Hosts           []Host          `json:"hosts"`                      // 去重归并后的主机列表
	Waves           []Wave          `json:"waves"`                      // 滚动发布波次
}

// ResolveDir 返回规范化后的部署根目录，空值回退到 DefaultDir。
func ResolveDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return DefaultDir
	}
	return dir
}

// ResolveTargetsDir 解析实例配置目录：显式参数优先，其次环境变量 TENON_DEPLOY_TARGETS，最后 <dir>/targets。
func ResolveTargetsDir(dir, override string) string {
	if s := strings.TrimSpace(override); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv(EnvTargetsDir)); s != "" {
		return s
	}
	return filepath.Join(ResolveDir(dir), TargetsDirName)
}

// AppFilePath 返回部署根目录下的 app.yaml 路径。
func AppFilePath(dir string) string {
	return filepath.Join(ResolveDir(dir), AppFileName)
}

// DiscoverTargets 扫描 targetsDir 下所有包含 target.yaml 的子目录，返回排序后的目标名列表；目录不存在时返回空切片。
func DiscoverTargets(targetsDir string) ([]string, error) {
	entries, err := os.ReadDir(targetsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("deploy: read targets dir %s: %w", targetsDir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		tf := filepath.Join(targetsDir, e.Name(), TargetFileName)
		if info, statErr := os.Stat(tf); statErr == nil && !info.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

// LoadAppFile 读取并校验 deploy/app.yaml。
func LoadAppFile(dir string) (AppFile, error) {
	p := AppFilePath(dir)
	data, err := os.ReadFile(p)
	if err != nil {
		return AppFile{}, fmt.Errorf("deploy: read %s: %w", p, err)
	}
	var af AppFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&af); err != nil {
		return AppFile{}, fmt.Errorf("deploy: parse %s: %w", p, err)
	}
	return af, nil
}

// LoadTopology 解析 deploy/app.yaml 与 targetsDir 下的全部实例，返回只读拓扑摘要。
func LoadTopology(opts LoadOptions) (Topology, error) {
	dir := ResolveDir(opts.Dir)
	af, err := LoadAppFile(dir)
	if err != nil {
		return Topology{}, err
	}
	app, repo, arch, keep, err := normalizeAppMeta(af, opts.DefaultApp)
	if err != nil {
		return Topology{}, err
	}
	targetsDir := ResolveTargetsDir(dir, opts.TargetsDir)
	names, err := DiscoverTargets(targetsDir)
	if err != nil {
		return Topology{}, err
	}
	displayArch := arch
	if displayArch == "" {
		displayArch = "auto"
	}
	topo := Topology{
		App:          app,
		Repo:         repo,
		Arch:         displayArch,
		KeepReleases: keep,
		TargetsDir:   filepath.ToSlash(targetsDir),
		Targets:      make(map[string]TargetTopology, len(names)),
	}
	for _, name := range names {
		t, err := loadOneTarget(af, dir, opts.DefaultApp, targetsDir, name)
		if err != nil {
			return Topology{}, err
		}
		topo.Targets[name] = TargetTopology{
			Instance:        t.Instance,
			User:            t.User,
			ObserveUser:     t.ObserveUser,
			ServiceUser:     t.ServiceUser,
			Dir:             t.Dir,
			EnvFile:         t.EnvFile,
			ConfigFiles:     t.RemoteConfigFiles(),
			MigrateHost:     t.MigrateHost().Addr,
			MaintenanceFile: t.MaintenanceFile,
			Roles:           t.Roles,
			Hosts:           t.Hosts,
			Waves:           t.RolloutWaves(),
		}
	}
	return topo, nil
}

// Load 读取单个目标实例配置；targetName 为空时仅当存在唯一实例自动选用，多实例时返回错误要求显式指定。
func Load(opts LoadOptions, targetName string) (Target, error) {
	list, err := LoadMany(opts, splitTargetNames(targetName), false)
	if err != nil {
		return Target{}, err
	}
	if len(list) != 1 {
		return Target{}, fmt.Errorf("deploy: this command accepts a single target, got %d", len(list))
	}
	return list[0], nil
}

// LoadMany 加载一个或多个目标实例；all 为真时加载全部已发现的实例。
// 未指定任何目标且存在多个实例时返回明确错误并列出可用实例，避免静默误选。
func LoadMany(opts LoadOptions, rawNames []string, all bool) ([]Target, error) {
	dir := ResolveDir(opts.Dir)
	af, err := LoadAppFile(dir)
	if err != nil {
		return nil, err
	}
	targetsDir := ResolveTargetsDir(dir, opts.TargetsDir)
	available, err := DiscoverTargets(targetsDir)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, r := range rawNames {
		names = append(names, splitTargetNames(r)...)
	}
	if all && len(names) > 0 {
		return nil, fmt.Errorf("deploy: --all cannot be combined with --target")
	}

	var selected []string
	switch {
	case all:
		if len(available) == 0 {
			return nil, fmt.Errorf("deploy: no targets found in %s", targetsDir)
		}
		selected = available
	case len(names) == 0:
		switch len(available) {
		case 0:
			return nil, fmt.Errorf("deploy: no targets found in %s (create one under %s/<name>/%s)", targetsDir, targetsDir, TargetFileName)
		case 1:
			selected = available
		default:
			return nil, fmt.Errorf("deploy: multiple targets exist; specify --target (available: %s)", strings.Join(available, ", "))
		}
	default:
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			if !slices.Contains(available, name) {
				if len(available) == 0 {
					return nil, fmt.Errorf("deploy: target %q not found in %s (no targets configured)", name, targetsDir)
				}
				return nil, fmt.Errorf("deploy: target %q not found in %s (available: %s)", name, targetsDir, strings.Join(available, ", "))
			}
			selected = append(selected, name)
		}
	}

	out := make([]Target, 0, len(selected))
	for _, name := range selected {
		t, err := loadOneTarget(af, dir, opts.DefaultApp, targetsDir, name)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// loadOneTarget 从 targetsDir/<name>/target.yaml 解析并构建完整的 Target。
func loadOneTarget(af AppFile, appDir, defaultApp, targetsDir, targetName string) (Target, error) {
	targetDir := filepath.Join(targetsDir, targetName)
	tfPath := filepath.Join(targetDir, TargetFileName)
	data, err := os.ReadFile(tfPath)
	if err != nil {
		return Target{}, fmt.Errorf("deploy: read %s: %w", tfPath, err)
	}
	var tf TargetFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&tf); err != nil {
		return Target{}, fmt.Errorf("deploy: parse %s: %w", tfPath, err)
	}
	return BuildTarget(af, appDir, defaultApp, targetName, tf, targetDir)
}

// BuildTarget 将 AppFile 与单个 TargetFile 合并校验并补齐全部默认值，返回可执行的 Target。
func BuildTarget(af AppFile, appDir, defaultApp, targetName string, tf TargetFile, targetDir string) (Target, error) {
	if !safeName.MatchString(targetName) {
		return Target{}, fmt.Errorf("deploy: target name %q must match %s", targetName, safeName)
	}
	app, repo, arch, keep, err := normalizeAppMeta(af, defaultApp)
	if err != nil {
		return Target{}, err
	}

	instance := strings.TrimSpace(tf.Instance)
	if instance == "" {
		instance = app
	}
	if !safeName.MatchString(instance) {
		return Target{}, fmt.Errorf("deploy: target %q instance %q must match %s", targetName, instance, safeName)
	}

	if a := strings.TrimSpace(tf.Arch); a != "" {
		if a != "amd64" && a != "arm64" {
			return Target{}, fmt.Errorf("deploy: target %q arch %q must be amd64 or arm64", targetName, a)
		}
		arch = a
	}
	if tf.KeepReleases < 0 {
		return Target{}, fmt.Errorf("deploy: target %q keep_releases must be >= 1, got %d", targetName, tf.KeepReleases)
	}
	if tf.KeepReleases > 0 {
		keep = tf.KeepReleases
	}

	user := strings.TrimSpace(tf.User)
	if user == "" {
		user = strings.TrimSpace(af.Defaults.User)
	}
	if user == "" {
		user = DefaultSSHUser
	}

	port := tf.Port
	if port == 0 {
		port = af.Defaults.Port
	}
	if port < 0 || port > 65535 {
		return Target{}, fmt.Errorf("deploy: target %q port %d out of range", targetName, port)
	}

	identityFile := strings.TrimSpace(tf.IdentityFile)
	identityBase := targetDir
	if identityFile == "" {
		identityFile = strings.TrimSpace(af.Defaults.IdentityFile)
		identityBase = appDir
	}
	if identityFile != "" {
		identityFile = anchorLocalPath(identityBase, identityFile)
	}

	observeUser := strings.TrimSpace(tf.ObserveUser)
	if observeUser == "" {
		observeUser = strings.TrimSpace(af.Defaults.ObserveUser)
	}
	if observeUser != "" && !safeUserName.MatchString(observeUser) {
		return Target{}, fmt.Errorf("deploy: target %q observe_user %q must match %s", targetName, observeUser, safeUserName)
	}

	observeIdentityFile := strings.TrimSpace(tf.ObserveIdentityFile)
	observeIdentityBase := targetDir
	if observeIdentityFile == "" {
		observeIdentityFile = strings.TrimSpace(af.Defaults.ObserveIdentityFile)
		observeIdentityBase = appDir
	}
	if observeIdentityFile != "" {
		observeIdentityFile = anchorLocalPath(observeIdentityBase, observeIdentityFile)
	}

	serviceUser := strings.TrimSpace(tf.ServiceUser)
	if serviceUser == "" {
		serviceUser = instance
	}
	if !safeName.MatchString(serviceUser) {
		return Target{}, fmt.Errorf("deploy: target %q service_user %q must match %s", targetName, serviceUser, safeName)
	}

	remoteDir := strings.TrimSpace(tf.Dir)
	if remoteDir == "" {
		remoteDir = path.Join("/opt", instance)
	}

	envFile := strings.TrimSpace(tf.EnvFile)
	envFileStrict := envFile != ""
	if !envFileStrict {
		envFile = path.Join("/etc", instance, "env")
	}

	if len(tf.ConfigFiles) > 0 && strings.TrimSpace(tf.ConfigDir) != "" {
		return Target{}, fmt.Errorf("deploy: target %q cannot set both config_dir and config_files", targetName)
	}

	var configFiles []ConfigFile
	var configDir string
	switch {
	case len(tf.ConfigFiles) > 0:
		configFiles, err = parseConfigFiles(targetName, targetDir, tf.ConfigFiles)
		if err != nil {
			return Target{}, err
		}
	case strings.TrimSpace(tf.ConfigDir) != "":
		configDir = anchorLocalPath(targetDir, strings.TrimSpace(tf.ConfigDir))
	default:
		// 约定优于配置：目标目录下存在 config.yaml 时自动作为独立配置同步并注入 --config。
		autoCfg := filepath.Join(targetDir, DefaultTargetConfig)
		if targetDir != "" {
			if info, statErr := os.Stat(autoCfg); statErr == nil && !info.IsDir() {
				configFiles, err = parseConfigFiles(targetName, targetDir, []string{DefaultTargetConfig})
				if err != nil {
					return Target{}, err
				}
			}
		}
		if len(configFiles) == 0 && len(af.Defaults.ConfigFiles) > 0 {
			configFiles, err = parseConfigFiles(targetName, appDir, af.Defaults.ConfigFiles)
			if err != nil {
				return Target{}, err
			}
		}
	}

	maintenanceFile := strings.TrimSpace(tf.MaintenanceFile)
	migrateHost := strings.TrimSpace(tf.MigrateHost)
	for field, val := range map[string]string{
		"user":                  user,
		"observe_user":          observeUser,
		"dir":                   remoteDir,
		"env_file":              envFile,
		"config_dir":            configDir,
		"identity_file":         identityFile,
		"observe_identity_file": observeIdentityFile,
		"maintenance_file":      maintenanceFile,
		"migrate_host":          migrateHost,
	} {
		if strings.ContainsAny(val, "\r\n\x00") {
			return Target{}, fmt.Errorf("deploy: target %q %s contains invalid control characters", targetName, field)
		}
	}
	if !strings.HasPrefix(remoteDir, "/") {
		return Target{}, fmt.Errorf("deploy: target %q dir %q must be an absolute path", targetName, remoteDir)
	}
	if !strings.HasPrefix(envFile, "/") {
		return Target{}, fmt.Errorf("deploy: target %q env_file %q must be an absolute path", targetName, envFile)
	}
	if maintenanceFile != "" && !strings.HasPrefix(maintenanceFile, "/") {
		return Target{}, fmt.Errorf("deploy: target %q maintenance_file %q must be an absolute path", targetName, maintenanceFile)
	}
	if len(tf.Hosts) > 0 && len(tf.Roles) > 0 {
		return Target{}, fmt.Errorf("deploy: target %q cannot set both hosts and roles", targetName)
	}
	rawRoles := tf.Roles
	if len(tf.Hosts) > 0 {
		rawRoles = map[string]Role{
			"all": {Parts: []string{"all"}, Hosts: tf.Hosts, MaxParallel: 1},
		}
	}
	if len(rawRoles) == 0 {
		return Target{}, fmt.Errorf("deploy: target %q must configure hosts or roles", targetName)
	}

	roles, hosts, err := normalizeRoles(targetName, rawRoles)
	if err != nil {
		return Target{}, err
	}

	migrateIdx := -1
	if migrateHost != "" {
		for i, h := range hosts {
			if h.Addr == migrateHost {
				migrateIdx = i
				break
			}
		}
		if migrateIdx < 0 {
			return Target{}, fmt.Errorf("deploy: target %q migrate_host %q is not in any role", targetName, migrateHost)
		}
	} else {
		// 未显式指定 migrate_host 时优先选择首个非入口节点，否则选择第一台主机。
		migrateIdx = 0
		for i, h := range hosts {
			if !h.HasHTTP() {
				migrateIdx = i
				break
			}
		}
	}
	hosts[migrateIdx].Migrate = true

	hasSSR := false
	if af.HasSSR != nil {
		hasSSR = *af.HasSSR
	}

	return Target{
		App:                 app,
		Name:                targetName,
		Instance:            instance,
		Repo:                repo,
		Arch:                arch,
		KeepReleases:        keep,
		HasSSR:              hasSSR,
		User:                user,
		Port:                port,
		IdentityFile:        identityFile,
		ObserveUser:         observeUser,
		ObserveIdentityFile: observeIdentityFile,
		ServiceUser:         serviceUser,
		Dir:                 remoteDir,
		EnvFile:             envFile,
		EnvFileStrict:       envFileStrict,
		ConfigDir:           configDir,
		ConfigFiles:         configFiles,
		MaintenanceFile:     maintenanceFile,
		Roles:               roles,
		Hosts:               hosts,
		MigrateHostIndex:    migrateIdx,
	}, nil
}

// normalizeAppMeta 校验并补齐 AppFile 的顶层产物元数据。
func normalizeAppMeta(af AppFile, defaultApp string) (app, repo, arch string, keep int, err error) {
	app = strings.TrimSpace(af.App)
	if app == "" {
		app = strings.TrimSpace(defaultApp)
	}
	if !safeName.MatchString(app) {
		return "", "", "", 0, fmt.Errorf("deploy: app %q must match %s", app, safeName)
	}
	repo = strings.TrimSpace(af.Repo)
	if repo != "" {
		parts := strings.Split(repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repo, " \t\r\n\x00") {
			return "", "", "", 0, fmt.Errorf("deploy: repo %q must be in owner/repo format", repo)
		}
	}
	arch = strings.TrimSpace(af.Arch)
	if arch != "" && arch != "amd64" && arch != "arm64" {
		return "", "", "", 0, fmt.Errorf("deploy: arch %q must be amd64 or arm64", arch)
	}
	keep = af.KeepReleases
	if keep == 0 {
		keep = DefaultKeepReleases
	}
	if keep < 1 {
		return "", "", "", 0, fmt.Errorf("deploy: keep_releases must be >= 1, got %d", keep)
	}
	return app, repo, arch, keep, nil
}

// normalizeRoles 校验角色配置并按主机地址归并去重，同时合并各 part 的 systemd drop-in 覆盖项。
func normalizeRoles(targetName string, raw map[string]Role) (map[string]Role, []Host, error) {
	roleNames := sortedRoleNames(raw)
	roles := make(map[string]Role, len(raw))
	hostOrder := make([]string, 0)
	byAddr := make(map[string]*Host)

	for _, rname := range roleNames {
		if !safeName.MatchString(rname) {
			return nil, nil, fmt.Errorf("deploy: target %q role %q must match %s", targetName, rname, safeName)
		}
		r := raw[rname]
		if len(r.Hosts) == 0 {
			return nil, nil, fmt.Errorf("deploy: target %q role %q has no hosts", targetName, rname)
		}
		if len(r.Parts) == 0 {
			r.Parts = []string{rname}
		}
		for _, p := range r.Parts {
			if !safeName.MatchString(p) {
				return nil, nil, fmt.Errorf("deploy: role %q part %q must match %s", rname, p, safeName)
			}
		}
		if r.MaxParallel == 0 {
			r.MaxParallel = 1
		}
		if r.MaxParallel < 1 {
			return nil, nil, fmt.Errorf("deploy: role %q max_parallel must be >= 1", rname)
		}
		r.StopTimeout = strings.TrimSpace(r.StopTimeout)
		if strings.ContainsAny(r.StopTimeout, " \t\r\n\x00") {
			return nil, nil, fmt.Errorf("deploy: role %q stop_timeout %q is invalid", rname, r.StopTimeout)
		}
		for k, v := range r.Env {
			if !safeEnvKey.MatchString(k) {
				return nil, nil, fmt.Errorf("deploy: role %q env key %q must match %s", rname, k, safeEnvKey)
			}
			if strings.ContainsAny(v, "\r\n\x00") {
				return nil, nil, fmt.Errorf("deploy: role %q env %q contains invalid control characters", rname, k)
			}
		}
		cleanHosts := make([]string, 0, len(r.Hosts))
		seenInRole := make(map[string]bool, len(r.Hosts))
		for _, addr := range r.Hosts {
			addr = strings.TrimSpace(addr)
			if addr == "" || strings.ContainsAny(addr, " \t\r\n\x00") {
				return nil, nil, fmt.Errorf("deploy: role %q host %q is invalid", rname, addr)
			}
			if seenInRole[addr] {
				return nil, nil, fmt.Errorf("deploy: role %q has duplicate host %q", rname, addr)
			}
			seenInRole[addr] = true
			cleanHosts = append(cleanHosts, addr)

			h, ok := byAddr[addr]
			if !ok {
				h = &Host{
					Addr:        addr,
					DropIns:     map[string]PartDropIn{},
					MaxParallel: r.MaxParallel,
				}
				byAddr[addr] = h
				hostOrder = append(hostOrder, addr)
			} else if r.MaxParallel < h.MaxParallel {
				h.MaxParallel = r.MaxParallel
			}
			h.Roles = append(h.Roles, rname)
			for _, p := range r.Parts {
				if !slices.Contains(h.Parts, p) {
					h.Parts = append(h.Parts, p)
				}
				if r.StopTimeout != "" || len(r.Env) > 0 {
					di := h.DropIns[p]
					if r.StopTimeout != "" {
						di.StopTimeout = r.StopTimeout
					}
					if len(r.Env) > 0 {
						if di.Env == nil {
							di.Env = make(map[string]string, len(r.Env))
						}
						for k, v := range r.Env {
							di.Env[k] = v
						}
					}
					h.DropIns[p] = di
				}
			}
		}
		r.Hosts = cleanHosts
		roles[rname] = r
	}

	hosts := make([]Host, 0, len(hostOrder))
	for _, addr := range hostOrder {
		h := byAddr[addr]
		slices.Sort(h.Roles)
		slices.Sort(h.Parts)
		if slices.Contains(h.Parts, "all") && len(h.Parts) > 1 {
			return nil, nil, fmt.Errorf("deploy: host %q cannot combine part \"all\" with other parts (%s)", addr, strings.Join(h.Parts, ", "))
		}
		hosts = append(hosts, *h)
	}
	return roles, hosts, nil
}

// parseConfigFiles 解析 config_files 清单：本地路径锚定到 baseDir（支持相对上层与绝对路径），
// 远端统一映射到 releases/<id>/config/ 下且禁止冲突。
func parseConfigFiles(targetName, baseDir string, raw []string) ([]ConfigFile, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]ConfigFile, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, item := range raw {
		s := strings.TrimSpace(item)
		if s == "" || strings.ContainsAny(s, " \t\r\n\x00,") {
			return nil, fmt.Errorf("deploy: target %q config_files entry %q is invalid", targetName, item)
		}
		slashed := strings.ReplaceAll(s, `\`, "/")
		if strings.HasSuffix(slashed, "/") {
			return nil, fmt.Errorf("deploy: target %q config_files entry %q must be a file path", targetName, item)
		}
		cleaned := path.Clean(slashed)
		if cleaned == "." || cleaned == ".." || cleaned == "/" {
			return nil, fmt.Errorf("deploy: target %q config_files entry %q is invalid", targetName, item)
		}
		var archiveRel string
		if strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			archiveRel = path.Base(cleaned)
		} else {
			archiveRel = strings.TrimPrefix(cleaned, "config/")
		}
		if archiveRel == "" || archiveRel == "." || archiveRel == ".." || archiveRel == "/" {
			return nil, fmt.Errorf("deploy: target %q config_files entry %q has invalid file name", targetName, item)
		}
		remotePath := path.Join("config", archiveRel)
		if seen[remotePath] {
			return nil, fmt.Errorf("deploy: target %q has duplicate remote config path %q", targetName, remotePath)
		}
		seen[remotePath] = true
		out = append(out, ConfigFile{
			LocalPath:  anchorLocalPath(baseDir, s),
			ArchiveRel: archiveRel,
			RemotePath: remotePath,
		})
	}
	return out, nil
}

// anchorLocalPath 将相对路径 p 锚定到 baseDir；绝对路径或 ~/ 开头保持原样。
func anchorLocalPath(baseDir, p string) string {
	if p == "" || baseDir == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "~/") {
		return p
	}
	return filepath.Join(baseDir, filepath.FromSlash(p))
}

// splitTargetNames 将逗号分隔的目标名字符串拆分为去空后的列表。
func splitTargetNames(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// sortedRoleNames 返回角色名排序后的列表。
func sortedRoleNames(m map[string]Role) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
