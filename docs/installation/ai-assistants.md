# AI 编程助手

Claude Code、Codex、Cursor 这类 AI 编程助手能替你写不少代码，前提是它知道项目的约定，读的是项目所用版本的文档。`tenon init` 生成的项目已经为它们准备好了：根目录的 `AGENTS.md` 写明怎样运行、提交前检查什么、代码按什么约定写；框架的文档复制在项目中，版本与 `go.mod` 一致；Go 的语言服务和 Mantine 的文档服务配置成了 MCP 服务。这一页介绍这些内容是什么、怎样保持更新，以及怎样按自己的需要调整。

阅读前需要了解：[脚手架](scaffolding.md)；[开发工具](tooling.md)。

- [示例用法](#example-usage)
- [AGENTS.md](#agents-md)
- [框架的文档](#framework-docs)
- [依赖的文档](#dependency-docs)
- [MCP 服务](#mcp-servers)
- [保持更新](#updating)
- [选择助手](#choosing-assistants)
- [常见问题](#troubleshooting)
- [进阶](#advanced)
    - [在线对话](#online-chat)
    - [Mantine 的技能](#mantine-skills)
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

`AGENTS.md` 是写给 AI 编程助手的项目说明，Claude Code、Codex、Cursor、GitHub Copilot 都会读它。它也适合人读：新加入的同事从它了解项目怎样运行。

文件分成两部分：

- **项目自己的约定**：开发环境、常用命令、目录、提交前的检查、编写测试的规则、代码约定（业务动作、注释、函数、错误、前端、接口）。`tenon init` 写出初稿，内容取自 Tenon 框架自己的开发约定。之后它归项目所有，按你的实际情况修改，`tenon` 不会再改动它。
- **tenon 维护的一段**：夹在 `<!-- tenon:begin -->` 和 `<!-- tenon:end -->` 两行注释之间，写着框架的版本、框架文档的位置、项目中的前端和客户端、依赖的版本和给 AI 读的文档、配置好的 MCP 服务。这一段由 `go tool tenon agents` 生成，不要手改，下次生成时会被覆盖。

例如加了一个运营后台以后，tenon 那一段中多出一行：

```markdown
- `apps/admin`：挂在 `/admin/`，只在浏览器中渲染。
```

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

<a id="dependency-docs"></a>
## 依赖的文档

项目还直接用到 Mantine、TanStack Router、Huma 这些社区组件。很多组件提供了给 AI 读的文档目录 `llms.txt`（格式见 [llmstxt.org](https://llmstxt.org)），`AGENTS.md` 中 tenon 的一段为项目用到的每个组件列出项目中的版本和这份文档，例如：

```markdown
| 依赖 | 版本 | 文档 |
| --- | --- | --- |
| Huma | v2.40.0 | https://huma.rocks |
| Mantine | 9.8.1 | https://mantine.dev/llms.txt；MCP 服务 `mantine` |
| TanStack Router | 1.170.46 | https://tanstack.com/router/latest/llms.txt |
```

只列出项目用到的组件：Go 的组件出现在应用的构建中才列出（例如用了队列才有 NATS），前端的组件写在某个前端或客户端的 `package.json` 中才列出。版本取自 `go.mod` 和 `node_modules`，还没有安装依赖时取 `package.json` 中写的范围。

网上的文档总是最新的版本。`AGENTS.md` 写明了：两者不一致时以项目的版本为准。Go 的组件大多没有 `llms.txt`，助手用 `go doc` 读与 `go.mod` 一致的版本，例如 `go doc github.com/danielgtaylor/huma/v2.Register`。

<a id="mcp-servers"></a>
## MCP 服务

[MCP](https://modelcontextprotocol.io) 让 AI 助手调用外部的工具。项目为助手配置了两个以 stdio 运行的 MCP 服务：

- **`gopls`**：Go 官方的语言服务，提供 `go_search`（查找符号）、`go_package_api`（读一个包的 API）、`go_diagnostics`（编译错误）等工具。它读的是项目和依赖的源码，版本与 `go.mod` 一致。需要先安装：

  ```bash
  go install golang.org/x/tools/gopls@latest
  ```

- **`mantine`**：Mantine 官方的文档服务（`@mantine/mcp-server`），提供 `search_docs`、`get_item_props` 等工具，查组件的属性和用法。项目中有前端时才配置，用 `bunx` 运行，第一次使用时自动下载。

每个助手的配置文件位置不同：

| 助手 | MCP 配置 |
| --- | --- |
| Claude Code | `.mcp.json` |
| Codex | `.codex/config.toml` |
| Cursor | `.cursor/mcp.json` |

`tenon` 只改写配置中名为 `gopls` 和 `mantine` 的两项，你自己加的其他 MCP 服务和设置保持原样。在 `.codex/config.toml` 中，这两项放在 `# tenon:begin` 和 `# tenon:end` 两行之间。这些文件也要提交。

确认服务已经连上：Claude Code 中输入 `/mcp`；Codex 运行 `codex mcp list`；Cursor 在设置的 MCP 页面中查看。

> [!NOTE]
> Codex 只读取受信任的项目中的 `.codex/config.toml`。第一次在项目中打开 Codex 时，选择信任这个项目。Claude Code 第一次遇到 `.mcp.json` 中的服务时也会询问是否启用。

<a id="updating"></a>
## 保持更新

下面这些变化之后，`AGENTS.md` 中 tenon 的一段和 MCP 配置要跟着更新：

- 升级框架：`go get github.com/go-tenon/tenon@latest`；
- 加了前端或客户端；
- 加减了依赖，例如开始使用队列。

运行：

```bash
bun run agents   # 即 go tool tenon agents
```

它更新 `.tenon/docs`，重新生成 `AGENTS.md` 中 tenon 的一段，改写 MCP 配置。可以反复运行，没有变化时文件保持不变。`tenon add frontend` 和 `tenon add client` 结束时会自动运行一次。运行后用 `git diff` 看一眼变化，再一起提交。

<a id="choosing-assistants"></a>
## 选择助手

默认为 Claude Code、Codex、Cursor 三个助手写 MCP 配置。只用其中一部分时，生成项目时用 `--agents` 选择：

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init todo --agents claude
```

`--agents none` 不写任何助手的配置，`AGENTS.md` 和 `.tenon/docs` 照样生成。选择记在 `tenon.json` 中，之后的 `tenon agents` 按它更新：

```json
{
  "agents": { "targets": ["claude"] }
}
```

已有的项目直接改 `tenon.json`，再运行 `bun run agents`。从列表中去掉的助手，它的配置文件留在原处，不再更新，不需要时自己删除。

<a id="troubleshooting"></a>
## 常见问题

**助手写出的代码用了不存在的函数或旧的写法**

它没有读项目中的文档，凭记忆写了别的版本的用法。确认 `.tenon/docs` 存在（运行一次 `bun run dev` 或 `bun run agents`），然后在提示中指明“先读 `.tenon/docs` 中讲 X 的那一页”。

**`/mcp` 中 gopls 显示连接失败**

没有安装 gopls，或者它不在 `PATH` 中。按[MCP 服务](#mcp-servers)中的命令安装，确认 `gopls version` 能运行，再重启助手。

**`AGENTS.md` 中 tenon 的一段还是旧的版本号**

升级框架以后没有运行 `bun run agents`。运行它，提交变化。

**`tenon agents` 把我写在 tenon 那一段中的内容删掉了**

这一段每次都重新生成。把你的内容移到 `<!-- tenon:begin -->` 之前或 `<!-- tenon:end -->` 之后。

<a id="advanced"></a>
## 进阶

<a id="online-chat"></a>
### 在线对话

在 ChatGPT、Claude 网页这类在线对话中问 Tenon 的用法时，把框架的 `llms.txt` 发给它：

```text
https://raw.githubusercontent.com/go-tenon/tenon/main/llms.txt
```

它是文档的目录，每一项链接到一页文档的 Markdown 原文。全部文档合成的一个文件是每个发布版本的附件：<https://github.com/go-tenon/tenon/releases/latest/download/llms-full.txt>。

<a id="mantine-skills"></a>
### Mantine 的技能

Mantine 还提供了几个技能（skill）：写给助手的、完成某类任务的详细步骤，例如用 `Combobox` 做自定义的选择框。它们取自 GitHub 上最新的版本，可能与项目中的 Mantine 版本不一致，所以 `tenon` 不默认安装。需要时自己安装：

```bash
npx skills add https://github.com/mantinedev/skills --skill mantine-combobox
```

可用的技能见 [mantinedev/skills](https://github.com/mantinedev/skills)。

<a id="reference"></a>
## 参考

### go tool tenon agents

```bash
go tool tenon agents
```

更新 AI 编程助手用到的内容：把项目所用版本的框架文档复制到 `.tenon/docs`；重新生成 `AGENTS.md` 中 `<!-- tenon:begin -->` 到 `<!-- tenon:end -->` 的一段，文件中没有这一段时加在末尾，没有这个文件时新建；为 `tenon.json` 中的每个助手改写 MCP 配置中的 `gopls` 和 `mantine`。没有安装 gopls 时打印安装命令。

### tenon init --agents

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init <dir> --agents claude,codex,cursor
```

为哪些助手写 MCP 配置，可选 `claude`、`codex`、`cursor`，默认是全部三个；`none` 表示都不写。与默认不同时写进 `tenon.json`。

### tenon.json 的 agents

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `agents.targets` | `["claude", "codex", "cursor"]` | 写 MCP 配置的助手；空数组时都不写 |
