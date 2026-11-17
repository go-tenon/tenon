# 前端

网页用 React、[Mantine](https://mantine.dev) 和 [TanStack Start](https://tanstack.com/start) 编写：官网和内容站在服务端渲染或预渲染，运营后台和控制台在浏览器中渲染，都调用同一组 [HTTP 接口](../http/README.md)。每个前端编译成一个 SSR 程序，嵌入 Go 程序，由 Go 托管运行，部署时仍然只有一个二进制文件。

- [服务端渲染](ssr.md)：一份 Bun 运行时和每个前端的代码嵌入 Go 程序，由 ssr 包托管，页面请求经 unix socket 转给它
- [TanStack Start 的接入](start.md)：@go-tenon/start，服务端渲染与浏览器调用同一组接口、预渲染和编译
- [在 React 中调用接口](client-react.md)：TanStack Query、校验规则、权限、枚举与分页
- [应用骨架](app-shell.md)：@go-tenon/shell 的路由、登录检查、权限、菜单和新版本提示
- [应用骨架：页面中的常用功能](app-shell-features.md)：多标签、租户、表单、列表、通知、后台操作与 AI 对话
- [界面组件](ui.md)：@go-tenon/ui，Mantine 的三套样式预设（运营后台、SaaS 控制台、官网），应用外壳、登录页、错误页、官网和文档页的布局，表格、表单、资源页面和队列面板
- [前端规范](conventions.md)：组件库的选择、目录结构与编码规范
- [资源](resources.md)：在 Go 中声明后台的增删改查页面，生成资源定义，ResourcePage 按它渲染
