# 接口文档

框架从路由的输入、输出类型和 `huma.Operation` 生成 OpenAPI 3.1 文档，不用手写。

包：`httpx/rest`、`command/httpcmd`。

<a id="viewing-docs"></a>
## 在线查看：`/docs` 和 `/openapi.json`

每个 `rest.API` 在自己的路由下提供文档页面 `/docs`、OpenAPI 3.1 文档 `/openapi.json` 和转换后的 3.0.3 文档 `/openapi-3.0.json`。API 挂在子路由下时文档也在子路由下，例如 `BasePath` 是 `/api` 时访问 `/api/docs`。`BasePath` 带路径参数时（例如每个租户一个前缀 `/t/{tenant}/api`），文档页和 OpenAPI 文档中的地址按这次访问的地址填写：打开 `/t/acme/api/docs` 时，文档页读取 `/t/acme/api/openapi.yaml`，“发送请求”也发到 `/t/acme/api` 下。

<a id="describing-apis"></a>
## 写说明和示例：`doc`、`example` 标签

`huma.Operation` 的 `Summary`、`Description`（支持 Markdown）成为路由的标题和说明。字段的 `doc` 标签写类型和校验规则看不出来的约定，`example` 标签写一个典型的值：

```go
type Changes struct {
    Title string `json:"title" minLength:"1" maxLength:"200" example:"Hello World"`
    Slug  string `json:"slug" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$" doc:"文章地址中的标识，全站唯一；发布后固定" example:"hello-world"`
}
```

- 没有 `example` 时，`/docs` 的请求示例是 `"string"`、`0` 这样的占位值。路径参数、查询参数、请求头和响应体的字段写法相同。
- 校验规则（`minLength`、`pattern`、`enum` 等）自动出现在文档中。
- `example` 按字段类型解析：数字写 `example:"50"`，数组和对象写 JSON，例如 `example:"[\"go\",\"api\"]"`。与类型不符时注册路由就 panic，运行 `routes` 命令即可发现。

<a id="exporting-openapi"></a>
## 离线导出：`openapi` 命令和 `rest.Lookup`

`httpcmd.Commands` 给应用加上 [`openapi` 命令](../general/commands.md#openapi-command)，离线构造路由并输出文档：

```bash
./bin/blog openapi > openapi.json
```

在 Go 代码中用 `rest.Lookup` 从路由中取出 API。`router` 是用默认配置和未打开资源的依赖构造的完整路由，与 `serve` 用同一个路由函数，见[多个运行部分](../concepts/dependencies.md#running-parts)：

```go
api, err := rest.Lookup(router, "") // 第二个参数是 BasePath；只有一个 API 时传空字符串
if err != nil {
    return err
}
data, err := api.OpenAPIJSON()
```

注册路由时只保存依赖、处理请求时才使用依赖，所以这样构造路由不会出错，导出的文档与运行的接口一致。

<a id="checking-changes"></a>
## 在 CI 中检查接口的变化

把导出的文档提交到仓库，CI 重新导出并比较，接口有变化时失败：

```bash
./bin/blog openapi > openapi.json
git diff --exit-code openapi.json
```

确认变化是有意的之后提交新的 `openapi.json`。脚手架生成的项目提交了前端的客户端代码，`bun run generate` 之后检查 `git diff` 效果相同，见[开发工具](../installation/tooling.md#generate)。

<a id="api-versioning"></a>
## API 版本：`rest.Options.BasePath`

API 和[页面](../frontend/hosting.md)共用一个 Router 时，API 挂在带版本号的子路由下，同一个前缀设为 `BasePath`。`Routes` 是注册这个 API 全部路由的函数：

```go
router.Route("/api/v1", func(r chi.Router) {
    r.Use(httpx.Timeout(cfg.HTTP.Timeout))
    Routes(rest.New(r, rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api/v1"}))
})
```

- `BasePath` 写入 OpenAPI 的 `servers`，文档页和生成的客户端据此拼出完整地址。
- 不兼容的修改发布为新的 API：挂在 `/api/v2` 下，用自己的 `rest.New` 和 `BasePath`，共用同一套业务动作，旧版本继续服务已发布的客户端。
- 有多个 API 时，`openapi` 和 [`client`](clients.md) 命令用 `--base-path /api/v2` 选择其中一个，不传时报错。
