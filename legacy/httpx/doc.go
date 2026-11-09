// Package httpx 提供基于 chi 的 HTTP 服务基础设施：路由构造、Server 生命周期，
// 以及与 logger 包对接的通用中间件。
//
// # 分工
//
// 路由直接使用 github.com/go-chi/chi/v5，本包不包装 chi 的 API。
// chi 自带、可直接使用的中间件不在本包重复实现：
//
//	middleware.Compress、Timeout、StripSlashes   github.com/go-chi/chi/v5/middleware
//	CORS                                         github.com/go-chi/cors
//	限流                                         github.com/go-chi/httprate
//	CSRF                                         标准库 http.CrossOriginProtection
//
// 本包只提供 chi 中缺少、或 chi 的实现不适合生产环境直接使用的部分：
//
//	RequestID      透传或生成 X-Request-ID，写入 logger 的 context 字段
//	RealIP         只信任配置中的代理（chi 的 RealIP 无条件信任 X-Forwarded-For，可被伪造）
//	AccessLog      基于 slog 的访问日志，记录 chi 的路由模板
//	Recover        panic 转 500，带调用栈记录日志（chi 的 Recoverer 直接写 stderr）
//	SecureHeaders  常用安全响应头
//	Server         超时默认值、健康检查、优雅停机
//
// # 用法
//
// 本包不替产品组装中间件，产品在 main 中显式挂载，推荐顺序如下：
//
//	r := chi.NewRouter()
//	r.Use(
//		httpx.RealIP(cfg.HTTP.TrustedProxies...), // 最外层：之后看到的都是真实客户端 IP
//		httpx.RequestID,                          // 在 AccessLog 之前：访问日志带 request_id
//		httpx.AccessLog(nil),
//		httpx.Recover(nil),                       // 在 AccessLog 之内：panic 产生的 500 也会被记录
//		httpx.SecureHeaders,
//	)
//	r.Get("/", home)
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//	if err := httpx.NewServer(cfg.HTTP, r).Run(ctx); err != nil { ... }
//
// 各中间件相互独立，可以按需取舍或在其间插入其他中间件。
// AccessLog 需要通过 chi 的 Use 挂载，才能读取到匹配的路由模板。
//
// 中间件记录日志时使用 slog.Default()（在请求时读取），
// 因此 main 中调用 slog.SetDefault 的先后顺序不影响结果。
package httpx
