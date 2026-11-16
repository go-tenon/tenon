# 前端规范

一个产品的后端和全部前端放在同一个仓库中：Go 写的后端，加上运营后台、租户控制台、用户端等几个网页前端。前端多了以后，如果每个前端各选一套组件库、各写一套请求和表单的方式，代码很快就难以维护，AI 编程助手也不知道该照哪种写法生成代码。

这一页规定 Tenon 应用的网页前端用什么技术、目录怎样组织、代码怎样写。`tenon init` 和 `tenon add frontend` 生成的前端已经按这里的规定组织好，每个前端的 `AGENTS.md` 也是从这里摘出来的。

阅读前需要了解：[脚手架](../installation/scaffolding.md)；[单页应用](spa.md)；[在 React 中调用接口](client-react.md)。

- [技术选型](#tech-stack)
    - [按前端选择组件库](#choosing-ui-library)
    - [共用的技术栈](#shared-stack)
    - [两套组件库各自的写法](#per-library)
    - [不使用的库](#not-used)
- [目录结构](#layout)
    - [仓库](#repository)
    - [分层规则](#layering)
    - [一个前端的内部](#inside-a-frontend)
    - [命名](#naming)
- [编码规范](#coding)
    - [路由](#routes)
    - [接口与服务端数据](#server-data)
    - [状态的归属](#state)
    - [表单](#forms)
    - [样式](#styles)
    - [生成的代码](#generated-code)
    - [检查与测试](#checks)
- [给 AI 编程助手的约束](#agents)
- [以后增加的端](#other-platforms)
- [常见问题](#troubleshooting)

<a id="tech-stack"></a>
## 技术选型

<a id="choosing-ui-library"></a>
### 按前端选择组件库

| 前端 | 组件库 | 模板 |
| --- | --- | --- |
| 运营后台、纯内部系统 | antd + ProComponents | `--template admin`，见[运营后台](admin.md) |
| 面向客户的后台（如租户控制台）、用户端 | shadcn/ui | `--template console`，见 [shadcn registry 与控制台](registry.md) |
| 面向客户、以大量数据录入为主的后台（如 ERP、财务、仓储） | antd + ProComponents，使用品牌主题 | `--template admin` |
| 公开页面（官网、帮助中心） | 服务端渲染，templ + shadcn-templ | 见[视图](views.md)和[界面组件](components.md) |

面向客户的后台用哪一套，看它以什么为主：以展示、流程和品牌感为主的用 shadcn；以复杂表格和大表单为主的用 antd。**一个前端只使用一套组件库**，两套的品牌色、圆角来自同一份[设计令牌](theme.md)，看起来仍然是一个产品。

<a id="shared-stack"></a>
### 共用的技术栈

| 层 | 选择 |
| --- | --- |
| 语言 | TypeScript，开启 `strict` |
| 框架 | React |
| 构建 | Vite，构建产物嵌入 Go 程序，见[单页应用](spa.md) |
| 路由 | TanStack Router，文件路由 |
| 服务端数据 | TanStack Query + `tenon generate` 生成的客户端，见[客户端代码](clients.md) |
| 框架的前端包 | `@go-tenon/client`、`@go-tenon/shell`；antd 的前端加 `@go-tenon/admin`，shadcn 的前端用 tenon 的 shadcn registry |
| 图表 | ECharts |
| AI 对话 | `@ai-sdk/react` 的 `useChat`，对接 [AI 助手](../ai/chat.md) |
| 富文本 | Tiptap |
| 拖拽 | dnd-kit |
| 流程图 | xyflow |
| 代码编辑 | CodeMirror 6 |
| 包管理与脚本 | Bun |
| 格式与检查 | Biome |
| 测试 | Vitest、Playwright |

<a id="per-library"></a>
### 两套组件库各自的写法

| 层 | antd | shadcn |
| --- | --- | --- |
| 组件 | antd、ProComponents | shadcn/ui（Radix），源码复制在项目中 |
| 图标 | `@ant-design/icons` | lucide-react |
| 样式 | antd 的 token、`classNames`/`styles`；布局用 Flex、Space、Grid | Tailwind v4 + shadcn 的主题变量 |
| 表单 | antd Form、ProForm | react-hook-form |
| 表格 | ProTable；超大数据量用 AG Grid | TanStack Table + TanStack Virtual |
| 后台框架 | `@go-tenon/admin` 的布局（基于 ProLayout） | registry 的布局（基于 shadcn 的 Sidebar） |
| 提示 | antd 的 message、notification | sonner |
| AI 对话界面 | Ant Design X | AI Elements |
| 图表 | ECharts | 常规图表用 shadcn Chart，复杂图表用 ECharts |
| 动效 | 不使用 | motion |
| 给 AI 编程助手查文档 | antd CLI 的 MCP，版本与项目的 antd 一致 | shadcn 的 MCP |

<a id="not-used"></a>
### 不使用的库

- 其他组件库：MUI、Chakra、Mantine、HeroUI 等。
- 其他样式方案：CSS-in-JS、styled-components、Sass/Less。antd 的前端可以用少量 CSS Modules。
- 其他数据请求方式：在组件中用 `useEffect` 请求数据、手写 `fetch`、axios、其他 OpenAPI 生成器。
- 其他状态库：Redux、Zustand、Jotai。应用级的共享状态用 React Context。
- 其他路由：React Router。

需要引入表外的库时，先写一份[架构决策记录](#adr)，再更新本规范。

<a id="layout"></a>
## 目录结构

<a id="repository"></a>
### 仓库

仓库分三部分：根目录是 Go 后端，`apps/` 是各个前端，`packages/` 是多个前端共用的 TypeScript 包。`tenon init` 生成的项目只有一个前端 `apps/web`；用 `tenon add frontend` 加入更多前端后是这样的：

```text
helpdesk/
├── go.mod
├── cmd/  internal/  config/   后端，见“目录结构”一页
├── package.json           Bun workspace（apps/*、packages/*）；dev、build、generate 脚本
├── bun.lock
├── tenon.json             开发工具的配置，可以没有
├── AGENTS.md              各个前端的使用者、组件库、挂载路径和调用的 API
├── .mcp.json              antd 或 shadcn 的 MCP 服务
├── theme/tokens.json      设计令牌：品牌色、圆角、字体
├── apps/                  每个前端一个目录
│   ├── admin/             运营后台（antd）
│   ├── console/           租户控制台（shadcn）
│   └── web/               用户端（shadcn）
├── packages/
│   ├── client/            生成的客户端
│   ├── theme/             由设计令牌生成的主题
│   └── ui/                多个 shadcn 前端共用的组件（需要时才建）
└── docs/adr/              架构决策记录
```

- 每个前端是 `apps/` 下的一个目录，有自己的 `package.json`、`vite.config.ts`、`AGENTS.md`，以及把自己的 `dist` 嵌入程序的 `frontend.go`。
- `packages/client` 和 `packages/theme` 由工具生成，从第一个前端起就有。`packages/ui` 在第二个 shadcn 前端出现时，才从前端中抽出共用的组件。
- 生成的客户端按 API 分目录：每个 API（`rest.New` 的一个 `BasePath`）生成到 `packages/client/src/<名称>`，名称默认是 `BasePath` 的最后一段，前端用 `@<项目名>/client/<名称>` 导入，例如 `@helpdesk/client/admin`。一个前端只导入自己调用的 API。见[开发工具](../installation/tooling.md#conventions)。
- 后端的目录见[目录结构](../concepts/structure.md)。

<a id="layering"></a>
### 分层规则

| 层 | 可以依赖 | 不可以依赖 |
| --- | --- | --- |
| `apps/*` | `packages/*` | 其他 `apps/*` |
| `packages/ui` | `packages/theme` | `packages/client`、任何 `apps/*` |
| `packages/client` | React、TanStack Query | DOM 与浏览器 API（`window`、`document`、`localStorage`）、`react-dom`、`packages/ui`、任何组件库 |

为什么 `client` 不能依赖 DOM？以后的桌面端、移动端如果也用 TypeScript（例如 React Native、Tauri），可以直接复用它。平台相关的能力（存储、跳转、WebSocket 的创建）由前端在创建客户端时传进去。这些规则可以用 Biome 的 `noRestrictedImports`、`noRestrictedGlobals` 检查。

<a id="adr"></a>
**架构决策记录**：影响多个前端或难以撤回的决定，写在 `docs/adr/NNNN-<标题>.md` 中，包括背景、决定、后果和状态。新的决定推翻旧的决定时，旧记录标注被哪一份取代，不删除。引入表外的库、增加一个前端、选择桌面端或移动端的技术，都先写一份决策记录。

<a id="inside-a-frontend"></a>
### 一个前端的内部

模板生成的前端只有 `main.tsx`、`app.ts` 和 `routes/`。页面多了以后，按下面的结构增加目录：

```text
apps/admin/
├── AGENTS.md
├── frontend.go            go:embed dist
├── index.html
├── package.json
├── vite.config.ts
├── tsconfig.json
├── public/
├── tests/                 unit/、e2e/
└── src/
    ├── main.tsx           挂载应用
    ├── app.ts             创建应用：Router、QueryClient、会话
    ├── routes/            文件路由
    ├── features/          按业务领域分组的页面和组件
    │   └── user/
    │       ├── queries.ts       组合多个接口的查询（单个接口的查询选项是生成的）
    │       ├── user-list-page.tsx
    │       └── user-form.tsx
    ├── components/        本前端内跨领域共用的组件；shadcn 的组件在 components/ui
    ├── lib/               与界面无关的工具函数
    └── routeTree.gen.ts   生成的路由树
```

路由文件只做路由的事，页面组件放在 `features/` 中，见下文的[路由](#routes)。

<a id="naming"></a>
### 命名

| 对象 | 规则 | 例子 |
| --- | --- | --- |
| 前端目录 | 按使用的人命名，小写 | `admin`、`console`、`web` |
| 前端的包名 | `@<项目名>/<前端目录>` | `@helpdesk/admin` |
| 共用包 | `@<项目名>/<包目录>` | `@helpdesk/client`、`@helpdesk/ui` |
| 文件 | kebab-case | `user-list-page.tsx` |
| 组件 | PascalCase，一个文件导出一个页面级组件 | `UserListPage` |
| hooks | `use` 开头的 camelCase | `useCurrentUser` |
| 业务领域目录 | 与 Go 的业务包同名，单数 | `features/article` 对应 `internal/article` |

<a id="coding"></a>
## 编码规范

<a id="routes"></a>
### 路由

- 路由文件只负责：路径与 search 参数的校验、权限要求、数据预加载、加载与错误状态。页面组件放在 `features/` 中。
- 分页、排序、筛选、选中的子页签放在 search 参数中，刷新和分享链接后保持不变，见[列表](app-shell-features.md#lists)。
- 登录检查放在布局路由上统一处理，见[登录检查](app-shell.md#requiring-sign-in)。
- 菜单由路由的元数据（`staticData`）生成，不手写第二份路由表，见[页面的元数据](app-shell.md#route-metadata)。
- 前端的权限判断只决定显示什么，权限名使用生成的类型；接口的授权由后端负责。

<a id="server-data"></a>
### 接口与服务端数据

- 所有请求都通过生成的客户端发出，不手写接口的类型和地址。修改接口后由 `tenon dev` 或 `tenon generate` 重新生成。
- 每个接口的查询选项和 query key 是生成的，页面和路由预加载直接使用，见 [TanStack Query](client-react.md#tanstack)。`features/<领域>/queries.ts` 只放组合多个接口的查询和乐观更新；有第二个前端用到时，移到 `packages/client`。
- 修改数据后，让相关的查询失效，不手动修改缓存，除非是有意的乐观更新。
- 接口错误用 `isApiError` 判断：校验错误按 `errors[].location` 显示在对应字段旁；业务错误按 `code` 处理；`401` 由会话统一处理，见[错误](clients.md#ts-errors)。
- 列表使用服务端分页，参数是 `page`、`per_page`，见[分页](responses.md#pagination)。
- 登录按 [JWT 与单页应用的登录](../security/jwt.md#single-page-applications)实现，令牌的保存和刷新交给 `@go-tenon/client` 的会话。

<a id="state"></a>
### 状态的归属

| 状态 | 放在哪里 | 例子 |
| --- | --- | --- |
| 可以分享的页面条件 | Router 的 search 参数 | 分页、排序、筛选 |
| 服务端数据 | TanStack Query | 列表、详情、当前用户 |
| 表单输入 | 表单库 | 未提交的编辑、校验错误 |
| 页面局部的交互 | 组件状态 | 弹窗开关、展开状态 |
| 应用共享的交互 | Context，或 `@go-tenon/shell` 的布局状态 | 侧栏折叠、标签页、主题 |

同一份数据只放在一个位置。不要把服务端数据复制到组件状态或 Context 中：复制出来的那一份不会随接口更新，两份很快就对不上了。

<a id="forms"></a>
### 表单

- antd 的前端用 antd Form 或 ProForm，shadcn 的前端用 react-hook-form。一个前端只有一种表单写法。
- 对接后端的表单不用 zod：校验规则是生成的，用 `@go-tenon/shell` 的 `antdRule` 或 `resolver`，规则和提示与服务端相同，见[表单](app-shell-features.md#forms)。zod 只用于纯前端、不提交给后端的表单。
- 服务端返回的字段错误用 `setServerErrors` 设置到对应字段上。
- 枚举的选项和显示文字使用生成的枚举，不手写，见[枚举](client-react.md#ts-enums)。
- 提交中禁用提交按钮；提交成功后关闭弹窗或跳转，并让相关查询失效。

<a id="styles"></a>
### 样式

- antd 的前端：主题通过 `ConfigProvider` 的 token 设置，组件局部的样式用 `classNames`/`styles`，布局用 antd 的布局组件。不覆盖 antd 的内部类名。
- shadcn 的前端：只用 Tailwind 工具类和 shadcn 的主题变量（`bg-primary`、`text-muted-foreground` 等），不写固定的颜色值。主题变量由[设计令牌](theme.md)生成。
- 修改 shadcn 组件时直接改 `components/ui` 中的源码，不在外面再包一层。

<a id="generated-code"></a>
### 生成的代码

| 代码 | 来源 | 是否提交 |
| --- | --- | --- |
| `packages/client/src/<名称>`：客户端、校验规则、验证提示、权限、查询选项、枚举、资源定义 | `tenon generate` | 提交 |
| `packages/theme`：三套界面的主题 | `tenon generate` 从 `theme/tokens.json` 生成 | 提交 |
| `src/routeTree.gen.ts` | TanStack Router 的 Vite 插件 | 提交 |
| `components/ui` | shadcn CLI 加入，之后由项目维护 | 提交 |

生成的代码不要手工修改，下次生成时会被覆盖。CI 中重新生成一次，再用 `git diff --exit-code` 检查，就能发现有人改了接口却忘了提交生成的代码，见[开发工具](../installation/tooling.md#generate)。只有属于产品、本来就要改的内容（如 `components/ui`）才复制进项目。

<a id="checks"></a>
### 检查与测试

- 依赖用 Bun 安装，提交 `bun.lock`，CI 使用 `bun install --frozen-lockfile`。
- 依赖的版本精确锁定；升级依赖单独提交。
- 提交前通过 `bun run typecheck`；admin 和 console 模板还有 `bun run lint`（Biome）。默认的 web 模板没有带 Biome，需要时自己加上。
- 纯逻辑（工具函数、状态转换）写 Vitest 单元测试；关键流程（登录、列表、表单提交）写 Playwright 测试。

<a id="agents"></a>
## 给 AI 编程助手的约束

AI 编程助手按项目中的说明写代码。说明越具体，它生成的代码越符合规范：

- **每个前端的 `AGENTS.md`**：列出本前端允许使用的库和写法，内容取自本页。admin 和 console 模板已经带着它。
- **根目录的 `AGENTS.md`**：列出每个前端的使用者、组件库、挂载路径和调用的 API，并写明：业务规则和权限判断写在 Go 的业务动作中，前端只负责展示和交互。模板不生成这个文件，加入第二个前端时自己写。
- **Biome 的 `noRestrictedImports`**：antd 的前端禁止 shadcn、Radix 和 Tailwind，shadcn 的前端禁止 antd。写错了，`bun run lint` 直接报错。模板已经配置好。
- **`.mcp.json`**：antd CLI 的 MCP 和 shadcn 的 MCP，让 AI 编程助手查到与项目版本一致的组件文档。`tenon add frontend` 按模板写入。

<a id="other-platforms"></a>
## 以后增加的端

桌面端、移动端的技术还没有确定，也不限定使用 React：可能是 Flutter、Go 编写的客户端、原生（Swift、Kotlin）或 React Native。第一个需要它们的产品先写一份架构决策记录，再放进 `apps/`，例如 `apps/desktop`、`apps/mobile`。

为了让任何技术都能接入，后端现在就遵守这些约定：

1. 业务规则、权限和校验都在 Go 中，所有端都只是 API 的调用方。
2. 每个 API 的 OpenAPI 文档是各端与后端之间唯一的契约；不兼容的修改通过新的 `BasePath` 发布，见[接口文档](../http/openapi.md)。任何语言都可以从它生成客户端，`tenon generate` 已经能生成 TypeScript 和 [Go](go-client.md) 的客户端。
3. 框架的约定（错误格式、分页、令牌刷新、实时频道、服务端推送与 AI 对话的消息流、上传、App 版本）写在与语言无关的[客户端协议](../http/protocol.md)中，`@go-tenon/client` 只是第一个实现。
4. 每个功能都能用 Bearer 令牌访问，refresh token 可以放在请求体中，不依赖只有浏览器才有的 cookie。
5. 实时消息用 Centrifuge，它有 JavaScript、Dart、Swift、Java、Go 等官方 SDK，见[实时频道](../libraries/realtime.md)；推送见[移动推送](../features/push.md)。
6. 设计令牌和文案的源文件是 JSON，不绑定某种语言。
7. App 从第一个版本起发送 `X-App-Version`，见 [App 版本](clients.md#app-version)。

<a id="troubleshooting"></a>
## 常见问题

**`bun run lint` 报错某个导入被禁止**

这个前端不允许使用这个库，例如在 antd 的前端中导入了 Tailwind 或 Radix。改用本前端组件库中对应的组件；确实需要时，先写架构决策记录，再修改本规范和 `biome.json`。

**两个前端要用同一个组件**

shadcn 的前端之间：把它移到 `packages/ui`。antd 和 shadcn 的前端之间不共用组件，只共用逻辑：逻辑放在 `@go-tenon/shell` 或 `packages/client`，各自用自己的组件库渲染。

**一个前端里既想用 antd 的表格，又想用 shadcn 的样式**

一个前端只使用一套组件库。按这个前端以什么为主重新选择，见[按前端选择组件库](#choosing-ui-library)。

**CI 中重新生成后有差异**

改了 Go 的接口或设计令牌，但没有提交重新生成的代码。在本地运行 `bun run generate`，把生成的文件一起提交。
