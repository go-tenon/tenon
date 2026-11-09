// Package cacheaside 演示直接使用 eko/gocache（内存 / Redis）的装配与约定。
//
// 本目录不是 mygoravel/cache 门面：调用方直接持有上游 Cache/Store 与 Redis 客户端，
// 自行决定键命名空间、TTL、错误策略与 Close 所有权。集成测试覆盖 cache-aside、
// miss/故障区分、提交后失效与两应用键空间隔离。
package cacheaside
