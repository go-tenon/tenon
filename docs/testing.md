# 测试

[文档首页](README.md) / 测试与部署

mygoravel 使用标准 `testing`、`httptest` 和 `go test`。单元测试不依赖外部服务；需要 PostgreSQL 的框架测试通过 integration build tag 显式运行。

本章内容：

- [运行单元测试](#unit-tests)
- [HTTP 测试](#http-tests)
- [业务与配置测试](#business-tests)
- [数据库测试](#database-tests)
- [运行完整集成测试](#integration-tests)
- [命令与生命周期测试](#lifecycle-tests)
- [离线文档与外部消费](#consumption)

<a id="unit-tests"></a>

## 运行单元测试

在仓库根目录执行：

```bash
go test ./...
go test -race ./...
```

在自己的应用中也使用相同命令。race 检测覆盖共享状态和并发访问；只在需要集成依赖时再添加 tag。

<a id="http-tests"></a>

## HTTP 测试

以下是完整的 `api_test.go`，可放在已添加框架依赖的应用中：

```go
package api_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/danielgtaylor/huma/v2"
    "github.com/ryan053857/mygoravel/httpx/rest"
)

func TestPing(t *testing.T) {
    type Input struct{}
    type Output struct {
        Body struct {
            Message string `json:"message"`
        }
    }
    api := rest.New(rest.Options{})
    op := huma.Operation{OperationID: "ping", Method: http.MethodGet, Path: "/ping"}
    rest.Public()(&op)
    rest.Register(api, op, func() func(context.Context, *Input) (*Output, error) {
        return func(context.Context, *Input) (*Output, error) {
            out := &Output{}
            out.Body.Message = "pong"
            return out, nil
        }
    })

    response := httptest.NewRecorder()
    api.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ping", nil))
    if response.Code != http.StatusOK {
        t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
    }
    var body struct {
        Message string `json:"message"`
    }
    if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
        t.Fatal(err)
    }
    if body.Message != "pong" || response.Header().Get("X-Request-ID") == "" {
        t.Fatalf("unexpected response: %s", response.Body.String())
    }
}
```

认证、校验错误和越权路径同样通过真实 Router 测试。不要只测试处理器函数，而遗漏认证中间件或错误转换。

<a id="business-tests"></a>

## 业务与配置测试

业务授权应该有直接调用服务的测试，证明脱离 HTTP 后所有权检查仍然有效。可以直接注入真实业务依赖；只有确实需要隔离某项能力时，才在消费方定义小接口或提供测试实现。

配置测试通过 `config.Parse` 和 `WithLookup` 注入字节与环境查询函数，覆盖默认值、零值、未知字段和敏感值脱敏。避免为了配置隔离而修改整个进程的环境。

<a id="database-tests"></a>

## 数据库测试

`dbtest.Open` 创建每测试独立的数据库。管理员 DSN 通过 `DATABASE_TEST_DSN` 传入，账户需要创建和删除数据库的权限。

将下面的测试放在应用迁移目录的上一级；`migrations` 中至少包含一个 SQL 文件：

```go
//go:build integration

package storage_test

import (
    "embed"
    "testing"

    "github.com/ryan053857/mygoravel/database/dbtest"
    "github.com/ryan053857/mygoravel/database/migrate"
)

//go:embed migrations/*.sql
var files embed.FS

func TestDatabase(t *testing.T) {
    migrations, err := migrate.Sub(files, "migrations")
    if err != nil {
        t.Fatal(err)
    }
    db := dbtest.Open(t, migrations)
    var name string
    if err := db.QueryRow("select current_database()").Scan(&name); err != nil {
        t.Fatal(err)
    }
    if name == "" {
        t.Fatal("missing isolated database")
    }
}
```

测试结束时关闭连接并删除数据库。不提供迁移时传 nil，得到空库。不要让集成测试使用生产数据库凭据。

<a id="integration-tests"></a>

## 运行完整集成测试

启动独立的 PostgreSQL 测试实例：

```bash
docker run --name mygoravel-test-postgres --rm -d \
  -e POSTGRES_PASSWORD=postgres \
  -p 127.0.0.1:55432:5432 postgres:18
```

使用 `docker exec mygoravel-test-postgres pg_isready -U postgres` 确认数据库就绪，再在仓库根目录执行：

```bash
export DATABASE_TEST_DSN='postgres://postgres:postgres@127.0.0.1:55432/postgres?sslmode=disable'
go test -race -count=1 -tags=integration ./...
```

缺少数据库地址会导致测试失败，不会静默跳过。应用如果使用缓存等其他外部服务，需要自行补充对应的集成测试。

完成后执行 `docker stop mygoravel-test-postgres` 清理临时实例。

<a id="lifecycle-tests"></a>

## 命令与生命周期测试

命令通过注入输出和回调测试，无需启动应用进程。下面是完整的测试文件：

```go
package cli_test

import (
    "bytes"
    "context"
    "fmt"
    "testing"

    "github.com/ryan053857/mygoravel/command"
    "github.com/spf13/cobra"
)

func TestOpenAPICommand(t *testing.T) {
    var out, diagnostic bytes.Buffer
    root := command.Root("api", "HTTP API", command.IO{Out: &out, Err: &diagnostic})
    root.AddCommand(command.NewOpenAPI(func(ctx context.Context, cmd *cobra.Command, args []string) error {
        _, err := fmt.Fprintln(cmd.OutOrStdout(), `{"openapi":"3.1.0"}`)
        return err
    }))
    root.SetArgs([]string{"openapi"})
    if err := root.ExecuteContext(context.Background()); err != nil {
        t.Fatal(err)
    }
    if out.String() != "{\"openapi\":\"3.1.0\"}\n" || diagnostic.Len() != 0 {
        t.Fatalf("stdout=%q stderr=%q", out.String(), diagnostic.String())
    }
}
```

这里的固定输出只验证命令接线；生成 OpenAPI 的回调应另行验证真实 schema。配置失败、依赖不可用和非法参数也应断言对应的 `command.ExitCode`。

HTTP 生命周期测试应覆盖正常取消、正在处理的请求、监听异常和排空超时。应用入口自行处理信号时，还应运行子进程并发送 SIGTERM，验证资源关闭与退出状态。

<a id="consumption"></a>

## 离线文档与外部消费

路由文档测试使用 ModeOpenAPI，并让处理器构造回调一旦执行就使测试失败。错误响应还应对生成的 schema 做校验，避免正文与文档漂移。

仓库的独立消费测试把当前代码制成模块代理 zip，在临时 module 中运行 HTTP、配置、错误和命令的公开 API 契约测试。它通过 GOPROXY 获取候选版本，不使用本地 replace，用于验证框架可以被外部应用正常引用。

---

[上一篇：应用命令](commands.md) · [下一篇：部署](deployment.md)
