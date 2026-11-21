---
name: tenon-action
description: 在 Tenon 应用中新增或修改一个业务用例（业务动作）：internal/<模块>/<动作>.go 中的类型、构造函数和 Handle，以及 HTTP、队列、定时任务、命令行、MCP 工具、RPC 这些入口方法。新增接口、后台操作、运维命令，或者修改业务规则时使用。
---

# 业务动作

Tenon 的业务逻辑按业务动作组织：一个用例是一个类型，单独放在一个文件中，各个入口都是它的方法。动手前先读 `.tenon/docs/concepts/actions.md`。

## 步骤

1. **找到模块**：业务代码在 `internal/<模块>/` 中，例如 `internal/article`。新模块就新建一个包。
2. **新建文件**：类型名是动词，包名已经说明了对象，例如 `article.Publish` 放在 `publish.go`。
3. **结构体只保存依赖**：构造函数 `NewPublish(db *bun.DB, ...)` 只把参数存进字段，不打开连接、不读配置。生成 OpenAPI、列出路由时会用零值的依赖构造动作。
4. **`Handle` 写全部业务规则**：调用者的身份作为参数显式传入，例如 `Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) (*Article, error)`。命令行、队列和测试都直接调用它。
5. **模块共用的东西放在 `<模块>.go`**：模型、业务错误、包内的查询函数、授权函数（例如 `CanEdit`），见 `.tenon/docs/concepts/actions.md#shared-code`。
6. **业务错误**：用 `errorx.Define(errorx.NotFound, "article.not_found")` 定义，`Handle` 中返回 `ErrNotFound.New()`；系统故障用 `errors.Wrap(err, "load article")` 包装后返回。分类与状态码见 `.tenon/docs/general/errors.md#defining-errors`。
7. **加上入口方法**：入口只做转换，从输入中取出参数、从 `ctx` 中取出调用者，调用 `Handle`，再转换结果。

| 入口 | 方法 | 在哪里登记 | 文档 |
| --- | --- | --- | --- |
| HTTP 接口 | `HTTP(ctx, *Input) (*Output, error)` | 模块的 `routes.go` | `.tenon/docs/concepts/actions.md#as-http`，技能 `tenon-http-api` |
| 队列任务 | `Job(ctx, payload) error` | 模块的 `jobs.go` | `.tenon/docs/concepts/actions.md#as-jobs` |
| 定时任务 | `Handle` 本身或 `func(ctx) error` | 模块的 `tasks.go` | `.tenon/docs/concepts/actions.md#as-scheduled-tasks` |
| 命令行 | `Command(cmd *cobra.Command, args []string) error` | 模块的 `commands.go` | `.tenon/docs/concepts/actions.md#as-commands` |
| MCP 工具 | `Tool(ctx, Args) (*Out, error)` | 模块的 `tools.go` | `.tenon/docs/concepts/actions.md#as-tools`，技能 `tenon-mcp-tools` |
| RPC | `RPC(ctx, Args) (*Out, error)` | 登记端点处 | `.tenon/docs/concepts/actions.md#as-rpc` |

8. **测试 `Handle`**：在真实的测试数据库上直接调用 `Handle`，用不同的 `identity.Subject` 模拟不同的调用者，见技能 `tenon-testing`。
9. **改了接口**：运行 `bun run generate`，把生成的客户端一起提交。

## 要点

- `Handle` 返回的业务错误原样返回，HTTP 转换为状态码，命令转换为退出码，队列任务不再重试。
- 一个动作需要另一个动作时，通过构造函数接收它，直接调用它的 `Handle`。
- 其他模块要在动作完成后跟着做事（例如发布文章后加积分）时，动作声明一个钩子类型，由入口传入，不用事件分发器，见 `.tenon/docs/concepts/actions.md#follow-ups`。钩子中的写入都用传入的 `tx`。带钩子或同时挂在多个入口的动作，在 `bootstrap` 的 `App` 上用工厂方法统一构造（例如 `a.publishArticle()`），各入口共用，避免漏传钩子。
- 两个动作的规则真正相同时才提取公共函数，只是几行代码相似时各写各的。
- 依赖都通过构造函数显式传递，不用包级变量保存可变的状态，见 `.tenon/docs/concepts/dependencies.md#constructor-injection`。
