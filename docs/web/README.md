# 服务端页面

官网、文章页、帮助中心这类公开页面，由服务端直接生成 HTML：用 templ 写页面，htmx 处理局部更新。需要登录的界面通常是[单页应用](../frontend/README.md)。

- [视图](views.md)：用 templ 渲染公开页面
- [表单](forms.md)：服务端渲染页面中的表单
- [前端资源](assets.md)：CSS、JavaScript 与静态文件
- [界面组件](components.md)：web/ui 提供的组件
- [文档页](doc-pages.md)：把 Markdown 目录发布为帮助中心
