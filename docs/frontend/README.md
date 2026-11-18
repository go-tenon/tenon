# 前端

网页用 React、[Mantine](https://mantine.dev) 和 [TanStack Start](https://tanstack.com/start) 编写：官网和内容站在服务端渲染或预渲染，运营后台和控制台在浏览器中渲染，都调用同一组 [HTTP 接口](../http/README.md)。每个前端编译成一个 SSR 程序，嵌入 Go 程序，由 Go 托管运行，部署时仍然只有一个二进制文件。

框架的前端包有三个：`@go-tenon/client` 是与界面无关的客户端运行时（会话、错误、校验、上传），`@go-tenon/start` 把 TanStack Start 接到 Go，`@go-tenon/ui` 是应用骨架和界面组件。

写页面时按这个顺序读：

- [搭建前端应用](app.md)：创建应用、样式预设与主题、页面的元数据、登录、权限、错误页和新版本提示
- [布局与导航](layout.md)：管理后台和 console 的布局、侧栏菜单、顶栏、多标签、租户，以及自己写布局
- [在 React 中调用接口](client-react.md)：TanStack Query、校验规则、权限、枚举与分页
- [列表与表单](lists-forms.md)：地址中的列表状态、表格、按生成的规则校验的表单、未保存的修改
- [资源与设置页面](resources.md)：在 Go 中声明增删改查和设置的页面，`ResourcePage`、`SettingsForm` 按声明渲染
- [后台功能](features.md)：通知、后台操作、操作记录、导入导出、队列面板和 AI 对话
- [官网与文档站](site.md)：服务端渲染与预渲染、官网的布局、首页的占位和文档页

构建和运行：

- [服务端渲染](ssr.md)：一份 Bun 运行时和每个前端的代码嵌入 Go 程序，由 ssr 包托管，页面请求经 unix socket 转给它
- [TanStack Start 的接入](start.md)：@go-tenon/start，服务端渲染与浏览器调用同一组接口、预渲染、打印模板和编译

规范：

- [前端规范](conventions.md)：组件库的选择、目录结构与编码规范
