---
name: tenon-http-api
description: 在 Tenon 应用中新增或修改 JSON 接口：routes.go 中的路由声明、访问策略、请求的输入类型与验证标签、响应与分页、业务错误，以及改完后重新生成前端的客户端。写 REST 接口、改请求或响应的字段、加分页和筛选时使用。
---

# HTTP 接口

接口用 `rest.Register` 注册，基于 Huma：请求和响应是 Go 的结构体，标签声明验证规则，OpenAPI 文档和前端的客户端由它们生成。业务规则写在业务动作的 `Handle` 中（技能 `tenon-action`），这里只讲 HTTP 这一层。

## 步骤

1. **输入类型**：路径参数 `path:"id"`，查询参数 `query:"page"`，请求头 `header:"..."`，JSON 请求体放在 `Body` 字段中。请求体通常是模块中共用的结构体，例如 `Body Changes`。见 `tenon/docs/http/requests.md#input-types`。
2. **验证标签**：`minLength`、`maxLength`、`pattern`、`minimum`、`enum`、`format:"uuid"` 等，写在结构体字段上，前端的校验规则也由它生成。请求体字段默认必填，`json` 标签加 `omitempty` 变为可选；查询参数和请求头默认可选。见 `tenon/docs/http/requests.md#available-validation-rules`、`tenon/docs/http/requests.md#required-and-optional-fields`。
3. **响应类型**：`type ArticleOutput struct { Body ArticleDTO }`。响应用 DTO 和转换函数，不直接返回数据库模型，见 `tenon/docs/http/routing.md#shared-responses`。
4. **处理器**：业务动作的 `HTTP` 方法，`identity.FromContext(ctx)` 取出调用者，调用 `Handle`，见 `tenon/docs/http/routing.md#http-actions`。
5. **在模块的 `routes.go` 中声明路由**：`huma.Operation{OperationID: "article-publish", Method: http.MethodPost, Path: "/articles/{id}/publish", Summary: "发布文章"}`。`OperationID` 的第一段是分组，生成的客户端按它分组（`api.article.publish`）。见 `tenon/docs/http/routing.md#module-registration`。
6. **声明访问策略**：每条路由都要显式声明，否则注册时 panic。公开的路由 `rest.Public()(&op)`；需要登录的路由用入口传进来的 `rest.RequireToken(api, validate, "articles:write")`。见 `tenon/docs/http/routing.md#access-policies`，权限见技能 `tenon-auth`。
7. **注册**：`rest.Register(api, op, NewPublish(db).HTTP)`。新模块要在 `cmd/<应用>/main.go` 的 `routes` 中调用它的 `RegisterRoutes`。
8. **重新生成客户端**：`bun run generate`（`bun run dev` 运行时会自动生成），提交 `packages/client` 中的变化。
9. **检查**：`go run ./cmd/<应用> routes` 看路由和访问策略，`go run ./cmd/<应用> openapi` 看接口文档。

## 要点

- 创建资源的接口在 `Operation` 中写 `DefaultStatus: http.StatusCreated`，状态码见 `tenon/docs/http/responses.md#status-codes`。
- 列表用框架的分页：固定分页见 `tenon/docs/http/responses.md#offset-pagination`，游标分页见 `tenon/docs/http/responses.md#cursor-pagination`，筛选与排序见 `tenon/docs/http/responses.md#filtering-and-sorting`。
- 需要显示文字的枚举用 `enum` 包，见 `tenon/docs/http/requests.md#enums`。
- 业务错误从 `Handle` 原样返回，不在处理器中自己写状态码，见 `tenon/docs/general/errors.md#returning-errors`。
- 线路上的约定（错误响应、分页、请求头）以客户端协议为准：`tenon/docs/http/protocol.md`。
- 不兼容的修改通过新的 `BasePath` 发布，见 `tenon/docs/http/openapi.md`。
- 生成的客户端不手改，见 `tenon/docs/http/clients.md#generating-types`。
