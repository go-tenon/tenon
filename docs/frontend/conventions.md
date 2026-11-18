# 前端规范

一个产品的后端和全部前端放在同一个仓库中：Go 写的后端，加上运营后台、租户控制台、用户端等几个网页前端。前端多了以后，如果每个前端各选一套组件库、各写一套请求和表单的方式，代码很快就难以维护，AI 编程助手也不知道该照哪种写法生成代码。

这一页规定 Tenon 应用的网页前端用什么技术、目录怎样组织、代码怎样写。`tenon init` 和 `tenon add frontend` 生成的前端已经按这里的规定组织好，每个前端的 `AGENTS.md` 也是从这里摘出来的。

阅读前需要了解：[脚手架](../installation/scaffolding.md)；[TanStack Start 的接入](start.md)；[在 React 中调用接口](client-react.md)。

- [技术选型](#tech-stack)
    - [按前端选择样式预设](#choosing-ui-library)
    - [技术栈](#shared-stack)
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
### 按前端选择布局和样式预设

所有前端都用同一套组件库 [Mantine](https://mantine.dev) 和框架的 [`@go-tenon/ui`](app.md)，按界面的样子选择布局、样式预设和渲染方式：

| 界面 | 选项 | 渲染 |
| --- | --- | --- |
| 表格和大表单多的后台（运营后台、商家后台、ERP、财务、仓储） | `--layout frame`（默认 `compact`） | 只在浏览器中渲染 |
| 以模块切换为主、页面不密集的应用（SaaS 控制台、聊天工具、用户中心） | `--layout rail`（默认 `comfortable`） | 只在浏览器中渲染 |
| 公开页面（官网、内容站、帮助中心） | `--layout site`（默认 `spacious`） | 服务端渲染，不随请求变化的页面构建时预渲染 |

表中是每种布局的默认值。登录页（`--login`）、样式预设（`--preset`）和渲染方式（`--render`）都可以单独换掉，与布局任意搭配，例如官网加上登录页用 `--layout site --login centered`，见 [tenon add frontend](../installation/tooling.md#add-frontend)。

三套预设共用一套设计语言，品牌色在每个前端的 `src/theme.ts` 中设置，看起来仍然是一个产品，见[样式预设](app.md#presets)。

<a id="shared-stack"></a>
### 技术栈

| 层 | 选择 |
| --- | --- |
| 语言 | TypeScript，开启 `strict` |
| 框架 | React、[TanStack Start](start.md)，构建后嵌入 Go 程序，见[托管前端与服务端渲染](hosting.md) |
| 路由 | TanStack Router，文件路由 |
| 服务端数据 | TanStack Query + `tenon generate` 生成的客户端，见[客户端代码](../http/clients.md) |
| 框架的前端包 | `@go-tenon/client`、`@go-tenon/start`、`@go-tenon/ui` |
| 组件 | Mantine；表格用 `@go-tenon/ui` 的 `DataTable`（基于 mantine-datatable） |
| 表单 | `@mantine/form`，用 `@go-tenon/ui` 的 `useSchemaForm` 接上生成的校验规则 |
| 图标 | Tabler Icons（`@tabler/icons-react`） |
| 提示与确认 | `@go-tenon/ui` 的 `notifySuccess`、`notifyError`、`confirm` |
| 图表 | ECharts |
| AI 对话 | `@ai-sdk/react` 的 `useChat`，对接 [AI 助手](../ai/chat.md) |
| 富文本 | Tiptap |
| 拖拽 | dnd-kit |
| 流程图 | xyflow |
| 代码编辑 | CodeMirror 6 |
| 包管理与脚本 | Bun |
| 格式与检查 | Biome |
| 测试 | Vitest、Playwright |

<a id="not-used"></a>
### 不使用的库

- 其他组件库：antd、MUI、Chakra、shadcn/ui、HeroUI 等。
- 其他样式方案：Tailwind、CSS-in-JS、styled-components、Sass/Less。Mantine 的样式属性不够用时用 CSS Modules。
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
├── AGENTS.md              各个前端的使用者、样式预设、挂载路径和调用的 API
├── apps/                  每个前端一个目录
│   ├── admin/             运营后台（--layout frame）
│   ├── console/           控制台（--layout rail）
│   └── web/               官网和用户端（--layout site）
├── packages/
│   ├── client/            生成的客户端
│   └── ui/                多个前端共用的业务组件（需要时才建）
└── docs/adr/              架构决策记录
```

- 每个前端是 `apps/` 下的一个目录，有自己的 `package.json`（其中的 `tenon` 字段声明挂载路径和是否在服务端渲染）、`vite.config.ts` 和 `AGENTS.md`。全部前端的构建产物在 `apps/dist` 中，由 `apps/apps.go` 嵌入程序。
- `packages/client` 由工具生成，从第一个前端起就有。`packages/ui` 在第二个前端用到同一个业务组件时，才从前端中抽出来。
- 生成的客户端按 API 分目录：每个 API（`rest.New` 的一个 `BasePath`）生成到 `packages/client/src/<名称>`，名称默认是 `BasePath` 的最后一段，前端用 `@<项目名>/client/<名称>` 导入，例如 `@helpdesk/client/admin`。一个前端只导入自己调用的 API。见[开发工具](../installation/tooling.md#conventions)。
- 后端的目录见[目录结构](../concepts/structure.md)。

<a id="layering"></a>
### 分层规则

| 层 | 可以依赖 | 不可以依赖 |
| --- | --- | --- |
| `apps/*` | `packages/*` | 其他 `apps/*` |
| `packages/ui` | `@go-tenon/ui`、Mantine | `packages/client`、任何 `apps/*` |
| `packages/client` | React、TanStack Query | DOM 与浏览器 API（`window`、`document`、`localStorage`）、`react-dom`、`packages/ui`、任何组件库 |

为什么 `client` 不能依赖 DOM？以后的桌面端、移动端如果也用 TypeScript（例如 React Native、Tauri），可以直接复用它。平台相关的能力（存储、跳转、WebSocket 的创建）由前端在创建客户端时传进去。这些规则可以用 Biome 的 `noRestrictedImports`、`noRestrictedGlobals` 检查。

<a id="adr"></a>
**架构决策记录**：影响多个前端或难以撤回的决定，写在 `docs/adr/NNNN-<标题>.md` 中，包括背景、决定、后果和状态。新的决定推翻旧的决定时，旧记录标注被哪一份取代，不删除。引入表外的库、增加一个前端、选择桌面端或移动端的技术，都先写一份决策记录。

<a id="inside-a-frontend"></a>
### 一个前端的内部

模板生成的前端只有 `router.tsx`、`start.ts`、`server.ts`、`theme.ts` 和 `routes/`。页面多了以后，按下面的结构增加目录：

```text
apps/admin/
├── AGENTS.md
├── package.json
├── vite.config.ts
├── tsconfig.json
├── public/
├── tests/                 unit/、e2e/
└── src/
    ├── router.tsx         getRouter：创建应用（Router、QueryClient、会话）
    ├── start.ts           TanStack Start 的设置
    ├── server.ts          服务端渲染的入口
    ├── theme.ts           样式预设和品牌色
    ├── auth.ts            会话和登录接口
    ├── routes/            文件路由
    ├── features/          按业务领域分组的页面和组件
    │   └── user/
    │       ├── queries.ts       组合多个接口的查询（单个接口的查询选项是生成的）
    │       ├── user-list-page.tsx
    │       └── user-form.tsx
    ├── components/        本前端内跨领域共用的组件
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
- 分页、排序、筛选、选中的子页签放在 search 参数中，刷新和分享链接后保持不变，见[列表](lists-forms.md#lists)。
- 登录检查放在布局路由上统一处理，见[登录](app.md#requiring-sign-in)。
- 菜单由路由的元数据（`staticData`）生成，不手写第二份路由表，见[页面的元数据](app.md#route-metadata)。
- 前端的权限判断只决定显示什么，权限名使用生成的类型；接口的授权由后端负责。

<a id="server-data"></a>
### 接口与服务端数据

- 所有请求都通过生成的客户端发出，不手写接口的类型和地址。修改接口后由 `tenon dev` 或 `tenon generate` 重新生成。
- 每个接口的查询选项和 query key 是生成的，页面和路由预加载直接使用，见 [TanStack Query](client-react.md#tanstack)。`features/<领域>/queries.ts` 只放组合多个接口的查询和乐观更新；有第二个前端用到时，移到 `packages/client`。
- 修改数据后，让相关的查询失效，不手动修改缓存，除非是有意的乐观更新。
- 接口错误用 `isApiError` 判断：校验错误按 `errors[].location` 显示在对应字段旁；业务错误按 `code` 处理；`401` 由会话统一处理，见[错误](../http/clients.md#ts-errors)。
- 列表使用服务端分页，参数是 `page`、`per_page`，见[分页](../http/responses.md#pagination)。
- 登录凭据是服务端写入的[会话 cookie](../security/api-tokens.md#browser-sessions)，前端不保管 token；当前是谁由 `@go-tenon/client` 的会话记录。

<a id="state"></a>
### 状态的归属

| 状态 | 放在哪里 | 例子 |
| --- | --- | --- |
| 可以分享的页面条件 | Router 的 search 参数 | 分页、排序、筛选 |
| 服务端数据 | TanStack Query | 列表、详情、当前用户 |
| 表单输入 | 表单库 | 未提交的编辑、校验错误 |
| 页面局部的交互 | 组件状态 | 弹窗开关、展开状态 |
| 应用共享的交互 | Context，或 `@go-tenon/ui` 的布局状态 | 侧栏折叠、标签页、主题 |

同一份数据只放在一个位置。不要把服务端数据复制到组件状态或 Context 中：复制出来的那一份不会随接口更新，两份很快就对不上了。

<a id="forms"></a>
### 表单

- 表单用 `@mantine/form`，对接后端的表单用 `@go-tenon/ui` 的 `useSchemaForm`：校验规则是生成的，规则和提示与服务端相同，见[表单](lists-forms.md#forms)。不用 zod 重写一遍规则；zod 只用于纯前端、不提交给后端的表单。
- 服务端返回的字段错误用 `applyServerErrors` 设置到对应字段上，其余错误交给 `FormError` 显示。
- 枚举的选项和显示文字使用生成的枚举，不手写，见[枚举](client-react.md#ts-enums)。
- 提交中禁用提交按钮；提交成功后关闭弹窗或跳转，并让相关查询失效。

<a id="styles"></a>
### 样式

- 主题在 `src/theme.ts` 中用 `createUiTheme` 设置：样式预设、品牌色、圆角、字体，见[样式预设](app.md#presets)。不在组件中写固定的颜色值，用 Mantine 的主题颜色（`c="dimmed"`、`color="red"`）和 CSS 变量。彩色用颜色生成器的预设名称（例如 `color="deep-orange"`），见[样式预设与主题](app.md#tokens)。
- 布局用 Mantine 的 `Stack`、`Group`、`Grid`，间距用主题的尺寸（`gap="md"`）。组件局部的样式用 `classNames`/`styles` 或 CSS Modules，不覆盖 Mantine 的内部类名。
- 亮色和暗色都要能用：用主题颜色就自动适配，自己写的 CSS 用 `light-dark()` 或 Mantine 的变量。

<a id="generated-code"></a>
### 生成的代码

| 代码 | 来源 | 是否提交 |
| --- | --- | --- |
| `packages/client/src/<名称>`：客户端、校验规则、验证提示、权限、查询选项、枚举、资源定义 | `tenon generate` | 提交 |
| `src/routeTree.gen.ts` | TanStack Router 的 Vite 插件 | 提交 |

生成的代码不要手工修改，下次生成时会被覆盖。CI 中重新生成一次，再用 `git diff --exit-code` 检查，就能发现有人改了接口却忘了提交生成的代码，见[开发工具](../installation/tooling.md#generate)。

<a id="checks"></a>
### 检查与测试

- 依赖用 Bun 安装，提交 `bun.lock`，CI 使用 `bun install --frozen-lockfile`。
- 依赖的版本精确锁定；升级依赖单独提交。
- 提交前通过 `bun run typecheck`。模板没有带 Biome，需要时自己加上。
- 纯逻辑（工具函数、状态转换）写 Vitest 单元测试；关键流程（登录、列表、表单提交）写 Playwright 测试。

<a id="agents"></a>
## 给 AI 编程助手的约束

AI 编程助手按项目中的说明写代码。说明越具体，它生成的代码越符合规范：

- **每个前端的 `AGENTS.md`**：列出本前端允许使用的库和写法，内容取自本页。
- **根目录的 `AGENTS.md`**：列出每个前端的使用者、样式预设、挂载路径和调用的 API，并写明：业务规则和权限判断写在 Go 的业务动作中，前端只负责展示和交互。
- **Biome 的 `noRestrictedImports`**：禁止[不使用的库](#not-used)，例如 antd、Tailwind。写错了，`bun run lint` 直接报错。
- **Mantine 的 LLM 文档**：Mantine 提供给 AI 编程助手读的 `llms.txt`，版本与项目的 Mantine 一致时，生成的代码用对了组件的属性。

<a id="other-platforms"></a>
## 以后增加的端

桌面端、移动端的技术还没有确定，也不限定使用 React：可能是 Flutter、Go 编写的客户端、原生（Swift、Kotlin）或 React Native。第一个需要它们的产品先写一份架构决策记录，再放进 `apps/`，例如 `apps/desktop`、`apps/mobile`。

为了让任何技术都能接入，后端现在就遵守这些约定：

1. 业务规则、权限和校验都在 Go 中，所有端都只是 API 的调用方。
2. 每个 API 的 OpenAPI 文档是各端与后端之间唯一的契约；不兼容的修改通过新的 `BasePath` 发布，见[接口文档](../http/openapi.md)。任何语言都可以从它生成客户端，`tenon generate` 已经能生成 TypeScript 和 [Go](../http/go-client.md) 的客户端。
3. 框架的约定（错误格式、分页、会话、实时频道、服务端推送与 AI 对话的消息流、上传、App 版本）写在与语言无关的[客户端协议](../http/protocol.md)中，`@go-tenon/client` 只是第一个实现。
4. 每个功能都能用 Bearer 令牌访问：浏览器用会话 cookie，其他客户端从登录接口的响应体中拿到 token，不依赖只有浏览器才有的 cookie。
5. 实时消息用 Centrifuge，它有 JavaScript、Dart、Swift、Java、Go 等官方 SDK，见[实时频道](../libraries/realtime.md)；推送见[移动推送](../features/push.md)。
6. 文案的源文件是 YAML，不绑定某种语言。
7. App 从第一个版本起发送 `X-App-Version`，见 [App 版本](../http/clients.md#app-version)。

<a id="troubleshooting"></a>
## 常见问题

**`bun run lint` 报错某个导入被禁止**

这个前端不允许使用这个库，例如导入了 antd 或 Tailwind。改用 Mantine 或 `@go-tenon/ui` 中对应的组件；确实需要时，先写架构决策记录，再修改本规范和 `biome.json`。

**两个前端要用同一个组件**

把它移到 `packages/ui`。只是逻辑（不含界面）时放在 `packages/client`。

**CI 中重新生成后有差异**

改了 Go 的接口，但没有提交重新生成的代码。在本地运行 `bun run generate`，把生成的文件一起提交。
