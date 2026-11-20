# 前端

网页用 React、[Mantine](https://mantine.dev) 和 [TanStack Start](https://tanstack.com/start) 编写：官网和内容站在服务端渲染或预渲染，后台类的应用在浏览器中渲染，都调用同一组 [HTTP 接口](../http/routing.md)。每个前端编译成一个 SSR 程序，嵌入 Go 程序，由 Go 托管运行，部署时仍然只有一个二进制文件。

网页用到的框架前端包有三个：`@go-tenon/client` 是与界面无关的客户端运行时（会话、错误、校验、上传），`@go-tenon/start` 把 TanStack Start 接到 Go，`@go-tenon/ui` 是应用骨架和界面组件。同一个前端还可以在桌面端中运行，这时用到 `@go-tenon/host` 和 `@go-tenon/electron`，见[客户端](../clients/README.md)。

<a id="ui-layers"></a>
## 前端包的分层

`@go-tenon/ui` 中的东西按粒度分成五层。找一个组件或函数时，先想它属于哪一层：

| 层 | 内容 | 例子 |
| --- | --- | --- |
| 主题 `theme` | 样式预设、由品牌色生成主题、图标、配色切换 | `createUiTheme`、`UIProvider`、`ColorSchemeToggle` |
| 应用骨架 `app` | 装配 Router、QueryClient 和会话，登录与权限检查，由路由生成菜单和面包屑，插槽，多标签，租户，新版本提示 | `createApp`、`requireAuth`、`Fill`、`TabsOutlet` |
| 部件 `components` | 页面中用的部件，Mantine 中没有的那些 | `DataTable`、`useSchemaForm`、`Page`、`DescriptionList`、`confirm` |
| 区块 `blocks` | 整块的界面，只描述样子：布局、登录页的布局、错误页、用户菜单 | `FrameLayout`、`RailLayout`、`SiteLayout`、`CenteredAuthLayout`、`UserMenu` |
| 功能 `features` | 框架功能的前端，接后端的模型和它的外观放在一起 | `ResourcePage`、`SettingsForm`、`SettingsPage`、`LoginForm`、`NotificationsMenu`、`QueuePanel` |

按钮、输入框、弹窗这些基础组件直接用 Mantine，图表用 [`@mantine/charts`](https://mantine.dev/charts/getting-started/)，`@go-tenon/ui` 中没有对应的一层。

上层只引用下层。区块和功能在同一层，但互相不引用：布局只描述界面的样子，不知道通知、后台任务这些功能；把它们组合起来的是你的应用。`tenon add frontend` 生成的前端就是这样一个组合：布局（`--layout`）、登录页（`--login`）、样式预设（`--preset`）和渲染方式（`--render`）各选一种，把 `UserMenu` 放进布局，把 `LoginForm` 放进登录页的布局。这几个维度互相独立，换掉其中一个不影响其他的，见 [tenon add frontend](../installation/tooling.md#add-frontend)；通知菜单这类功能由你按需放进布局的参数或插槽。

写页面时按这个顺序读：

- [搭建前端应用](app.md)：创建应用、样式预设与主题、页面的元数据、登录、权限、错误页和新版本提示
- [布局与导航](layout.md)：`FrameLayout` 和 `RailLayout` 两个布局、侧栏菜单、顶栏、多标签、租户，以及自己写布局
- [在 React 中调用接口](client-react.md)：TanStack Query、校验规则、权限、枚举与分页
- [列表与表单](lists-forms.md)：地址中的列表状态、表格、按生成的规则校验的表单、未保存的修改
- [资源与设置页面](resources.md)：在 Go 中声明增删改查和设置的页面，`ResourcePage`、`SettingsForm`、`SettingsPage` 按声明渲染
- [后台功能](features.md)：通知、后台操作、操作记录、导入导出、队列面板和 AI 对话
- [官网与文档站](site.md)：服务端渲染与预渲染、官网的布局、首页的占位和文档页

构建和运行：

- [托管前端与服务端渲染](hosting.md)：前端嵌入 Go 程序，由 frontend 包按挂载路径提供；服务端渲染的前端由 Bun 渲染，只在这时程序中才带 Bun
- [TanStack Start 的接入](start.md)：@go-tenon/start，服务端渲染与浏览器调用同一组接口、预渲染、打印模板和编译

规范：

- [前端规范](conventions.md)：组件库的选择、目录结构与编码规范
