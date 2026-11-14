# 前端与多端规划

本文规划 Tenon 应用的前端：各类产品端按什么规则选组件库，多个端怎样和后端放在同一个仓库里开发、构建和部署，框架为此要提供哪些能力，以及以后加入桌面端和移动端时现在要守住哪些约定。

- [目标](#goals)
- [产品端的划分](#clients)
- [组件库选型](#ui-stacks)
- [仓库结构](#repository-layout)
- [框架提供的能力](#framework)
    - [多前端的开发与构建](#multi-frontend)
    - [前端包](#packages)
    - [运营后台的 Resource 层](#resources)
    - [访客端 widget](#widget)
    - [设计令牌](#design-tokens)
    - [面向 agent 的约束](#agents)
- [部署](#deployment)
- [桌面端与移动端](#native)
- [实施阶段](#phases)
- [待决定的问题](#open-questions)

<a id="goals"></a>
## 目标

1. 每类产品端有确定的技术栈。同一个项目中，agent 只会用到这一端规定的组件和写法。
2. 一个产品的后端和全部前端放在同一个仓库中：接口改动和前端改动在同一个 PR 中评审，生成的客户端随接口同步更新。
3. 默认部署仍然是单个二进制：所有网页端嵌入 Go 程序，与 API 同源。
4. 运营后台的增删改查以声明和生成为主，一个普通资源的前端代码在一百行左右。
5. 以后加入的桌面端和移动端，无论原生还是跨平台，都直接使用现有的 API、认证、实时消息和推送，不需要改后端的结构。

<a id="clients"></a>
## 产品端的划分

产品端按使用的人划分，与[多租户](../docs/security/tenancy.md)中的三类用户对应：

| 端 | 使用的人 | 出现的情况 | 形态 |
| --- | --- | --- | --- |
| 运营后台 | 平台的运营、客服、管理员 | 几乎每个产品都有；纯内部系统只有这一端 | 单页应用 |
| 租户控制台 | 租户的成员，例如使用 SaaS 的公司员工 | 多租户产品 | 单页应用 |
| 用户端 | 终端用户，或租户的客户 | 面向 C 端或租户客户的产品 | 单页应用；以后可能有 App |
| 嵌入端 | 第三方网站上的访客，例如客服系统的访客 | 客服、评论、表单这类嵌入其他网站的产品 | 加载脚本 + iframe 中的单页应用 |
| 公开页面 | 未登录的访客 | 官网、帮助中心、文档 | 服务端渲染（templ + shadcn-templ），已有 |
| 桌面端、移动端 | 以上任意一类用户 | 以后按产品需要加入 | 原生或跨平台，见[桌面端与移动端](#native) |

一个产品通常是"运营后台 + 若干个其他端"。客服产品是：运营后台、租户的客服工作台（租户控制台）、访客端 widget，以后可能再加客服的桌面端和移动端。

<a id="ui-stacks"></a>
## 组件库选型

React 是唯一的网页端框架。组件库有两套，按端选择：

| 端 | 组件库 | 理由 |
| --- | --- | --- |
| 运营后台 | antd + ProComponents | 数据密度高；树选择、级联、穿梭框、可编辑表格、联动表单等复杂组件齐全；ProTable、ProForm 支撑声明式的 Resource 层 |
| 租户控制台 | 默认 shadcn/ui；以大量数据录入为主时用 antd | 默认与公开页面共用主题，品牌一致；ERP、财务、仓储这类重录入的控制台用 antd，加品牌主题 |
| 用户端 | shadcn/ui | 视觉和交互可以完全定制，与公开页面共用主题 |
| 嵌入端 | shadcn/ui | 体积小、样式自有，在 iframe 中与宿主页面隔离 |

判断租户控制台用哪一套：主要是展示、流程和品牌感的，用 shadcn；主要是复杂表格和大表单的，用 antd。一个前端只用一套，不混用。

两套共用的部分：

| 层 | 选择 |
| --- | --- |
| 路由 | TanStack Router，文件路由 |
| 服务端数据 | TanStack Query + `tenon generate` 生成的客户端 |
| 构建 | Vite，构建产物嵌入 Go 程序 |
| 图表 | ECharts；shadcn 的端中常规图表也可以用 shadcn Chart |
| AI 对话 | `@ai-sdk/react` 的 `useChat`，对接 [AI 助手](../docs/ai/chat.md) 的接口 |
| 代码规范 | Biome、TypeScript `strict` |
| 测试 | Vitest、Playwright |

各自的部分：

| 层 | antd 的端 | shadcn 的端 |
| --- | --- | --- |
| 组件 | antd、ProComponents、`@ant-design/icons` | shadcn/ui（Radix）、lucide-react |
| 样式 | antd 的 token 和 `classNames`/`styles`；布局用 Flex、Space、Grid | Tailwind v4 + shadcn 主题变量 |
| 表单 | antd Form、ProForm | react-hook-form + zod |
| 表格 | ProTable；超大数据量用 AG Grid | TanStack Table + TanStack Virtual |
| AI 对话界面 | Ant Design X | AI Elements |
| 提示 | antd 的 message、notification | sonner |

专项需求统一一个选择：富文本用 Tiptap，拖拽用 dnd-kit，流程图用 xyflow，代码编辑用 CodeMirror 6，动效用 motion（只在 shadcn 的端）。

不引入其他组件库（MUI、Chakra、Mantine、HeroUI 等）和其他样式方案（CSS-in-JS、styled-components）。

<a id="repository-layout"></a>
## 仓库结构

Go 模块和 Bun workspace 在同一个仓库的根目录：

```text
helpdesk/
├── go.mod                 ignore ./node_modules、./apps/*/node_modules
├── package.json           Bun workspace：apps/*、packages/*；dev、build、generate 脚本
├── bun.lock
├── tenon.json             前端和 API 的清单
├── AGENTS.md              全局约定：各端的用户、技术栈、路径、调用的 API
├── .mcp.json              antd CLI MCP、shadcn MCP
├── cmd/api/main.go
├── internal/              业务代码
├── config/
├── openapi/               每个 API 的 OpenAPI 文档，提交到仓库
├── theme/tokens.json      设计令牌，见下文
├── apps/
│   ├── admin/             运营后台（antd），挂在 /admin/
│   │   ├── AGENTS.md
│   │   ├── frontend.go    go:embed dist
│   │   ├── index.html
│   │   ├── vite.config.ts
│   │   └── src/
│   ├── console/           租户控制台（shadcn），挂在 /
│   └── widget/            访客端：loader（widget.js）+ iframe 中的单页应用，挂在 /widget/
└── packages/
    ├── api-admin/         生成的 /api/admin 客户端
    ├── api-v1/            生成的 /api/v1 客户端
    ├── api-widget/        生成的 /api/widget 客户端
    ├── ui/                shadcn 的端共用的组件（shadcn monorepo 结构）
    └── theme/             由 theme/tokens.json 生成的 shadcn 变量和 antd 主题
```

约定：

- 每个前端是 `apps/` 下的一个目录，有自己的 `package.json`、`vite.config.ts` 和 `AGENTS.md`，在 `frontend.go` 中嵌入自己的 `dist`。
- 每个 API（`rest.New` 的一个 `BasePath`）生成一个客户端包。前端只依赖自己调用的 API 的包：访客端的构建产物中不会出现运营后台的接口。
- `go.mod` 用 `ignore` 指令排除 `node_modules`，`go build ./...` 和 `go test ./...` 不会进入其中。
- 只有一个前端的项目继续使用现在的 `frontend/` 结构，`tenon.json` 的 `frontend` 字段保持兼容。

<a id="framework"></a>
## 框架提供的能力

<a id="multi-frontend"></a>
### 多前端的开发与构建

`tenon.json` 列出前端和 API：

```json
{
  "apis": [
    { "base": "/api/admin", "ts": "packages/api-admin" },
    { "base": "/api/v1", "ts": "packages/api-v1", "go": "../helpdesk-go-client" },
    { "base": "/api/widget", "ts": "packages/api-widget" }
  ],
  "frontends": [
    { "name": "admin", "dir": "apps/admin", "base": "/admin/" },
    { "name": "console", "dir": "apps/console", "base": "/" },
    { "name": "widget", "dir": "apps/widget", "base": "/widget/" }
  ]
}
```

| 命令 | 多前端时的行为 |
| --- | --- |
| `tenon dev` | 为每个前端启动一个 Vite，端口自动分配；把各个 Vite 的地址传给应用，应用按前端的 `name` 转发页面请求，不再手写 `frontend_dev_server`。修改 Go 代码后按 `apis` 重新生成全部客户端 |
| `tenon build` | 依次构建全部前端，再编译 Go 程序 |
| `tenon generate` | 按 `apis` 逐个生成客户端，同时把 OpenAPI 文档写到 `openapi/`。`client --base-path` 已经支持按 `BasePath` 选择 API |
| `tenon init` | `--frontends admin,console` 选择初始的端，每个端使用对应的模板 |
| `tenon add frontend <name> --template admin\|console\|app\|widget` | 给已有项目加一个端，更新 `tenon.json` 和 workspace |

应用这一侧，每个前端挂一个 `httpx.SPA`：

```go
router.Handle("/admin/*", httpx.NewSPA(admin.Dist, httpx.SPAOptions{Base: "/admin/", DevServer: dev.Frontend("admin")}))
router.Handle("/widget/*", httpx.NewSPA(widget.Dist, httpx.SPAOptions{Base: "/widget/", FrameAncestors: widgetOrigins}))
router.Handle("/*", httpx.NewSPA(console.Dist, httpx.SPAOptions{DevServer: dev.Frontend("console")}))
```

框架需要补充：

- 开发时取各前端 Vite 地址的方式（上面的 `dev.Frontend`）。
- `SPAOptions.FrameAncestors` 支持按请求计算，嵌入端按租户登记的域名放行。

<a id="packages"></a>
### 前端包

框架发布三个前端的部分，版本号与框架一致，`tenon init` 固定为同一个版本：

| 包 | 内容 | 依赖 |
| --- | --- | --- |
| `@go-tenon/web` | 与组件库无关的核心：创建生成的客户端、令牌与会话刷新（对接 `apitoken` 的 cookie 和请求体两种方式）、接口错误的统一类型与 401 处理、`errors[].location` 到表单字段的映射、`page`/`per_page` 分页参数与表格状态的转换、新版本提示、实时频道和后台操作进度的 hooks | React、TanStack Query；核心逻辑不依赖 DOM，存储和跳转由调用方注入 |
| `@go-tenon/admin` | 运营后台：ProLayout 后台框架、菜单与权限、多标签页与页面保活、登录页、错误边界、Resource 层，以及 `@go-tenon/web` 在 antd Form 和 ProTable 上的适配 | `@go-tenon/web`、antd、ProComponents、TanStack Router |
| shadcn registry `@tenon` | shadcn 的端用的组件，按 shadcn 的方式复制进项目：数据表格、接口表单（react-hook-form 适配）、错误提示、通知铃铛、后台操作进度、对话界面（基于 AI Elements） | `@go-tenon/web` |

来源：

- `@go-tenon/admin` 由 shellphy-kit `starter-umi-ant` 的 `@shellphy/web-kit` 迁入。保留后台框架、多标签页与保活、登录与权限、新版本提示和它们的测试；去掉 TanStack Start、Nitro 和 Mock 层，改为 Vite 单页应用；http 模块换成 Tenon 生成的客户端。
- `@go-tenon/web` 合并两个 starter 中重复的会话、错误和新版本提示代码。

这些包的源码放在 Tenon 仓库的 `js/` 目录（Bun workspace），与 Go 代码在同一个 PR 中修改，发布到 npm。

<a id="resources"></a>
### 运营后台的 Resource 层

运营后台的大部分页面是增删改查。一个资源用一个文件声明：

```tsx
export const articles = defineResource({
  name: "article",
  title: "文章",
  api: api.article,
  columns: [
    { dataIndex: "title", title: "标题", search: true },
    { dataIndex: "status", title: "状态", valueType: "select", valueEnum: articleStatus },
    { dataIndex: "updated_at", title: "更新时间", valueType: "dateTime", sorter: true },
  ],
  form: [
    { name: "title", label: "标题", rules: [{ required: true }] },
    { name: "content", label: "正文", valueType: "textarea" },
  ],
  actions: [publishAction],
})
```

`defineResource` 生成列表、详情、新建和编辑页面与路由：

- 列表用 ProTable，查询条件、排序和分页同步到 URL，`request` 调用生成的客户端。
- 新建和编辑用 DrawerForm 或 ModalForm，服务端返回的校验错误显示在对应字段旁。
- 删除、批量操作和自定义操作带确认，完成后让列表的查询失效。
- 按 `authz` 的权限隐藏菜单和按钮。

`tenon make:resource article --api /api/admin` 从 OpenAPI 读出列表的响应和创建、更新的请求体，生成这个文件：字段类型决定 `valueType`，枚举生成选项。agent 在生成的文件上补充业务操作和自定义列。

复杂的页面不用 Resource 层，直接用 antd 和 ProComponents 编写。

<a id="widget"></a>
### 访客端 widget

嵌入端分成两部分，都在 `apps/widget` 中构建：

| 部分 | 内容 | 地址 |
| --- | --- | --- |
| loader | 原生 TypeScript 写的小脚本，打包为一个文件。在宿主页面上显示入口按钮、创建 iframe、通过 `postMessage` 传递配置和页面信息、显示未读数 | `/widget.js`，短缓存 |
| 单页应用 | React + shadcn，运行在 iframe 中，与宿主页面的样式和脚本隔离 | `/widget/` |

约定：

- iframe 中第三方 cookie 不可用，访客令牌使用请求体方式，保存在 iframe 自己的存储中。
- 宿主站点已登录的用户，由宿主的后端用租户密钥对用户 ID 签名，widget 携带签名换取访客令牌，防止冒充他人。
- `/widget/` 的 `frame-ancestors` 按租户登记的域名放行；`/api/widget` 只开放 widget 用到的接口和 CORS。
- 消息用[实时频道](../docs/libraries/realtime.md)，AI 回答用[服务端推送](../docs/responses/sse.md)。

同样的结构也用于其他需要嵌入第三方页面的场景。

<a id="design-tokens"></a>
### 设计令牌

品牌相关的设计决定只写一次，放在 `theme/tokens.json`，采用 W3C Design Tokens（DTCG）格式。范围限定为：品牌色、中性色、语义色（成功、警告、错误）、圆角、字体、密度。

`tenon generate` 从它生成：

| 产物 | 使用者 |
| --- | --- |
| `packages/theme/shadcn.css` | shadcn 的端 |
| `packages/theme/antd.ts`（`ThemeConfig`） | antd 的端，经 `ConfigProvider` 使用 |
| `web/ui` 的 `vars`、`dark_vars` | 公开页面 |
| Flutter、React Native、原生平台的主题文件 | 以后的桌面端和移动端，需要时增加生成器 |

运营后台默认只取品牌色和圆角，其余保持 antd 的紧凑风格。

<a id="agents"></a>
### 面向 agent 的约束

规则写在项目里，并且用工具强制执行：

- 根目录的 `AGENTS.md` 列出每个端的用户、技术栈、挂载路径和调用的 API，并规定：业务规则和权限判断写在 Go 的业务动作中，前端只负责展示；一个端只调用自己的 API。
- 每个端的 `AGENTS.md` 列出允许使用的库和这一端的写法（Resource 层、表单、表格、样式）。
- Biome 的 `noRestrictedImports` 按端限制依赖：antd 的端禁止 Radix、Tailwind 相关的包；shadcn 的端禁止 antd。
- `.mcp.json` 配置 antd CLI 的 MCP（版本与项目的 antd 一致）和 shadcn 的 MCP；`.agents/skills/` 提供 antd 的 skill 和 Tenon 前端约定的 skill。
- 生成的目录（`packages/api-*`、`routeTree.gen.ts`、`packages/theme`）在 `AGENTS.md` 中标明禁止手工修改，CI 重新生成后检查差异。

<a id="deployment"></a>
## 部署

默认部署是一个二进制，所有网页端按路径挂载，与 API 同源：

| 路径 | 内容 |
| --- | --- |
| `/admin/` | 运营后台 |
| `/api/admin` | 运营后台的 API |
| `/`、`/api/v1` | 租户控制台或用户端，以及它的 API |
| `/widget.js`、`/widget/`、`/api/widget` | 嵌入端 |
| 其他路径 | 公开页面 |

需要时可以调整：

- **按域名区分**：`admin.example.com`、`app.example.com` 指向同一个二进制，路由按 Host 挂载不同的前端。同站点的子域名之间按[单页应用的登录](../docs/security/api-tokens.md#single-page-applications)配置 cookie 的域和 CORS。
- **运营后台只对内网开放**：运营后台和它的 API 挂在单独的监听地址上，由内网的负载均衡访问。框架需要支持一个程序提供多个监听地址。
- **前端单独部署到 CDN**：构建产物照常生成，API 开启 CORS。不作为默认方式。

<a id="native"></a>
## 桌面端与移动端

桌面端和移动端的技术在第一个需要它们的产品中决定。可选的方案：

| 方案 | 复用的部分 | 适合 |
| --- | --- | --- |
| Tauri 2 | 网页端的 React 代码和全部前端包 | 桌面端主要是"网页端加上托盘、通知、本地文件" |
| Wails 3 | 网页端的 React 代码；Go 写的本地逻辑和 Go 客户端 | 同上，且本地逻辑多、团队希望用 Go 写；选用前确认已发布稳定版本 |
| React Native（Expo） | 生成的 TS 客户端、`@go-tenon/web` 的核心、TanStack Query；界面重写 | 移动端的默认选择，与网页端共用 TypeScript |
| Flutter | 从 OpenAPI 生成 Dart 客户端，Centrifuge 有 Dart SDK；界面重写 | 对动画和跨端一致性要求高，团队有 Dart 经验 |
| 原生（Swift、Kotlin） | 从 OpenAPI 生成客户端，Centrifuge 有 Swift、Java SDK | 对平台体验要求最高的端 |
| gomobile | 把 Go 写的核心逻辑编译成库，供原生界面调用 | 只在大量离线计算、加密等逻辑需要跨端共享时使用 |

Electron 体积大，不作为选项。

为了让以后的任何选择都不需要改后端，现在遵守这些约定：

1. 业务规则、权限和校验都在 Go 的业务动作中，所有端都只是 API 的调用方。
2. 每个 API 的 OpenAPI 文档提交到仓库，CI [检查不兼容的变更](../docs/responses/clients.md#checking-changes)；不兼容的修改通过新的 `BasePath`（如 `/api/v2`）发布。
3. `@go-tenon/web` 的核心和生成的客户端不依赖 DOM 和浏览器 API，React Native、Tauri、Wails 中可以直接使用。
4. App 使用 Bearer 令牌，刷新令牌放在请求体中；从第一个版本起发送 [`X-App-Version`](../docs/responses/clients.md#app-version)，并处理 `426`。
5. 实时消息用 Centrifuge（各平台都有官方 SDK），推送用 [移动推送](../docs/features/push.md)，文件用预签名地址直传。
6. 品牌和主题只在 `theme/tokens.json` 中定义。

桌面端和移动端放在同一个仓库中：

| 端 | 位置 |
| --- | --- |
| React Native | `apps/mobile`，属于 Bun workspace |
| Tauri | `apps/desktop`，前端部分属于 Bun workspace |
| Wails 3 | `apps/desktop`，前端部分属于 Bun workspace；Go 部分是独立的 module，本地用 `go.work` 关联 |
| Flutter、原生 | `apps/mobile-flutter`、`apps/ios`、`apps/android`，使用各自的工具链，客户端从 `openapi/` 生成 |

CI 按路径只运行受影响的端的检查。

<a id="phases"></a>
## 实施阶段

每个阶段拆成若干 PR，每个 PR 同时更新框架文档。

### 阶段 1：多前端工程化

- `tenon.json` 增加 `apis`、`frontends`，`frontend` 保持兼容。
- `tenon dev`、`build`、`generate` 支持多个前端和多个 API；应用开发时按前端名取 Vite 地址。
- `tenon init --frontends`、`tenon add frontend`；生成的项目使用 `apps/`、`packages/` 结构和 `go.mod` 的 `ignore` 指令。

验收：生成一个包含 admin 和 console 的项目，两个前端同时热更新；构建出一个二进制，两个前端的深链接刷新都正常；修改接口后两个客户端包都重新生成。

### 阶段 2：前端包和两套模板

- 建立 `js/` workspace 和发布流程。
- `@go-tenon/web`：会话、错误、表单错误映射、分页转换、新版本提示。
- `@go-tenon/admin`：从 `@shellphy/web-kit` 迁入后台框架、多标签页与保活、登录与权限，保留其单元测试和 E2E。
- shadcn registry `@tenon` 的第一批组件。
- `admin`、`console` 两个模板，带 `AGENTS.md`、`.mcp.json`、Biome 限制。

验收：从发布的包生成的新项目，在仓库之外安装、类型检查、构建和 E2E 全部通过。

### 阶段 3：Resource 层

- `defineResource` 和 `tenon make:resource`。
- 用它重写用户和角色管理的示例，对比迁移前每个资源约 590 行的代码量。

验收：普通资源的前端代码在一百行左右；生成后不修改即可运行。

### 阶段 4：嵌入端、AI 对话和实时

- `widget` 模板：loader、iframe 单页应用、访客令牌与签名校验。
- `SPAOptions.FrameAncestors` 按请求计算。
- 两套对话界面（Ant Design X、AI Elements）对接 `ai/chat`；实时频道和后台操作进度的 hooks。

### 阶段 5：设计令牌

- `theme/tokens.json` 的格式和生成器，输出 shadcn 变量、antd 主题和 `web/ui` 的主题。

### 阶段 6：第一个桌面端或移动端

在第一个需要的产品中进行。先写一份决策记录，对照[桌面端与移动端](#native)的方案做出选择，再做试点；需要的框架改动（例如 Dart 客户端生成、新的令牌生成器）在这时加入。

<a id="open-questions"></a>
## 待决定的问题

1. 运营后台是否默认开启多标签页与页面保活。
2. 默认按路径还是按域名部署多个端。
3. 前端包发布到公共 npm（`@go-tenon`）还是私有仓库。
4. `@shellphy/web-kit` 迁入 Tenon 仓库后，shellphy-kit 的 starter 是否保留。
5. ProComponents 3.x 仍是预发布版本：锁定版本，正式版发布后单独升级验证。
6. 微信内的用户端 H5 先用 shadcn 的响应式布局；需要原生手感（ActionSheet、Picker、下拉刷新）的产品是否引入 antd-mobile。
