# 前端

调用 [HTTP 接口](../http/README.md)的单页应用：用户端、运营后台和控制台。构建产物嵌入 Go 程序，部署时仍然只有一个二进制文件。

- [单页应用](spa.md)：把 React 单页应用嵌入程序并对外提供
- [服务端渲染](ssr.md)：Bun 编译的 SSR 可执行文件嵌入 Go 程序，由 ssr 包托管，页面请求经 unix socket 转给它
- [在 React 中调用接口](client-react.md)：TanStack Query、校验规则、权限、枚举与分页
- [应用骨架](app-shell.md)：@go-tenon/shell 的路由、登录检查、权限、菜单和新版本提示
- [应用骨架：页面中的常用功能](app-shell-features.md)：多标签、租户、表单、列表、通知、后台操作与 AI 对话
- [运营后台](admin.md)：@go-tenon/admin，antd 的布局、标签栏、登录页和 admin 模板
- [shadcn registry 与控制台](registry.md)：shadcn 的布局、表格、表单等组件和 console 模板
- [前端规范](conventions.md)：组件库的选择、目录结构与编码规范
- [资源](resources.md)：在 Go 中声明后台的增删改查页面，生成与组件库无关的资源定义
- [设计令牌](theme.md)：品牌只在 theme/tokens.json 中定义一次，生成公开页面、shadcn 和 antd 的主题
