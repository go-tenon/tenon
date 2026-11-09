// Package app 是产品的装配层：读取配置、初始化日志、连接数据库、执行迁移、
// 组装全局中间件与 huma API、创建邮件发送器（a.Mailer）、文件存储（a.Storage）、消息总线（a.Bus）
// 与定时任务（a.Scheduler），提供命令行子命令、优雅停机。serve 同时运行 HTTP 服务、消息处理函数与定时任务，
// 在 systemd 中运行时发送就绪通知与看门狗心跳（systemd 子命令输出 unit 文件）。
// 产品只需要写配置结构体、迁移，以及在 Setup 中构造自己的服务并挂载路由。
//
// app 只依赖 go-kit 的基础设施包，不认识任何业务模块；go-kit 的业务模块也不依赖 app，
// 它们提供服务、迁移与 rest.Endpoint，由产品在 Setup 中显式组装。
//
// # 产品结构
//
//	myapp/
//		main.go            app.Main(&cfg, app.Options{...})
//		config.go          type Config struct { app.Config `yaml:",inline"`; ... }
//		config.yaml
//		routes.go          Setup：构造服务、挂载中间件、API 与页面路由
//		migrations/        migrations.go（embed）与全部 .sql
//		<业务模块>/
//
// # Setup
//
//	func Setup(a *app.App, cfg *Config) error {
//		users := account.NewService(a.DB, ...)
//
//		// 1. 先挂载中间件（chi 要求在注册路由之前）。
//		a.Router.Use(auth.LoadSession(a.Sessions, users.LoadPrincipal))
//
//		// 2. 再注册路由。a.API() 首次调用时会注册 /docs 等文档路由。
//		api := a.API()
//		api.UseMiddleware(rest.Authorize(api, users.VerifyAccessToken))
//		v1 := huma.NewGroup(api, "/api/v1")
//		rest.Post(v1, "/auth/login", account.Login(users)) // go-kit 模块提供的接口
//		rest.Mount(v1, order.APIActions(orders)...)        // 产品自己的接口
//
//		a.Router.Get("/", pages.Home)
//		return nil
//	}
//
// # 迁移
//
// 迁移目录与命名规则见 database 包文档。开发时：
//
//	go run . migrate create create_orders   新建迁移
//	go run . migrate publish                复制 Options.Publish 中模块的新迁移（会话表的迁移总会发布）
//
// serve 启动时自动执行迁移（带 advisory lock，多实例同时启动也安全）；
// 需要单独执行时用 migrate up 并给 serve 加 --migrate=false。
package app
