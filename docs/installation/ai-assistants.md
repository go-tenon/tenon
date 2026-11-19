# AI 编程助手

Claude Code、Codex、Cursor、Antigravity、GitHub Copilot 这类 AI 编程助手能替你写不少代码，前提是它知道项目的约定，读的是项目所用版本的文档。`tenon init` 生成的项目已经为它们准备好了：根目录的 `AGENTS.md` 写明怎样运行、提交前检查什么、代码按什么约定写；框架的文档复制在项目中，版本与 `go.mod` 一致；新增业务动作、写接口、加表这类常做的事各有一个技能，写明步骤和要点；Tenon 的 MCP 服务让助手检索文档、读开发环境的输出。这一页介绍这些内容是什么、怎样保持更新，以及怎样按自己的需要调整。

Tenon 只接入它自己的这些内容。项目依赖的 Go、Mantine、TanStack 等也为 AI 编程助手提供了文档、MCP 服务和技能，按它们各自的官方方案接入：生成的项目的 `README.md` 写好了接入的命令，见[依赖的官方方案](#dependencies)。

阅读前需要了解：[脚手架](scaffolding.md)；[开发工具](tooling.md)。

- [示例用法](#example-usage)
- [AGENTS.md](#agents-md)
- [框架的文档](#framework-docs)
- [技能](#skills)
- [MCP 服务](#mcp-servers)
- [保持更新](#updating)
- [选择助手](#choosing-assistants)
- [依赖的官方方案](#dependencies)
- [常见问题](#troubleshooting)
- [进阶](#advanced)
    - [在线对话](#online-chat)
- [参考](#reference)

<a id="example-usage"></a>
## 示例用法

生成一个项目，在项目目录中打开 Claude Code：

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init todo
cd todo
claude
```

然后直接提需求，例如“给待办事项加一个截止日期，列表按截止日期排序”。助手启动时读到 `AGENTS.md`，知道业务代码按动作放在 `internal/todo` 中、改了接口要运行 `bun run generate`、提交前要通过哪些检查；需要细节时，它去 `.tenon/docs` 中读对应的文档页。

Codex 和 Cursor 的用法相同：在项目目录中打开它们，它们同样读取 `AGENTS.md` 和各自的 MCP 配置。

<a id="agents-md"></a>
## AGENTS.md

`AGENTS.md` 是写给 AI 编程助手的项目说明，Claude Code、Codex、Cursor、Antigravity、GitHub Copilot 都会读它。它也适合人读：新加入的同事从它了解项目怎样运行。

`tenon init` 写出初稿：框架文档在哪里、怎样查，开发环境、常用命令、目录、提交前的检查、编写测试的规则、代码约定（业务动作、注释、函数、错误、前端、接口），内容取自 Tenon 框架自己的开发约定。之后它归项目所有，按你的实际情况修改，`tenon` 不会再改动它。

`AGENTS.md` 只写不随项目变化的内容。会变的信息各有来源，助手需要时自己去读：框架的版本在 `go.mod` 中；前端和客户端在 `tenon.json` 和 `apps/` 中，MCP 工具 `tenon_app_info` 也会列出它们；技能的名称和用途由助手自动读进上下文；MCP 工具自带说明。

`AGENTS.md` 要提交到代码仓库：别人 clone 下来不运行任何命令，打开助手就能用。

<a id="framework-docs"></a>
## 框架的文档

AI 记得的 Tenon 用法可能来自别的版本，或者根本没有见过。项目中有一份与 `go.mod` 中的版本完全一致的文档，在 `.tenon/docs` 中，`AGENTS.md` 告诉助手先读目录 `.tenon/docs/README.md`，写某一部分的代码前读对应的页面。

`.tenon/docs` 从模块缓存中的框架源码复制过来，`bun run dev` 每次启动时更新，所以升级框架之后文档跟着更新。它在 `.tenon/` 中，不提交。

`.tenon` 被 `.gitignore` 忽略，ripgrep 这类搜索工具在项目根目录搜索时跳过它，搜索业务代码时不会混进文档。要搜索文档时指定这个目录：

```bash
rg 迁移 .tenon/docs   # 列出讲迁移的文档
```

`.tenon/docs` 中有一个内容为 `!*` 的 `.ignore` 文件，它让指定目录的搜索不受 `.gitignore` 影响。

<a id="skills"></a>
## 技能

`AGENTS.md` 每次都会读进助手的上下文，所以只写每次都要遵守的约定。做某件具体的事需要的步骤放在**技能**（skill）中：每个技能是一个目录，里面的 `SKILL.md` 开头写着名称和什么时候用它，助手只在任务与之相符时才读全文。技能是各个助手共用的开放格式（[agentskills.io](https://agentskills.io)）。

`tenon` 为项目安装这些技能：

| 技能 | 什么时候用 |
| --- | --- |
| `tenon-action` | 新增或修改业务动作：`Handle` 和 HTTP、队列、命令行、MCP 等入口 |
| `tenon-http-api` | 写接口：路由、访问策略、输入与验证、响应与分页，改完重新生成客户端 |
| `tenon-database` | 接入数据库、新建迁移、模型、查询与事务 |
| `tenon-testing` | 测试业务动作、HTTP 入口和命令 |
| `tenon-background` | 队列任务和定时任务 |
| `tenon-auth` | 保护路由、权限、资源归属、角色、登录与令牌 |
| `tenon-mcp-tools` | 把业务动作作为 MCP 工具提供给应用的用户 |
| `tenon-frontend` | 写前端页面：路由与菜单、查询、列表与表单、颜色。项目有前端时才安装 |
| `tenon-electron` | Electron 桌面端。项目有 Electron 客户端时才安装 |

技能只写步骤和要点，细节链接到 `.tenon/docs` 中的文档页，与项目的框架版本一致。例如助手要加一个接口时读到 `tenon-http-api`，它按步骤写输入类型、声明访问策略、在 `routes.go` 中注册，最后运行 `bun run generate`。

各个助手从不同的目录读取技能，`tenon` 按[选择的助手](#choosing-assistants)写进对应的目录，表格见那一节。同时选了 Claude Code 和 Codex（或 Antigravity）时，`.claude/skills` 和 `.agents/skills` 中各有一份，Cursor 两个目录都读，会看到两份同名的技能。技能目录也要提交。

`tenon` 只管理名字以 `tenon-` 开头的技能目录，你自己写的技能和按[官方方案](#dependencies)装上的依赖的技能放在同样的目录中，`tenon` 不会改动。想改写某个框架的技能时，复制一份换个名字（例如 `acme-frontend`），在 `tenon.json` 中跳过原来的：

```json
{
  "agents": { "skip": ["tenon-frontend"] }
}
```

<a id="mcp-servers"></a>
## MCP 服务

[MCP](https://modelcontextprotocol.io) 让 AI 助手调用外部的工具。项目为助手配置了 Tenon 自己的 MCP 服务 `tenon`：用 `go tool tenon mcp` 以 stdio 运行，版本与项目的框架版本相同，不需要另外安装。它提供四个工具，名字都以 `tenon_` 开头，与其他服务的同名工具（例如 Mantine 的 `search_docs`）区分开：

| 工具 | 作用 |
| --- | --- |
| `tenon_search_docs` | 在 `.tenon/docs` 中按小节检索，返回最相关的小节的标题、路径和锚点、片段。中文按相邻的两个字切分，不需要分词词典 |
| `tenon_read_doc` | 读一页的目录（全部小节的锚点）、一个小节连同它的子小节，或者整页 |
| `tenon_app_info` | 项目的框架版本、入口、前端与客户端、安装的技能 |
| `tenon_dev_logs` | `bun run dev` 运行期间的输出：编译错误、应用的日志、前端开发服务器的输出；可以只取包含某段文字的行，例如 `ERR` |

助手改完代码后调用 `tenon_dev_logs`，就能看到重新编译有没有失败、应用有没有报错，不用你把终端里的内容复制给它。

每个助手的 MCP 配置文件位置不同，见[选择助手](#choosing-assistants)中的表格。`tenon` 只改写配置中名为 `tenon` 的一项，按[官方方案](#dependencies)加上的 gopls、Mantine 等服务和其他设置保持原样。在 `.codex/config.toml` 中，这一项放在 `# tenon:begin` 和 `# tenon:end` 两行之间。这些文件也要提交。

确认服务已经连上：Claude Code、Antigravity 中输入 `/mcp`；Codex 运行 `codex mcp list`；Cursor 在设置的 MCP 页面中查看；VS Code 中运行命令“MCP: List Servers”。

> [!NOTE]
> Codex 只读取受信任的项目中的 `.codex/config.toml`。第一次在项目中打开 Codex 时，选择信任这个项目。Claude Code 第一次遇到 `.mcp.json` 中的服务时也会询问是否启用。

`tenon_dev_logs` 读的是 `.tenon/dev.log`。`tenon dev` 把各进程的输出同时写进终端和这个文件，写进文件时去掉颜色；每次启动时清空，超过 8 MB 时从头写起。因为子进程的输出先经过 `tenon dev` 再到终端，它给子进程设置 `FORCE_COLOR=1`，终端中的日志照样是着色的文本，见[日志的格式](../general/logging.md#formats)。

<a id="updating"></a>
## 保持更新

下面这些变化之后，框架文档、技能和 MCP 配置要跟着更新：

- 升级框架：`go get github.com/go-tenon/tenon@latest`；
- 加了前端或客户端。

运行：

```bash
bun run agents   # 即 go tool tenon agents
```

它更新 `.tenon/docs`，重新安装技能，改写 MCP 配置。可以反复运行，没有变化时文件保持不变。`tenon add frontend` 和 `tenon add client` 结束时会自动运行一次。运行后用 `git diff` 看一眼变化，再一起提交。

<a id="choosing-assistants"></a>
## 选择助手

`tenon` 支持五个助手，它们都读 `AGENTS.md`，区别在技能目录和 MCP 配置的位置：

| 助手 | `--agents` 中的名字 | 技能 | MCP 配置 | 默认 |
| --- | --- | --- | --- | --- |
| Claude Code | `claude` | `.claude/skills` | `.mcp.json` | 是 |
| Codex | `codex` | `.agents/skills` | `.codex/config.toml` | 是 |
| Cursor | `cursor` | 读 `.claude/skills` 和 `.agents/skills` | `.cursor/mcp.json` | 是 |
| Antigravity | `antigravity` | `.agents/skills` | `.agents/mcp_config.json` | 是 |
| GitHub Copilot（VS Code） | `copilot` | `.github/skills` | `.vscode/mcp.json` | 否 |

Antigravity 是 Google 的编程助手，它的 CLI（`agy`）取代了 Gemini CLI，项目中的配置与 Gemini CLI 不同：MCP 服务写在 `.agents/mcp_config.json`，技能在 `.agents/skills`。还在用 Gemini CLI 的，从 Gemini CLI 迁移到 Antigravity 即可，见 Google 的[迁移说明](https://antigravity.google/docs/cli/gcli-migration)。

Copilot 只从 `.github/skills` 读取技能，选它就要再装一份技能，所以默认不选。用 Copilot 时把它加进 `--agents`。

只用其中一部分时，生成项目时用 `--agents` 选择：

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init todo --agents claude,copilot
```

`--agents none` 不安装技能，也不写任何助手的配置，`AGENTS.md` 和 `.tenon/docs` 照样生成。选择记在 `tenon.json` 中，之后的 `tenon agents` 按它更新：

```json
{
  "agents": { "targets": ["claude", "copilot"] }
}
```

已有的项目直接改 `tenon.json`，再运行 `bun run agents`。从列表中去掉的助手，它的配置文件和技能目录留在原处，不再更新，不需要时自己删除。

<a id="dependencies"></a>
## 依赖的官方方案

项目直接用到的组件也为 AI 编程助手准备了资源：给 AI 读的文档目录 `llms.txt`（格式见 [llmstxt.org](https://llmstxt.org)）、MCP 服务和技能。它们由各自的项目维护，接入方式随版本变化，所以 `tenon` 不替你安装，而是在生成的项目的 `README.md` 中写好按官方方法接入的命令，你按需执行：

| 组件 | 给 AI 的资源 | 官方说明 |
| --- | --- | --- |
| Go | gopls 的 MCP 服务（`gopls mcp`）：查找符号、读包的 API、检查编译错误，版本与 `go.mod` 一致 | [Gopls: MCP](https://go.dev/gopls/features/mcp) |
| Mantine | `llms.txt`、MCP 服务 `@mantine/mcp-server`、技能（`mantinedev/skills`） | [Mantine：LLMs](https://mantine.dev/guides/llms/) |
| TanStack Router、Start、Query | `llms.txt`；Router 和 Start 随包发布的技能，用 TanStack Intent 接入 | [TanStack Intent](https://tanstack.com/intent/latest) |
| React、Vite、Bun | `llms.txt` | [react.dev/llms.txt](https://react.dev/llms.txt)、[vite.dev/llms.txt](https://vite.dev/llms.txt)、[bun.com/llms.txt](https://bun.com/llms.txt) |
| Electron | 文档 | [electronjs.org/docs](https://www.electronjs.org/docs/latest) |

例如在 Claude Code 中加上 gopls 和 Mantine 的 MCP 服务（官方的命令）：

```bash
go install golang.org/x/tools/gopls@latest
claude mcp add gopls -- gopls mcp
claude mcp add mantine -- npx -y @mantine/mcp-server
```

加上 `-s project` 时写进项目的 `.mcp.json`，提交后团队共用。

> [!IMPORTANT]
> 依赖的文档和技能讲的是它们自己的通用用法，不知道 Tenon 的约定。两者冲突时以 `AGENTS.md`、`.tenon/docs` 和 `tenon-*` 技能为准。最常见的是 TanStack Start：它的技能教你在前端写服务端函数、服务端路由，而 Tenon 的前端不写服务端逻辑，业务逻辑都在 Go 的接口中。用 TanStack Intent 选择技能时，不要启用 Start 中讲服务端函数、服务端路由和部署的技能。

Go 的其他依赖（Huma、chi、Bun ORM、goose 等）大多没有 `llms.txt`，助手用 `go doc` 读与 `go.mod` 一致的版本，例如 `go doc github.com/danielgtaylor/huma/v2.Register`。网上的文档总是最新的版本，与项目的版本不一致时以项目的版本为准。

<a id="troubleshooting"></a>
## 常见问题

**助手写出的代码用了不存在的函数或旧的写法**

它没有读项目中的文档，凭记忆写了别的版本的用法。确认 `.tenon/docs` 存在（运行一次 `bun run dev` 或 `bun run agents`），然后在提示中指明“先读 `.tenon/docs` 中讲 X 的那一页”。

**`/mcp` 中 tenon 第一次连接很慢或超时**

`go tool tenon` 第一次运行时要编译 `tenon`，之后用缓存。先在终端中运行一次 `bun run dev` 或 `bun run agents`，再重启助手。

**`/mcp` 中 gopls 显示连接失败**

没有安装 gopls，或者它不在 `PATH` 中。按[依赖的官方方案](#dependencies)中的命令安装，确认 `gopls version` 能运行，再重启助手。

**CI 的 Agents 一步失败，技能有差异**

升级框架以后没有运行 `bun run agents`，提交的技能还是旧版本的。运行它，提交变化。

**助手没有用技能**

助手按技能开头的 `description` 判断要不要读它。任务描述得太笼统时可能没有匹配上，直接点名，例如“按 tenon-http-api 技能加一个接口”。Claude Code 中输入 `/` 能看到已安装的技能，Codex 中是 `/skills`。

<a id="advanced"></a>
## 进阶

<a id="online-chat"></a>
### 在线对话

在 ChatGPT、Claude 网页这类在线对话中问 Tenon 的用法时，把框架的 `llms.txt` 发给它：

```text
https://raw.githubusercontent.com/go-tenon/tenon/main/llms.txt
```

它是文档的目录，每一项链接到一页文档的 Markdown 原文。全部文档合成的一个文件是每个发布版本的附件：<https://github.com/go-tenon/tenon/releases/latest/download/llms-full.txt>。

<a id="reference"></a>
## 参考

### go tool tenon agents

```bash
go tool tenon agents
```

更新 AI 编程助手用到的内容：把项目所用版本的框架文档复制到 `.tenon/docs`；为 `tenon.json` 中的每个助手重新安装 `tenon-` 开头的技能，改写 MCP 配置中的 `tenon`。

### go tool tenon mcp

```bash
go tool tenon mcp
```

以 stdio 运行给 AI 编程助手用的 MCP 服务，提供 `tenon_search_docs`、`tenon_read_doc`、`tenon_app_info`、`tenon_dev_logs` 四个工具。助手按 MCP 配置启动它，不需要手动运行。可以在项目的任何子目录中运行：项目的根目录取 `go env GOMOD` 所在的目录。启动时先更新 `.tenon/docs`。

### tenon init --agents

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init <dir> --agents claude,codex,cursor,antigravity
```

为哪些助手安装技能、写 MCP 配置，可选 `claude`、`codex`、`cursor`、`antigravity`、`copilot`，默认是除 `copilot` 以外的四个；`none` 表示都不写。与默认不同时写进 `tenon.json`。

### tenon.json 的 agents

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `agents.targets` | `["claude", "codex", "cursor", "antigravity"]` | 安装技能、写 MCP 配置的助手，可选的见[选择助手](#choosing-assistants)；空数组时都不写 |
| `agents.skip` | 空 | 不安装的框架技能，例如 `["tenon-electron"]` |
