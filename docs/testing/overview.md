# 测试：快速入门

Tenon 在设计时就考虑了测试。所有的依赖都是显式传入的，所以在测试中，你可以直接构造 API、业务动作和命令。

Tenon 直接使用 Go 标准库的 `testing`、`net/http/httptest` 和 `go test` 命令。需要数据库、Redis 的地方，使用真实的服务：每个测试一个隔离的数据库，测试的结果和生产环境的行为一致。

- [示例用法](#introduction)
- [运行测试](#running-tests)
- [准备测试数据](#test-data)
- [控制时间](#controlling-time)
- [测试配置](#testing-configuration)
- [测试平滑停止](#testing-shutdown)
- [各组件的测试工具](#test-helpers)

<a id="introduction"></a>
## 示例用法

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

这个测试创建一篇文章再发布它，用的是真实的业务动作和真实的数据库。本页介绍运行测试、准备数据和控制时间等基础；数据库测试的细节见[数据库与业务动作](database.md)，HTTP 接口、页面和命令的测试见 [HTTP 与命令](http.md)。

<a id="running-tests"></a>
## 运行测试

在应用根目录中执行：

```bash
go test ./...
```

建议同时开启竞态检测，它可以帮你发现并发访问共享数据的问题：

```bash
go test -race ./...
```

需要真实数据库的测试见[数据库测试](database.md#database-tests)。

<a id="test-data"></a>
## 准备测试数据

测试数据通过业务动作创建，这样数据一定满足业务规则，与线上可能出现的数据一致。许多测试都要用的数据，在测试文件中写一个带默认值的函数，例如 `internal/article/helpers_test.go`：

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

每个测试只写出它关心的字段：

```go
item := newArticle(t, db, alice, func(c *article.Changes) { c.Slug = "hello-world" })
```

- 内容用固定的值。只有必须唯一的字段用随机值，例如 slug，这样一个测试可以创建多篇文章。
- 需要已发布的文章时再调用 `Publish`。「两天前发布」这类和时间有关的数据，用下一节的 `synctest` 推进时间得到，数据库中的时间始终由业务动作写入。
- 只有业务动作产生不了的数据，例如从旧系统迁移来的文章，才用 `db.NewInsert()` 直接写入。
- 其他包的测试也要创建文章时，把这些函数导出到 `internal/article/articletest` 包中。

<a id="controlling-time"></a>
## 控制时间

和时间有关的测试使用 Go 标准库的 [testing/synctest](https://pkg.go.dev/testing/synctest)。在 `synctest.Test` 中，`time.Now`、`time.Sleep`、定时器和 `context` 的超时都使用一个假时钟：代码执行时时钟不走，`time.Sleep` 立即把时钟拨到之后。业务动作照常调用 `time.Now`。

下面的测试检查草稿放了两天才发布时，发布时间是发布那一刻：

```go
func TestPublishRecordsTime(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        ctx := context.Background()
        db := dbtest.Open(t, migrations.FS)
        alice := identity.Subject{Type: user.Type, ID: "alice"}
        item := newArticle(t, db, alice, nil)

        time.Sleep(48 * time.Hour)
        published, err := article.NewPublish(db).Handle(ctx, alice, item.ID)
        if err != nil {
            t.Fatal(err)
        }
        // 时钟只在 Sleep 时移动，所以可以精确比较
        if !published.PublishedAt.Equal(time.Now()) {
            t.Fatalf("published at %v", published.PublishedAt)
        }
    })
}
```

使用时注意：

- 数据库在 `synctest.Test` 中打开。只有 Go 程序中的时钟是假的：查询用到的时间都要由 Go 算出后作为参数传入，例如 `Where("expires_at > ?", time.Now())`；迁移中的时间列也不设默认值，见[时间戳](../database/quick-start.md#timestamps)。框架自己的 SQL 都遵守这条规则，由 `internal/checks/clocktest` 检查。
- 由服务端计算的过期时间按真实时钟走，例如 Redis 的 `EXPIRE`。
- 假时钟从 2000-01-01 00:00 UTC 开始，需要某个具体时刻时用 `time.Sleep(time.Until(target))` 拨过去。
- `t.Run` 和 `t.Parallel` 在 `synctest.Test` 之外调用。要在几种情况下测试时，在每个子测试中分别调用 `synctest.Test`。

<a id="testing-configuration"></a>
## 测试配置

把配置内容写进临时文件，用 `t.Setenv` 提供环境变量，再调用 `config.Load`：

```go
func TestLoadConfig(t *testing.T) {
    t.Setenv("HTTP_ADDR", ":9000")
    path := filepath.Join(t.TempDir(), "config.yaml")
    data := "http:\n  addr: ${HTTP_ADDR}\ndatabase:\n  dsn: postgres://localhost/test\n"
    if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
        t.Fatal(err)
    }

    cfg, err := config.Load[Config](path)
    if err != nil {
        t.Fatal(err)
    }
    if cfg.HTTP.Addr != ":9000" {
        t.Fatalf("addr = %q", cfg.HTTP.Addr)
    }
}
```

`t.Setenv` 会在测试结束时恢复环境变量，使用它的测试串行运行。

建议覆盖以下情况：默认值、显式的零值、未知字段和校验失败。

<a id="testing-shutdown"></a>
## 测试平滑停止

如果你的应用对停机行为有要求，可以为 `httpx.Serve` 编写测试，覆盖以下场景：

- 取消 context 后，服务正常退出；
- 停机时正在处理的请求能够完成；
- 排空超时后，未完成的请求被取消；
- 监听发生错误时，`Serve` 返回该错误。

如果入口自己处理系统信号，还可以在测试中以子进程方式启动应用，发送 `SIGTERM`，验证资源被正确关闭以及进程的退出码。

<a id="test-helpers"></a>
## 各组件的测试工具

| 包 | 用途 | 文档 |
| --- | --- | --- |
| `database/dbtest` | 每个测试一个临时数据库 | [数据库测试](database.md#database-tests) |
| `web/webtest` | 页面测试：保存 cookie、htmx 请求头 | [页面测试](http.md#page-tests) |
| `logging/logtest` | 捕获日志 | [测试日志](../general/logging.md#testing-logs) |
| `outbound/outboundtest` | 登记外部 HTTP 服务的响应，检查发出的请求 | [出站请求](../libraries/outbound.md#testing) |
| `login/logintest` | 读取发出的验证码，计算二次验证的数字 | [登录与注册](../security/login.md#testing) |
| `queue/queuetest`、`mail/mailtest`、`sms/smstest`、`pdf/pdftest` 等 | 各组件的替身 | 各组件页面的“测试”一节 |
