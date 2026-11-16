// Package natsrpc 是业务动作的 RPC 入口：其他服务经由 NATS 的请求-响应同步调用业务动作，调用方可以用任何语言编写。
// 线路协议见 docs/rpc/protocol.md，它建立在 NATS 官方的服务协议（Service API）之上，
// 数据格式、错误模型和认证与 HTTP 接口相同。
//
// 提供端点：
//
//	srv := natsrpc.New(natsrpc.Options{Prefix: "blog.v1", Validate: tokens.Validate, Accounts: accounts})
//	natsrpc.Register(srv, natsrpc.Endpoint{Name: "articles.get", Permissions: []string{"articles:read"}, Idempotent: true}, get.RPC)
//	err := srv.Serve(ctx, nc) // nc 来自 natsx.Connect，直到 ctx 被取消
//
// 调用端点：
//
//	article, err := natsrpc.Call[ArticleDTO](ctx, nc, "blog.v1.articles.get", GetInput{ID: id}, natsrpc.Token(token))
package natsrpc
