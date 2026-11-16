# 接口文档

**OpenAPI** 是描述 HTTP 接口的标准格式：有哪些接口，每个接口的方法、路径、参数和返回的字段。有了它，就能自动生成可以交互的文档页面、各种语言的客户端代码，也能在 CI 中检查接口有没有被意外修改。

在 Tenon 中你不需要手写 OpenAPI 文档：[路由](routing.md)的输入、输出类型和 `huma.Operation` 已经描述了接口，框架据此生成文档。这一页介绍怎样查看和导出文档、怎样让文档写得更清楚，以及怎样给 API 加上版本号。

阅读前需要了解：[路由](routing.md)；[结构体标签](../concepts/go-primer.md#struct-tags)。

- [在线查看](#viewing-docs)
- [说明与示例](#describing-apis)
- [离线导出](#exporting-openapi)
- [在 CI 中发现接口的变化](#checking-changes)
- [API 版本](#api-versioning)
- [常见问题](#troubleshooting)

<a id="viewing-docs"></a>
## 在线查看

应用运行时，Huma 会自动提供接口文档：

- `/docs`：可交互的接口文档页面；
- `/openapi.json`：OpenAPI 3.1 文档。

<a id="describing-apis"></a>
## 说明与示例

Go 类型只能说明字段的类型和取值规则，字段的含义和示例值需要你自己写进声明：

| 写在哪里 | 写什么 | 在文档中成为 |
| --- | --- | --- |
| `huma.Operation` | `Summary` 一句话说明，`Description` 写更长的说明，支持 Markdown | 路由的标题和说明 |
| 字段的 `doc` 标签 | 字段的含义，以及类型和验证规则看不出来的约定 | 字段的 `description` |
| 字段的 `example` 标签 | 一个典型的值 | 字段的 `examples` |

不写 `example` 时，文档页只能按类型填 `"string"`、`0` 这样的占位值。给创建文章的请求体加上示例：

```go
type Changes struct {
    Title   string `json:"title" minLength:"1" maxLength:"200" example:"Hello World"`
    Slug    string `json:"slug" maxLength:"100" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$" patternDescription:"小写字母、数字和连字符" doc:"文章地址中的标识，全站唯一；发布后固定" example:"hello-world"`
    Content string `json:"content" example:"这是我的第一篇文章。"`
}
```

`/docs` 中这个接口的请求示例就会是 `{"title":"Hello World","slug":"hello-world","content":"这是我的第一篇文章。"}`，调用方可以直接修改后发送。路径参数、查询参数和请求头的写法相同，例如 `` ID uuid.UUID `path:"id" example:"0193a5c2-7d1e-7b3a-9f61-2c4e8d0a1b2c"` ``；响应体的写法见[共享响应结构](routing.md#shared-responses)中的 `ArticleDTO`。

`doc` 只写类型和规则看不出来的约定。`minLength`、`pattern`、`enum` 等验证规则本身就会出现在文档中。

`example` 的值按字段类型解析：数字写 `example:"50"`，数组和对象写 JSON，例如 `example:"[\"go\",\"api\"]"`。值和类型不符时，`rest.Register` 在注册时 panic，执行 `routes` 或 `openapi` 命令就能发现。这些标签只写入文档。

<a id="exporting-openapi"></a>
## 离线导出

很多时候，你希望离线生成 OpenAPI 文档，例如在 CI 中检查接口变更，或者为前端[生成客户端代码](../responses/clients.md)。只需用默认配置和 `nil` 依赖调用构造路由的同一个函数，再用 `rest.Lookup` 取出其中的 API：

```go
// routes 是 serve 也在用的路由函数，见依赖装配
api, err := rest.Lookup(routes(config.Default[Config](), nil), "")
if err != nil {
    return err
}
data, err := api.OpenAPIJSON()
```

`data` 就是 OpenAPI 文档的 JSON 内容。`Lookup` 的第二个参数是 `BasePath`，路由中只有一个 API 时传空字符串。因为服务运行和导出文档用的是同一个路由函数，导出的文档与实际运行的接口始终保持一致。

> [!TIP]
> 应用的 [`openapi` 命令](../general/commands.md#openapi-command)就是这样导出的：`./bin/api openapi > openapi.json`。

<a id="checking-changes"></a>
## 在 CI 中发现接口的变化

改了一个字段的名字，前端和其他调用方就可能出错。把导出的文档提交到代码仓库，CI 中重新导出一次，和提交的版本比较，接口有变化就让检查失败：

```bash
./bin/api openapi > openapi.json
git diff --exit-code openapi.json
```

`git diff --exit-code` 在文件有差异时以非 0 退出，CI 的这一步随之失败，并显示接口具体改了什么。确认变化是有意的之后，提交新的 `openapi.json`。这样，每次接口变化都会出现在代码评审中，不会被悄悄地带上线。

用脚手架生成的项目中，前端的客户端代码也提交在仓库中，它同样随接口变化，所以 `bun run generate` 之后检查 `git diff` 也能达到同样的效果，见[开发工具](../installation/tooling.md#generate)。

<a id="api-versioning"></a>
## API 版本

只提供 JSON API 的应用，通常直接挂在根路径上，由部署决定前缀，例如 `https://api.example.com/v1`。但如果 API 要和[页面](../responses/views.md)或单页应用共用一个 Router，就需要把它挂在带版本号的子路由下，并把同一个前缀设为 `BasePath`：

```go
router.Route("/api/v1", func(r chi.Router) {
    r.Use(httpx.Timeout(cfg.HTTP.Timeout))
    Routes(rest.New(r, rest.Options{Title: "Blog API", Version: "1.0.0", BasePath: "/api/v1"}))
})
```

路由按子路由注册，接口地址是 `/api/v1/articles`。`BasePath` 写入 OpenAPI 文档的 `servers`，文档页 `/api/v1/docs` 和生成的客户端据此拼出完整地址；`routes` 命令显示的也是完整路径。

需要不兼容的修改时，新版本挂在另一个子路由下，用自己的 `rest.API`。两个版本各有一份 OpenAPI 文档，共用同一套业务动作：

```go
router.Route("/api/v2", func(r chi.Router) {
    r.Use(httpx.Timeout(cfg.HTTP.Timeout))
    RoutesV2(rest.New(r, rest.Options{Title: "Blog API", Version: "2.0.0", BasePath: "/api/v2"}))
})
```

一个应用中有多个 API 时，[`openapi`](../general/commands.md#openapi-command) 和 [`client`](../responses/clients.md) 命令用 `--base-path` 选择其中一个，例如 `./bin/api openapi --base-path /api/v2`。

<a id="troubleshooting"></a>
## 常见问题

**`/docs` 返回 404**

API 挂在子路由下时，文档页也在子路由下，例如 `BasePath` 是 `/api` 时访问 `/api/docs`。

**文档页中的请求示例是 `"string"`、`0` 这样的占位值**

字段上没有写 `example` 标签，见[说明与示例](#describing-apis)。

**注册时 panic，提示 example 的值无法解析**

`example` 的值和字段类型不符，例如数字字段写了 `example:"abc"`，数组字段没有写成 JSON。改正后用 `routes` 命令检查。

**`openapi` 命令报错找到了多个 API**

一个应用中有多个 API（例如 `/api/v1` 和 `/api/v2`），用 `--base-path` 选择其中一个，见 [API 版本](#api-versioning)。
