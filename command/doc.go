// Package command 用 Cobra 构造应用的命令行。
// Root 创建根命令，应用用 AddCommand 显式装配需要的命令，也可以加入任意 Cobra 命令。
// Serve 构造运行常驻进程的 serve 命令：应用把 HTTP 服务、队列 worker 等登记为 Part，
// serve 运行全部部分，serve <part> 只运行一个部分。Bind 声明需要应用资源的一次性命令，
// 资源推迟到命令执行时才打开；Run 构造只需要配置的命令。
// Daemon 取得监听端口、报告就绪，在 systemd 中支持不停机升级；Concurrently 同时运行多个函数。
package command
