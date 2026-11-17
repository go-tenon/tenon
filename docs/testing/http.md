# 测试：HTTP 与命令

测试 HTTP 接口时，在进程内完成：用 `httptest` 创建请求和响应记录器，然后直接调用 `api.Handler()` 即可。

有一点很重要：请调用应用自己的路由注册函数。这样，测试覆盖的就是真实的路由配置、认证和输入验证；如果有人改坏了 `routes.go`，测试会发现它。命令的测试也一样，在进程内执行真实的根命令。

阅读前需要了解：[测试：快速入门](overview.md)；[路由](../http/routing.md)；[命令行](../general/commands.md)。

- [HTTP 测试](#http-tests)
    - [测试认证与错误响应](#testing-auth-and-errors)
- [页面测试](#page-tests)
- [命令测试](#command-tests)
- [测试 OpenAPI 文档](#testing-openapi)
- [常见问题](#troubleshooting)

<a id="http-tests"></a>
## HTTP 测试

测试文件按业务动作命名。下面测试[创建文章](../http/routing.md#http-actions)的 HTTP 入口：

```go
func newTestAPI() *rest.API {
    api := rest.New(chi.NewRouter(), rest.Options{Accounts: identity.Accounts{user.Type: nil}})
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
    // 这些请求在调用 Handle 之前就会被拒绝，所以 db 传 nil
    article.Register(api, rest.RequireToken(api, validate, "articles"), nil)
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

这个测试用表格列出了三种在调用 `Handle` 之前就会被拒绝的请求：没有 token、缺少权限、输入格式不对。它们止步于数据库之前，所以 `db` 可以传 `nil`，测试运行得很快。

<a id="testing-auth-and-errors"></a>
### 测试认证与错误响应

请通过完整的 `api.Handler()` 来测试认证、验证错误和越权访问。这样测试才能覆盖认证中间件、输入验证和错误转换的完整过程。一组典型的接口测试包括：

- 不带 `Authorization` 头的请求返回 `401`；
- 缺少权限的 token 返回 `403`；
- 不合法的请求体返回 `422`，并检查 `errors[].location`；
- 访问他人的资源返回 `403` 或 `404`；
- 错误响应中的 `code` 与预期的错误码一致。

前三种由上面的测试覆盖。后两种取决于数据库中的数据，更适合直接测试业务动作，见[测试业务动作](database.md#testing-actions)。

<a id="page-tests"></a>
## 页面测试

页面由前端渲染，数据来自 JSON 接口，所以服务端只需要测试接口，见上文。页面本身在前端测试：组件和 hook 用 Vitest 和 Testing Library，需要真实浏览器的交互用 Playwright，见[前端规范](../frontend/conventions.md)。`@go-tenon/ui` 的 `UIProvider` 传 `env="test"`，关闭过渡动画和 portal。

<a id="command-tests"></a>
## 命令测试

测试命令行时，同样在进程内完成。根命令是原生的 `*cobra.Command`，用 Cobra 的 `SetOut`、`SetArgs` 把输出重定向到 `bytes.Buffer` 并指定参数，然后执行命令并检查输出和退出码。下面测试应用真实的根命令，`newRoot` 是 `main.go` 中组装根命令的函数，见[命令行](../general/commands.md#declaring-the-cli)：

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

前两个测试确认 `openapi`、`routes` 命令可以离线查看真实的接口声明；最后一个测试确认配置文件缺失时，`config check` 以 `ExitConfig` 退出。

<a id="testing-openapi"></a>
## 测试 OpenAPI 文档

生成文档时，路由用 `nil` 依赖构造，处理器只被登记。用应用的 [DocRouter](../concepts/dependencies.md#running-parts) 构造全部路由并生成文档，可以确保每个构造函数在注册时都只保存依赖，使用了依赖的会让测试 panic：

```go
func TestOpenAPI(t *testing.T) {
    api, err := rest.Lookup(bootstrap.DocRouter(), "")
    if err != nil {
        t.Fatal(err)
    }
    data, err := api.OpenAPIJSON()
    if err != nil {
        t.Fatal(err)
    }
    // 与仓库中的 openapi.json 比较……
}
```

你还可以把生成的文档与仓库中保存的 `openapi.json` 进行比较，这样每次接口变更都会在代码审查中清晰可见。
<a id="troubleshooting"></a>
## 常见问题

**测试中请求返回 404，但接口确实存在**

测试中没有调用应用自己的路由注册函数，或者请求的路径缺少子路由的前缀（例如 `/api`）。用 `routes` 命令查看实际的路径。

**测试 OpenAPI 文档时 panic**

有构造函数或 `Register` 在注册时使用了依赖，而生成文档时依赖是 `nil`。构造函数只保存依赖，见[依赖装配](../concepts/dependencies.md#constructor-injection)。

**命令测试的输出是空的**

命令把结果写到了 `os.Stdout`。改用 `cmd.OutOrStdout()`，测试中用 `SetOut` 才能捕获，见[输入与输出](../general/commands.md#input-and-output)。
