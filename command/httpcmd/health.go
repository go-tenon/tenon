package httpcmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/httpx"
)

// LivePath 是 health 命令请求的存活检查路径，应用在路由中注册它，例如 router.Get(LivePath, ...)。
const LivePath = "/livez"

// Health 构造 health 命令：加载配置，请求本机 HTTP 服务的 LivePath，2xx 时以 0 退出，否则以 ExitFailure 退出。
// 它用于 Docker Compose 和 Swarm 的健康检查，镜像中不需要 curl：healthcheck 的 test 写 ["CMD", "/app/blog", "health"]。
// http 从应用配置中取出 HTTP 服务的配置；监听地址的主机为空、0.0.0.0 或 :: 时请求 127.0.0.1。
func Health[C any](http func(C) httpx.Config) *cobra.Command {
	var timeout time.Duration
	cmd := leaf("health", "检查本机的 HTTP 服务是否存活，供 Docker 的健康检查使用", func(cmd *cobra.Command) error {
		cfg, err := command.LoadConfig[C](cmd)
		if err != nil {
			return err
		}
		url, err := liveURL(http(cfg).Addr)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		return command.AsExitError(command.ExitFailure, probe(ctx, url))
	})
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "等待响应的最长时间")
	return command.InGroup(cmd, command.GroupOps)
}

// liveURL 返回 HTTP 服务监听在 addr 上时本机访问 LivePath 的地址。
func liveURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", command.AsExitError(command.ExitConfig, errors.Wrapf(err, "health: http.addr %q", addr))
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + LivePath, nil
}

// probe 请求 url，响应不是 2xx 时返回错误。
func probe(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "health")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("health: GET %s: %s", url, resp.Status)
	}
	return nil
}
