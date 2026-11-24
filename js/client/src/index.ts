// @go-tenon/client 是对接 Tenon 约定的客户端运行时，建立在 tenon generate 生成的客户端之上，按客户端协议实现：
// 会话、界面文字的翻译、错误分类、校验、分页、查询缓存、上传、后台操作、AI 对话，以及原生客户端在系统浏览器中登录（authorize）。
// 网页中的登录凭据是服务端写入的 HttpOnly 会话 cookie，运行时不接触它。它不依赖任何包、DOM 和 Node：
// tsconfig 的 lib 只有 es2022，网页、Electron 的主进程、React Native 都能使用。
export * from "./authorize"
export * from "./chat"
export * from "./errors"
export * from "./fetch"
export * from "./i18n"
export * from "./operation"
export * from "./pagination"
export * from "./permissions"
export * from "./queries"
export * from "./request-store"
export * from "./session"
export * from "./upload"
export * from "./validate"
