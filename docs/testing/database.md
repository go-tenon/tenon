# 测试：数据库与业务动作

测试数据库代码最可靠的方式，是使用与生产相同的 PostgreSQL：唯一约束冲突、死锁和类型错误都和线上一样出现。`dbtest.Open` 为**每个测试**创建一个独立的临时数据库，执行迁移，测试结束时删除，测试之间相互隔离，可以并行运行。业务规则直接调用业务动作的 `Handle` 测试。

- [数据库测试](#database-tests)
    - [创建测试数据库](#creating-test-databases)
    - [在本地运行数据库测试](#running-database-tests-locally)
- [测试业务动作](#testing-actions)
    - [测试命令入口](#testing-action-commands)
- [API 参考](#api-reference)

<a id="database-tests"></a>
## 数据库测试

<a id="creating-test-databases"></a>
### 创建测试数据库

测试数据库代码时，最可靠的方式是使用与生产相同的真实数据库。`dbtest.Open` 会为**每个测试**创建一个独立的临时数据库，执行你的迁移，并在测试结束时删除它。测试之间互不干扰，可以放心地并行运行。

在测试中：

```go
db := dbtest.Open(t, migrations.FS)
```

`dbtest.Open` 返回一个已执行迁移的 `*bun.DB`，你可以把它直接传给业务动作的构造函数。迁移参数传 `nil` 时会创建一个空数据库。

`dbtest` 通过环境变量 `POSTGRES_TEST_URL` 获取一个管理员连接，该账户需要有创建和删除数据库的权限。没有设置这个变量时，测试直接失败——这样 CI 中的数据库测试一定真正运行过。

> [!WARNING]
> 请为这个变量使用专门的测试数据库实例，与存放真实数据的数据库分开。

<a id="running-database-tests-locally"></a>
### 在本地运行数据库测试

在项目根目录放一个只给测试用的 `compose.test.yml`，启动应用用到的 PostgreSQL、[Redis](../libraries/redis.md#testing) 和 [Memcached](../libraries/cache.md#testing)：

```yaml
# compose.test.yml
name: blog-test

services:
  postgres:
    image: postgres:18
    # 使用队列时需要：任务经由逻辑复制转发，每个测试数据库一个复制槽
    command: [postgres, -c, wal_level=logical, -c, max_replication_slots=256, -c, max_wal_senders=64]
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

  redis:
    image: redis:8
    ports:
      - 127.0.0.1:56379:6379
    tmpfs:
      - /data
    healthcheck:
      test: redis-cli ping
      interval: 1s
      retries: 30

  memcached:
    image: memcached:1.6-alpine
    ports:
      - 127.0.0.1:51211:11211
    healthcheck:
      test: nc -z 127.0.0.1 11211
      interval: 1s
      retries: 30
```

`--wait` 等到所有服务的健康检查通过才返回，之后就可以直接运行测试：

```bash
docker compose -f compose.test.yml up -d --wait
export POSTGRES_TEST_URL='postgres://postgres:postgres@127.0.0.1:55432/postgres?sslmode=disable'
export REDIS_TEST_URL='redis://127.0.0.1:56379/0'
export MEMCACHED_TEST_ADDR='127.0.0.1:51211'
go test -race -count=1 ./...
```

每个测试使用自己的临时库，服务可以一直开着反复运行测试。数据放在 tmpfs 中，执行 `docker compose -f compose.test.yml down -v` 清理后数据全部消失。

Redis 的集成测试见 [Redis](../libraries/redis.md#testing)。

<a id="testing-actions"></a>
## 测试业务动作

业务规则（尤其是资源所有权检查）直接调用业务动作的 `Handle` 测试。由于主体是作为参数传入的，你可以很方便地模拟不同的调用者。下面在真实的测试数据库上验证草稿的可见性和发布规则：

```go
func TestDraftVisibilityAndPublishing(t *testing.T) {
    ctx := context.Background()
    db := dbtest.Open(t, migrations.FS)
    create, get, publish := article.NewCreate(db), article.NewGet(db), article.NewPublish(db)
    alice := identity.Subject{Type: user.Type, ID: "alice"}
    bob := identity.Subject{Type: user.Type, ID: "bob"}

    item, err := create.Handle(ctx, alice, article.Changes{Title: "Hello World", Slug: "hello-world"})
    if err != nil {
        t.Fatal(err)
    }

    // 草稿只有作者能看到
    if _, err := get.Handle(ctx, bob, item.ID); !errors.Is(err, article.ErrNotFound) {
        t.Fatalf("bob read a draft: %v", err)
    }
    // 只有作者能发布，而且每篇只发布一次
    if _, err := publish.Handle(ctx, bob, item.ID); !errors.Is(err, article.ErrForbidden) {
        t.Fatalf("bob published: %v", err)
    }
    if _, err := publish.Handle(ctx, alice, item.ID); err != nil {
        t.Fatal(err)
    }
    if _, err := publish.Handle(ctx, alice, item.ID); !errors.Is(err, article.ErrAlreadyPublished) {
        t.Fatalf("published twice: %v", err)
    }
    // 发布之后所有人都能看到
    if _, err := get.Handle(ctx, bob, item.ID); err != nil {
        t.Fatal(err)
    }
    // slug 全站唯一
    _, err = create.Handle(ctx, bob, article.Changes{Title: "Another", Slug: "hello-world"})
    if !errors.Is(err, article.ErrSlugTaken) {
        t.Fatalf("duplicate slug: %v", err)
    }
}
```

`dbtest.Open` 为这个测试创建一个独立的数据库并执行 `migrations.FS` 中的迁移，详见下面的[数据库测试](#database-tests)。

<a id="testing-action-commands"></a>
### 测试命令入口

业务动作的[命令入口](../concepts/actions.md#as-commands)通过模块的 `Commands` 测试。它返回原生的 `*cobra.Command`，直接传入测试数据库即可：

```go
func TestPublishCommand(t *testing.T) {
    ctx := context.Background()
    db := dbtest.Open(t, migrations.FS)
    alice := identity.Subject{Type: user.Type, ID: "alice"}
    item, err := article.NewCreate(db).Handle(ctx, alice, article.Changes{Title: "Hello World", Slug: "hello-world"})
    if err != nil {
        t.Fatal(err)
    }

    run := func(args ...string) (string, error) {
        var out bytes.Buffer
        cmd := article.Commands(db)
        cmd.SetOut(&out)
        cmd.SetErr(&out)
        cmd.SetArgs(args)
        err := cmd.ExecuteContext(ctx)
        return out.String(), err
    }
    if out, err := run("publish", item.ID.String()); err != nil || out != "hello-world\n" {
        t.Fatalf("out = %q, err = %v", out, err)
    }
    // 业务错误按分类转换为退出码：重复发布是 Conflict，以 1 退出
    if _, err := run("publish", item.ID.String()); command.ExitCode(err) != command.ExitFailure {
        t.Fatalf("exit code = %d, err = %v", command.ExitCode(err), err)
    }
    // 参数不是合法的 ID 时是用法错误
    if _, err := run("publish", "not-a-uuid"); command.ExitCode(err) != command.ExitUsage {
        t.Fatalf("exit code = %d, err = %v", command.ExitCode(err), err)
    }
}
```

命令入口只负责解析参数和输出，业务规则仍然在上面的 `Handle` 测试中覆盖。

优先使用真实的依赖。只有在确实需要隔离某项外部能力时（例如对象存储），才在业务包中定义一个小接口，并在测试中提供简单的实现，见[依赖装配](../concepts/dependencies.md#replacing-dependencies-in-tests)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/database/dbtest`。

### Open

```go
func Open(t testing.TB, migrations fs.FS) *bun.DB
```

- **参数**
    - `t`：当前测试，测试结束时删除数据库
    - `migrations`：要执行的迁移，为 `nil` 时是一个空数据库
- **返回**：已经执行了迁移的临时数据库

管理员连接取自环境变量 `POSTGRES_TEST_URL`，没有设置时测试直接失败。

```go
db := dbtest.Open(t, migrations.FS)
```

### Replica

```go
func Replica(t testing.TB, db *bun.DB) *bun.DB
```

返回同一个测试数据库的只读连接池，模拟[只读副本](../database/quick-start.md#read-replicas)。

### URL

```go
func URL(t testing.TB) string
```

返回 `POSTGRES_TEST_URL`，没有设置时测试失败。
