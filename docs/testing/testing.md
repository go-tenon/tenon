# 测试

应用的测试用标准库的 `testing`、`net/http/httptest` 和 `go test`。依赖都显式传入，测试直接构造业务动作、API 和命令；数据库用真实的 PostgreSQL，每个测试一个临时库。

包：`database/dbtest`。

<a id="introduction"></a>
## 示例

```go
func TestPublish(t *testing.T) {
    t.Parallel()
    db := dbtest.Open(t, migrations.FS) // 独立的临时数据库，已执行迁移
    alice := identity.Subject{Type: user.Type, ID: "alice"}

    item, err := article.NewCreate(db).Handle(t.Context(), alice, article.Changes{Title: "Hello", Slug: "hello"})
    if err != nil {
        t.Fatal(err)
    }
    if _, err := article.NewPublish(db).Handle(t.Context(), alice, item.ID); err != nil {
        t.Fatal(err)
    }
}
```

`migrations.FS` 是应用[嵌入的迁移](../database/migrations.md#embedding-migrations)，`article` 是应用的模块。

<a id="running-tests"></a>
## 运行测试

```bash
go test ./...
go test -race ./...   # 竞态检测
```

数据库测试需要环境变量 `POSTGRES_TEST_URL`，见[在本地运行数据库测试](#running-database-tests-locally)。`-race` 报 `DATA RACE` 时，常见原因是业务动作的字段中保存了请求的数据。

<a id="test-data"></a>
## 准备测试数据

测试数据通过业务动作创建，数据一定满足业务规则。常用的数据写成带默认值的函数，例如 `internal/article/helpers_test.go`：

```go
// newArticle 以 author 的身份创建一篇草稿。edit 修改默认内容，使用默认内容时传 nil。
func newArticle(t *testing.T, db *bun.DB, author identity.Subject, edit func(*article.Changes)) *article.Article {
    t.Helper()
    c := article.Changes{Title: "Hello World", Slug: strings.ToLower(rand.Text()), Content: "……"}
    if edit != nil {
        edit(&c)
    }
    item, err := article.NewCreate(db).Handle(context.Background(), author, c)
    if err != nil {
        t.Fatal(err)
    }
    return item
}
```

- 每个测试只写出它关心的字段：`newArticle(t, db, alice, func(c *article.Changes) { c.Slug = "hello-world" })`。只有必须唯一的字段（例如 slug）用随机值。
- “两天前发布”这类数据，用 [`synctest`](#controlling-time) 推进时间后调用业务动作得到；业务动作产生不了的数据才用 `db.NewInsert()` 写入。
- 其他包也要用时，导出到 `internal/article/articletest` 包中。

<a id="controlling-time"></a>
## 控制时间：`testing/synctest`

在 [`synctest.Test`](https://pkg.go.dev/testing/synctest) 中，`time.Now`、`time.Sleep`、定时器和 `context` 的超时都用假时钟，`time.Sleep` 立即把时钟拨到之后，业务动作照常调用 `time.Now`。`newArticle` 见[准备测试数据](#test-data)：

```go
func TestPublishRecordsTime(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        db := dbtest.Open(t, migrations.FS)
        alice := identity.Subject{Type: user.Type, ID: "alice"}
        item := newArticle(t, db, alice, nil)

        time.Sleep(48 * time.Hour)
        published, err := article.NewPublish(db).Handle(t.Context(), alice, item.ID)
        if err != nil {
            t.Fatal(err)
        }
        if !published.PublishedAt.Equal(time.Now()) { // 时钟只在 Sleep 时移动，可以精确比较
            t.Fatalf("published at %v", published.PublishedAt)
        }
    })
}
```

- 数据库在 `synctest.Test` 中打开。
- 只有 Go 中的时钟是假的。SQL 中的时间由 Go 传入，例如 `Where("expires_at > ?", time.Now())`，不用 `NOW()`，见[时间戳](../database/database.md#timestamps)。服务端计算的过期时间（例如 NATS KV 的 TTL）按真实时钟走。
- 假时钟从 2000-01-01 00:00 UTC 开始，用 `time.Sleep(time.Until(target))` 拨到具体时刻。
- `t.Run` 和 `t.Parallel` 在 `synctest.Test` 之外调用，每个子测试中分别调用 `synctest.Test`。

<a id="database-tests"></a>
## 数据库测试：`dbtest`

`dbtest.Open` 为每个测试创建一个独立的临时数据库，执行迁移，测试结束时删除。测试之间相互隔离，可以用 `t.Parallel()` 并行运行。

- 管理员连接取自环境变量 `POSTGRES_TEST_URL`，账户要能创建和删除数据库。没有设置时测试直接失败，CI 中的数据库测试因此一定真正运行过。
- 只检查认证和输入校验的 HTTP 测试不用数据库，`db` 传 `nil`，见[测试 HTTP 接口](#http-tests)。

> [!WARNING]
> `POSTGRES_TEST_URL` 指向专门的测试实例，与存放真实数据的数据库分开。

<a id="creating-test-databases"></a>
### 用 `dbtest.Open` 创建测试数据库

```go
db := dbtest.Open(t, migrations.FS)
```

- 返回已执行迁移的 `*bun.DB`，直接传给业务动作的构造函数。`migrations.FS` 是应用嵌入的迁移。
- 可以传几个迁移，例如 `dbtest.Open(t, queue.Migrations(), operation.Migrations())`，合在一起按版本顺序执行；不传时是空数据库。
- `dbtest.Replica(t, db)` 返回同一个库的只读连接池，代替[单独的副本连接池](../database/database.md#explicit-replica)。
- `dbtest.DSN(t, db)` 返回这个库的连接串。
- `dbtest.CheckSchema(t, db)` 检查时间列以及所有表和字段的 `COMMENT`（见[时间列与注释](../database/migrations.md#timestamp-columns)）；`dbtest.CheckComments(t, db)` 单独检查表和字段的 `COMMENT`。

<a id="running-database-tests-locally"></a>
### 在本地运行数据库测试：`compose.test.yml`

在项目根目录放一个测试用的 `compose.test.yml`，启动应用用到的服务。用到 [Redis](../libraries/cache.md#testing)、NATS 时照样加上它们：

```yaml
name: blog-test

services:
  postgres:
    image: postgres:18
    environment:
      POSTGRES_PASSWORD: postgres
    ports:
      - 127.0.0.1:55432:5432
    tmpfs:
      - /var/lib/postgresql
    healthcheck:
      test: pg_isready -U postgres
      interval: 1s
      retries: 60
```

```bash
docker compose -f compose.test.yml up -d --wait   # 等到健康检查通过才返回
export POSTGRES_TEST_URL='postgres://postgres:postgres@127.0.0.1:55432/postgres?sslmode=disable'
go test -race -count=1 ./...
```

服务可以一直开着反复运行测试。数据在 tmpfs 中，`down -v` 后全部清除，包括测试进程被强行结束时留下的临时库。

<a id="database-tests-in-ci"></a>
### 在 CI 中运行数据库测试

`tenon init` 生成的 [CI](../installation/installing.md#ci) 中，`check` 任务运行 `go test ./...`。用到数据库后，在 `check` 的 `env` 中设置 `POSTGRES_TEST_URL: postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable`，在测试之前启动 PostgreSQL：

```yaml
      - name: Start PostgreSQL
        run: |
          docker run -d --name postgres -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:18
          for i in $(seq 1 60); do docker exec postgres pg_isready -U postgres -h 127.0.0.1 && exit 0; sleep 1; done
          docker logs postgres
          exit 1
```

用到 Redis、NATS 时同样启动它们，并设置对应的环境变量。

<a id="testing-actions"></a>
## 测试业务动作的 `Handle`

业务规则（尤其是资源所有权）直接调用 `Handle` 测试，主体作为参数传入，可以模拟不同的调用者。`newArticle` 见[准备测试数据](#test-data)：

```go
func TestPublishRules(t *testing.T) {
    t.Parallel()
    db := dbtest.Open(t, migrations.FS)
    get, publish := article.NewGet(db), article.NewPublish(db)
    alice := identity.Subject{Type: user.Type, ID: "alice"}
    bob := identity.Subject{Type: user.Type, ID: "bob"}
    item := newArticle(t, db, alice, nil)

    if _, err := get.Handle(t.Context(), bob, item.ID); !errors.Is(err, article.ErrNotFound) {
        t.Fatalf("bob read a draft: %v", err)
    }
    if _, err := publish.Handle(t.Context(), bob, item.ID); !errors.Is(err, article.ErrForbidden) {
        t.Fatalf("bob published: %v", err)
    }
}
```

优先用真实的依赖。确实要隔离某项外部能力（例如对象存储）时，在业务包中定义一个小接口，测试中给出简单的实现，见[依赖装配](../concepts/dependencies.md#replacing-dependencies-in-tests)。

<a id="testing-action-commands"></a>
### 测试业务动作的命令入口

业务动作的[命令入口](../concepts/actions.md#as-commands)通过模块的 `Commands(db)` 测试，它返回 `*cobra.Command`。`db` 来自 `dbtest.Open`，`item` 是用 `newArticle` 创建的草稿：

```go
var out bytes.Buffer
cmd := article.Commands(db)
cmd.SetOut(&out)
cmd.SetErr(&out)
cmd.SetArgs([]string{"publish", item.ID.String()})
err := cmd.ExecuteContext(t.Context())
// 成功时 out 是 "hello-world\n"；重复发布是 Conflict，command.ExitCode(err) 为 command.ExitFailure；
// 参数不是合法的 ID 时为 command.ExitUsage
```

命令入口只测参数解析、输出和退出码，业务规则在 [`Handle` 的测试](#testing-actions)中覆盖。

<a id="http-tests"></a>
## 测试 HTTP 接口

在进程内用 `httptest` 调用 `api.Handler()`，路由用模块自己的 `RegisterRoutes` 注册，测试覆盖真实的路由、认证、输入校验和错误响应。下面的请求在 `Handle` 之前就被拒绝，所以 `db` 传 `nil`：

```go
func TestCreateRejectsBadRequests(t *testing.T) {
    t.Parallel()
    api := rest.New(chi.NewRouter(), rest.Options{Accounts: identity.Accounts{user.Type: nil}})
    validate := func(_ context.Context, token string) (identity.Subject, error) {
        if token == "guest-token" {
            return identity.Subject{Type: user.Type, ID: "guest"}, nil
        }
        return identity.Subject{}, identity.ErrUnauthenticated.New()
    }
    article.RegisterRoutes(api, rest.RequireToken(api, validate, "articles"), nil)

    for token, want := range map[string]int{"": http.StatusUnauthorized, "guest-token": http.StatusForbidden} {
        req := httptest.NewRequest(http.MethodPost, "/articles", strings.NewReader(`{"title":"Hello","slug":"hello","content":"……"}`))
        req.Header.Set("Content-Type", "application/json")
        if token != "" {
            req.Header.Set("Authorization", "Bearer "+token)
        }
        rr := httptest.NewRecorder()
        api.Handler().ServeHTTP(rr, req)
        if rr.Code != want {
            t.Fatalf("token %q: status=%d body=%s", token, rr.Code, rr.Body.String())
        }
    }
}
```

请求返回 404 而接口确实存在时，检查是否调用了模块的路由注册函数、路径是否少了子路由的前缀（例如 `/api`）。

<a id="testing-auth-and-errors"></a>
### 测试认证与错误响应

一组接口测试通常检查：

- 不带 `Authorization` 头返回 `401`，错误码 `auth.unauthenticated`；
- 缺少权限的 token 返回 `403`，错误码 `auth.forbidden`；
- 不合法的请求体返回 `422`，检查响应 `errors[].location`，例如 `body.slug`；
- 错误响应中的 `code` 与预期的错误码一致。

访问他人的资源返回 `403` 或 `404`，取决于数据库中的数据，在[业务动作的测试](#testing-actions)中覆盖。

<a id="page-tests"></a>
## 页面测试

页面的数据来自 JSON 接口，服务端只测接口。页面在前端测试，见[前端规范](../frontend/conventions.md#checks)。测试中 `@go-tenon/ui` 的 `UIProvider` 传 `env="test"`，关闭过渡动画和 portal。

<a id="command-tests"></a>
## 测试命令：`SetOut`、`SetArgs`

在进程内执行应用真实的根命令，检查输出和退出码。`newRoot` 是 `main.go` 中组装根命令的函数，见[命令行](../general/commands.md#declaring-the-cli)：

```go
func TestConfigCheckReportsMissingFile(t *testing.T) {
    t.Parallel()
    var out bytes.Buffer
    root := newRoot()
    root.SetOut(&out)
    root.SetErr(&out)
    root.SetArgs([]string{"config", "check", "--config", filepath.Join(t.TempDir(), "missing.yaml")})
    err := root.ExecuteContext(t.Context())
    if command.ExitCode(err) != command.ExitConfig {
        t.Fatalf("exit code = %d, err = %v", command.ExitCode(err), err)
    }
}
```

`openapi`、`routes` 命令不需要配置，可以用同样的方法检查接口声明。命令的输出写到 `cmd.OutOrStdout()`，`SetOut` 才能捕获，见[输入与输出](../general/commands.md#input-and-output)。

<a id="testing-openapi"></a>
## 测试 OpenAPI 文档

用应用的 [`DocRouter`](../concepts/dependencies.md#running-parts) 构造全部路由并生成文档。路由以 `nil` 依赖构造，注册时使用了依赖的构造函数或 `RegisterRoutes` 会让测试 panic，见[构造函数注入](../concepts/dependencies.md#constructor-injection)：

```go
api, err := rest.Lookup(bootstrap.DocRouter(), "")
if err != nil {
    t.Fatal(err)
}
data, err := api.OpenAPIJSON() // 与仓库中的 openapi.json 比较，接口的变化在代码审查中可见
```

<a id="testing-configuration"></a>
## 测试配置

把配置写进 `t.TempDir()` 中的文件，用 `t.Setenv` 提供其中引用的环境变量，再调用 `config.Load[Config](path)` 检查结果，`Config` 是应用的配置结构。覆盖默认值、显式的零值、未知字段和校验失败。`t.Setenv` 修改进程的环境变量，用它的测试不调用 `t.Parallel`。

<a id="test-helpers"></a>
## 各组件的测试工具

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `database/dbtest` | 每个测试一个临时数据库 | [数据库测试](#database-tests) |
| `logging/logtest` | 捕获日志 | [测试日志](../general/logging.md#testing-logs) |
| `outbound/outboundtest` | 登记外部 HTTP 服务的响应，检查发出的请求 | [出站请求](../libraries/outbound.md#testing) |
| `login/logintest` | 读取发出的验证码，计算二次验证的数字 | [登录与注册](../security/login.md#testing) |
| `queue/queuetest`、`mail/mailtest`、`sms/smstest`、`pdf/pdftest` 等 | 各组件的替身 | 各组件页面的“测试”一节 |

能在进程内模拟的外部服务就在进程内模拟：S3 用 gofakes3，HTTP API 用 `httptest`。
