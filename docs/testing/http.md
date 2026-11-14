# 测试：HTTP 与命令

测试 HTTP 接口时，不需要真正监听端口：用 `httptest` 创建请求和响应记录器，然后直接调用 `api.Handler()` 即可。

有一点很重要：请调用应用自己的路由注册函数，而不是在测试中重新声明路由。这样，测试覆盖的就是真实的路由配置、认证和输入验证；如果有人改坏了 `routes.go`，测试会发现它。命令的测试也一样，在进程内执行真实的根命令。

- [HTTP 测试](#http-tests)
    - [测试认证与错误响应](#testing-auth-and-errors)
- [页面测试](#page-tests)
- [命令测试](#command-tests)
- [测试 OpenAPI 文档](#testing-openapi)
- [API 参考](#api-reference)

<a id="http-tests"></a>
## HTTP 测试

测试 HTTP 接口时，不需要真正监听端口。用 `httptest` 创建请求和响应记录器，然后直接调用 `api.Handler()` 即可。

测试文件按业务动作命名。下面测试[创建文章](../http/routing.md#http-actions)的 HTTP 入口：

```go
func newTestAPI() *rest.API {
    api := rest.New(rest.Options{Accounts: identity.Accounts{user.Type: nil}})
    tokens := map[string]identity.Subject{
        "alice-token": {Type: user.Type, ID: "alice", Permissions: []string{"articles"}},
        "guest-token": {Type: user.Type, ID: "guest"},
    }
    validate := func(_ context.Context, token string) (identity.Subject, error) {
        if sub, ok := tokens[token]; ok {
            return sub, nil
        }
        return identity.Subject{}, identity.ErrUnauthenticated.New()
    }
    // 这些请求在调用 Handle 之前就会被拒绝，所以不需要数据库
    article.Register(api, rest.RequireBearer(api, validate, "articles"), nil)
    return api
}

func TestCreateRejectsBadRequests(t *testing.T) {
    const valid = `{"title":"Hello World","slug":"hello-world","content":"……"}`
    tests := []struct {
        name     string
        token    string
        body     string
        status   int
        code     string
        location string
    }{
        {"没有 token", "", valid, http.StatusUnauthorized, "auth.unauthenticated", ""},
        {"缺少权限", "guest-token", valid, http.StatusForbidden, "auth.forbidden", ""},
        {"slug 格式不对", "alice-token", `{"title":"Hello World","slug":"Hello World","content":""}`, http.StatusUnprocessableEntity, "", "body.slug"},
    }
    api := newTestAPI()
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            req := httptest.NewRequest(http.MethodPost, "/articles", strings.NewReader(tc.body))
            req.Header.Set("Content-Type", "application/json")
            if tc.token != "" {
                req.Header.Set("Authorization", "Bearer "+tc.token)
            }
            rr := httptest.NewRecorder()
            api.Handler().ServeHTTP(rr, req)

            var problem struct {
                Code   string `json:"code"`
                Errors []struct {
                    Location string `json:"location"`
                } `json:"errors"`
            }
            if err := json.Unmarshal(rr.Body.Bytes(), &problem); err != nil {
                t.Fatal(err)
            }
            if rr.Code != tc.status || problem.Code != tc.code {
                t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
            }
            if tc.location != "" && (len(problem.Errors) == 0 || problem.Errors[0].Location != tc.location) {
                t.Fatalf("errors=%s", rr.Body.String())
            }
        })
    }
}
```

这个测试用表格列出了三种在调用 `Handle` 之前就会被拒绝的请求：没有 token、缺少权限、输入格式不对。它们不会触及数据库，所以 `db` 可以传 `nil`，测试运行得很快。

<a id="testing-auth-and-errors"></a>
### 测试认证与错误响应

请通过完整的 `api.Handler()` 来测试认证、验证错误和越权访问，而不是直接调用处理器函数。这样测试才能覆盖认证中间件、输入验证和错误转换的完整过程。一组典型的接口测试包括：

- 不带 `Authorization` 头的请求返回 `401`；
- 缺少权限的 token 返回 `403`；
- 不合法的请求体返回 `422`，并检查 `errors[].location`；
- 访问他人的资源返回 `403` 或 `404`；
- 错误响应中的 `code` 与预期的错误码一致。

前三种由上面的测试覆盖。后两种取决于数据库中的数据，更适合直接测试业务动作，见[测试业务动作](database.md#testing-actions)。

<a id="page-tests"></a>
## 页面测试

[服务端渲染的页面](../responses/views.md)用 `web/webtest` 测试。`webtest.Client` 像浏览器一样向页面路由发送请求，保存响应设置的 cookie，并读取 htmx 的响应头。下面测试文章页和列表页：

```go
func TestPages(t *testing.T) {
    db := dbtest.Open(t, migrations.FS)
    alice := identity.Subject{Type: user.Type, ID: "alice", Permissions: []string{"articles"}}
    ctx := context.Background()
    draft, err := article.NewCreate(db).Handle(ctx, alice, article.Changes{Title: "Draft", Slug: "draft"})
    if err != nil {
        t.Fatal(err)
    }
    hello, err := article.NewCreate(db).Handle(ctx, alice, article.Changes{Title: "Hello", Slug: "hello"})
    if err != nil {
        t.Fatal(err)
    }
    if _, err := article.NewPublish(db).Handle(ctx, alice, hello.ID); err != nil {
        t.Fatal(err)
    }

    pages := web.New(web.Options{ErrorPage: ui.ErrorPage})
    router := chi.NewRouter()
    articleweb.Register(router, pages, db)
    c := webtest.New(t, router)

    // 已发布的文章可以访问，草稿对访客是 404
    if res := c.Get("/articles/hello"); res.Code != http.StatusOK || !strings.Contains(res.Text(), "<h1>Hello</h1>") {
        t.Fatalf("published: %d", res.Code)
    }
    if res := c.Get("/articles/" + draft.Slug); res.Code != http.StatusNotFound {
        t.Fatalf("draft: %d", res.Code)
    }
    // 列表的局部请求只返回列表区域，并把地址栏改为这个状态
    res := c.Get("/articles?q=hel&page=1", webtest.HTMX)
    if !strings.Contains(res.Text(), "Hello") || res.PushURL() != "/articles?q=hel" {
        t.Fatalf("list: %d %q", res.Code, res.PushURL())
    }
}
```

页面路由与应用相同，测试覆盖了授权、错误页和局部更新。请求默认是浏览器的整页请求。`webtest.HTMX` 让它像 htmx 替换页面中某个元素时发出的一样，`webtest.Header` 设置其他请求头，例如 `Accept-Language`。multipart 表单等其他请求用 `c.Do` 发送。响应除了 `Code`、`Text()`，还可以读取 htmx 的响应头：

| 方法 | 返回 |
| --- | --- |
| `Redirect()` | 跳转的目标：`303` 的 `Location`，或 htmx 请求的 `HX-Redirect` |
| `Messages()` | 随局部请求送达的错误提示，见[错误页](../responses/views.md#error-pages) |
| `Event(name)` | `HX-Trigger` 中事件 `name` 的 detail |
| `PushURL()` | `web.PushURL` 推送的地址 |

`webtest` 不运行浏览器中的脚本：确认对话框能否打开、提示是否弹出这类交互，由框架的组件保证，应用测试服务端的响应即可。

<a id="command-tests"></a>
## 命令测试

测试命令行时，不需要启动新的进程。根命令是原生的 `*cobra.Command`，用 Cobra 的 `SetOut`、`SetArgs` 把输出重定向到 `bytes.Buffer` 并指定参数，然后执行命令并检查输出和退出码。下面测试应用真实的根命令，`newRoot` 是 `main.go` 中组装根命令的函数，见[命令行](../general/commands.md#declaring-the-cli)：

```go
func run(args ...string) (string, error) {
    var out bytes.Buffer
    root := newRoot()
    root.SetOut(&out)
    root.SetErr(&out)
    root.SetArgs(args)
    err := root.ExecuteContext(context.Background())
    return out.String(), err
}

func TestOpenAPIDoesNotNeedConfig(t *testing.T) {
    out, err := run("openapi")
    if err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(out, `"/articles/{id}/publish"`) {
        t.Fatalf("missing route in %s", out)
    }
}

func TestRoutesFilters(t *testing.T) {
    out, err := run("routes", "--method", "post", "--path", "/articles")
    if err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(out, "article-create") || !strings.Contains(out, "article-publish") {
        t.Fatalf("missing route in %s", out)
    }
    if strings.Contains(out, "article-get") || strings.Contains(out, "/docs") {
        t.Fatalf("route filters did not apply: %s", out)
    }
}

func TestConfigCheckReportsMissingFile(t *testing.T) {
    _, err := run("config", "check", "--config", filepath.Join(t.TempDir(), "missing.yaml"))
    if command.ExitCode(err) != command.ExitConfig {
        t.Fatalf("exit code = %d, err = %v", command.ExitCode(err), err)
    }
}
```

前两个测试确认 `openapi`、`routes` 命令可以离线查看真实的接口声明，不需要配置文件；最后一个测试确认配置文件缺失时，`config check` 以 `ExitConfig` 退出。

<a id="testing-openapi"></a>
## 测试 OpenAPI 文档

生成文档时，路由用 `nil` 依赖注册，处理器不会被调用。用应用的 [DocRoutes](../concepts/dependencies.md#assembling-the-application) 注册全部路由并生成文档，可以确保没有哪个构造函数在注册时使用了依赖，否则测试会 panic：

```go
func TestOpenAPI(t *testing.T) {
    opts := bootstrap.API
    opts.Mode = rest.ModeOpenAPI
    api := rest.New(opts)
    bootstrap.DocRoutes(api)

    data, err := api.OpenAPIJSON()
    if err != nil {
        t.Fatal(err)
    }
    // 与仓库中的 openapi.json 比较……
}
```

你还可以把生成的文档与仓库中保存的 `openapi.json` 进行比较，这样每次接口变更都会在代码审查中清晰可见。
<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/web/webtest`。

### New

```go
func New(t testing.TB, h http.Handler) *Client
```

创建测试页面用的客户端，在多个请求之间保存 cookie。

### HTMX、HTMXPage、Header

```go
func HTMX(r *http.Request)
func HTMXPage(r *http.Request)
func Header(name, value string) Option
```

请求选项：`HTMX` 模拟局部 htmx 请求，`HTMXPage` 模拟整页 htmx 请求，`Header` 设置请求头。
