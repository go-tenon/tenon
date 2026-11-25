package deploy

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
)

// SetupOptions 控制 tenon deploy setup 的行为。
type SetupOptions struct {
	Role   string // 仅初始化指定角色的主机，留空表示全部角色
	Host   string // 仅初始化指定主机，留空表示全部主机
	HasSSR bool   // 项目是否包含服务端渲染前端；为真时校验目标机带有 glibc
	DryRun bool   // 为真时只打印计划，不修改远端主机
}

// SetupReport 是 tenon deploy setup 的结构化执行结果。
type SetupReport struct {
	App         string   `json:"app"`          // 应用名
	Env         string   `json:"env"`          // 环境名
	DryRun      bool     `json:"dry_run"`      // 是否为预演模式
	UnitPath    string   `json:"unit_path"`    // systemd 模板 unit 路径
	WrapperPath string   `json:"wrapper_path"` // 安装在 PATH 中的运维包装脚本路径
	Hosts       []string `json:"hosts"`        // 已初始化的主机地址列表
}

// Setup 幂等初始化目标主机：检查 Linux/systemd/glibc，创建系统用户、部署目录和环境变量文件骨架，
// 写入 systemd 模板 unit、角色级 drop-in 覆盖文件与 /usr/local/bin/<app> 包装脚本并执行 daemon-reload。
func Setup(ctx context.Context, r Runner, t Target, opts SetupOptions, out io.Writer) (*SetupReport, error) {
	hosts, err := t.FilterHosts(opts.Role, opts.Host)
	if err != nil {
		return nil, err
	}
	unitPath := TemplateUnitPath(t.App)
	unitContent := RenderUnit(t)
	wrapperPath := WrapperPath(t.App)
	wrapperContent := RenderWrapper(t)
	envDir := path.Dir(t.EnvFile)
	binDir := path.Join(t.Dir, "bin")
	releasesDir := path.Join(t.Dir, "releases")
	storageDir := path.Join(t.Dir, "storage")
	report := &SetupReport{
		App:         t.App,
		Env:         t.EnvName,
		DryRun:      opts.DryRun,
		UnitPath:    unitPath,
		WrapperPath: wrapperPath,
		Hosts:       make([]string, 0, len(hosts)),
	}

	for _, h := range hosts {
		report.Hosts = append(report.Hosts, h.Addr)
		fmt.Fprintf(out, "[%s] 初始化 %s（角色 %s，用户 %s，目录 %s）\n",
			h.Addr, t.App, strings.Join(h.Roles, ","), t.ServiceUser, t.Dir)
		if opts.DryRun {
			fmt.Fprintf(out, "[%s] (dry-run) 写入 %s 与 %s，并创建目录 %s、%s、%s\n", h.Addr, unitPath, wrapperPath, binDir, releasesDir, storageDir)
			continue
		}
		// 1. 检查运行环境：必须为带 systemd 的 Linux；包含 SSR 前端时检查 glibc。
		checkScript := "set -e\n" +
			"test \"$(uname -s)\" = Linux || { echo 'target host must run Linux' >&2; exit 1; }\n" +
			"command -v systemctl >/dev/null || { echo 'systemd (systemctl) is required' >&2; exit 1; }\n"
		if opts.HasSSR {
			checkScript += "ldd --version 2>&1 | grep -qiE 'glibc|gnu libc' || { echo 'SSR frontend requires glibc (musl/Alpine is unsupported)' >&2; exit 1; }\n"
		}
		if _, err := RunCapture(ctx, r, h, checkScript); err != nil {
			return nil, fmt.Errorf("[%s] 环境检查失败: %w", h.Addr, err)
		}

		// 2. 创建系统用户、部署目录和环境变量文件骨架（已有 env 文件不覆盖）。
		prepScript := fmt.Sprintf(`set -e
if ! id -u %s >/dev/null 2>&1; then
  sudo useradd --system --no-create-home --shell /usr/sbin/nologin %s
fi
sudo install -d -m 755 %s %s %s
sudo install -d -m 755 -o %s -g %s %s
sudo install -d -m 750 -o root -g root %s
if ! sudo test -f %s; then
  sudo install -m 600 -o root -g root /dev/null %s
fi
`,
			shellQuote(t.ServiceUser), shellQuote(t.ServiceUser),
			shellQuote(t.Dir), shellQuote(binDir), shellQuote(releasesDir),
			shellQuote(t.ServiceUser), shellQuote(t.ServiceUser), shellQuote(storageDir),
			shellQuote(envDir),
			shellQuote(t.EnvFile), shellQuote(t.EnvFile),
		)
		if _, err := RunCapture(ctx, r, h, prepScript); err != nil {
			return nil, fmt.Errorf("[%s] 创建用户和目录失败: %w", h.Addr, err)
		}

		// 3. 写入 systemd 模板 unit 并同步角色级 drop-in，随后 daemon-reload。
		writeUnit := installFromStdinCommand("644", unitPath) + " && sudo systemctl daemon-reload"
		if err := r.Run(ctx, h, writeUnit, strings.NewReader(unitContent), io.Discard, out); err != nil {
			return nil, fmt.Errorf("[%s] 写入 %s 失败: %w", h.Addr, unitPath, err)
		}
		if err := syncDropIns(ctx, r, t, h); err != nil {
			return nil, err
		}
		// 4. 在 /usr/local/bin/<app> 安装运维包装脚本，登录服务器后可在任意目录直接运行应用子命令。
		writeWrapper := "sudo install -d -m 755 -o root -g root /usr/local/bin && " + installFromStdinCommand("755", wrapperPath)
		if err := r.Run(ctx, h, writeWrapper, strings.NewReader(wrapperContent), io.Discard, out); err != nil {
			return nil, fmt.Errorf("[%s] 写入 %s 失败: %w", h.Addr, wrapperPath, err)
		}
		fmt.Fprintf(out, "[%s] 已就绪：%s（命令入口 %s）\n", h.Addr, unitPath, wrapperPath)
	}
	fmt.Fprintf(out, "\n初始化完成。下一步：\n  1. 填写 .env 并执行 go tool tenon deploy env push .env --env %s\n  2. 执行 go tool tenon deploy --env %s\n", t.EnvName, t.EnvName)
	return report, nil
}
