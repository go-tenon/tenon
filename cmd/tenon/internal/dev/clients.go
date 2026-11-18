package dev

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/cmd/tenon/internal/project"
	"github.com/go-tenon/tenon/frontend"
)

// ClientWorkspaces 返回种类 kind 的客户端要加入 Bun workspace 的框架前端包，例如 .tenon/js/electron。
func ClientWorkspaces(kind string) []string {
	switch kind {
	case "electron":
		return []string{JSDir + "/host", JSDir + "/electron"}
	}
	return nil
}

// clientWatcher 返回 tenon dev 中运行客户端 c 的进程：electron 在客户端目录中运行 bun run dev（打包主进程后启动
// Electron），页面从前端的开发服务器取（TENON_DEV_FRONTEND），接口转发给本机的应用（TENON_SERVER）。
func (d dev) clientWatcher(c project.Client) watcher {
	return watcher{c.Dir, []string{"bun", "run", "dev"}, []string{
		"TENON_DEV_FRONTEND=" + d.servers[c.Frontend],
		"TENON_SERVER=" + d.cfg.URL,
	}}
}

// BuildClient 打包客户端 c：网页宿主先把它显示的前端 f 按 SPA 模式构建（TENON_RENDER=csr，不论 f 在网页上是否服务端
// 渲染），复制到 c.Dir/dist/renderer，再在客户端目录中运行 bun run package。
func BuildClient(ctx context.Context, c project.Client, f project.Frontend, out, errOut io.Writer) error {
	if c.Kind != "electron" {
		return errors.Newf("dev: cannot build %s clients", c.Kind)
	}
	build := exec.CommandContext(ctx, "bun", "run", "build")
	build.Dir = f.Dir
	build.Env = append(os.Environ(), "TENON_RENDER=csr")
	build.Stdout, build.Stderr = out, errOut
	if err := build.Run(); err != nil {
		return errors.Wrapf(err, "dev: build %s for %s", f.Dir, c.Dir)
	}
	renderer := filepath.Join(c.Dir, "dist", "renderer")
	if err := os.RemoveAll(renderer); err != nil {
		return errors.Wrap(err, "dev: clear renderer")
	}
	if err := copyBuild(filepath.Join(f.Dir, "dist", "client"), renderer, true); err != nil {
		return errors.Wrapf(err, "dev: %s", f.Dir)
	}
	pack := exec.CommandContext(ctx, "bun", "run", "package")
	pack.Dir = c.Dir
	pack.Stdout, pack.Stderr = out, errOut
	return errors.Wrapf(pack.Run(), "dev: package %s", c.Dir)
}

// ensureShell 确认 dir 中有页面外壳：没有时说明前端的 Vite 配置没有用 @go-tenon/start 的插件。
func ensureShell(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, frontend.ShellFile)); err != nil {
		return errors.Wrapf(err, "no page shell in %s; use tenon() from @go-tenon/start/vite in the vite.config", dir)
	}
	return nil
}
