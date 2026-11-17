# 开发说明

本文是开发 Tenon 框架本身的说明：怎样运行测试、提交前检查什么、代码和文档按什么约定写。应用的测试方法见[测试](docs/testing/overview.md)。

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

每个测试使用自己的临时库、随机名字的 stream、subject 前缀和 collection 前缀，结束时删除，所以服务可以一直开着反复运行测试。只运行一部分时指定包，例如 `go test ./filesystem/...`。数据放在 tmpfs 中，执行 `docker compose -f compose.test.yml down -v` 之后全部消失。

### 提交前

CI（[ci.yml](.github/workflows/ci.yml)）并行运行格式和静态检查、前端包的检查和测试、`go test ./...`，以及脚手架测试 `TestInitEndToEnd` 和 `TestTutorial`。提交之前在本地至少运行：

```bash
test -z "$(gofmt -l $(go list -f '{{.Dir}}' ./...))"
go vet ./...
golangci-lint run   # v2.15.0，配置在 .golangci.yml
go test ./...       # 包括下文的模块检查
```

改了 `js` 时另外运行下文“前端包”中的命令。CI 不开 `-race`，它会让整个流程慢好几倍；改动涉及并发时，在本地用 `go test -race` 运行相关的包，时序敏感的测试还要用普通模式多跑几轮。

### 编写测试

- 顶层测试的第一行调用 `t.Parallel()`。修改全局状态的测试串行运行，例如用到 `t.Setenv`、`slog.SetDefault` 或包内 `export_test.go` 中缩短间隔的 `Set*` 函数的测试，以及比较耗时和内存分配量的测试。
- 依赖数据库的测试用 `database/dbtest` 的 `Open` 为每个测试建一个临时库。
- 能在进程内模拟的外部服务就在进程内模拟：S3 用 [gofakes3](https://github.com/johannesboyne/gofakes3)，SMTP 用进程内的最小服务器，Resend、SES 这类 HTTP API 用 `httptest`。
- `go test` 的整体耗时取决于最慢的那个包。每次判断都要查询数据库或调用服务的测试只用少量样本：统计性质留给纯函数的测试，经过服务的测试只检查接线，例如 `feature` 的放量。

## 模块检查

`internal/checks` 下的测试检查整个模块，随 `go test ./...` 运行：

- `archtest`：分层和依赖方向。每个包属于[四层](docs/concepts/structure.md#framework-packages)之一：基础、基础设施、入口、功能。包只能引用同层或更低层的包，并且每一个引用都要列在测试的 `rules` 中，例如 `database` 只能引用 `errorx`。规则的键是包路径，对子包同样生效；子包需要更多依赖时单独写一条，例如 `sheet` 只引用 `errorx` 和 `form`，`sheet/imports` 另外引用 `operation` 和 `filesystem`。新增依赖时先确认它符合分层，再更新规则；不再使用的依赖也要从规则中删除。
- `migrationstest`：各个包内嵌的迁移能通过 `publish` 命令原样发布。新增带迁移的包时在它的 `sources` 中登记。
- `clocktest`：SQL 和迁移中的时间都由 Go 算出后作为参数传入，不用 `NOW()`、`CURRENT_TIMESTAMP` 这类数据库的时钟函数。这样 [testing/synctest](docs/testing/overview.md#controlling-time) 的假时钟对整个框架有效。
- `doctest`：编译文档中的代码。除入门教程外，每个以 `package` 开头的代码块都是完整文件，单独放进临时 module 编译；其余片段只用于阅读。入门教程由 `cmd/tenon` 的 `TestTutorial` 在 CI 的 scaffold 任务中按章节做一遍。
- `doclinks`：`README.md`、`AGENTS.md` 和 `docs` 中的相对链接指向的文件存在，带锚点时目标文档中有 `<a id="锚点"></a>`。
- `i18ntest`：框架用 `errorx.Define` 定义的每个错误码在内置的每种语言中都有翻译。

## 前端包

`js` 是前端包的 Bun workspace。这些包不发布到 npm，随 Go 模块分发：应用中的 `tenon` 把框架版本中的 `js` 复制到项目的 `.tenon/js`，见[框架的前端包](docs/installation/tooling.md#framework-packages)。

- `client` 是 `@go-tenon/client`，与界面无关的客户端运行时。它不依赖 DOM：`tsconfig.json` 的 `lib` 没有 `dom`，用到 `window`、`document` 等浏览器 API 时类型检查失败。
- `start` 是 `@go-tenon/start`，把 TanStack Start 接到 Go 托管的 Bun，以及 Vite 插件。
- `ui` 是 `@go-tenon/ui`，应用骨架、基于 Mantine 的界面和三套样式预设；`ui/src/shell` 是不导入 Mantine 的逻辑。React、TanStack Router、TanStack Query 和 Mantine 是 peer 依赖。
- `playground` 是 ui 的演示，用 `bun run playground` 启动。
- `e2e` 是 Playwright 在真实浏览器中运行的测试，检查 happy-dom 测不到的部分，例如浏览器的前进后退、隐藏标签暂停 effect、刷新后恢复标签栏。它的应用只用 `ui/src/shell`，没有 `package.json`，不是 workspace 中的包，在 `vite.config.ts` 和 `tsconfig.json` 中按路径引用框架的包。

在 `js` 目录中运行：

```bash
bun install --frozen-lockfile
bun run check       # Biome
bun run typecheck   # 每个包的 tsc --noEmit
bun run test        # Vitest
bun run e2e         # Playwright，第一次运行前执行 bunx playwright install chromium
```

- 每个有 `package.json` 的目录都会被复制到项目中安装，所以开发和测试用到的包（React、Testing Library、happy-dom、Vite 等）写在 `js/package.json` 的 `devDependencies` 中，不写在包自己的 `package.json` 里。测试文件（`*.test.ts`、`*.test.tsx`）不复制，可以用这些包。
- 需要 DOM 的测试文件在开头写 `// @vitest-environment happy-dom`。
- 在生成的项目中试用改动：用 `tenon init --tenon <框架目录>` 生成项目，`tenon dev` 每次启动时复制本地的 `js`。

## 编码风格

- 遵循[目录结构](docs/concepts/structure.md#naming-conventions)中的命名约定；
- 直接暴露社区组件的原生类型（`*bun.DB`、`*slog.Logger`、`*cobra.Command` 等）；
- 数据库句柄有两种约定：存储（`apitoken.NewStore`、`feature.NewStore` 等）接收 `bun.IDB`，可以用连接池或业务事务构造；自己开启事务的服务（`operation`、`sheet/imports`、`sheet/exports`、`filesystem`、`privacy`）接收 `*bun.DB`，要与业务数据一起提交的方法另外接收 `tx bun.IDB` 参数，或提供 `WithDB`；
- 所有依赖都通过参数显式传递，包级变量只用于只读的值（错误模板、正则表达式等）。

## 接口契约

API 的客户端不只是网页：App、桌面端和其他服务都可能调用它，用的语言也不一定是 TypeScript 或 Go。[客户端协议](docs/http/protocol.md)和 [RPC 协议](docs/rpc/protocol.md)是写给这些客户端的，框架的改动不能让它们只能用某一种语言、只能在浏览器中实现：

- 认证只用 `Authorization: Bearer`，不依赖 Cookie 这类只有浏览器才有的机制；原生应用登录走授权码 + PKCE（[OAuth 服务端](docs/security/oauth-server.md)）；
- 改动线路上的行为（请求头、状态码、错误响应、分页、事件流、实时频道等）时，同时更新客户端协议。规则以服务端和这一页为准，生成的运行时（`runtime.ts`、`wire/wire.go`）只是它的实现，不能只写在运行时里；
- 客户端需要的元数据先放进语言无关的来源：接口的信息写进 OpenAPI，框架的扩展字段如 `x-tenon-permissions`、`x-required-permissions`、`x-tenon-enum`；OpenAPI 表达不了的，例如 `errorx.Define` 的错误码和验证提示的翻译，由 `clientgen` 中各语言共用的部分收集。各语言的生成器只负责输出，不自己从 Go 代码推导别的生成器拿不到的信息；
- `@go-tenon/client` 不依赖 DOM，是其他语言实现客户端时的参考；只在网页中成立的功能放在 `@go-tenon/ui`。

## 编写文档

用户文档位于 `docs` 目录，按主题分成若干子目录，每个子目录有一个 `README.md` 索引页，列出本组的每一页和一句话说明。与入口无关的内容（配置、错误、日志、数据库、库、功能、安全等）和各个入口（`http`、`web`、`background`、命令行、MCP、实时频道）分开放：讲业务动作、错误、认证这类共用的部分时，不假设调用来自 HTTP；某个入口特有的写法放在这个入口的目录中。`frontend` 是调用 HTTP 接口的单页应用。以后新增入口时，新建一个与 `http` 平级的目录。[docs/README.md](docs/README.md) 是总目录，按学习顺序排列：入门 → 基础 → 组织代码 → 前端与页面 → 常用功能 → 安全 → 进阶。[入门教程](docs/tutorial/README.md)从零做出一个博客。

### 读者

假设读者是 Go 初级开发工程师：

- **会的：** Go 的基本语法、结构体和方法、接口、错误处理、goroutine，用过 `go mod` 和 `go test`，写过简单的命令行程序。
- **不一定会的：**
  - Go 中稍深一些的用法：`context` 的传递、错误的包装与判断、结构体标签、泛型、`embed`。这些集中在[Go 知识准备](docs/concepts/go-primer.md)中讲，正文第一次用到时链接到对应的小节。
  - 后端开发的常见概念：HTTP 中间件、SQL 事务与迁移、OpenAPI、JWT、OAuth、缓存失效、消息队列、OpenTelemetry。
  - 前端：React、TanStack Router、Vite。

不要假设读者用过其他框架，也不要和其他框架比较。

### 每一页的结构

每一页按读者的学习顺序展开，读者读到哪里都能停下，前面的部分已经可以用：

1. **开头**：一段话说明这一页讲什么、解决什么问题、什么时候需要。下一行写“阅读前需要了解：”，链接前置的页面；没有前置要求时不写。
2. **最小可运行的例子**：能直接复制运行，说明运行后会看到什么。
3. **逐步扩展**：每一节只加一个新需求，按“需求 → 代码 → 解释”来写：先用一两句话说明为什么需要它，再给代码，最后解释代码做了什么、为什么这样做。后面的小节建立在前面的基础上。
4. **常见问题**：容易犯的错误，按“现象 → 原因 → 解决”来写。
5. **进阶**：少数场景才需要的用法。
6. **API 参考**：每一项依次是签名、参数、返回值，再加一两句行为说明。

篇幅短、只讲一件事的页面可以省略其中几部分，但顺序不变。

### 写法

- **一节只引入一个新概念。** 术语第一次出现时用一两句话解释它是什么、为什么需要，再继续；详细内容链接到专门讲它的那一页，例如“事务把几条 SQL 语句变成一个整体：要么全部生效，要么全部不生效，见[事务](docs/database/quick-start.md#transactions)。”
- **给规则时说明理由**，写在同一段中，例如“缓存要在事务提交之后再删除：如果事务回滚了，缓存却已经删除，下一次读取会把旧数据重新写进缓存。”
- **代码之前**用一句话说明它展示什么，以冒号结尾。一个代码块只说明一件事，运行结果写在注释中。
- **代码之后**按顺序解释它做了什么；能运行的例子说明运行后会看到什么。
- **片段中的变量都要交代来源**，例如“`db` 是[连接数据库](docs/database/quick-start.md#connecting)时得到的连接”。以 `package` 开头的代码块是完整文件，必须能单独编译（`doctest` 会检查）。
- **对读者说“你”。** 句子要短，一句只说一件事；用具体的例子代替抽象的描述。
- **配置项**：每一项依次说明作用、默认值、什么时候需要修改。表格只用于对照多种情况，例如“情况 → 处理方式”。
- **提示块**：`> [!NOTE]` 补充说明和背后的机制；`> [!IMPORTANT]` 必须遵守的约定；`> [!WARNING]` 安全问题和容易踩的坑，并说明后果；`> [!TIP]` 实用建议。
- **链接**：第一次提到另一页的概念时给出链接；上游库的细节直接链接到它的文档，不在这里重复。

### 篇幅与新增页面

- 每页控制在 150–450 行，过长时按主题拆成同一目录下的几页，并在开头说明阅读顺序。
- 新增一页时，把它加入本组的 `README.md` 和 [docs/README.md](docs/README.md) 中合适的学习阶段；新增的包同时加入[框架包一览](docs/concepts/structure.md#framework-packages)中同一分类的表格。
