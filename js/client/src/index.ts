// @go-tenon/client 是对接 Tenon 约定的客户端运行时，建立在 tenon generate 生成的客户端之上，按客户端协议实现：
// 会话、错误分类、校验、分页和查询缓存。它不依赖任何包和 DOM：tsconfig 的 lib 中没有 dom，网页、React Native、
// 桌面端都能使用，存储这类平台能力由调用方传入。
export * from "./errors"
export * from "./pagination"
export * from "./queries"
export * from "./session"
export * from "./validate"
