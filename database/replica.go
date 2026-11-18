package database

import (
	"context"
	"database/sql"
	"sync/atomic"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// primaryKey 是 Primary 在 context 中的键。
type primaryKey struct{}

// Primary 返回的 ctx 中，查询都在主库执行。配置了读库时，事务之外的 SELECT 默认分到读库，
// 而读库比主库晚一点：需要读到刚写入的数据时用它，例如处理刚发布的任务、填充缓存。
// 没有配置读库时它不改变任何行为。
func Primary(ctx context.Context) context.Context {
	return context.WithValue(ctx, primaryKey{}, true)
}

// replicas 是 Open 注册的 bun.ConnResolver：事务之外的 SELECT 在读库之间轮流执行，其余语句返回 nil，由 Bun 交给主库。
// 事务中的语句已经绑定了连接，Bun 不经过它。
type replicas struct {
	dbs  []*sql.DB     // 读库的连接池，顺序与 Config.Replicas 相同
	next atomic.Uint64 // 轮询的计数
}

var _ bun.ConnResolver = (*replicas)(nil)

// ResolveConn 为只读查询选择一个读库；写操作、原始 SQL 和 Primary 的 ctx 中的查询返回 nil。
// ctx 中有 queryHook 放入的 target 时，把选中的读库记在里面。
func (r *replicas) ResolveConn(ctx context.Context, query bun.Query) bun.IConn {
	if !bun.IsReadOnlyQuery(query) || ctx.Value(primaryKey{}) != nil {
		return nil
	}
	i := int((r.next.Add(1) - 1) % uint64(len(r.dbs)))
	if t, ok := ctx.Value(targetKey{}).(*target); ok {
		t.server = i + 1
	}
	return r.dbs[i]
}

// Close 关闭全部读库的连接池，*bun.DB 关闭时调用。
func (r *replicas) Close() error {
	var errs []error
	for _, db := range r.dbs {
		errs = append(errs, db.Close())
	}
	return errors.Join(errs...)
}

// targetKey 是 target 在 context 中的键。
type targetKey struct{}

// target 记录一条语句在哪个库执行：queryHook 在执行前放进 ctx，选择器选中读库时写入，
// 执行后按它记录 server.address 等属性。
type target struct {
	server int // 0 是主库，i 是第 i 个读库
}
