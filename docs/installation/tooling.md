# 开发工具

开发工具 `tenon` 生成项目、运行开发环境、生成客户端、构建、加入前端和客户端、更新给 AI 编程助手的内容。它按目录约定找到项目中的前端和 API，改默认行为时写 `tenon.json`。

包：`cmd/tenon`。

<a id="installation"></a>
## 安装：`go get -tool`

`tenon init` 生成的项目已经装好。已有的项目把它加为 `go.mod` 中的工具，版本随框架固定：

```bash
go get -tool github.com/go-tenon/tenon/cmd/tenon
go tool tenon dev
```

根目录 `package.json` 的脚本 `dev`、`build`、`generate`、`agents` 是 `go tool tenon <同名命令>`。`go.mod` 中加一行 `ignore node_modules`，让 `go build ./...` 跳过前端的依赖。

<a id="overview"></a>
## 命令一览

| 命令 | 什么时候用 |
| --- | --- |
| `tenon init` | 生成新项目 |
| `tenon dev` | 开发：运行应用和前端的开发服务器，改代码后重新编译 |
| `tenon generate` | 接口变了以后重新生成客户端；`dev` 自动运行它 |
| `tenon check` | 静态检查项目约定：前端文字与颜色、SQL 时钟、错误码翻译、前端依赖版本 |
| `tenon build` | 构建可执行文件，前端打包在其中 |
| `tenon deploy` | 通过 SSH 和 systemd 执行原生二进制零停机部署与远端运维 |
| `tenon add frontend` | 再加一个前端，例如运营后台 |
| `tenon add client` | 加桌面端等网页以外的客户端 |
| `tenon add deploy` | 生成原生二进制与 systemd 部署配置（`deploy.yaml`、`.env.example`） |
| `tenon add language` | 为项目加入一种语言，启用多语言翻译文件和检查 |
| `tenon agents` | 升级框架后更新给 AI 编程助手的文档、技能和 MCP 配置 |
| `tenon mcp` | AI 编程助手按 MCP 配置启动它，不手动运行 |

`serve`、`migrate`、`routes` 这类命令在应用自己的程序中，见[命令行](../general/commands.md)。

<a id="init"></a>
## `tenon init`：生成项目

```bash
go run github.com/go-tenon/tenon/cmd/tenon@latest init todo
```

生成的内容见[安装与生成项目](installing.md#creating-a-project)。选项：

- `--module`、`--name`：Go module 的路径和应用的名称，默认都是目录名。
- `--languages`：应用支持的语言列表（第一项是源语言，默认 `zh-CN`）。只写一种时生成单语言项目（前端直写文字，无 `locales/`）；写多种（例如 `zh-CN,en`）时启用多语言。
- `--agents`：接入哪些 AI 编程助手，默认 `claude,codex,cursor,antigravity`，还可以选 `copilot`，`none` 表示都不接入，见[选择助手](ai-assistants.md#choosing-assistants)。
- `--tenon <框架目录>`：开发 Tenon 框架本身时使用，`go.mod` 用 `replace` 指向本地的框架代码。

<a id="dev"></a>
## `tenon dev`：开发

```bash
bun run dev   # 即 go tool tenon dev
```

启动前检查需要的命令是否在 `PATH` 中，更新 `tenon/docs` 和 `tenon/js`。然后同时运行：

1. **每个前端的开发服务器**（Vite），各用一个空闲的端口，地址交给应用（见[应用这一侧](#app-side)）。服务端渲染时经环境变量 `API_ORIGIN` 调用应用的接口。
2. **应用**：运行 `serve`，把页面请求转发给开发服务器。只访问应用的地址，默认 <http://localhost:8080>。
3. **监视 Go 代码**：`.go`（测试文件除外）、`go.mod`、`go.sum`，以及与带 `//go:embed` 的 `.go` 文件同目录的文件（例如 `locales`、`migrations`）。变化后重新编译：成功时平滑地重启应用，再运行一次 [`generate`](#generate)；失败时打印错误，旧的程序继续运行。
4. `tenon.json` 的 `tools`，见 [tenon.json](#configuration)。

输出同时写进 `.tenon/dev.log`，AI 编程助手用 `tenon_dev_logs` 读它（见 [MCP 服务](ai-assistants.md#mcp-servers)）。`--client <名称>` 同时运行 `clients` 中的客户端。Ctrl+C 停止后运行 `tools` 的 `build` 命令。

<a id="generate"></a>
## `tenon generate`：生成客户端

```bash
bun run generate   # 即 go tool tenon generate
```

编译应用，运行它的 `client` 命令，生成每个 API 的 TypeScript 客户端 `packages/client/src/<名称>`（见[客户端代码](../http/clients.md#typescript)），以及 `tenon.json` 中 `apis.<BasePath>.go` 要求的 Go 客户端（见 [Go 客户端](../http/clients.md#go-client)）。

`tenon dev` 每次重新编译成功后自动运行它。生成的代码要提交：CI 重新生成一次，有差异时失败。

<a id="check"></a>
## `tenon check`：静态检查约定

```bash
bun run check   # 即 go tool tenon check
```

检查项目是否遵守框架约定，有问题时以非零状态退出，适合本地提交前和 CI 使用。用 `--only text,clock` 只运行指定项：

| 检查项 | 检查什么 | 行级豁免 |
| --- | --- | --- |
| `text` | 多语言项目（`languages` 多于一种）中前端 `src` 注释以外的汉字（单语言项目默认跳过，`--only text` 可显式运行） | `// i18n-ignore` |
| `clock` | `.sql` 和非测试 `.go` 字符串中的 `NOW()`、`CURRENT_TIMESTAMP` 等数据库时钟 | `// clock-ignore`、`-- clock-ignore` |
| `errors` | 附加语言及已创建的服务端翻译文件中，`errorx.Define` 的错误码是否补齐译文（源语言内联提示无需重复写） | — |
| `colors` | 前端 `src` 中写死的十六进制色值和 `rgb()`、`hsl()` 等颜色函数 | `// color-ignore`、`/* color-ignore */` |
| `deps` | 前端与客户端 `package.json` 的依赖版本是否满足框架包的 `peerDependencies` | — |
| `wiring` | 框架装配检查：热重载选项、事务内 `.WithDB(tx)`、搜索队列主题、限流/锁清理、上传体上限与 SSR `requestMiddleware` | `// wiring-ignore` |

跳过某项检查或豁免特定目录时，在 `tenon.json` 的 `check.skip`、`check.exempt` 中配置。

<a id="build"></a>
## `tenon build`：构建

```bash
bun run build   # 即 go tool tenon build，输出 bin/<入口包的目录名>
```

有前端时先依次：

1. `bun install --frozen-lockfile`：与 `bun.lock` 不一致时失败，改了前端的依赖要提交 `bun.lock`。
2. 清空 `apps/dist`，在每个前端中运行 `bun run build`，把 `dist/client` 复制到 `apps/dist/<名称>`。
3. 有服务端渲染的前端时，用 `tenon-start compile` 把它们连同 Bun 编译成 `apps/dist/ssr`，见[编译 SSR 程序](../frontend/hosting.md#compiling)。
4. 写入清单 `apps/dist/frontends.json`。

最后 `go build -trimpath`，`apps/apps.go` 嵌入整个 `apps/dist`。

- `-o`：输出的文件。
- `--arch amd64|arm64`：为 Linux 的另一种架构构建。不要用 `GOARCH=amd64 bun run build`：`go tool tenon` 会把 tenon 本身也编译成目标架构，在本机无法运行。
- `--client <名称>`：只打包这个客户端，见 [Electron 桌面端](../clients/electron.md#packaging)。

Go 托管的 SSR 进程只支持 Linux；前端都只在浏览器中渲染时，程序可以在任何系统上运行。部署见[部署](deployment.md)。

<a id="add-frontend"></a>
## `tenon add frontend`：再加一个前端

```bash
go tool tenon add frontend admin --layout frame
```

在 `apps/admin` 中生成前端，挂在 `/admin/`（`--base` 可改，不能与已有的前端相同），界面文字写在 `locales/admin.zh-CN.yaml` 中并生成 `apps/admin/src/messages.gen.ts`，见[界面文字](../frontend/i18n.md)。然后加入 workspace，运行 `bun install` 和 `tenon agents`。入口不用改：`frontend.Host` 按 `apps/dist` 中的清单托管全部前端，见[托管前端](../frontend/hosting.md#wiring)。

四个选项各自独立，没有写的取这种布局的默认值：

| 选项 | 可选值 | 默认 |
| --- | --- | --- |
| `--layout` | `rail`（双栏导航）、`frame`（顶栏加侧栏菜单、多标签）、`site`（官网和内容站） | `rail` |
| `--login` | `centered`、`split`、`none`（没有登录页，页面都公开） | `rail`、`frame` 是 `centered`，`site` 是 `none` |
| `--preset` | `compact`、`comfortable`、`spacious` | `frame` 是 `compact`，`rail` 是 `comfortable`，`site` 是 `spacious` |
| `--render` | `csr`（只在浏览器中渲染）、`ssr`（服务端渲染） | `rail`、`frame` 是 `csr`，`site` 是 `ssr` |

`rail`、`frame` 的页面在布局路由 `_app` 之下，有登录页时要求先登录。见[布局与导航](../frontend/layout.md)、[登录](../frontend/app.md#auth)、[样式预设](../frontend/app.md#presets)、[切换渲染方式](../frontend/hosting.md#switching)。

<a id="add-frontend-api"></a>
### `--api`：前端调用的 API

`--api` 是登录页调用的 API 的路径（它的 `POST /auth/login`、`POST /auth/logout`、`GET /me`），默认 `/api`。前端使用另一套账号时写它自己的 API，例如 `--api /ops/api`，见[运营后台与多租户](../security/tenancy.md)。

这个 API 的客户端已经生成并且有这三个接口时，`src/api.ts` 导出会话 `session`、客户端 `api` 和元数据 `kit`，登录调用生成的方法；否则会话在 `src/auth.ts` 中，按约定的路径自己发请求。所以先注册 API 和登录接口、运行一次 `tenon generate`，再加前端。

<a id="add-client"></a>
## `tenon add client`：桌面端等客户端

```bash
go tool tenon add client admin-desktop --kind electron --frontend admin
```

在 `clients/admin-desktop` 中生成 Electron 桌面端，窗口中显示 `apps/admin`（它要有登录页），并在 `apps/admin` 中加上授权页，授权页的文字追加到 `locales/admin.zh-CN.yaml` 的末尾。`--title` 是窗口标题和安装包的名称。之后用 `go tool tenon dev --client admin-desktop` 运行，`go tool tenon build --client admin-desktop` 打包，见 [Electron 桌面端](../clients/electron.md)。

<a id="add-deploy"></a>
## `tenon add deploy`：部署配置

```bash
go tool tenon add deploy --user deploy --host 203.0.113.10
```

在项目根目录生成 `deploy.yaml` 和 `.env.example`：

- `deploy.yaml` 声明目标环境、主机、服务部分（`all` 或 `http`、`worker`、`scheduler`）、部署目录与环境变量路径，进仓库保存，不含密钥。
- `.env.example` 是生产环境变量模板，复制填写后通过 `tenon deploy env push` 写入远端。
- 目标文件已存在时报错，传入 `--force` 覆盖。完整部署流程见[部署](deployment.md)。

<a id="deploy"></a>
## `tenon deploy`：原生二进制零停机部署

```bash
go tool tenon deploy setup                           # 首次初始化目标主机和 systemd unit
go tool tenon deploy env push .env.production        # 安全上传环境变量到 /etc/<应用>/env（权限 600）
go tool tenon deploy                                 # 交叉编译、自检、迁移并零停机滚动升级
```

- 常用标志：`--env`（默认 `production`）、`--file`（默认 `deploy.yaml`）、`--format text|json`（结构化 JSON 输出供 AI 编程助手与脚本解析）、`--binary`（跳过本地构建）、`--version`、`--skip-doctor`、`--dry-run`。
- 远端运维子命令：`rollback [version]`、`status [--role ...]`（表格化集群视图）、`logs [--role ...] [-F] [--since 15m] [--contains ERR]`（多机实时日志汇流）、`run -- <命令>`、`env pull`、`maintenance on|off`、`unlock`。
- 通过本机系统 `ssh`/`scp` 连接目标机并启用 `ControlMaster` 复用，直接兼容 `~/.ssh/config`、跳板机与 SSH Agent。细节见[使用 `tenon deploy` 自动发布](deployment.md#tenon-deploy)。

<a id="add-language"></a>
## `tenon add language`：加入一种语言

```bash
go tool tenon add language en
```

把语言加入 `tenon.json` 的 `languages`，创建 `locales/embed.go`、服务端 `locales/<语言>.yaml` 及各前端的翻译文件与 `src/i18n.ts`，重新生成 `messages.gen.ts` 并同步 AI 助手的多语言约定。之后运行 `go tool tenon check --only text,errors` 查看待抽离的前端文字与待补译的业务错误码，见[界面文字](../frontend/i18n.md)。

<a id="agents"></a>
## `tenon agents`：AI 编程助手

`bun run agents` 更新 `tenon/docs`，同步 `AGENTS.md` 的框架约定区块，为 `tenon.json` 中的每个助手重新安装 `tenon-*` 技能，改写 MCP 配置中的 `tenon`。升级框架后运行；`init`、`add frontend`、`add client` 结束时自动运行。见 [AI 编程助手](ai-assistants.md#updating)。

<a id="conventions"></a>
## 前端与 API 怎样被发现

**前端**是 `apps` 中带 `vite.config.ts`（或 `.mts`、`.js`、`.mjs`）的目录，目录名是前端的名称：小写字母开头，由小写字母、数字和连字符组成。`package.json` 的 `tenon` 字段写挂载路径和渲染方式，见[两种渲染方式](../frontend/hosting.md#modes)：

```json
{ "tenon": { "base": "/admin/", "ssr": false } }
```

构建结果 `apps/dist` 中只提交 `.gitignore`，让 `go:embed all:dist` 在刚 clone 时有文件可嵌入。缺少它时编译报 `pattern all:dist: no matching files found`：在根目录 `.gitignore` 中加 `!apps/dist/`，运行 `go tool tenon generate`，提交 `apps/dist/.gitignore`。

**API** 是路由中用 `rest.New` 创建的每一个 API，TypeScript 客户端生成到 `packages/client/src/<名称>`，前端用 `@<项目名>/client/<名称>` 导入。名称是 `BasePath` 从后往前第一个不是 `api`、也不是路径参数的段：

| BasePath | 名称 |
| --- | --- |
| `/api` | `api` |
| `/api/admin`、`/admin/api` | `admin` |
| `/t/{tenant}/api` | `t` |
| `/api/v1` | `v1` |

两个 API 名称相同时（例如 `/api/v1` 和 `/admin/v1`）`generate` 报错，在 `tenon.json` 中给其中一个改名：`"apis": {"/admin/v1": {"name": "admin"}}`。生成的文件见[客户端代码](../http/clients.md#generating-types)。

<a id="framework-packages"></a>
## 框架的前端包：`tenon/js`

`@go-tenon/client`、`@go-tenon/ui`、`@go-tenon/start`、`@go-tenon/host` 等前端包随 Go module 分发，版本就是 `go.mod` 中框架的版本。`init`、`add frontend`、`dev`、`build`、`generate` 把它们同步到 `tenon/js/<包名>`，根目录 `package.json` 的 `workspaces` 列出这些目录，前端用 `"@go-tenon/ui": "workspace:*"` 依赖它们。

`tenon/js` 随仓库提交（`.gitattributes` 将 `tenon/**` 标记为 `linguist-generated=true`，PR 中自动折叠），clone 或新建 worktree 后可直接运行 `bun install`。升级框架后运行 `bun run generate` 同步新版本的包并一起提交，需要时自动运行 `bun install`。运行期的临时文件（`dev.yaml`、`dev.log`）放在被 `.gitignore` 忽略的 `.tenon/` 中。

<a id="configuration"></a>
## `tenon.json`

在项目根目录，所有字段都可以省略：

```json
{
  "$schema": "https://raw.githubusercontent.com/go-tenon/tenon/main/cmd/tenon/schema.json",
  "apis": { "/api/v1": { "name": "public", "go": "../api" } }
}
```

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `main` | `cmd` 下唯一一个有 `main.go` 的目录 | 应用入口的包；有多个程序时写明 |
| `serve` | `["serve", "--config", "config/app.yaml,config/local.yaml"]`，`local.yaml` 不存在时去掉 | 开发时运行应用的参数；有前端时必须有 `--config` |
| `url` | 端口取 `--config` 文件中的 `http.addr`，主机 `localhost` | 应用的地址：`dev` 打印它，前端的开发服务器经它调用接口 |
| `apis.<BasePath>.name` | 见[前端与 API 怎样被发现](#conventions) | TypeScript 客户端的目录名 |
| `apis.<BasePath>.go` | 不生成 | Go 客户端的目录 |
| `languages` | `["zh-CN"]` | 支持的语言列表（第一项为源语言）；两种及以上时启用多语言模式和 `text` 检查 |
| `tools` | 空 | 其他工具，每项 `{"watch": [命令…], "build": [命令…]}`：`watch` 随 `dev` 运行，`build` 在 `dev` 停止后运行 |
| `agents.targets` | `["claude", "codex", "cursor", "antigravity"]` | 接入的 AI 编程助手，见[选择助手](ai-assistants.md#choosing-assistants) |
| `agents.skip` | 空 | 不安装的框架技能，见[技能](ai-assistants.md#skills) |
| `check.skip` | 空 | `tenon check` 默认跳过的检查项，例如 `["text"]` |
| `check.exempt` | 空 | `tenon check` 豁免的文件或目录路径，例如 `["migrations/legacy"]` |

端口被占用（`address already in use`）时，在 `config/local.yaml` 中改 `http.addr`，`url` 跟着变。

<a id="app-side"></a>
## 应用这一侧

`tenon init` 生成的入口已经写好两件事，见[入口文件](installing.md#entrypoint)：

- **导出接口**：根命令加入 `httpcmd.Commands`，参数是用默认配置和 `nil` 依赖构造全部路由的函数；`tenon generate` 运行其中的 `client` 命令，见[命令行](../general/commands.md#declaring-the-cli)。
- **接收前端开发服务器的地址**：`tenon dev` 把地址写进 `.tenon/dev.yaml` 的 `dev_servers`，追加在 `serve` 的 `--config` 之后。应用的 `Config` 用 `DevServers map[string]string` 接收，交给 `frontend.Options.DevServers`，见[托管前端：开发](../frontend/hosting.md#development)。
