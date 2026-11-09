// Package database 提供 PostgreSQL（Bun + pgx）连接装配、事务约定与错误识别。
//
// 直接暴露 *bun.DB / bun.IDB，不建立通用 Repository。迁移见 database/migrate，
// 测试隔离见 database/dbtest。第一期仅支持 PostgreSQL。
package database
