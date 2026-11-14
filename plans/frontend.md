# 前端规范

本文规定 Tenon 应用的前端技术选型、目录结构和编码规范。一个产品的后端和全部前端放在同一个仓库中，每个前端按它的使用场景选择组件库。

- [技术选型](#stack)
    - [按端选择组件库](#ui-library)
    - [共用的技术栈](#shared-stack)
    - [两套组件库各自的写法](#per-library)
    - [不使用的库](#not-used)
- [目录结构](#layout)
    - [仓库](#repository)
    - [分层规则](#layering)
    - [架构决策记录](#adr)
    - [一个前端的内部](#frontend-layout)
    - [命名](#naming)
- [编码规范](#conventions)
    - [路由](#routing)
    - [接口与服务端数据](#data)
    - [状态的归属](#state)
    - [表单](#forms)
    - [样式](#styling)
    - [生成的代码](#generated-code)
    - [工具链、检查与测试](#tooling)
    - [agent 的约束](#agents)
- [以后增加的端](#future-clients)

<a id="stack"></a>
## 技术选型

<a id="ui-library"></a>
### 按端选择组件库

| 端 | 组件库 |
| --- | --- |
| 运营后台、纯内部系统 | antd + ProComponents |
| 面向客户的后台（如租户控制台）、用户端 | shadcn/ui |
| 面向客户、以大量数据录入为主的后台（如 ERP、财务、仓储） | antd + ProComponents，使用品牌主题 |
| 嵌入第三方页面的端 | shadcn/ui |
| 公开页面（官网、帮助中心） | 服务端渲染，templ + shadcn-templ，见[界面组件](../docs/responses/components.md) |

判断面向客户的后台用哪一套：以展示、流程和品牌感为主的用 shadcn；以复杂表格和大表单为主的用 antd。一个前端只使用一套组件库。

<a id="shared-stack"></a>
### 共用的技术栈

| 层 | 选择 |
| --- | --- |
| 语言 | TypeScript，开启 `strict` |
| 框架 | React |
| 构建 | Vite，构建产物嵌入 Go 程序，见[单页应用](../docs/responses/spa.md) |
| 路由 | TanStack Router，文件路由 |
| 服务端数据 | TanStack Query + `tenon generate` 生成的客户端 |
| 框架套件 | `@go-tenon/client`、`@go-tenon/shell`；antd 的端加 `@go-tenon/admin`，shadcn 的端用 registry `@tenon`，见[前端套件规划](frontend-kit.md) |
| 图表 | ECharts |
| AI 对话 | `@ai-sdk/react` 的 `useChat`，对接 [AI 助手](../docs/ai/chat.md) |
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
| 组件 | antd、ProComponents | shadcn/ui（Radix），源码在项目中 |
| 图标 | `@ant-design/icons` | lucide-react |
| 样式 | antd 的 token、`classNames`/`styles`；布局用 Flex、Space、Grid | Tailwind v4 + shadcn 主题变量 |
| 表单 | antd Form、ProForm | react-hook-form + zod |
| 表格 | ProTable；超大数据量用 AG Grid | TanStack Table + TanStack Virtual |
| 后台框架 | ProLayout | shadcn 的 Sidebar |
| 提示 | antd 的 message、notification | sonner |
| AI 对话界面 | Ant Design X | AI Elements |
| 图表 | ECharts | 常规图表用 shadcn Chart，复杂图表用 ECharts |
| 动效 | 不使用 | motion |
| agent 文档 | antd CLI 的 MCP，版本与项目的 antd 一致 | shadcn 的 MCP |

<a id="not-used"></a>
### 不使用的库

- 其他组件库：MUI、Chakra、Mantine、HeroUI 等。
- 其他样式方案：CSS-in-JS、styled-components、Sass/Less。antd 的端可以用少量 CSS Modules。
- 其他数据请求方式：在组件中用 `useEffect` 请求数据、手写 `fetch`、axios、其他 OpenAPI 生成器。
- 其他状态库：Redux、Zustand、Jotai。应用级的共享状态用 React Context。
- 其他路由：React Router。

需要引入表外的库时，先更新本文。

<a id="layout"></a>
## 目录结构

<a id="repository"></a>
### 仓库

仓库分三部分：根目录是 Go 后端，`apps/` 是各个端，`packages/` 是多个端共用的 TypeScript 包。只有一个前端的项目用 `frontend/` 目录，结构与 `tenon init` 生成的一致；有多个前端时用下面的结构：

```text
helpdesk/
├── go.mod                 用 ignore 指令排除 node_modules
├── cmd/                   后端：Tenon 约定的目录
├── internal/
├── config/
├── package.json           Bun workspace（apps/*、packages/*）；dev、build、generate 脚本
├── bun.lock
├── biome.json
├── tenon.json
├── AGENTS.md              各个端的用户、技术栈、挂载路径和调用的 API
├── .mcp.json
├── apps/                  每个端一个目录
│   ├── admin/             运营后台（antd）
│   ├── console/           租户控制台（shadcn）
│   └── widget/            访客端（shadcn）
├── packages/              多个端共用的包
│   ├── client/            生成的客户端和不依赖 DOM 的共用逻辑
│   ├── ui/                多个 shadcn 端共用的组件
│   ├── tokens/            颜色、圆角、字体等设计值
│   └── i18n/              前端文案
└── docs/
    └── adr/               架构决策记录
```

- 每个端是 `apps/` 下的一个目录。网页端有自己的 `package.json`、`vite.config.ts`、`AGENTS.md`，以及嵌入自己 `dist` 的 `frontend.go`。
- `packages/` 中的包在有两个以上的端使用时才建立。`client` 从第一个前端起就有；`ui` 在第二个 shadcn 端出现时从前端中抽出；`tokens`、`i18n` 在需要跨端统一设计值或支持多语言时建立。
- 框架工具（`tenon.json`、`tenon dev`、`tenon build`、`tenon generate`）需要支持多个前端和多个 API，这部分单独开 PR 实现。

`packages/client` 的结构：

```text
packages/client/src/
├── api/
│   ├── admin/             生成的 /api/admin 客户端
│   └── v1/                生成的 /api/v1 客户端
├── session.ts             令牌的保存与刷新，存储由调用方传入
├── errors.ts              接口错误的类型与判断
├── pagination.ts          page、per_page 与表格状态的转换
└── index.ts
```

每个 API（`rest.New` 的一个 `BasePath`）生成到 `api/<名称>`，通过子路径导出（`@helpdesk/client/api/admin`）。一个端只导入自己调用的 API。

<a id="layering"></a>
### 分层规则

| 层 | 可以依赖 | 不可以依赖 |
| --- | --- | --- |
| `apps/*` | `packages/*` | 其他 `apps/*` |
| `packages/ui` | `packages/tokens`、`packages/i18n` | `packages/client`、任何 `apps/*` |
| `packages/client` | React、TanStack Query、`packages/i18n` | DOM 与浏览器 API（`window`、`document`、`localStorage`）、`react-dom`、`packages/ui`、任何组件库 |
| `packages/tokens`、`packages/i18n` | 无 | 其他任何包 |

平台相关的能力（存储、跳转、WebSocket 的创建）由端在创建时传给 `client`。规则用 Biome 的 `noRestrictedImports`、`noRestrictedGlobals` 和一个检查导入关系的测试强制执行。

<a id="adr"></a>
### 架构决策记录

影响多个端或难以撤回的决定写在 `docs/adr/NNNN-<标题>.md` 中：背景、决定、后果、状态。新的决定推翻旧的决定时，旧记录标注被哪一份取代，不删除。引入表外的库、增加一个端、选择桌面端或移动端的技术，都先写一份决策记录。

<a id="frontend-layout"></a>
### 一个前端的内部

```text
apps/admin/
├── AGENTS.md
├── frontend.go            go:embed dist
├── index.html
├── package.json
├── vite.config.ts
├── tsconfig.json
├── public/
├── tests/
│   ├── unit/
│   └── e2e/
└── src/
    ├── main.tsx           创建 Router 和 QueryClient，挂载应用
    ├── app/               应用级装配：Provider、客户端、认证、菜单、布局
    ├── routes/            文件路由
    ├── features/          按业务领域分组的页面和组件
    │   └── user/
    │       ├── queries.ts       这个领域的 queryOptions 和 mutation
    │       ├── user-list-page.tsx
    │       └── user-form.tsx
    ├── components/        本前端内跨领域共用的组件；shadcn 的组件在 components/ui
    ├── lib/               与界面无关的工具函数
    ├── styles/
    └── routeTree.gen.ts   生成的路由树
```

<a id="naming"></a>
### 命名

| 对象 | 规则 | 例子 |
| --- | --- | --- |
| 前端目录 | 按使用的人命名，小写 | `admin`、`console`、`widget` |
| 前端的包名 | `@<项目名>/<前端目录>` | `@helpdesk/admin` |
| 共用包 | `@<项目名>/<包目录>` | `@helpdesk/client`、`@helpdesk/ui` |
| 文件 | kebab-case | `user-list-page.tsx` |
| 组件 | PascalCase，一个文件导出一个页面级组件 | `UserListPage` |
| hooks | `use` 开头的 camelCase | `useCurrentUser` |
| 业务领域目录 | 与 Go 的业务包同名，单数 | `features/article` ↔ `internal/article` |

<a id="conventions"></a>
## 编码规范

<a id="routing"></a>
### 路由

- 路由文件只负责：路径与 search 参数的校验、权限要求、数据预加载、加载与错误状态。页面组件放在 `features/` 中。
- 分页、排序、筛选、选中的子页签放在 search 参数中，刷新和分享链接后保持不变。
- 登录检查放在布局路由上统一处理。前端的权限判断只用于显示，接口的授权由后端负责。
- 菜单由路由的元数据生成，不手写第二份路由表。

<a id="data"></a>
### 接口与服务端数据

- 所有请求都通过 `@<项目名>/client` 中生成的客户端发出，不手写接口的类型和地址。修改接口后由 `tenon dev` 或 `tenon generate` 重新生成。
- 每个领域的 query key 和请求函数集中在 `features/<领域>/queries.ts`，用 `queryOptions` 定义，页面和路由预加载复用同一份。有第二个端调用同一组接口时，把它移到 `packages/client`。
- 修改数据后，让相关的查询失效，不手动修改缓存，除非是有意的乐观更新。
- 接口错误用生成客户端的 `isApiError` 判断：校验错误按 `errors[].location` 显示在对应字段旁；业务错误按 `code` 处理；401 统一跳转登录。
- 列表使用服务端分页，参数为 `page`、`per_page`，见[分页](../docs/responses/responses.md#pagination)。
- 认证按[单页应用的登录](../docs/security/api-tokens.md#single-page-applications)实现，令牌的保存和刷新集中在 `app/` 中。

<a id="state"></a>
### 状态的归属

| 状态 | 位置 | 例子 |
| --- | --- | --- |
| 可分享的页面条件 | Router 的 search 参数 | 分页、排序、筛选 |
| 服务端数据 | TanStack Query | 列表、详情、当前用户 |
| 表单输入 | 表单库 | 未提交的编辑、校验错误 |
| 页面局部交互 | 组件状态 | 弹窗开关、展开状态 |
| 应用共享交互 | `app/` 中的 Context | 侧栏折叠、标签页、主题 |

同一份数据只放在一个位置。不把服务端数据复制到组件状态或 Context 中。

<a id="forms"></a>
### 表单

- antd 的端用 antd Form 或 ProForm，shadcn 的端用 react-hook-form + zod。一个前端只有一种表单写法。
- 前端校验只用于即时提示，以服务端的校验结果为准。服务端返回的字段错误设置到对应字段上。
- 提交中禁用提交按钮；提交成功后关闭弹窗或跳转，并让相关查询失效。

<a id="styling"></a>
### 样式

- antd 的端：主题通过 `ConfigProvider` 的 token 设置，组件局部样式用 v6 的 `classNames`/`styles`，布局用 antd 的布局组件。不覆盖 antd 的内部类名。
- shadcn 的端：只用 Tailwind 工具类和 shadcn 的主题变量（`bg-primary`、`text-muted-foreground` 等），不写固定的颜色值。主题变量与公开页面的 `web/ui` 使用同一套值。
- 修改 shadcn 组件时直接改 `components/ui` 中的源码，不在外面再包一层。

<a id="generated-code"></a>
### 生成的代码

| 代码 | 来源 | 是否提交 |
| --- | --- | --- |
| `packages/client/src/api/*`（或单前端的 `src/api`） | `tenon generate` | 提交，CI 重新生成后检查差异 |
| `routeTree.gen.ts` | TanStack Router 的 Vite 插件 | 不提交，开发和构建时生成 |
| `components/ui` | shadcn CLI 添加，之后由项目维护 | 提交 |

前两项禁止手工修改。

<a id="tooling"></a>
### 工具链、检查与测试

- 依赖用 Bun 安装，提交 `bun.lock`，CI 使用 `--frozen-lockfile`。
- 依赖版本精确锁定；升级依赖单独提交。
- 提交前通过 `biome check` 和 `tsc --noEmit`。
- 纯逻辑（工具函数、状态转换）写 Vitest 单元测试；关键流程（登录、列表、表单提交）写 Playwright 测试。

<a id="agents"></a>
### agent 的约束

- 根目录的 `AGENTS.md` 列出每个前端的使用者、组件库、挂载路径和调用的 API，并规定：业务规则和权限判断写在 Go 的业务动作中，前端只负责展示和交互。
- 每个前端的 `AGENTS.md` 列出本前端允许使用的库和写法，内容取自本文。
- Biome 的 `noRestrictedImports` 按前端限制依赖：antd 的前端禁止 shadcn 相关的包和 Tailwind，shadcn 的前端禁止 antd。
- `.mcp.json` 配置 antd CLI 的 MCP 和 shadcn 的 MCP。

<a id="future-clients"></a>
## 以后增加的端

桌面端、移动端的技术尚未确定，在第一个需要它们的产品中写决策记录选择。无论选择什么，它们都放在 `apps/` 中（如 `apps/desktop`、`apps/mobile`），并遵守：

1. 业务规则、权限和校验都在 Go 中，所有端都只是 API 的调用方。
2. 每个 API 的 OpenAPI 文档提交到仓库，不兼容的修改通过新的 `BasePath` 发布。
3. 共用逻辑，不共用界面。TypeScript 的端（如 React Native、Tauri）直接使用 `packages/client`、`tokens`、`i18n`；其他语言的端（如 Flutter、原生、Wails 中的 Go）从 OpenAPI 生成自己的客户端，设计值从 `packages/tokens` 生成。
4. App 从第一个版本起发送 [`X-App-Version`](../docs/responses/clients.md#app-version)。
