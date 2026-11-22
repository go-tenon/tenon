// Package deploy 实现 tenon deploy：基于 SSH 和 systemd 的原生单二进制零停机部署与远端运维。
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

// safeName 限制应用名、服务用户名和服务部分名只包含小写字母、数字和连字符，避免作为路径、Linux 用户名或 unit 名时产生歧义。
var safeName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// File 是 deploy.yaml 的顶层结构。
type File struct {
	App          string                 `yaml:"app"`           // 应用与二进制名称，默认入口包目录名
	Arch         string                 `yaml:"arch"`          // 目标 Linux 架构（amd64、arm64）；留空时自动探测
	KeepReleases int                    `yaml:"keep_releases"` // 保留的历史发布目录数量，默认 5
	Environments map[string]Environment `yaml:"environments"`  // 各环境的部署配置
}

// Environment 是一个部署环境（例如 production、staging）的目标主机与运行参数。
type Environment struct {
	User            string `yaml:"user"`             // SSH 登录用户，默认 deploy
	Port            int    `yaml:"port"`             // SSH 端口，0 表示沿用 ssh 配置
	IdentityFile    string `yaml:"identity_file"`    // SSH 私钥路径，留空时沿用 ssh/agent 配置
	ServiceUser     string `yaml:"service_user"`     // 运行应用的 Linux 系统用户，默认与 app 相同
	Dir             string `yaml:"dir"`              // 远端部署目录，默认 /opt/<app>
	EnvFile         string `yaml:"env_file"`         // 远端环境变量文件路径，默认 /etc/<app>/env
	ConfigDir       string `yaml:"config_dir"`       // 要同步的本地配置目录，默认 config（自动排除 local.yaml）
	MaintenanceFile string `yaml:"maintenance_file"` // 反向代理维护页标记文件路径，可选
	Hosts           []Host `yaml:"hosts"`            // 目标主机列表
}

// Host 是一台目标主机的地址、运行的服务部分和迁移角色。
type Host struct {
	Addr    string   `yaml:"addr"`    // 主机地址或 ~/.ssh/config 中的别名
	Parts   []string `yaml:"parts"`   // 运行的服务部分名（如 all、http、worker），默认 ["all"]
	Migrate bool     `yaml:"migrate"` // 是否在此节点执行数据库迁移；均未设置时默认首台主机
}

// HasHTTP 报告该主机是否运行 HTTP 服务（all 或 http），用于决定是否执行 health 检查。
func (h Host) HasHTTP() bool {
	return slices.Contains(h.Parts, "all") || slices.Contains(h.Parts, "http")
}

// Target 是补齐默认值并通过校验的单个部署环境。
type Target struct {
	App          string // 应用与二进制名称
	Arch         string // 目标架构，空字符串表示由远端 uname -m 探测
	KeepReleases int    // 保留的历史发布目录数量
	EnvName      string // 当前选中的环境名
	Environment
	MigrateHostIndex int // 执行迁移和预检的主节点在 Hosts 中的下标
}

// MigrateHost 返回执行预检与数据库迁移的主节点。
func (t Target) MigrateHost() Host {
	return t.Hosts[t.MigrateHostIndex]
}

// FilterHosts 按 addr 筛选目标主机；addr 为空时返回全部主机。
func (t Target) FilterHosts(addr string) ([]Host, error) {
	if addr == "" {
		return t.Hosts, nil
	}
	for _, h := range t.Hosts {
		if h.Addr == addr {
			return []Host{h}, nil
		}
	}
	return nil, fmt.Errorf("deploy: host %q is not in environment %q", addr, t.EnvName)
}

// Load 读取 filePath 指定的 deploy.yaml，选择 envName 对应的环境，补齐默认值并校验字段。
// defaultApp 在文件未写 app 时作为默认应用名。
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
	if env.ConfigDir == "" {
		env.ConfigDir = "config"
	}
	for name, val := range map[string]string{
		"user":             env.User,
		"dir":              env.Dir,
		"env_file":         env.EnvFile,
		"config_dir":       env.ConfigDir,
		"identity_file":    env.IdentityFile,
		"maintenance_file": env.MaintenanceFile,
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
	if len(env.Hosts) == 0 {
		return Target{}, fmt.Errorf("deploy: environment %q has no hosts", selected)
	}
	migrateIdx := -1
	seen := make(map[string]bool, len(env.Hosts))
	for i := range env.Hosts {
		h := &env.Hosts[i]
		h.Addr = strings.TrimSpace(h.Addr)
		if h.Addr == "" || strings.ContainsAny(h.Addr, " \t\r\n\x00") {
			return Target{}, fmt.Errorf("deploy: environment %q host[%d] addr %q is invalid", selected, i, h.Addr)
		}
		if seen[h.Addr] {
			return Target{}, fmt.Errorf("deploy: environment %q has duplicate host %q", selected, h.Addr)
		}
		seen[h.Addr] = true
		if len(h.Parts) == 0 {
			h.Parts = []string{"all"}
		}
		for _, p := range h.Parts {
			if !safeName.MatchString(p) {
				return Target{}, fmt.Errorf("deploy: host %q part %q must match %s", h.Addr, p, safeName)
			}
		}
		if h.Migrate {
			if migrateIdx >= 0 {
				return Target{}, fmt.Errorf("deploy: environment %q marks multiple hosts with migrate: true", selected)
			}
			migrateIdx = i
		}
	}
	if migrateIdx < 0 {
		migrateIdx = 0
		env.Hosts[0].Migrate = true
	}
	return Target{
		App:              app,
		Arch:             arch,
		KeepReleases:     keep,
		EnvName:          selected,
		Environment:      env,
		MigrateHostIndex: migrateIdx,
	}, nil
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

// sortedKeys 返回 map 排序后的键列表。
func sortedKeys(m map[string]Environment) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
