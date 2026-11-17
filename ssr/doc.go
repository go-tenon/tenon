// Package ssr 托管页面的服务端渲染进程：Bun 编译出的 SSR 可执行文件嵌入 Go 程序，由 Host 从内存中启动、
// 监控和重启，页面请求经 unix socket 转给它。Go 仍然是唯一的 HTTP 入口和唯一的部署单元：
// 接口由 Go 处理，页面由 Bun 渲染，Bun 需要数据时经另一个 unix socket 回调 Go 的接口。
// Bun 不连接数据库，也不持有密钥。
//
// Host.Handler 是页面的处理器，挂在接口之后：每个请求生成 CSP nonce 交给 Bun，HTML 响应带上对应的
// Content-Security-Policy；请求 ID 和 W3C traceparent 一并转交，渲染和它调用的接口在同一条链路中。
// Host.Run 包住对外的 HTTP 服务：先启动内部接口入口和 SSR 进程，等它就绪后再开始接收请求；
// 停止时先排空对外的请求（进行中的页面渲染完成），再停 SSR 进程，最后关闭内部入口。
//
//	pages, err := ssr.New(ssr.Options{Executable: web.SSR, API: api})
//	router.Mount("/api", api)
//	router.Handle("/*", pages.Handler())
//	return pages.Run(ctx, func(ctx context.Context) error {
//	    return httpx.ListenAndServe(ctx, cfg.HTTP, router)
//	})
//
// Host.Render 用 SSR 进程中的打印模板把数据渲染成完整的 HTML，交给 pdf 包生成 PDF；需要生成 PDF 的进程
// （例如队列 worker）同样用 Run 包住自己的运行函数。
//
// Options.DevServer 不为空时（tenon dev），不启动 SSR 进程，页面请求转给前端的开发服务器。
// 从内存执行依赖 Linux 的 memfd；其他系统上 Run 返回 ErrUnsupported，开发时用 DevServer。
package ssr
