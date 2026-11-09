// Package httpx 提供 net/http 生命周期辅助与通用中间件约定。
//
// Serve 运行已构造的 *http.Server 与 net.Listener，并协调排空停机；
// 不引入通用 Service 协议或多服务监督器。
package httpx
