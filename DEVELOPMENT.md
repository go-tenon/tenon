# 开发说明

- [简介](#introduction)
- [开发环境](#development-environment)
- [运行测试](#running-tests)
    - [单元测试](#unit-tests)
    - [集成测试](#integration-tests)
    - [架构测试](#architecture-tests)
    - [文档用法测试](#doc-tests)
    - [前端包](#js-packages)
- [持续集成](#continuous-integration)
- [编码风格](#coding-style)
- [编写文档](#writing-documentation)

<a id="introduction"></a>
## 简介

本文是团队内部开发 Tenon 框架本身的说明：怎样运行测试、CI 做了什么、代码和文档按什么约定写。应用的测试方法见[测试](docs/testing/overview.md)。

<a id="development-environment"></a>
## 开发环境

- Go，版本以仓库 [go.mod](go.mod) 中的声明为准；
- [Bun](https://bun.sh)，用于 `js` 中的前端包和脚手架的测试，版本与 CI 相同（1.4.4）；
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

下面这些测试在进程内完成；依赖 PostgreSQL 等服务的测试见[集成测试](#integration-tests)。

`filesystem/s3` 的存储流程测试使用 [gofakes3](https://github.com/johannesboyne/gofakes3) 的内存后端，通过 `httptest` 自动启动 S3 服务，真实 AWS SDK 会向它发送请求。它覆盖对象读写、服务端分片上传、预签名直传、分片合并和取消，全部在进程内完成。

`mail/smtp` 的测试在进程内启动一个最小的 SMTP 服务器，`mail/resend` 和 `mail/ses` 的测试用 `httptest` 代替 Resend 和 SES API，全部在进程内完成。

<a id="integration-tests"></a>
### 集成测试

依赖外部服务的测试需要真实的 PostgreSQL、Redis、Memcached、NATS、Gotenberg 和 Qdrant。仓库根目录的 `compose.test.yml` 启动这些服务，版本与 CI 相同，`--wait` 等到它们全部就绪才返回：

```bash
docker compose -f compose.test.yml up -d --wait
export POSTGRES_TEST_URL='postgres://postgres:postgres@127.0.0.1:55432/postgres?sslmode=disable'
export REDIS_TEST_URL='redis://127.0.0.1:56379/0'
export REDIS_CLUSTER_TEST_URL='redis://127.0.0.1:56380'
export MEMCACHED_TEST_ADDR='127.0.0.1:51211'
export NATS_TEST_URL='nats://127.0.0.1:54222'
export GOTENBERG_TEST_URL='http://127.0.0.1:53000'
export QDRANT_TEST_ADDR='127.0.0.1:56334'
go test -race -count=1 ./...
```

Redis 的锁、限流、在线成员和广播的测试对单机 Redis 和集群各运行一遍。集群由 `internal/redistest/cluster.sh` 在一个容器里启动，包含三个主节点。

框架的测试默认并行运行：顶层测试的第一行调用 `t.Parallel()`。修改全局状态的测试串行运行，例如使用 `t.Setenv`、`slog.SetDefault` 或包内 `export_test.go` 中缩短间隔的 `Set*` 函数的测试，以及比较耗时和内存分配量的测试。Go 先依次运行这些测试，再运行并行的测试，两者先后进行。

没有设置这些环境变量时，集成测试直接失败。每个数据库测试使用自己的临时库，Redis 测试给每个键加随机前缀，队列测试使用随机名字的 stream、向量测试给每个 collection 加随机前缀，都在结束时删除，所以服务可以一直开着反复运行测试。数据放在 tmpfs 中，测试完成后执行 `docker compose -f compose.test.yml down -v` 清理，数据全部消失。

依赖数据库的测试用 `database/dbtest` 的 `Open` 为每个测试建一个临时库。

集成测试中包含对迁移失败场景的演练，例如非事务迁移执行到一半失败后，检查数据库状态、人工恢复并再次执行。

文件服务的测试使用本地存储或 gofakes3，验证单张 `files` 表的状态变化、预签名直传、分片上传、确认、删除和清理。只运行文件相关测试可以执行 `go test -race ./filesystem/...`，仍需设置上述环境变量。

<a id="architecture-tests"></a>
### 架构测试

`internal/checks/archtest` 检查框架各个包的分层和依赖方向。每个包属于[四层](docs/concepts/structure.md#framework-packages)之一：基础、基础设施、入口、功能。包只能引用同层或更低层的包，并且每一个引用都要列在测试的 `rules` 中，例如 `database` 只能引用 `errorx`；HTTP、数据库和命令行组件只出现在基础层之上。

规则的键是包路径，对它的子包同样生效；子包需要更多依赖时单独写一条规则，例如 `sheet` 只引用 `errorx` 和 `form`，`sheet/imports` 另外引用 `operation` 和 `filesystem`。如果你的修改引入了新的包依赖，请先确认它符合分层，再相应地更新规则；弃用的依赖也要从规则中删除，测试会检查这一点。

`internal/checks/migrationstest` 检查各个包内嵌的迁移能通过 `publish` 命令原样发布。新增带迁移的包时在它的 `sources` 中登记，遗漏时测试会失败。

`internal/checks/clocktest` 检查框架的 SQL 和迁移中的时间都由 Go 算出后作为参数传入，数据库的时钟函数（例如 `NOW()`、`CURRENT_TIMESTAMP`）一律由 Go 代替。这样 [testing/synctest](docs/testing/overview.md#controlling-time) 的假时钟对整个框架有效，测试和时间有关的行为时用 `time.Sleep` 推进时间，包直接调用 `time.Now`。

<a id="doc-tests"></a>
### 文档用法测试

`internal/checks/doctest` 编译文档中的代码：除入门教程外，每个以 `package` 开头的代码块都是完整文件，单独放进临时 module 编译；其余片段只用于阅读。入门教程由 `cmd/tenon` 的 `TestTutorial` 在 CI 的 scaffold 任务中按章节做一遍。修改框架 API 时，测试会指出文档中需要同步修改的代码。

<a id="js-packages"></a>
### 前端包

`js` 是前端包的 Bun workspace：`js/client` 是 `@go-tenon/client`，`js/shell` 是 `@go-tenon/shell`，`js/admin` 是 `@go-tenon/admin`（antd 的外观），`js/registry` 是 tenon 的 shadcn registry。

`js/registry` 的 `src/components/tenon` 是框架的组件，`src/components/ui`、`src/hooks`、`src/lib`、`src/index.css` 是 shadcn 生成的代码，保持原样（Biome 跳过它们）。修改 `src/components/tenon` 或 `registry.json` 后运行 `bun registry/build.ts` 重新生成 `r/`，`registry.test.ts` 检查它是最新的，并检查每个条目带上了它导入的 shadcn 组件和 npm 包。更新 shadcn 的组件时，在一个单独的 Vite 项目中运行 `bunx shadcn@<版本> init` 和 `add`，再把生成的文件复制过来，组件用到的 npm 包（以那个项目的 `package.json` 为准）加到 `js/package.json` 的 `devDependencies`：在 `js` 这个 Bun workspace 中运行时，shadcn 没有把组件的 npm 依赖完整写进 `package.json`。已有的文件在提示覆盖时选否，保持与 registry 中的版本一致。AI 对话的组件（Message、Bubble、Trace、Message Scroller 等）来自 shadcn/ui 的 AI Elements，同样这样生成。console 模板安装的条目是 `cmd/tenon/internal/scaffold` 的 `ConsoleRegistryItems`，`TestInstallRegistry` 检查它们（连同逐层导入的文件）用到的 npm 包都在 console 模板的 `package.json` 中。它们不发布到 npm，随 Go 模块分发：应用中的 `tenon` 把框架版本中的 `js` 复制到项目的 `.tenon/js`，见[框架的前端包](docs/installation/tooling.md#framework-packages)。在 `js` 目录中运行：

```bash
bun install --frozen-lockfile
bun run check       # Biome
bun run typecheck   # 每个包的 tsc --noEmit
bun run test        # Vitest
bun run e2e         # Playwright，第一次运行前执行 bunx playwright install chromium
```

`@go-tenon/client` 不依赖 DOM：它的 `tsconfig.json` 中 `lib` 没有 `dom`，用到 `window`、`document` 等浏览器 API 时类型检查失败。`@go-tenon/shell` 用于网页，依赖 React、TanStack Router 和 TanStack Query，都是 peer 依赖；开发和测试用到的包（React、Testing Library、happy-dom、Vite 等）写在 `js/package.json` 的 `devDependencies` 中，不写在包自己的 `package.json` 里，否则会随包复制到项目中被安装。需要 DOM 的测试文件在开头写 `// @vitest-environment happy-dom`。测试文件（`*.test.ts`、`*.test.tsx`）不复制到项目中，可以用只在 `js/package.json` 中的包，例如表单适配器的测试用真实的 react-hook-form 和 antd。`js/e2e` 是在真实浏览器中运行的测试：一个只用 `@go-tenon/shell` 的小应用（Vite 启动），Playwright 检查多标签与保活中 happy-dom 测不到的部分，例如浏览器的前进后退、隐藏标签暂停 effect、刷新浏览器后恢复标签栏。它没有 `package.json`，不是 workspace 中的包：每个有 `package.json` 的目录都会被复制到项目中，框架的包在它的 `vite.config.ts` 和 `tsconfig.json` 中按路径引用。在生成的项目中试用改动时，用 `tenon init --tenon <框架目录>` 生成项目，`tenon dev` 每次启动时复制本地的 `js`。

<a id="continuous-integration"></a>
## 持续集成

CI 配置位于 [.github/workflows/ci.yml](.github/workflows/ci.yml)，每次推送和 Pull Request 都会同时运行四个 job：

- `lint` 依次执行：
  1. `gofmt` 格式检查；
  2. `go vet ./...`；
  3. `golangci-lint run`，配置在 [.golangci.yml](.golangci.yml)；
  4. 在 `web/ui` 中重新运行 `go tool templ generate`，确认提交的 `_templ.go` 是最新的。
- `js` 在 `js` 中安装依赖，运行 Biome、类型检查、测试和 Playwright 的 E2E 测试，见[前端包](#js-packages)。
- `test` 执行 `go test ./...`，使用 PostgreSQL 18、Redis 8、Memcached、NATS、Gotenberg 和 Qdrant 服务容器。
- `scaffold` 运行 `TestInitEndToEnd` 和 `TestTutorial`，见[文档用法测试](#doc-tests)。

`go test` 同时运行多个包，整体耗时取决于最慢的那个包。每次判断都要查询数据库或调用服务的测试只用少量样本：把统计性质留给纯函数的测试，经过服务的测试只用少量数据检查接线，例如 `feature` 的放量。

CI 运行普通的 `go test`，`-race` 会让整个流程慢好几倍。提交之前，请在本地至少运行前三项和 `go test ./...`；改动涉及并发时，另外用 `go test -race` 运行相关的包，时序敏感的测试还要用普通模式多跑几轮。

<a id="coding-style"></a>
## 编码风格

- 代码使用 `gofmt` 格式化；
- 遵循[目录结构](docs/concepts/structure.md#naming-conventions)中的命名约定；
- 直接暴露社区组件的原生类型（`*bun.DB`、`*slog.Logger`、`*cobra.Command` 等）；
- 数据库句柄有两种约定：存储（`apitoken.NewStore`、`feature.NewStore` 等）接收 `bun.IDB`，可以用连接池或业务事务构造；自己开启事务的服务（`operation`、`sheet/imports`、`sheet/exports`、`filesystem`）接收 `*bun.DB`，要与业务数据一起提交的方法另外接收 `tx bun.IDB` 参数，或提供 `WithDB`；
- 所有依赖都通过参数显式传递，包级变量只用于只读的值（错误模板、正则表达式等）。

<a id="writing-documentation"></a>
## 编写文档

用户文档位于 `docs` 目录，按主题分成若干子目录，每个子目录有一个 `README.md` 索引页，列出本组的每一页和一句话说明。与入口无关的内容（配置、错误、日志、数据库、库、功能、安全等）和各个入口（`http`、`web`、`background`、命令行、MCP、实时频道）分开放：讲业务动作、错误、认证这类共用的部分时，不假设调用来自 HTTP；某个入口特有的写法放在这个入口的目录中。`frontend` 是调用 HTTP 接口的单页应用。以后新增入口（例如 RPC）时，新建一个与 `http` 平级的目录。[docs/README.md](docs/README.md) 是总目录，按学习顺序排列：入门 → 基础 → 组织代码 → 前端与页面 → 常用功能 → 安全 → 进阶。[入门教程](docs/tutorial/README.md)从零做出一个博客。

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
- **片段中的变量都要交代来源**，例如“`db` 是[连接数据库](docs/database/quick-start.md#connecting)时得到的连接”。以 `package` 开头的代码块是完整文件，必须能单独编译（见上文的“文档用法测试”）。
- **对读者说“你”。** 句子要短，一句只说一件事；用具体的例子代替抽象的描述。
- **配置项**：每一项依次说明作用、默认值、什么时候需要修改。表格只用于对照多种情况，例如“情况 → 处理方式”。
- **提示块**：`> [!NOTE]` 补充说明和背后的机制；`> [!IMPORTANT]` 必须遵守的约定；`> [!WARNING]` 安全问题和容易踩的坑，并说明后果；`> [!TIP]` 实用建议。
- **链接**：第一次提到另一页的概念时给出链接；上游库的细节直接链接到它的文档，不在这里重复。

### 篇幅与新增页面

- 每页控制在 150–450 行，过长时按主题拆成同一目录下的几页，并在开头说明阅读顺序。
- 新增一页时，把它加入本组的 `README.md` 和 [docs/README.md](docs/README.md) 中合适的学习阶段；新增的包同时加入[框架包一览](docs/concepts/structure.md#framework-packages)中同一分类的表格。
