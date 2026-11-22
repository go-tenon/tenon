# 前端规范

Tenon 应用网页前端的技术选型、目录结构和编码规范。`tenon init`、`tenon add frontend` 生成的前端按这一页组织，项目 `AGENTS.md` 中前端的约定和 `tenon-frontend` 技能取自这一页；它们与 TanStack、Mantine 的官方文档或技能冲突时，以这一页为准。

包：`@go-tenon/client`、`@go-tenon/start`、`@go-tenon/ui`。

业务规则、权限判断和校验写在 Go 的接口中，前端只负责展示和交互：网页、桌面端和 App 调用同一组接口，规则只有一份。

<a id="tech-stack"></a>
<a id="shared-stack"></a>
## 技术栈

| 用途 | 选择 |
| --- | --- |
| 语言、框架 | TypeScript（`strict`）、React、TanStack Start，构建后嵌入 Go 程序，见[托管前端](hosting.md) |
| 路由 | TanStack Router 的文件路由 |
| 服务端数据 | TanStack Query 加生成的客户端，见[在 React 中调用接口](client-react.md) |
| 组件、表格 | Mantine、`@go-tenon/ui`；表格用 `DataTable` |
| 表单 | `@mantine/form`；对接接口的表单用 `useSchemaForm` |
| 图标 | `@tabler/icons-react` |
| 提示与确认 | `@go-tenon/ui` 的 `notifySuccess`、`notifyError`、`confirm` |
| 图表 | `@mantine/charts` |
| AI 对话 | `@ai-sdk/react` 的 `useChat`，见 [AI 助手](../ai/chat.md) |
| 富文本、拖拽、流程图、代码编辑 | Tiptap、dnd-kit、xyflow、CodeMirror 6 |
| 包管理、检查、测试 | Bun；Biome（模板没有带，需要时加上）；Vitest、Playwright |

<a id="choosing-ui-library"></a>
## 按界面选择布局、样式预设和渲染方式

| 界面 | `tenon add frontend` 的选项 | 默认的预设和渲染 |
| --- | --- | --- |
| 模块切换为主（SaaS 控制台、用户中心） | `--layout rail`（默认） | `comfortable`，浏览器中渲染 |
| 表格和大表单多（运营后台、ERP） | `--layout frame` | `compact`，浏览器中渲染 |
| 公开页面（官网、内容站、帮助中心） | `--layout site` | `spacious`，服务端渲染 |

登录页（`--login`）、样式预设（`--preset`）和渲染方式（`--render`）各自独立，可以与任何布局搭配，见 [tenon add frontend](../installation/tooling.md#add-frontend)。

<a id="not-used"></a>
## 不使用的库

- 其他组件库：antd、MUI、Chakra、shadcn/ui、HeroUI。
- 其他样式方案：Tailwind、CSS-in-JS、styled-components、Sass/Less。Mantine 的样式属性不够用时用 CSS Modules。
- 其他请求方式：在 `useEffect` 中请求数据、手写 `fetch`、axios、其他 OpenAPI 生成器。
- 其他状态库：Redux、Zustand、Jotai。应用级的共享状态用 React Context。
- React Router；TanStack Start 的服务端函数（数据都来自 Go 的接口）。

需要表外的库时先写一份[架构决策记录](#adr)。要强制检查时，给前端加上 Biome 的 `noRestrictedImports`。

<a id="ui-layers"></a>
## `@go-tenon/ui` 的分层

`@go-tenon/ui` 分成六层，上层只引用下层。找组件或函数时先确定它在哪一层：

| 层 | 内容 | 例子 |
| --- | --- | --- |
| 界面文字 `i18n` | 组件内置的文字，取文字的钩子，见[界面文字](i18n.md) | `useT`、`useLanguage`、`uiMessages` |
| 主题 `theme` | 样式预设、由品牌色生成主题、图标、配色切换 | `createUiTheme`、`UIProvider`、`ColorSchemeToggle` |
| 应用骨架 `app` | 装配 Router、QueryClient 和会话，登录与权限检查，菜单，插槽，多标签，租户，新版本提示 | `createApp`、`requireAuth`、`Fill`、`TabsOutlet` |
| 部件 `components` | Mantine 没有的部件 | `DataTable`、`useSchemaForm`、`Page`、`DescriptionList`、`confirm` |
| 区块 `blocks` | 只描述样子的整块界面：布局、登录页的布局、错误页、用户菜单 | `FrameLayout`、`RailLayout`、`SiteLayout`、`CenteredAuthLayout`、`UserMenu` |
| 功能 `features` | 框架功能的前端，模型和外观在一起 | `ResourcePage`、`SettingsPage`、`LoginForm`、`NotificationsMenu`、`QueuePanel` |

基础组件直接用 Mantine。区块和功能互相不引用：布局不绑定身份、权限或租户，由应用把功能放进布局的参数或插槽，例如把 `LoginForm` 放进登录页的布局，见[布局与导航](layout.md)。

<a id="layout"></a>
<a id="repository"></a>
## 仓库的目录结构

`tenon init` 生成的项目只有 `apps/web`；用 `tenon add frontend` 加入更多前端后：

```text
helpdesk/
├── cmd/  internal/  config/   Go 后端，见“目录结构”一页
├── package.json           Bun workspace；dev、build、generate、typecheck 脚本
├── apps/                  网页前端，每个一个目录
│   ├── apps.go            把构建结果 apps/dist 嵌入程序
│   ├── admin/             运营后台（--layout frame）
│   └── web/               官网和用户端（--layout site）
├── packages/
│   ├── client/            生成的客户端
│   └── ui/                多个前端共用的业务组件（需要时才建）
├── clients/               桌面端（tenon add client）
└── docs/adr/              架构决策记录
```

- 每个前端的 `package.json` 用 `tenon` 字段写挂载路径和是否服务端渲染。
- 每个 API（`rest.New` 的一个 `BasePath`）的客户端生成到 `packages/client/src/<名称>`，前端用 `@<项目名>/client/<名称>` 导入，只导入自己调用的 API，见[前端与 API 怎样被发现](../installation/tooling.md#conventions)。后端的目录见[目录结构](../concepts/structure.md)。

<a id="layering"></a>
## 前端之间的依赖规则

| 目录 | 可以依赖 | 不可以依赖 |
| --- | --- | --- |
| `apps/*` | `packages/*` | 其他 `apps/*` |
| `packages/ui` | `@go-tenon/ui`、Mantine | `packages/client`、`apps/*` |
| `packages/client` | React、TanStack Query | DOM 和浏览器 API、`react-dom`、`packages/ui`、组件库 |

两个前端用到同一个组件时移到 `packages/ui`，与界面无关的逻辑放在 `packages/client`。`packages/client` 不依赖 DOM，React Native 等客户端可以复用它。

<a id="adr"></a>
## 架构决策记录

影响多个前端或难以撤回的决定（引入表外的库、增加前端、选择桌面端或 App 的技术）写在 `docs/adr/NNNN-<标题>.md` 中：背景、决定、后果、状态。被推翻的记录标注被哪一份取代，不删除。

<a id="inside-a-frontend"></a>
## 一个前端的目录结构

生成的前端有 `router.tsx`、`start.ts`、`server.ts`、`theme.ts`、`i18n.ts`、`messages.gen.ts` 和 `routes/`，有 API 时加上 `api.ts`，有登录页时加上 `auth.ts`。页面多了以后：

```text
apps/admin/src/
├── router.tsx         getRouter：createApp 创建应用
├── start.ts、server.ts  TanStack Start 的设置和服务端渲染的入口
├── theme.ts           样式预设和品牌色
├── i18n.ts            界面文字的翻译 i18n，见界面文字
├── api.ts             生成的客户端 api 和元数据 kit
├── auth.ts            会话、登录、退出
├── routes/            文件路由，只写路由的事
├── features/          按业务领域分组的页面和组件
│   └── user/
│       ├── queries.ts       组合多个接口的查询
│       ├── user-list-page.tsx
│       └── user-form.tsx
├── components/        本前端内跨领域共用的组件
├── lib/               与界面无关的工具函数
├── messages.gen.ts    生成的界面文字目录
└── routeTree.gen.ts   生成的路由树
```

<a id="naming"></a>
## 命名

| 对象 | 规则 | 例子 |
| --- | --- | --- |
| 前端目录 | 按使用的人命名；小写字母、数字和连字符 | `admin`、`web` |
| 包名 | `@<项目名>/<目录>` | `@helpdesk/admin`、`@helpdesk/ui` |
| 文件 | kebab-case | `user-list-page.tsx` |
| 组件 | PascalCase，一个文件一个页面级组件 | `UserListPage` |
| hooks | `use` 开头的 camelCase | `useCurrentUser` |
| 业务领域目录 | 与 Go 的业务包同名，单数 | `features/article` 对应 `internal/article` |

<a id="coding"></a>
<a id="routes"></a>
## 路由

- 路由文件只写 search 参数的校验、权限要求、数据预加载、加载和错误状态，页面组件放在 `features/` 中。
- 分页、排序、筛选、子页签放在 search 参数中，刷新和分享链接后不变，见[列表](lists-forms.md#lists)。
- 登录检查放在布局路由上，见[登录](app.md#requiring-sign-in)；菜单由路由的 `staticData` 生成，见[页面的元数据](app.md#route-metadata)。
- 前端的权限判断只决定显示什么，权限名用生成的 `Permission` 类型。

<a id="server-data"></a>
## 接口与服务端数据

- 请求都通过生成的客户端发出，不手写接口的类型和地址。
- 页面和路由预加载用生成的查询选项，见 [TanStack Query](client-react.md#tanstack)；`features/<领域>/queries.ts` 只放组合多个接口的查询和乐观更新。
- 修改数据后让相关的查询失效；除了乐观更新，不手动改缓存。
- 错误用 `describeError` 分类，见[处理错误](client-react.md#describe-error)；列表用服务端分页，见[分页列表](client-react.md#pagination)。
- 网页的登录凭据是 HttpOnly 的[会话 cookie](../security/api-tokens.md#browser-sessions)，前端不保管 token。

<a id="state"></a>
## 状态放在哪里

| 状态 | 放在哪里 |
| --- | --- |
| 分页、排序、筛选这类可以分享的条件 | Router 的 search 参数 |
| 服务端数据 | TanStack Query |
| 未提交的表单输入 | `@mantine/form` |
| 弹窗开关等页面局部的交互 | 组件状态 |
| 侧栏折叠、标签页、主题 | Context，或 `@go-tenon/ui` 的布局状态 |

服务端数据不复制到组件状态或 Context 中：复制出的那一份不随接口更新。

<a id="forms"></a>
## 表单

- 对接接口的表单用 `useSchemaForm`，校验规则和提示是生成的，与服务端相同，见[表单](lists-forms.md#forms)。zod 只用于不提交给后端的表单。
- 服务端返回的字段错误用 `applyServerErrors` 设置到字段上，其余错误交给 `FormError`。
- 枚举的选项和显示文字用生成的枚举，见[枚举](client-react.md#ts-enums)。
- 提交中禁用提交按钮；成功后关闭弹窗或跳转，并让相关查询失效。

<a id="styles"></a>
## 样式与颜色

- 主题在 `src/theme.ts` 中用 `createUiTheme` 设置样式预设和品牌色，见[样式预设与主题](app.md#tokens)。
- 品牌色是 `brand`，组件不写 `color` 时用它。其他彩色写预设的名称：`color="deep-orange"`、`var(--mantine-color-deep-orange-6)`；浅色底用 `-light` 变体。Mantine 自带的 `teal`、`orange`、`blue`、`cyan`、`grape`、`indigo`、`lime` 不在预设中，不使用。
- 状态的颜色：成功 `green`，进行中 `bright-blue`，等待处理 `yellow`，需要注意 `deep-orange`，失败 `red`，没有状态 `gray`。
- 中性色用 `gray`、`dark` 色板、`c="dimmed"` 和 `--tn-*` 变量（`--tn-text`、`--tn-border` 等）。
- 不写十六进制或 `rgb()` 的颜色：写死的值不随品牌色和暗色模式变化。半透明用 `color-mix(in srgb, var(--mantine-color-violet-7) 50%, transparent)`。
- 局部样式用 `classNames`、`styles` 或 CSS Modules，不覆盖 Mantine 的内部类名。

<a id="generated-code"></a>
## 生成的代码

`packages/client/src/<名称>`（`tenon generate` 生成）和 `src/routeTree.gen.ts`（TanStack Router 的 Vite 插件生成）都提交，不手改，下次生成时被覆盖。CI 中重新生成再用 `git diff --exit-code` 检查：改了接口没有提交生成的代码时失败，在本地运行 `bun run generate` 后一起提交，见 [tenon generate](../installation/tooling.md#generate)。

<a id="checks"></a>
## 检查与测试

- 提交 `bun.lock`，CI 用 `bun install --frozen-lockfile`；升级依赖单独提交。
- 提交前通过 `bun run typecheck`。
- 纯逻辑写 Vitest 单元测试；登录、列表、表单提交这类关键流程写 Playwright 测试。
- 测试中 `UIProvider` 传 `env="test"`，关闭过渡动画和 portal。

<a id="agents"></a>
## 给 AI 编程助手的约束

项目的 `AGENTS.md`、`tenon-frontend` 技能和 `tenon/docs/frontend/conventions.md`（这一页，与项目的框架版本一致）都由 `tenon init`、`tenon agents` 写好。Mantine、TanStack 的官方文档和技能按[依赖的文档](../installation/ai-assistants.md#dependencies)接入。

<a id="other-platforms"></a>
## 桌面端和 App

桌面端用 `tenon add client` 生成，放在 `clients/` 中，见[客户端](../clients/overview.md)。其他技术（Flutter、原生、React Native）写的客户端先写一份架构决策记录；各端的契约是每个 API 的 [OpenAPI 文档](../http/openapi.md)和[客户端协议](../http/protocol.md)。
