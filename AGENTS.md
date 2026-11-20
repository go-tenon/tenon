# 开发说明

本文是开发 Tenon 框架本身的说明：怎样运行测试、提交前检查什么、代码按什么约定写。应用的测试方法见[测试](docs/testing/testing.md)。

只在改某一部分时用到的约定放在 `contributing` 中，改这些部分之前先读对应的一页：

- 改 `docs` 中的文档：[编写文档](contributing/docs.md)；
- 改 `js` 中的前端包、playground，或者脚手架模板中的界面：[前端包](contributing/frontend.md)，其中有颜色的约定；
- 新增或修改网页以外的客户端（Electron 等）：[客户端](contributing/clients.md)。

## 开发环境

- Go，版本以 [go.mod](go.mod) 为准；
- [Bun](https://bun.sh) 1.4.4（与 CI 相同），用于 `js` 中的前端包和脚手架的测试；
- Docker，用于启动测试所需的服务。

## 运行测试

一部分测试需要真实的 PostgreSQL、Memcached、NATS、Gotenberg 和 Qdrant，没有设置下面的环境变量时这些测试直接失败。`compose.test.yml` 启动这些服务，版本与 CI 相同，`--wait` 等到它们全部就绪才返回：

```bash
docker compose -f compose.test.yml up -d --wait
export POSTGRES_TEST_URL='postgres://postgres:postgres@127.0.0.1:55432/postgres?sslmode=disable'
export MEMCACHED_TEST_ADDR='127.0.0.1:51211'
export NATS_TEST_URL='nats://127.0.0.1:54222'
export GOTENBERG_TEST_URL='http://127.0.0.1:53000'
export QDRANT_TEST_ADDR='127.0.0.1:56334'
go test ./...
```

每个测试使用自己的临时库、随机名字的 stream、subject 前缀和 collection 前缀，结束时删除，所以服务可以一直开着反复运行测试。只运行一部分时指定包和测试名，例如 `go test ./filesystem/... -run TestUpload`。数据放在 tmpfs 中，执行 `docker compose -f compose.test.yml down -v` 之后全部消失。

### 提交前

CI（[ci.yml](.github/workflows/ci.yml)）并行运行格式和静态检查、前端包的检查和测试、`go test ./...`，以及脚手架测试 `TestInitEndToEnd` 和 `TestTutorial`。提交之前在本地至少运行：

```bash
test -z "$(gofmt -l $(go list -f '{{.Dir}}' ./...))"
go vet ./...
golangci-lint run   # v2.15.0，配置在 .golangci.yml
go test ./...       # 包括下文的模块检查
```

改了 `js` 时另外运行[前端包](contributing/frontend.md)中的命令。CI 不开 `-race`，它会让整个流程慢好几倍；改动涉及并发时，在本地用 `go test -race` 运行相关的包，时序敏感的测试还要用普通模式多跑几轮。

### 编写测试

- 顶层测试的第一行调用 `t.Parallel()`。修改全局状态的测试串行运行，例如用到 `t.Setenv`、`slog.SetDefault` 或包内 `export_test.go` 中缩短间隔的 `Set*` 函数的测试，以及比较耗时和内存分配量的测试。
- 依赖数据库的测试用 `database/dbtest` 的 `Open` 为每个测试建一个临时库。
- 能在进程内模拟的外部服务就在进程内模拟：S3 用 [gofakes3](https://github.com/johannesboyne/gofakes3)，SMTP 用进程内的最小服务器，Resend、SES 这类 HTTP API 用 `httptest`。
- `go test` 的整体耗时取决于最慢的那个包。每次判断都要查询数据库或调用服务的测试只用少量样本：统计性质留给纯函数的测试，经过服务的测试只检查接线，例如 `feature` 的放量。

## 模块检查

`internal/checks` 下的测试检查整个模块，随 `go test ./...` 运行。失败时按下面的说明修改代码或登记：

- `archtest`：分层和依赖方向。每个包属于[四层](docs/concepts/structure.md#framework-packages)之一：基础、基础设施、入口、功能。包只能引用同层或更低层的包，并且每一个引用都要列在测试的 `rules` 中，例如 `database` 只能引用 `errorx`。规则的键是包路径，对子包同样生效；子包需要更多依赖时单独写一条。新增依赖时先确认它符合分层，再更新规则；不再使用的依赖也要从规则中删除。
- `migrationstest`：各个包内嵌的迁移能通过 `publish` 命令原样发布，全部执行之后，以 `id` 为主键的表前三列依次是 `id`、`created_at`、`updated_at`（[时间列](docs/database/migrations.md#timestamp-columns)），两个时间列都是 `timestamptz NOT NULL`；有 `deleted_at` 时它是第四列，允许为空。模型嵌入 `database.Timestamps`，由 Bun 的钩子写入这两列；`Column(...)`、传入模型的 `Set(...)`、upsert 和原生 SQL 自己写 `updated_at`。新增带迁移的包时在它的 `sources` 中登记；结构由上游库决定的表登记在 `timestampExempt` 中。
- `clocktest`：SQL 和迁移中的时间都由 Go 算出后作为参数传入，不用 `NOW()`、`CURRENT_TIMESTAMP` 这类数据库的时钟函数。这样 [testing/synctest](docs/testing/testing.md#controlling-time) 的假时钟对整个框架有效。
- `doctest`：编译文档中的代码。除入门教程外，每个以 `package` 开头的代码块都是完整文件，单独放进临时 module 编译；其余片段只用于阅读。入门教程由 `cmd/tenon` 的 `TestTutorial` 在 CI 的 scaffold 任务中按章节做一遍。
- `doclinks`：`README.md`、`AGENTS.md`、`contributing` 和 `docs` 中的相对链接指向的文件存在，带锚点时目标文档中有 `<a id="锚点"></a>`。
- `uilayertest`：`@go-tenon/ui` 的分层，见[前端包](contributing/frontend.md)。
- `i18ntest`：框架用 `errorx.Define` 定义的每个错误码在内置的每种语言中都有翻译。
- `llmstest`：根目录的 [llms.txt](llms.txt) 与 `docs/README.md` 生成的结果一致，`docs` 中的每一页都列在 `docs/README.md` 中。改了 `docs/README.md` 后在仓库根目录运行 `go run ./internal/llmstxt/gen` 重新生成。
- `docbudget`：项目 `AGENTS.md` 的模板、技能、文档目录和文档每一节的篇幅，上限见[编写文档](contributing/docs.md#budget)。
- `agents` 的 `TestSkillReferences`：`cmd/tenon/internal/agents/skills` 中给 AI 编程助手的技能（`tenon init` 装进生成的项目）、项目 `AGENTS.md` 的模板中写的 `.tenon/docs/<页面>#<锚点>` 都存在于 `docs` 中。技能是从文档摘出的步骤和要点，改了文档中的写法、路径或锚点时同步修改对应的技能；改了应用的约定时同步修改模板 `cmd/tenon/internal/scaffold/template/project/AGENTS.md.tmpl`。

## 代码约定

### 兼容

v1.0 之前框架还没有使用者：迁移、配置和接口的改动不考虑兼容，直接修改原来的迁移和代码，不写兼容层，也不为改表新增迁移。

### 注释

- 包、类型、函数（包括非导出的函数和实现接口的方法）都写中文注释，直接说明它是什么、做什么、返回什么，例如“`Open` 为当前测试创建一个空的临时数据库，返回 `*bun.DB`；测试结束时删除。”
- 结构体字段的含义写在字段行尾的注释中。
- 较长的函数可以按步骤加几行注释，说明每一段做什么。
- 注释用简洁的直述句，直接说明它是什么，不用否定或对比来描述，例如写“`Bearer` 令牌放在 `Authorization` 头中”，不写“令牌不是放在 Cookie 里，而是放在 `Authorization` 头中”。

### 函数

- 保持函数的完整阅读流，单个函数可以写到 100–200 行。只在代码被复用、或者一段职责明显独立时才抽出函数。
- 标准库和依赖直接调用，不再包一层。
- 直接暴露社区组件的原生类型（`*bun.DB`、`*slog.Logger`、`*cobra.Command` 等）。
- 所有依赖都通过参数显式传递，包级变量只用于只读的值（错误模板、正则表达式等）。
- 数据库句柄有两种约定：存储（`apitoken.NewStore`、`feature.NewStore` 等）接收 `bun.IDB`，可以用连接池或业务事务构造；自己开启事务的服务（`operation`、`sheet/imports`、`sheet/exports`、`filesystem`、`privacy`）接收 `*bun.DB`，要与业务数据一起提交的方法另外接收 `tx bun.IDB` 参数，或提供 `WithDB`。
- 命名遵循[目录结构](docs/concepts/structure.md#naming-conventions)中的约定，包名什么时候加 `x` 后缀见[包名的后缀](docs/concepts/structure.md#package-suffix)。

### 错误

尽早暴露失败和错误，让问题尽早被发现、尽早修复：出错时返回错误，调用方处理不了就继续向上返回。不为不会发生的情况加防御检查，不吞掉错误，也不用默认值掩盖错误。

## 接口契约

API 的客户端包括网页、App、桌面端和其他服务，用的语言可能是 TypeScript 和 Go 以外的任何语言。[客户端协议](docs/http/protocol.md)和 [RPC 协议](docs/rpc/protocol.md)是写给这些客户端的，框架的改动不能让它们只能用某一种语言、只能在浏览器中实现：

- 每个接口都能用 `Authorization: Bearer` 调用。浏览器中的页面用[会话 cookie](docs/http/protocol.md#sessions) 携带同一个 token，其他客户端的认证只用 Bearer；原生应用登录走授权码 + PKCE（[OAuth 服务端](docs/security/oauth-server.md)）；
- 改动线路上的行为（请求头、状态码、错误响应、分页、事件流、实时频道等）时，同时更新客户端协议。规则以服务端和这一页为准，生成的运行时（`runtime.ts`、`wire/wire.go`）只是它的实现，不能只写在运行时里；
- 客户端需要的元数据先放进语言无关的来源：接口的信息写进 OpenAPI，框架的扩展字段如 `x-tenon-permissions`、`x-required-permissions`、`x-tenon-enum`；OpenAPI 表达不了的，例如 `errorx.Define` 的错误码和验证提示的翻译，由 `clientgen` 中各语言共用的部分收集。各语言的生成器只负责输出，不自己从 Go 代码推导别的生成器拿不到的信息；
- HTTP、RPC、MCP 三个入口共用的结构描述放在 `contract` 中：Go 类型的 JSON Schema 和校验（`NewSchema`）、schema 名（`SchemaName`）、枚举的命名类型（`EnumSchema`）、验证错误的规则码和译文（`Describe`）。入口用到它们时从 `contract` 取，不各写一套；登记、认证和响应格式各入口不同，留在各自的包中；
- `@go-tenon/client` 不依赖 DOM，是其他语言实现客户端时的参考；只在网页中成立的功能放在 `@go-tenon/ui`。

## 提交与 PR

提交信息、PR 的标题和描述都用中文。
