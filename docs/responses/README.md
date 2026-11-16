# 构建响应

向客户端返回结果：JSON 接口、生成的客户端、单页应用、服务端推送和服务端渲染页面。

- [响应](responses.md)：JSON 响应、分页、列表筛选与排序
- [客户端代码](clients.md)：由路由生成 Go 与 TypeScript 客户端
- [单页应用](spa.md)：把 React 单页应用嵌入程序并对外提供
- [应用骨架](app-shell.md)：@go-tenon/shell 的路由、登录检查、权限、菜单和新版本提示
- [资源](resources.md)：在 Go 中声明后台的增删改查页面，生成与组件库无关的资源定义
- [shadcn registry 与控制台](registry.md)
- [嵌入第三方网站](embed.md)：客服挂件这类放进客户网站的端：挂件脚本、iframe 与宿主之间的协议、宿主签名的用户身份：shadcn 的布局、表格、表单等组件和 console 模板
- [运营后台](admin.md)：@go-tenon/admin，antd 的布局、标签栏、登录页和 admin 模板
- [设计令牌](theme.md)：品牌只在 theme/tokens.json 中定义一次，生成公开页面、shadcn 和 antd 的主题
- [服务端推送](sse.md)：Server-Sent Events
- [视图](views.md)：用 templ 渲染公开页面
- [表单](forms.md)：服务端渲染页面中的表单
- [前端资源](assets.md)：CSS、JavaScript 与静态文件
- [界面组件](components.md)：web/ui 提供的组件
- [文档页](doc-pages.md)：把 Markdown 目录发布为帮助中心
