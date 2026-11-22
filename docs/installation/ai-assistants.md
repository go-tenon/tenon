# AI 编程助手

`tenon init` 为 Claude Code、Codex、Cursor、Antigravity、GitHub Copilot 准备好项目说明 `AGENTS.md`、与 `go.mod` 版本一致的框架文档 `tenon/docs`、`tenon-*` 技能和 MCP 服务 `tenon`，`tenon agents` 保持它们更新。

包：`cmd/tenon`。

Tenon 只接入它自己的这些内容。Go、Mantine、TanStack 等依赖给 AI 的资源按它们的官方方案接入，见[依赖的官方方案](#dependencies)。

<a id="agents-md"></a>
## `AGENTS.md`

项目根目录的 `AGENTS.md` 是给助手的项目说明，五个助手都读它，要提交到代码仓库。它分成两部分：

- **项目部分**：`tenon init` 写出初稿（项目名称、常用命令、目录），之后归项目自己维护，用来补充业务背景和项目特有的规则；
- **框架托管区块**（`<!-- tenon:begin -->` 到 `<!-- tenon:end -->`）：框架文档查询、提交前检查、测试和代码约定，由 `bun run agents` 按 `go.mod` 中的框架版本自动同步，不要手改。已有项目首次升级运行 `bun run agents` 时会在文件末尾追加托管区块，删掉上方重复的旧约定后提交即可。

<a id="framework-docs"></a>
## 框架的文档：`tenon/docs`

`tenon/docs` 是项目所用框架版本的文档，从模块缓存中的框架源码同步过来，随仓库提交（`.gitattributes` 将 `tenon/**` 标记为 `linguist-generated=true`，PR 中自动折叠），`bun run agents`、`bun run dev` 和 `tenon mcp` 启动时增量更新。

写 Tenon 的代码之前先查这里的文档，写法以它为准：记忆中的用法可能来自别的版本。

1. 有 MCP 时用 `tenon_search_docs` 检索小节，再用 `tenon_read_doc` 读它，见[查文档](#searching-docs)。
2. 没有 MCP 时直接读目录 `tenon/docs/README.md`，或用搜索工具直接检索 `tenon/docs` 与 `tenon/js`：

```bash
rg 迁移 tenon/docs
go doc github.com/go-tenon/tenon/cache.Cache.Remember   # 函数签名和说明以 go doc 为准
```

<a id="mcp-servers"></a>
## MCP 服务：`tenon mcp`

MCP 配置中的 `tenon` 一项用 `go tool tenon mcp` 以 stdio 运行，版本与项目的框架相同，可以在项目的任何子目录中启动。八个只读工具涵盖开发文档、本地日志和线上部署观测：

| 工具 | 作用 |
| --- | --- |
| `tenon_search_docs` | 在 `tenon/docs` 中按小节检索，返回标题、路径和锚点、片段 |
| `tenon_read_doc` | 读一页的目录、一个小节连同子小节，或者整页 |
| `tenon_app_info` | 框架版本、应用入口和地址、前端（目录、挂载路径、是否服务端渲染）、客户端、安装的技能 |
| `tenon_dev_logs` | `bun run dev` 的输出：Go 的编译错误、应用的日志、前端开发服务器的输出 |
| `tenon_deploy_info` | 读取 `deploy.yaml`，返回环境、角色、主机、主迁移节点和滚动波次拓扑（不含密钥） |
| `tenon_deploy_status` | 并发查询线上集群各主机当前的发布版本与每个 systemd unit 的运行状态 |
| `tenon_deploy_logs` | 按环境、角色、主机、服务部分、`since` 和 `contains` 聚合拉取多台线上主机的 `journalctl` 日志 |
| `tenon_deploy_plan` | 预演某环境的发布计划（波次、主机与服务部分），供执行变更前确认 |

- 改完代码后调用 `tenon_dev_logs` 确认编译成功、没有报错：`lines` 是返回最后几行（默认 100，最多 1000），`contains` 只保留包含这段文字的行，例如 `ERR`。它读 `.tenon/dev.log`，`tenon dev` 每次启动时清空它。
- 协助线上排障和发布时，先用 `tenon_deploy_info`、`tenon_deploy_status`、`tenon_deploy_logs` 和 `tenon_deploy_plan` 只读查看与预演，需要变更线上时在终端运行 `go tool tenon deploy --format json`（全子命令均支持 `--format json` 结构化输出，见[部署](deployment.md#tenon-deploy)）。
- `tenon` 只改写 MCP 配置中名为 `tenon` 的一项，其他服务保持原样；`.codex/config.toml` 中这一项在 `# tenon:begin` 和 `# tenon:end` 之间。配置文件要提交。
- 确认已连上：Claude Code、Antigravity 中 `/mcp`；`codex mcp list`；Cursor 设置的 MCP 页面；VS Code 的“MCP: List Servers”。Codex 只读受信任的项目中的 `.codex/config.toml`。`go tool tenon` 第一次运行要编译，可能超时：先运行一次 `bun run agents`，再重启助手。

<a id="searching-docs"></a>
### 查文档：`tenon_search_docs` 和 `tenon_read_doc`

- `tenon_search_docs` 的 `query` 写要找的内容，中文、英文和代码中的名字都可以，空格分开多个词，越具体越好，例如 `访问策略`、`迁移 回滚`、`RunInTx`；`limit` 默认 8，最多 20。每个结果给出“页面标题 › 小节标题”、`tenon/docs/<页面>#<锚点>` 和一段片段。
- `tenon_read_doc` 的 `path` 写 `http/routing.md`、`tenon/docs/http/routing.md` 或带锚点的 `http/routing.md#access-policies`。只给页面时返回介绍和全部小节的锚点；带锚点或给 `anchor` 时返回这个小节连同子小节；`full: true` 返回整页。

```text
tenon_search_docs  {"query": "提交事务之后 删除缓存"}
tenon_read_doc     {"path": "database/database.md#after-commit"}
```

每一节单独读得懂，一次只读需要的一两节；小节中链接到别的页面时，用链接中的路径和锚点再读。

<a id="skills"></a>
## 技能

技能是一个目录，`SKILL.md` 开头写名称和什么时候用它，助手在任务相符时才读全文。技能只写步骤和要点，细节链接到 `tenon/docs`。

| 技能 | 什么时候用 |
| --- | --- |
| `tenon-action` | 新增或修改业务动作：`Handle` 和 HTTP、队列、命令行、MCP 等入口 |
| `tenon-http-api` | 写接口：路由、访问策略、输入与校验、响应与分页，改完重新生成客户端 |
| `tenon-database` | 接入数据库、新建迁移、模型、查询与事务 |
| `tenon-testing` | 测试业务动作、HTTP 入口和命令 |
| `tenon-background` | 队列任务和定时任务 |
| `tenon-auth` | 保护路由、权限、资源归属、角色、登录与令牌 |
| `tenon-mcp-tools` | 把业务动作作为 MCP 工具提供给应用的用户 |
| `tenon-frontend` | 前端页面：路由与菜单、查询、列表与表单、颜色。有前端时安装 |
| `tenon-electron` | Electron 桌面端。有 Electron 客户端时安装 |

- 技能写进[所选助手](#choosing-assistants)的技能目录，要提交。
- `tenon` 只管理名字以 `tenon-` 开头的目录，自己写的技能和依赖的技能不受影响。改写框架技能时复制一份换个名字，在 `tenon.json` 中跳过原来的：`{"agents": {"skip": ["tenon-frontend"]}}`。
- 助手按技能的 `description` 判断要不要读它，没有匹配上时直接点名，例如“按 tenon-http-api 技能加一个接口”。

<a id="updating"></a>
## 保持更新：`tenon agents`

```bash
bun run agents   # 即 go tool tenon agents
```

更新 `tenon/docs`，同步 `AGENTS.md` 中的框架约定区块，为 `tenon.json` 中的每个助手重新安装 `tenon-*` 技能，改写 MCP 配置中的 `tenon`；没有变化时文件保持不变。升级框架后运行它，一起提交变化，否则 CI 的 `Agents` 一步失败。`tenon add frontend`、`tenon add client` 结束时自动运行。

<a id="choosing-assistants"></a>
## 选择助手

| 助手 | `--agents` 中的名字 | 技能 | MCP 配置 | 默认 |
| --- | --- | --- | --- | --- |
| Claude Code | `claude` | `.claude/skills` | `.mcp.json` | 是 |
| Codex | `codex` | `.agents/skills` | `.codex/config.toml` | 是 |
| Cursor | `cursor` | 读 `.claude/skills` 和 `.agents/skills` | `.cursor/mcp.json` | 是 |
| Antigravity（取代 Gemini CLI） | `antigravity` | `.agents/skills` | `.agents/mcp_config.json` | 是 |
| GitHub Copilot（VS Code） | `copilot` | `.github/skills` | `.vscode/mcp.json` | 否 |

生成项目时用 `--agents` 选择，例如 `init todo --agents claude,copilot`；`none` 表示都不接入，`AGENTS.md` 和 `tenon/docs` 照样生成。与默认不同时选择写进 `tenon.json` 的 `agents.targets`（空数组表示都不接入）。已有的项目改这个字段，再运行 `bun run agents`；去掉的助手的配置和技能目录留在原处，不再更新。

<a id="dependencies"></a>
## 依赖的官方方案

依赖给 AI 的资源由各自的项目维护，`tenon` 不安装它们；生成的项目的 `README.md` 写好了官方的接入命令。

| 组件 | 给 AI 的资源 | 官方说明 |
| --- | --- | --- |
| Go | gopls 的 MCP 服务（`gopls mcp`） | [Gopls: MCP](https://go.dev/gopls/features/mcp) |
| Mantine | `llms.txt`、MCP 服务 `@mantine/mcp-server`、技能 | [Mantine：LLMs](https://mantine.dev/guides/llms/) |
| TanStack Router、Start、Query | `llms.txt`；Router 和 Start 随包发布的技能 | [TanStack Intent](https://tanstack.com/intent/latest) |
| React、Vite、Bun | `llms.txt` | [react.dev](https://react.dev/llms.txt)、[vite.dev](https://vite.dev/llms.txt)、[bun.com](https://bun.com/llms.txt) |

```bash
claude mcp add gopls -s project -- gopls mcp   # -s project 写进 .mcp.json，提交后团队共用
claude mcp add mantine -s project -- npx -y @mantine/mcp-server
```

> [!IMPORTANT]
> 依赖的文档和技能与 Tenon 的约定冲突时，以 `AGENTS.md`、`tenon/docs` 和 `tenon-*` 技能为准。Tenon 的前端不写服务端逻辑，业务逻辑都在 Go 的接口中：不启用 TanStack Start 中讲服务端函数、服务端路由和部署的技能。

Go 的其他依赖（Huma、chi、Bun ORM、goose 等）用 `go doc` 读与 `go.mod` 一致的版本，例如 `go doc github.com/danielgtaylor/huma/v2.Register`。

<a id="online-chat"></a>
## 在线对话：`llms.txt`

在 ChatGPT、Claude 网页这类在线对话中问 Tenon 时，把 `https://raw.githubusercontent.com/go-tenon/tenon/main/llms.txt` 发给它：它是文档的目录，每一项链接到一页的 Markdown 原文，对应主分支。
