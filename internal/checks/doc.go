// Package checks 下的包检查整个模块：archtest 检查分层和依赖方向，
// migrationstest 检查各个包内嵌的迁移，clocktest 检查 SQL 中的时间都由 Go 传入，
// doctest 检查文档中的示例和文档推荐的社区组件用法。
// 它们只有测试，普通的 go test 就会运行。
package checks
