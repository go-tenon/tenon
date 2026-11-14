# 开发说明

- [简介](#introduction)
- [开发环境](#development-environment)
- [运行测试](#running-tests)
    - [单元测试](#unit-tests)
    - [集成测试](#integration-tests)
    - [架构测试](#architecture-tests)
    - [文档用法测试](#doc-tests)
- [持续集成](#continuous-integration)
- [编码风格](#coding-style)
- [编写文档](#writing-documentation)

<a id="introduction"></a>
## 简介

本文是团队内部开发 Tenon 框架本身的说明：怎样运行测试、CI 做了什么、代码和文档按什么约定写。在应用中使用 Tenon 时不需要读它，应用的测试方法见[测试](docs/testing/overview.md)。

<a id="development-environment"></a>
## 开发环境

- Go，版本以仓库 [go.mod](go.mod) 中的声明为准；
- Docker，用于在本地启动集成测试所需的服务。

<a id="running-tests"></a>
## 运行测试

<a id="unit-tests"></a>
### 单元测试

在仓库根目录执行：

```bash
go test ./...
go test -race ./...
```

下面这些测试不依赖外部服务；依赖 PostgreSQL 等服务的测试见[集成测试](#integration-tests)。

`filesystem/s3` 的存储流程测试使用 [gofakes3](https://github.com/johannesboyne/gofakes3) 的内存后端，通过 `httptest` 自动启动 S3 服务，真实 AWS SDK 会向它发送请求。它覆盖对象读写、服务端分片上传、预签名直传、分片合并和取消，不需要 AWS 凭证或独立 S3 容器。

`mail/smtp` 的测试在进程内启动一个最小的 SMTP 服务器，`mail/resend` 和 `mail/ses` 的测试用 `httptest` 代替 Resend 和 SES API，都不需要外部服务或 API key。

<a id="integration-tests"></a>
### 集成测试

依赖外部服务的测试需要真实的 PostgreSQL、Redis、Memcached、nsqd、Gotenberg 和 Qdrant。仓库根目录的 `compose.test.yml` 启动这些服务，版本与 CI 相同，`--wait` 等到它们全部就绪才返回：

```bash
docker compose -f compose.test.yml up -d --wait
export POSTGRES_TEST_URL='postgres://postgres:postgres@127.0.0.1:55432/postgres?sslmode=disable'
export REDIS_TEST_URL='redis://127.0.0.1:56379/0'
export REDIS_CLUSTER_TEST_URL='redis://127.0.0.1:56380'
export MEMCACHED_TEST_ADDR='127.0.0.1:51211'
export NSQD_TEST_ADDR='127.0.0.1:54150'
export NSQD_TEST_HTTP='http://127.0.0.1:54151'
export GOTENBERG_TEST_URL='http://127.0.0.1:53000'
export QDRANT_TEST_ADDR='127.0.0.1:56334'
go test -race -count=1 ./...
```

Redis 的锁、限流、在线成员和广播的测试对单机 Redis 和集群各运行一遍。集群由 `internal/redistest/cluster.sh` 在一个容器里启动，包含三个主节点。

框架的测试默认并行运行：顶层测试的第一行调用 `t.Parallel()`。修改全局状态的测试不调用它，例如使用 `t.Setenv`、`slog.SetDefault` 或包内 `export_test.go` 中缩短间隔的 `Set*` 函数的测试，以及比较耗时和内存分配量的测试。Go 先依次运行这些测试，再运行并行的测试，两者不会同时进行。

没有设置这些环境变量时，集成测试会失败而不是跳过。每个数据库测试使用自己的临时库，Redis 测试给每个键加随机前缀，队列测试给每个 topic、向量测试给每个 collection 加随机前缀并在结束时删除，所以服务可以一直开着反复运行测试。数据放在 tmpfs 中，测试完成后执行 `docker compose -f compose.test.yml down -v` 清理，不会留下任何数据。

依赖数据库的测试用 `database/dbtest` 的 `Open` 为每个测试建一个临时库。

集成测试中包含对迁移失败场景的演练，例如非事务迁移执行到一半失败后，检查数据库状态、人工恢复并再次执行。

文件服务的测试使用本地存储或 gofakes3，验证单张 `files` 表的状态变化、预签名直传、分片上传、确认、删除和清理。只运行文件相关测试可以执行 `go test -race ./filesystem/...`，仍需设置上述环境变量。

<a id="architecture-tests"></a>
### 架构测试

`internal/checks/archtest` 检查框架各个包的分层和依赖方向。每个包属于[四层](docs/concepts/structure.md#framework-packages)之一：基础、基础设施、入口、功能。包只能引用同层或更低层的包，并且每一个引用都要列在测试的 `rules` 中，例如 `database` 只能引用 `errorx`；基础层还不能引用 HTTP、数据库和命令行组件。

规则的键是包路径，对它的子包同样生效；子包需要更多依赖时单独写一条规则，例如 `sheet` 只引用 `errorx` 和 `form`，`sheet/imports` 另外引用 `operation` 和 `filesystem`。如果你的修改引入了新的包依赖，请先确认它符合分层，再相应地更新规则；不再用到的依赖也要从规则中删除，否则测试会失败。

`internal/checks/migrationstest` 检查各个包内嵌的迁移能通过 `publish` 命令原样发布。新增带迁移的包时在它的 `sources` 中登记，遗漏时测试会失败。

`internal/checks/clocktest` 检查框架的 SQL 和迁移不读取数据库的时钟，例如 `NOW()`、`CURRENT_TIMESTAMP`：时间都由 Go 算出后作为参数传入。这样 [testing/synctest](docs/testing/overview.md#controlling-time) 的假时钟对整个框架有效，测试和时间有关的行为时用 `time.Sleep` 推进时间，不要给包加可替换的时钟。

<a id="doc-tests"></a>
### 文档用法测试

`internal/checks/doctest` 编译文档中的代码：除入门教程外，每个以 `package` 开头的代码块都是完整文件，单独放进临时 module 编译；没有 `package` 的片段不编译。入门教程由 `cmd/tenon` 的 `TestTutorial` 在 CI 的 scaffold 任务中按章节做一遍。修改框架 API 时，测试会指出文档中不再能编译的代码。

<a id="continuous-integration"></a>
## 持续集成

CI 配置位于 [.github/workflows/ci.yml](.github/workflows/ci.yml)，每次推送和 Pull Request 都会同时运行两个 job：

- `lint` 依次执行：
  1. `gofmt` 格式检查；
  2. `go vet ./...`；
  3. `golangci-lint run`，配置在 [.golangci.yml](.golangci.yml)；
  4. 在 `web/ui` 中重新运行 `go tool templ generate`，确认提交的 `_templ.go` 是最新的。
- `test` 执行 `go test ./...`，使用 PostgreSQL 18、Redis 8、Memcached、nsqd、Gotenberg 和 Qdrant 服务容器。

`go test` 同时运行多个包，整体耗时取决于最慢的那个包。每次判断都要查询数据库或调用服务的测试，不要用大量样本检查统计性质：把统计性质留给纯函数的测试，经过服务的测试只用少量数据检查接线，例如 `feature` 的放量。

CI 不运行 `-race`，它让整个流程慢了好几倍。提交之前，请在本地至少运行前三项和 `go test ./...`；改动涉及并发时，另外用 `go test -race` 运行相关的包，时序敏感的测试还要不带 `-race` 多跑几轮。

<a id="coding-style"></a>
## 编码风格

- 代码使用 `gofmt` 格式化；
- 遵循[目录结构](docs/concepts/structure.md#naming-conventions)中的命名约定；
- 优先暴露社区组件的原生类型（`*bun.DB`、`*slog.Logger`、`*cobra.Command` 等），而不是再包装一层；
- 数据库句柄有两种约定：存储（`apitoken.NewStore`、`lock.NewDatabase` 等）接收 `bun.IDB`，可以用连接池或业务事务构造；自己开启事务的服务（`operation`、`sheet/imports`、`sheet/exports`、`filesystem`）接收 `*bun.DB`，要与业务数据一起提交的方法另外接收 `tx bun.IDB` 参数，或提供 `WithDB`；
- 不引入全局状态，所有依赖都通过参数显式传递。

<a id="writing-documentation"></a>
## 编写文档

用户文档位于 `docs` 目录，按主题分成若干子目录，每个子目录有一个 `README.md` 索引页，列出本组的每一页和一句话说明。[入门教程](docs/tutorial/README.md)从零做出一个博客。其余页面都是参考页：每页单独可读，示例不依赖教程中的博客。

参考页的结构：

````markdown
# 缓存

一两句话说明它解决什么问题，什么时候用。

- [示例用法](#example-usage)
- [配置](#configuration)
- ……
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

一个 10 行左右、最常见的用法。

<a id="configuration"></a>
## 配置

配置字段，每个字段一个小标题或一行表格。

## 各个主题……
## 命令行（如果有）
## 测试（如果有）

<a id="api-reference"></a>
## API 参考

### `cachex.New`

```go
func New[T any](c *memcache.Client, opts Options) *Cache[T]
```

- `c`：……
- `opts`：……

返回……
````

写作约定，参考 CodeIgniter 4 用户指南的语气和讲解方式：

- **开头**：第一句用平实的话说明它是什么（“X 是……”“X 提供……”），接着说明它解决什么问题、什么时候用。不写“简介”标题。
- **由浅入深**：先给最常见的用法，再逐步展开：配置 → 各种情况下的行为 → 进阶用法 → 部署与测试 → API 参考。后面的小节建立在前面的基础上，读者读到哪里都能停下。
- **概念页先讲问题**：先用一个具体的例子展示最直接的写法会遇到什么问题，问题逐步加重，再引出框架的做法，例如[依赖装配](docs/concepts/dependencies.md#why)。
- **人称**：对读者说“你”；解释设计理由时可以用“我们”；“让我们”只在入门教程中使用。
- **理由与规则写在一起**：给出一条规则时，在同一段中说明为什么，例如“删除应当放在事务提交之后：如果事务回滚了……”。
- **代码之前**：用一句话说明下面的代码展示什么，以冒号结尾。一个代码块只说明一件事；结果写在注释中，例如 `// 只有缓存中没有时才会调用`。
- **代码之后**：较长的示例按顺序解释它做了什么；能运行的示例说明运行后会看到什么。
- **选项**：每个配置项一个小标题或一段话，依次说明它的作用、默认值和影响。表格只用于对照多种情况，例如“情况 → 处理方式”。
- **提示块**：`> [!NOTE]` 补充说明和机制；`> [!IMPORTANT]` 必须遵守的约定；`> [!WARNING]` 安全问题和容易踩的坑，并说明后果；`> [!TIP]` 实用建议。
- **链接**：第一次提到另一页的概念时给出链接，例如“见[事务](docs/database/quick-start.md#transactions)”；需要深入时写“详见 ……”。
- **API 参考**：每一项依次是签名、**参数**列表、**返回**，然后一两句行为说明，必要时附一个短示例。
- 示例短小，一个示例只说明一件事。片段中用到的变量（`db`、`logger`、`api` 等）用一句话交代来源，不必给出完整的程序；以 `package` 开头的代码块是完整文件，必须能单独编译；
- 不重复上游库的文档，直接链接过去；
- 每页控制在 150–450 行，过长时按主题拆成同一目录下的几页；
- 新增一页时，把它加入本组的 `README.md` 和 [docs/README.md](docs/README.md)；新增的包同时加入[框架包一览](docs/concepts/structure.md#framework-packages)中同一分类的表格。
