# HTTP 接口

用 HTTP 提供 JSON API：定义路由，处理中间件，读取并验证输入，返回响应，再为前端、App 和其他服务生成客户端。业务规则写在[业务动作](../concepts/actions.md)中，这里只讲 HTTP 这一个入口。

- [路由](routing.md)：REST 路由、分组、OpenAPI 文档
- [接口文档](openapi.md)：查看和导出 OpenAPI 文档，说明与示例，API 版本
- [中间件](middleware.md)：默认中间件、编写中间件、超时与请求 ID
- [可选的中间件](optional-middleware.md)：限流、跨域、Basic 认证、App 最低版本、响应压缩
- [请求](requests.md)：路径参数、查询参数、请求体与上传
- [验证](validation.md)：输入校验规则与错误消息
- [响应](responses.md)：JSON 响应、分页、列表筛选与排序
- [服务端推送](sse.md)：Server-Sent Events
- [客户端协议](protocol.md)：请求头、错误、分页、会话、事件流等线路格式，任何语言的客户端按它实现
- [客户端代码](clients.md)：由路由生成 TypeScript 客户端并调用接口
- [Go 客户端](go-client.md)：其他 Go 服务和工具调用你的 API
