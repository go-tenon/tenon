## 框架与文档

- 框架版本以 `go.mod` 中的 `github.com/go-tenon/tenon` 为准，对应文档在 `tenon/docs`、前端包源码在 `tenon/js`，由 `bun run agents` / `bun run generate` 同步并提交。先读目录 `tenon/docs/README.md`，写代码前读对应页面。
- 查文档可用 MCP 工具 `tenon_search_docs`、`tenon_read_doc`，也可直接搜索阅读 `tenon/docs` 与 `tenon/js`。项目结构用 `tenon_app_info` 查看。
- Go 依赖的签名和说明用 `go doc` 读与 `go.mod` 一致的版本；Mantine、TanStack 等依赖的 AI 资源按 `README.md` 的“AI 编程助手”一节接入，与 Tenon 冲突时以本文和 `tenon/docs` 为准。

## 开发与部署约定

- Docker 仅用于开发和测试期：在主工作区启动 PostgreSQL、NATS、Redis 等依赖服务并常驻；业务服务与测试在独立的 `git worktree` 中运行（`bun run dev`、`go test ./...`），复用主工作区的依赖容器，通过各 worktree 的 `config/local.yaml` 错开 `http.addr` 端口（需要时指定独立数据库名）。
- 生产部署采用原生单二进制加 systemd 零停机升级：用 `go tool tenon add deploy` 生成 `deploy.yaml` 和 `.env.example`，用 `go tool tenon deploy` 初始化主机、发布与回滚，见 `tenon/docs/installation/deployment.md`。

## 提交前

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
bun run typecheck
bun run check
bun run generate && git diff --exit-code
```

CI（`.github/workflows/ci.yml`）运行同样的检查并构建一次；生成或同步的文件（`tenon/`、客户端、路由树、界面文字、技能、本节约定）未提交时失败。

## 编写测试

- 顶层测试第一行调用 `t.Parallel()`；修改全局状态（`t.Setenv`、`slog.SetDefault` 等）的测试串行运行。
- 依赖数据库的测试用 `database/dbtest` 的 `Open` 为每个测试建临时库；外部服务尽量在进程内模拟（S3 用 [gofakes3](https://github.com/johannesboyne/gofakes3)，HTTP API 用 `httptest`）。
- 时间相关逻辑用 `testing/synctest` 控制时间。SQL 中的时间由 Go 算出后作为参数传入，不用 `NOW()` 等数据库时钟函数。

## 代码约定

### 业务动作

- 一个用例写成一个业务动作，放在 `internal/<模块>/`，一个动作一个文件。HTTP、命令、队列、MCP 等入口只做输入转换并调用 `Handle`。
- 业务规则、权限判断和校验写在 Go 中；前端和 App 只负责展示和交互。
- {{error_i18n_rule}}

### 数据与事务

- 迁移中的每张表和每个字段都要写 `COMMENT ON TABLE` / `COMMENT ON COLUMN`，迁移测试调用 `dbtest.CheckSchema(t, db)` 检查时间列和表/字段注释。
- 多条写入需原子成功时放进 `database.RunInTx`；队列任务在同一个事务中发布。
- 回滚撤销不了的副作用（删缓存、广播）用 `tx.AfterCommit` 登记，提交后再执行。
- 缓存数据不带权限：命中缓存后仍须检查访问权限。

### 注释

- 包、类型、函数（含非导出和接口方法）都写注释，直述它是什么、做什么、返回什么；字段含义写在行尾；长函数按步骤加简短注释。

### 函数

- 保持完整阅读流（单函数可到 100–200 行），只在复用或职责独立时抽函数。
- 直接调用标准库和社区组件原生类型（`*bun.DB`、`*slog.Logger`、`*cobra.Command`），不再包一层。
- 依赖全部显式传参，包级变量只放只读值。存储接收 `bun.IDB`，自开事务的服务接收 `*bun.DB`。

### 错误

尽早暴露失败和错误：出错即返回，不加不会发生的防御检查，不吞错误，不用默认值掩盖。

### 前端

- 用 Mantine 和 `@go-tenon/ui` 组件，用生成的客户端和 TanStack Query 调接口，路由用 TanStack Router 文件路由。
- 颜色从 Mantine 主题取：默认品牌色 `brand`，彩色写预设名（`deep-orange`、`var(--mantine-color-deep-orange-6)`），中性色用 `gray`/`dark`/`c="dimmed"`/`--tn-*`，不写十六进制或 `rgb()`。
- 状态色固定：成功 `green`，进行中 `bright-blue`，等待 `yellow`，注意 `deep-orange`，失败 `red`，无状态 `gray`。
- {{frontend_i18n_rule}}
- 生成或同步的代码（`tenon/`、`packages/client`、`routeTree.gen.ts`、`messages.gen.ts`）不手改。

### 接口

- 改接口后运行 `bun run generate` 并与接口一起提交。
- 每个接口都能用 `Authorization: Bearer` 调用；浏览器用会话 cookie 携带同一 token。
- 前端所需元数据（校验、权限、枚举）写在 Go 接口定义中，经 OpenAPI 生成给客户端。
