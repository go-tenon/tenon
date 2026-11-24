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

// primaryTrackerKey 是 TrackPrimary 在 context 中的键，存放 *atomic.Bool。
type primaryTrackerKey struct{}

// Primary 返回的 ctx 中，查询都在主库执行。配置了读库时，事务之外的 SELECT 默认分到读库，
// 而读库比主库晚一点：需要读到刚写入的数据时用它，例如处理刚发布的任务、填充缓存。
// 没有配置读库时它不改变任何行为。
func Primary(ctx context.Context) context.Context {
	return context.WithValue(ctx, primaryKey{}, true)
}

// TrackPrimary 返回带写入追踪标记的 ctx：当同一个 ctx 内发生过 RunInTx 提交或主库写入后，
// 后续的只读查询会自动路由到主库，无需在写后每处手动包裹 Primary。通常由 HTTP 中间件在请求入口安装。
func TrackPrimary(ctx context.Context) context.Context {
	if _, ok := ctx.Value(primaryTrackerKey{}).(*atomic.Bool); ok {
		return ctx
	}
	return context.WithValue(ctx, primaryTrackerKey{}, &atomic.Bool{})
}

// markPrimary 把 ctx 中的写入追踪标记置为 true，让后续查询走主库。
func markPrimary(ctx context.Context) {
	if flag, ok := ctx.Value(primaryTrackerKey{}).(*atomic.Bool); ok {
		flag.Store(true)
	}
}

// usePrimary 报告 ctx 是否强制使用主库（通过 Primary 或已触发 TrackPrimary）。
func usePrimary(ctx context.Context) bool {
	if ctx.Value(primaryKey{}) != nil {
		return true
	}
	if flag, ok := ctx.Value(primaryTrackerKey{}).(*atomic.Bool); ok && flag.Load() {
		return true
	}
	return false
}

// replicas 是 Open 注册的 bun.ConnResolver：事务之外的 SELECT 在读库之间轮流执行，其余语句返回 nil，由 Bun 交给主库。
// 事务中的语句已经绑定了连接，Bun 不经过它。
type replicas struct {
	dbs  []*sql.DB     // 读库的连接池，顺序与 Config.Replicas 相同
	next atomic.Uint64 // 轮询的计数
}

var _ bun.ConnResolver = (*replicas)(nil)

// ResolveConn 为只读查询选择一个读库；写操作、原始 SQL 和已标记走主库的 ctx 中的查询返回 nil。
// 写操作自动触发 ctx 中的 TrackPrimary 标记，保证写后同请求内的读不再命中存在复制延迟的读库。
// ctx 中有 queryHook 放入的 target 时，把选中的读库记在里面。
func (r *replicas) ResolveConn(ctx context.Context, query bun.Query) bun.IConn {
	if !bun.IsReadOnlyQuery(query) {
		markPrimary(ctx)
		return nil
	}
	if usePrimary(ctx) {
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
