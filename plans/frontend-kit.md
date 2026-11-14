# 前端套件规划

Tenon 是 Go 后端框架，同时提供一套官方的客户端套件：对接框架约定的客户端运行时、应用骨架、两种外观（antd 和 shadcn）以及平台适配层。本文规划套件的分层、各个包的内容与分发方式，以及从已有实验项目中复用的代码。产品仓库怎样使用这些包，见[前端规范](frontend.md)。

- [封装的范围](#scope)
- [包与依赖关系](#packages)
- [@go-tenon/client](#client)
- [@go-tenon/shell](#shell)
- [@go-tenon/admin](#admin)
- [shadcn registry](#registry)
- [平台适配层](#platform)
- [嵌入第三方页面](#embed)
- [分发与版本](#distribution)
- [复用来源](#sources)
- [实施顺序](#order)

<a id="scope"></a>
## 封装的范围

满足下面任一条件的部分由框架封装：

- 后端的约定改变时它要跟着改，例如错误格式、分页、令牌刷新、实时频道。
- 每个产品都需要，而且逻辑复杂、出了问题应当一次修复，例如布局、多标签、页面保活、平台适配。

| 层 | 例子 | 是否封装 |
| --- | --- | --- |
| 基础组件 | Button、Input、Table、Modal | 不封装，直接用 antd 或 shadcn |
| 客户端运行时 | 错误、会话、分页、实时、上传、App 版本 | 封装 |
| 应用骨架 | 布局、侧栏、顶栏、多标签与保活、用户菜单、守卫、错误页、新版本提示 | 封装 |
| 框架功能的界面 | 通知、后台操作进度、AI 对话、设置、导入导出、操作日志 | 封装 |
| 平台适配层 | 安全存储、系统通知、托盘、窗口、深链接、文件对话框、客户端更新 | 封装 |
| 业务组件 | 各产品自己的 | 不封装 |

<a id="packages"></a>
## 包与依赖关系

| 包 | 内容 | 分发方式 |
| --- | --- | --- |
| `@go-tenon/client` | 客户端运行时，不依赖 DOM | npm |
| `@go-tenon/shell` | 应用骨架的逻辑，与组件库无关；平台适配的接口和网页实现 | npm |
| `@go-tenon/admin` | antd 外观的骨架和框架功能界面、Resource 层 | npm |
| shadcn registry `@tenon` | shadcn 外观的布局、骨架和框架功能界面 | 复制进项目 |
| `@go-tenon/platform-<平台>` | 平台适配层在桌面端、移动端技术上的实现 | npm，选定技术后建立 |
| `tenon generate` | 客户端代码生成；以后增加其他语言 | Go，已有 |

```text
@go-tenon/admin ──┐
                  ├──▶ @go-tenon/shell ──▶ @go-tenon/client
registry 组件 ────┘           ▲
@go-tenon/platform-* ─────────┘（实现 shell 中的平台接口）
```

- `client` 不依赖 DOM、`react-dom` 和任何组件库，React Native 中也能使用；可以依赖 React 和 TanStack Query，hooks 在各平台通用。
- `shell` 依赖 React、TanStack Router 和 `client`，不依赖组件库。
- `admin` 和 registry 只负责外观，复杂的逻辑都在 `shell` 中。registry 的组件复制进项目后，外观归产品所有，逻辑仍然随 `shell` 升级。

<a id="client"></a>
## @go-tenon/client

| 模块 | 内容 |
| --- | --- |
| 客户端创建 | 为 `tenon generate` 生成的代码提供运行时：基础地址、请求头、取消、`fetch` 注入 |
| 错误 | 接口错误的类型和判断；`errors[].location` 到字段路径的转换；401、403、426、网络错误的分类 |
| 会话 | 对接 `apitoken` 的两种方式（refresh cookie、请求体）；并发请求只刷新一次；存储由调用方传入 |
| 身份切换 | 会话代次：退出、切换账号或租户后，旧请求的结果不再交付，相关查询缓存清除 |
| 查询约定 | query key 的作用域（部署、账号、租户）；列表查询、分页查询、无限滚动的 hooks |
| 分页 | `page`/`per_page`/`total` 与表格状态、search 参数之间的转换 |
| 实时 | Centrifuge 连接（WebSocket 的创建由调用方传入）、频道订阅 hooks；SSE 事件流 |
| 后台操作 | 订阅 `operation` 的进度 |
| AI 对话 | 对接 `ai/chat` 的 `useChat` 配置：提交、恢复、停止、插话、审批 |
| 上传 | 预签名直传和分片上传的流程 |
| App 版本 | 发送 `X-App-Version`，识别 `426` |

<a id="shell"></a>
## @go-tenon/shell

| 模块 | 内容 |
| --- | --- |
| 应用装配 | 创建 Router、QueryClient，组合 Provider；按 `appId` 隔离本地存储的命名 |
| 守卫与权限 | 布局路由上的登录检查、登录后安全回跳、按权限过滤菜单和路由 |
| 菜单 | 由路由的元数据（标题、图标、排序、权限、是否进入标签）生成菜单和面包屑 |
| 多标签与保活 | 标签状态机、标签身份（路由 + 路径参数）、保活容器、每个标签的路由视图、LRU 淘汰、固定、关闭规则、键盘操作 |
| 未保存的修改 | 草稿登记；关闭标签、离开页面、刷新、退出前统一确认 |
| 布局偏好 | 侧栏折叠、多标签开关、主题，按用户保存 |
| 租户 | 当前租户的上下文、切换租户、切换后清理缓存和标签 |
| 新版本提示 | 构建时写入版本号和 `version.json` 的 Vite 插件；运行时检测；桌面端改为客户端更新 |
| 平台 | 平台识别（网页、桌面、移动）、平台能力的接口和网页实现，见[平台适配层](#platform) |
| 多平台入口 | 一个前端按平台加载不同的入口（网页、桌面、移动），共用 `features` |

<a id="admin"></a>
## @go-tenon/admin

| 模块 | 内容 |
| --- | --- |
| 布局 | 基于 ProLayout，side、top、mix 三种模式；顶栏、页脚、面包屑 |
| 标签栏 | `shell` 多标签的 antd 外观：拖拽排序、右键菜单、最大化、刷新 |
| 页面 | 登录页、403、404、错误边界 |
| 用户菜单 | 头像下拉、主题切换、退出 |
| 适配 | 服务端字段错误设置到 antd Form；ProTable 的 `request` 对接分页和排序 |
| Resource 层 | `defineResource`：一个资源一个声明，生成列表、详情、新建、编辑和操作；配套 `tenon make:resource` |
| 框架功能界面 | 通知、后台操作进度、AI 对话（Ant Design X）、设置、导入导出、操作日志 |
| 主题 | 默认的紧凑主题，品牌色和圆角由产品传入 |

<a id="registry"></a>
## shadcn registry

registry 中的组件用 `shadcn add @tenon/<名称>` 加入项目。

| 分组 | 组件 |
| --- | --- |
| 布局 | 可折叠侧栏、内嵌式侧栏、图标栏 + 二级面板、顶部导航 |
| 骨架 | 用户菜单、设置菜单、租户切换、前进后退（桌面端）、标签栏、⌘K 命令面板 |
| 页面 | 页头、页面加载与错误状态、列表加详情的可拖动分栏、右侧抽屉面板 |
| 列表 | 资源表格（服务端分页、筛选同步到 URL、保留滚动位置）、列表工具栏、行操作（右键与「⋯」共用一份菜单）、状态标签 |
| 表单 | react-hook-form + zod 的字段组件，服务端错误映射，自动保存与未保存提示 |
| 对话框 | 确认对话框（危险操作）、编辑对话框 |
| 桌面端 | 窗口控制按钮、应用菜单 |
| 框架功能界面 | 通知铃铛、后台操作进度、AI 对话（AI Elements）、消息 Markdown 渲染 |

<a id="platform"></a>
## 平台适配层

页面和骨架不直接调用浏览器或原生 API，统一通过 `shell` 的平台接口：

| 能力 | 网页实现 | 原生实现 |
| --- | --- | --- |
| 凭据 | cookie 或 localStorage | 系统钥匙串 |
| 通知 | Web Notification | 系统通知；点击通知后打开对应页面 |
| 未读提示 | 页面标题 | 托盘、Dock 角标 |
| 外部链接 | `window.open` | 系统浏览器 |
| 文件 | `<input type=file>`、浏览器下载 | 系统的打开、保存对话框 |
| 窗口 | 无 | 标题栏、窗口控制、独立窗口 |
| 菜单 | 无 | 应用菜单、托盘菜单 |
| 深链接 | URL | 自定义协议唤起，转给已运行的实例 |
| 更新 | 新版本提示后刷新 | 下载更新包、校验签名、重启 |
| 语言 | 页面语言 | 同步到托盘、菜单和原生错误文案 |

桌面端和移动端的技术尚未确定。luway 已经用 Wails v3 在 macOS、Windows、Linux、iOS、Android 上实现了上表的原生部分：Go 侧是 `internal/native`，前端是 `src/platform`。如果选择 Wails，原生部分抽成 Tenon 的 Go 包和 `@go-tenon/platform-wails`；选择其他技术时，按同一接口另写实现。Wails v3 目前仍是 beta 版本，选用前写决策记录。

<a id="embed"></a>
## 嵌入第三方页面

客服访客端这类嵌入其他网站的端，由三部分组成：

| 部分 | 内容 |
| --- | --- |
| loader | 原生 TypeScript 的小脚本：入口按钮、创建 iframe、未读数 |
| 宿主桥接协议 | iframe 与宿主页之间的 `postMessage` 协议：锁定来源，消息有类型（就绪、关闭、未读、提示；上下文、可见性、事件、关闭） |
| iframe 中的页面 | 用 shadcn registry 的组件编写 |

loader 和桥接协议两端的类型由框架提供（`@go-tenon/embed`），在第一个需要嵌入的产品中建立。

<a id="distribution"></a>
## 分发与版本

```text
tenon/
├── (Go 包)
├── cmd/tenon/
└── js/                    Bun workspace，go.mod 用 ignore 排除
    ├── client/
    ├── shell/
    ├── admin/
    └── registry/          registry 的源码，构建为 registry 的 JSON
```

- 打一个版本标签，Go 模块、npm 包和 registry 一起发布，版本号相同。`tenon init` 写入同一个版本，`tenon dev` 检查版本是否一致。
- CI 增加 Bun 的检查与测试。修改 Go 侧的约定时，同一个 PR 中修改 JS 侧。
- 发布前在仓库之外的空目录安装发布产物、生成新项目，通过类型检查、构建和 E2E。

<a id="sources"></a>
## 复用来源

| 项目 | 复用 | 去向 | 处理 |
| --- | --- | --- | --- |
| shellphy-kit/starter-umi-ant（`@shellphy/web-kit`） | 多标签、保活、草稿登记、标签栏、后台布局、错误边界、认证与权限、菜单、新版本提示和 Vite 版本插件，及其单元测试和 E2E | `shell`、`admin` | 逻辑与外观拆开；去掉 TanStack Start、Nitro、Mock；http 换成 `client` |
| | 发布产物的独立安装验证、生成项目的验证脚本 | `js/` 的发布流程 | 改为验证 `tenon init` 生成的项目 |
| shellphy-kit/starter-tanstack-heroui | 令牌保存与会话刷新、新版本提示条 | `client`、`shell` | 与 web-kit 的同类代码合并；HeroUI 外观不复用 |
| runforyou-ai/luway-cloud | 平台识别与平台能力（`src/platform`）、多平台入口 | `shell` | 原样迁入接口与网页实现 |
| | Wails 原生能力（`internal/native`：通知、托盘、独立窗口、深链接、单实例、签名的客户端更新） | Tenon 的 Go 包、`platform-wails` | 选定 Wails 后迁入 |
| | 数据 hooks（`use-resource`、`resource-keys` 的作用域、`use-session-generation`、`use-list-search-params`） | `client` | 去掉对 react-router 的依赖 |
| | 页面组件（`page-header`、`page-split`、`resource-table`、`row-actions-menu`、`confirmation-dialog`、`unsaved-changes-guard`、`list-toolbar`、`status-badge`）、表单错误处理（`form-errors`、`use-form-save`） | registry | 改为 TanStack Router；逻辑部分移到 `shell` |
| | 工作区布局（`workspace-layout`、`workspace-rail`、`workspace-user-menu`、`workspace-settings-menu`、`workspace-history-nav`、`workspace-gate`） | registry 的"图标栏 + 二级面板"布局和骨架组件 | 去掉业务内容，保留结构 |
| | 桌面端组件（`desktop-window-controls`、`desktop-menu`） | registry | 通过平台接口调用 |
| | 访客端的 loader（`internal/web/messenger/widget.js`） | `@go-tenon/embed` | 与 helmdesk 的桥接协议合并 |
| | 前端约定（feature 单向依赖由 Biome 校验、表单与管理页的交互规范） | [前端规范](frontend.md)、registry 组件的默认行为 | 整理后写入 |
| helmdesk-ai/helmdesk-saas | 访客端 iframe 与宿主页的桥接协议（`useWidgetHostBridge`）、渠道预览的桥接 | `@go-tenon/embed` | 前端是 Vue，只复用协议和消息类型 |

不复用：TanStack Start 与 Nitro、Mock 层、hey-api 生成的客户端、HeroUI、react-router、Vue 代码。

<a id="order"></a>
## 实施顺序

1. `js/` workspace 和发布流程；`@go-tenon/client`。
2. `@go-tenon/shell`：从 web-kit 迁入多标签与保活、守卫与菜单、新版本提示；从 luway 迁入平台识别和平台接口。
3. `@go-tenon/admin` 和 admin 模板。
4. registry 的第一批组件（布局、页面、列表、表单、对话框）和 console 模板。
5. Resource 层和 `tenon make:resource`。
6. 平台实现和 `@go-tenon/embed`，在第一个需要的产品中进行。
