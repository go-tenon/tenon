// Package frontend 托管应用的前端：go tool tenon build 把 apps 中的每个前端构建好，放进 apps/dist，
// 应用用 go:embed 嵌入这个目录，Host 按挂载路径提供它们。Go 仍然是唯一的 HTTP 入口和唯一的部署单元。
//
// 每个前端在 package.json 的 tenon 字段中声明挂载路径（base）和是否在服务端渲染（ssr）：
//
//   - 只在浏览器中渲染（ssr 为 false）：Go 直接返回静态文件，其余路径返回页面外壳（ShellFile），
//     页面由浏览器中的路由渲染。程序中不带 Bun，可以在任何系统上运行。
//   - 服务端渲染（ssr 为 true）：静态文件和预渲染的页面同样由 Go 返回，其余请求经 unix socket 转给这个前端的
//     SSR 进程。全部 SSR 前端编进同一个 Bun 可执行文件（ExecutableFile），只在有 SSR 前端时存在；
//     Host 从内存中为每个 SSR 前端启动一个进程，监控并在意外退出时重启。Bun 需要数据时经另一个 unix socket
//     回调 Go 的接口，它不连接数据库，也不持有密钥。
//
// 返回 HTML 时，Host 为每个请求生成 CSP nonce，替换构建时写入页面的占位 nonce（或交给 SSR 进程），
// 并带上对应的 Content-Security-Policy；请求 ID 和 W3C traceparent 一并转交，渲染和它调用的接口在同一条链路中。
//
//	pages, err := frontend.New(apps.Dist, frontend.Options{API: api, DevServers: cfg.DevServers})
//	router.Mount("/api", api)
//	router.Handle("/*", pages.Handler())
//	return pages.Run(ctx, func(ctx context.Context) error {
//	    return httpx.ListenAndServe(ctx, cfg.HTTP, router)
//	})
//
// Host.Run 包住对外的 HTTP 服务：先启动内部接口入口和 SSR 进程，等它们就绪后再开始接收请求；停止时先排空
// 对外的请求（进行中的页面渲染完成），再停 SSR 进程，最后关闭内部入口。没有 SSR 前端时它只运行 serve。
//
// Host.Render 用 SSR 前端中的打印模板把数据渲染成完整的 HTML，交给 pdf 包生成 PDF；需要生成 PDF 的进程
// （例如队列 worker）同样用 Run 包住自己的运行函数。
//
// Options.DevServers 不为空时（tenon dev），不启动 SSR 进程，每个前端的请求转给它的开发服务器。
// 从内存执行 SSR 进程依赖 Linux 的 memfd；其他系统上有 SSR 前端时 Run 返回 ErrUnsupported，开发时用 DevServers。
package frontend
