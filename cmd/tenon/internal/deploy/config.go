// Package deploy 实现 tenon deploy：基于 SSH 和 systemd 的原生单二进制零停机部署与远端运维，
// 支持单机极简配置、多服务角色（roles）拓扑、分波次金丝雀滚动发布以及面向人类和 AI 助手的双模输出。
package deploy

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

const (
	// DefaultFile 是项目根目录默认的部署配置文件名。
	DefaultFile = "deploy.yaml"
	// DefaultEnv 是未指定 --env 时的默认环境名。
	DefaultEnv = "production"
	// DefaultKeepReleases 是各节点默认保留的历史发布目录数量。
	DefaultKeepReleases = 5
	// DefaultSSHUser 是未填写 SSH 登录用户时的默认值。
	DefaultSSHUser = "deploy"
)

// safeName 限制应用名、服务用户名、角色名和服务部分名只包含小写字母、数字和连字符。
var safeName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// safeEnvKey 限制角色级 systemd drop-in 环境变量键为标准 POSIX 环境变量名。
var safeEnvKey = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// File 是 deploy.yaml 的顶层结构。
type File struct {
	App          string                 `yaml:"app"`           // 应用与二进制名称，默认入口包目录名
	Repo         string                 `yaml:"repo"`          // GitHub 仓库（owner/repo），供 --tag 拉取 Release 产物；留空时从 git origin 推导
	Arch         string                 `yaml:"arch"`          // 目标 Linux 架构（amd64、arm64）；留空时自动探测
	KeepReleases int                    `yaml:"keep_releases"` // 保留的历史发布目录数量，默认 5
	Environments map[string]Environment `yaml:"environments"`  // 各环境的部署配置
}

// Environment 是一个部署环境（例如 production、staging）的拓扑与运行参数。
// 单机可直接写 hosts（等价于单个 all 角色），多服务多机用 roles 按角色编排。
type Environment struct {
	User            string          `yaml:"user"`             // SSH 登录用户，默认 deploy
	Port            int             `yaml:"port"`             // SSH 端口，0 表示沿用 ssh 配置
	IdentityFile    string          `yaml:"identity_file"`    // SSH 私钥路径，留空时沿用 ssh/agent 配置
	ServiceUser     string          `yaml:"service_user"`     // 运行应用的 Linux 系统用户，默认与 app 相同
	Dir             string          `yaml:"dir"`              // 远端部署目录，默认 /opt/<app>
	EnvFile         string          `yaml:"env_file"`         // 远端环境变量文件路径，默认 /etc/<app>/env
	ConfigDir       string          `yaml:"config_dir"`       // 要同步的本地配置目录，默认 config（自动排除 local.yaml），与 config_files 互斥
	ConfigFiles     []string        `yaml:"config_files"`     // 该环境按顺序叠加加载的本地配置文件列表（例如 [config/app.yaml, config/prod.yaml]）；精确同步并自动传入 --config
	MaintenanceFile string          `yaml:"maintenance_file"` // 反向代理维护页标记文件路径，可选
	MigrateHost     string          `yaml:"migrate_host"`     // 执行预检与数据库迁移的主机地址，可选
	Hosts           []string        `yaml:"hosts"`            // 单机或同构简写：所有主机运行 all
	Roles           map[string]Role `yaml:"roles"`            // 多服务角色拓扑：键为角色名（如 http、worker、scheduler）
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
	LocalPath  string `json:"local_path"`  // 本地相对路径，例如 config/prod.yaml
	ArchiveRel string `json:"archive_rel"` // 远端 releases/<version>/config 内的相对路径，例如 prod.yaml
	RemotePath string `json:"remote_path"` // 远端工作目录下的相对路径，例如 config/prod.yaml
}

// Target 是补齐默认值、按主机归并角色并通过校验的单个部署环境。
type Target struct {
	App              string          `json:"app"`                    // 应用与二进制名称
	Repo             string          `json:"repo,omitempty"`         // GitHub 仓库（owner/repo）
	Arch             string          `json:"arch,omitempty"`         // 目标架构，为空表示由远端 uname -m 探测
	KeepReleases     int             `json:"keep_releases"`          // 保留的历史发布目录数量
	EnvName          string          `json:"env"`                    // 当前选中的环境名
	User             string          `json:"user"`                   // SSH 登录用户
	Port             int             `json:"port,omitempty"`         // SSH 端口
	IdentityFile     string          `json:"-"`                      // SSH 私钥路径（不输出到结构化拓扑）
	ServiceUser      string          `json:"service_user"`           // 运行应用的系统用户
	Dir              string          `json:"dir"`                    // 远端部署目录
	EnvFile          string          `json:"env_file"`               // 远端环境变量文件路径
	ConfigDir        string          `json:"config_dir,omitempty"`   // 本地配置目录（未配置 config_files 时生效）
	ConfigFiles      []ConfigFile    `json:"config_files,omitempty"` // 按顺序叠加加载的配置文件清单
	MaintenanceFile  string          `json:"maintenance_file"`       // 维护页标记文件路径
	Roles            map[string]Role `json:"roles"`                  // 标准化后的角色拓扑
	Hosts            []Host          `json:"hosts"`                  // 按主机去重归并后的主机列表
	MigrateHostIndex int             `json:"-"`                      // 主迁移节点在 Hosts 中的下标
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
			return nil, fmt.Errorf("deploy: role %q not found in environment %q (available: %s)", role, t.EnvName, strings.Join(sortedRoleNames(t.Roles), ", "))
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
		return nil, fmt.Errorf("deploy: no matching host in environment %q (role=%q host=%q)", t.EnvName, role, addr)
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

// Topology 是 deploy.yaml 的安全只读摘要，供 MCP 工具 tenon_deploy_info 和 JSON 输出使用，不含密钥。
type Topology struct {
	App          string                 `json:"app"`            // 应用名
	Repo         string                 `json:"repo,omitempty"` // GitHub 仓库（owner/repo）
	Arch         string                 `json:"arch"`           // 目标架构，auto 表示自动探测
	KeepReleases int                    `json:"keep_releases"`  // 保留版本数
	Environments map[string]EnvTopology `json:"environments"`   // 各环境拓扑摘要
}

// EnvTopology 是单个环境的拓扑摘要。
type EnvTopology struct {
	User            string          `json:"user"`                       // SSH 用户
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

// LoadTopology 解析 filePath 并返回所有环境的只读拓扑摘要。
func LoadTopology(filePath, defaultApp string) (Topology, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Topology{}, fmt.Errorf("deploy: read %s: %w", filePath, err)
	}
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return Topology{}, fmt.Errorf("deploy: parse config: %w", err)
	}
	if len(f.Environments) == 0 {
		return Topology{}, fmt.Errorf("deploy: environments is empty")
	}
	topo := Topology{
		Environments: make(map[string]EnvTopology, len(f.Environments)),
	}
	for _, name := range sortedKeys(f.Environments) {
		t, err := Parse(data, defaultApp, name)
		if err != nil {
			return Topology{}, err
		}
		topo.App = t.App
		topo.Repo = t.Repo
		topo.KeepReleases = t.KeepReleases
		topo.Arch = t.Arch
		if topo.Arch == "" {
			topo.Arch = "auto"
		}
		topo.Environments[name] = EnvTopology{
			User:            t.User,
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

// Load 读取 filePath 指定的 deploy.yaml，选择 envName 对应的环境，补齐默认值并校验字段。
func Load(filePath, defaultApp, envName string) (Target, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Target{}, fmt.Errorf("deploy: read %s: %w", filePath, err)
	}
	return Parse(data, defaultApp, envName)
}

// Parse 解析 deploy.yaml 的字节内容并返回校验后的 Target。
func Parse(data []byte, defaultApp, envName string) (Target, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return Target{}, fmt.Errorf("deploy: parse config: %w", err)
	}
	app := strings.TrimSpace(f.App)
	if app == "" {
		app = defaultApp
	}
	if !safeName.MatchString(app) {
		return Target{}, fmt.Errorf("deploy: app %q must match %s", app, safeName)
	}
	repo := strings.TrimSpace(f.Repo)
	if repo != "" {
		parts := strings.Split(repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repo, " \t\r\n\x00") {
			return Target{}, fmt.Errorf("deploy: repo %q must be in owner/repo format", repo)
		}
	}
	arch := strings.TrimSpace(f.Arch)
	if arch != "" && arch != "amd64" && arch != "arm64" {
		return Target{}, fmt.Errorf("deploy: arch %q must be amd64 or arm64", arch)
	}
	keep := f.KeepReleases
	if keep == 0 {
		keep = DefaultKeepReleases
	}
	if keep < 1 {
		return Target{}, fmt.Errorf("deploy: keep_releases must be >= 1, got %d", keep)
	}
	if len(f.Environments) == 0 {
		return Target{}, fmt.Errorf("deploy: environments is empty")
	}
	selected, env, err := selectEnvironment(f.Environments, envName)
	if err != nil {
		return Target{}, err
	}
	if env.User == "" {
		env.User = DefaultSSHUser
	}
	if env.Port < 0 || env.Port > 65535 {
		return Target{}, fmt.Errorf("deploy: environment %q port %d out of range", selected, env.Port)
	}
	if env.ServiceUser == "" {
		env.ServiceUser = app
	}
	if !safeName.MatchString(env.ServiceUser) {
		return Target{}, fmt.Errorf("deploy: service_user %q must match %s", env.ServiceUser, safeName)
	}
	if env.Dir == "" {
		env.Dir = path.Join("/opt", app)
	}
	if env.EnvFile == "" {
		env.EnvFile = path.Join("/etc", app, "env")
	}
	if len(env.ConfigFiles) > 0 && strings.TrimSpace(env.ConfigDir) != "" {
		return Target{}, fmt.Errorf("deploy: environment %q cannot set both config_dir and config_files", selected)
	}
	configFiles, err := parseConfigFiles(selected, env.ConfigFiles)
	if err != nil {
		return Target{}, err
	}
	if len(configFiles) == 0 && env.ConfigDir == "" {
		env.ConfigDir = "config"
	}
	for name, val := range map[string]string{
		"user":             env.User,
		"dir":              env.Dir,
		"env_file":         env.EnvFile,
		"config_dir":       env.ConfigDir,
		"identity_file":    env.IdentityFile,
		"maintenance_file": env.MaintenanceFile,
		"migrate_host":     env.MigrateHost,
	} {
		if strings.ContainsAny(val, "\r\n\x00") {
			return Target{}, fmt.Errorf("deploy: %s contains invalid control characters", name)
		}
	}
	if !strings.HasPrefix(env.Dir, "/") {
		return Target{}, fmt.Errorf("deploy: dir %q must be an absolute path", env.Dir)
	}
	if !strings.HasPrefix(env.EnvFile, "/") {
		return Target{}, fmt.Errorf("deploy: env_file %q must be an absolute path", env.EnvFile)
	}
	if env.MaintenanceFile != "" && !strings.HasPrefix(env.MaintenanceFile, "/") {
		return Target{}, fmt.Errorf("deploy: maintenance_file %q must be an absolute path", env.MaintenanceFile)
	}
	if len(env.Hosts) > 0 && len(env.Roles) > 0 {
		return Target{}, fmt.Errorf("deploy: environment %q cannot set both hosts and roles", selected)
	}
	rawRoles := env.Roles
	if len(env.Hosts) > 0 {
		rawRoles = map[string]Role{
			"all": {Parts: []string{"all"}, Hosts: env.Hosts, MaxParallel: 1},
		}
	}
	if len(rawRoles) == 0 {
		return Target{}, fmt.Errorf("deploy: environment %q must configure hosts or roles", selected)
	}

	roles, hosts, err := normalizeRoles(selected, rawRoles)
	if err != nil {
		return Target{}, err
	}

	migrateIdx := -1
	if mh := strings.TrimSpace(env.MigrateHost); mh != "" {
		for i, h := range hosts {
			if h.Addr == mh {
				migrateIdx = i
				break
			}
		}
		if migrateIdx < 0 {
			return Target{}, fmt.Errorf("deploy: environment %q migrate_host %q is not in any role", selected, mh)
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

	return Target{
		App:              app,
		Repo:             repo,
		Arch:             arch,
		KeepReleases:     keep,
		EnvName:          selected,
		User:             env.User,
		Port:             env.Port,
		IdentityFile:     env.IdentityFile,
		ServiceUser:      env.ServiceUser,
		Dir:              env.Dir,
		EnvFile:          env.EnvFile,
		ConfigDir:        env.ConfigDir,
		ConfigFiles:      configFiles,
		MaintenanceFile:  env.MaintenanceFile,
		Roles:            roles,
		Hosts:            hosts,
		MigrateHostIndex: migrateIdx,
	}, nil
}

// normalizeRoles 校验角色配置并按主机地址归并去重，同时合并各 part 的 systemd drop-in 覆盖项。
func normalizeRoles(envName string, raw map[string]Role) (map[string]Role, []Host, error) {
	roleNames := sortedRoleNames(raw)
	roles := make(map[string]Role, len(raw))
	hostOrder := make([]string, 0)
	byAddr := make(map[string]*Host)

	for _, rname := range roleNames {
		if !safeName.MatchString(rname) {
			return nil, nil, fmt.Errorf("deploy: environment %q role %q must match %s", envName, rname, safeName)
		}
		r := raw[rname]
		if len(r.Hosts) == 0 {
			return nil, nil, fmt.Errorf("deploy: environment %q role %q has no hosts", envName, rname)
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

// selectEnvironment 从 environments 中选择指定名称的环境；envName 为空时优先取 production，
// 若只有一个环境则直接选用它。
func selectEnvironment(envs map[string]Environment, envName string) (string, Environment, error) {
	if envName != "" {
		env, ok := envs[envName]
		if !ok {
			return "", Environment{}, fmt.Errorf("deploy: environment %q not found (available: %s)", envName, strings.Join(sortedKeys(envs), ", "))
		}
		return envName, env, nil
	}
	if env, ok := envs[DefaultEnv]; ok {
		return DefaultEnv, env, nil
	}
	if len(envs) == 1 {
		for k, v := range envs {
			return k, v, nil
		}
	}
	return "", Environment{}, fmt.Errorf("deploy: specify --env (available: %s)", strings.Join(sortedKeys(envs), ", "))
}

// sortedKeys 返回环境名排序后的列表。
func sortedKeys(m map[string]Environment) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// parseConfigFiles 校验并规范化 config_files 清单，计算每个文件在远端发布目录与工作目录中的映射路径。
func parseConfigFiles(envName string, raw []string) ([]ConfigFile, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]ConfigFile, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, item := range raw {
		s := strings.TrimSpace(item)
		if s == "" || strings.ContainsAny(s, " \t\r\n\x00,") {
			return nil, fmt.Errorf("deploy: environment %q config_files entry %q is invalid", envName, item)
		}
		slashed := strings.ReplaceAll(s, `\`, "/")
		if strings.HasPrefix(slashed, "/") || strings.HasSuffix(slashed, "/") {
			return nil, fmt.Errorf("deploy: environment %q config_files entry %q must be a relative file path", envName, item)
		}
		cleaned := path.Clean(slashed)
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned == "config" {
			return nil, fmt.Errorf("deploy: environment %q config_files entry %q escapes project directory", envName, item)
		}
		archiveRel := cleaned
		if after, ok := strings.CutPrefix(cleaned, "config/"); ok {
			archiveRel = after
		}
		remotePath := path.Join("config", archiveRel)
		if seen[remotePath] {
			return nil, fmt.Errorf("deploy: environment %q has duplicate remote config path %q", envName, remotePath)
		}
		seen[remotePath] = true
		out = append(out, ConfigFile{
			LocalPath:  cleaned,
			ArchiveRel: archiveRel,
			RemotePath: remotePath,
		})
	}
	return out, nil
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
