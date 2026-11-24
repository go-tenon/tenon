package schedule

import (
	"context"
	"time"
)

// ClaimResult 是尝试领取一次定时触发的结果。
type ClaimResult int

const (
	// Claimed 表示当前实例推进了触发水位并取得运行租约（或任务允许重叠），应当执行任务。
	Claimed ClaimResult = iota + 1
	// AlreadyClaimed 表示该触发时刻或更晚的时刻已被其他实例领取，当前实例静默跳过。
	AlreadyClaimed
	// Overlap 表示当前实例推进了触发水位，但上一次执行尚未结束，应当记录一次重叠跳过。
	Overlap
)

// TaskState 是状态存储中一个定时任务的持久状态。
type TaskState struct {
	LastTick time.Time // 最近一次已领取的按表达式触发时刻；从未触发时为零值。
	Running  bool      // 此刻是否持有未过期的运行租约。
}

// Store 保存定时任务的单调触发水位和运行租约。每个方法都必须是原子的，
// 所有调度器实例共用同一个实现，例如 schedule/pgschedule；测试使用 schedule/scheduletest。
type Store interface {
	// Claim 原子检查任务 name 的 last_tick 是否早于 tick：若是则把 last_tick 推进到 tick，
	// 并在 allowOverlap 为 false 时尝试以 owner 取得有效期为 ttl 的运行租约。
	// 每个 tick 在整个集群中至多返回一次 Claimed 或 Overlap，其余实例返回 AlreadyClaimed。
	Claim(ctx context.Context, name string, tick time.Time, owner string, ttl time.Duration, allowOverlap bool) (ClaimResult, error)
	// AcquireRunning 用于手动触发（RunNow）：不推进 last_tick，仅在没有未过期租约时以 owner 取得有效期为 ttl 的运行租约。
	AcquireRunning(ctx context.Context, name, owner string, ttl time.Duration) (bool, error)
	// Refresh 延长 owner 持有的运行租约至当前时间之后 ttl；租约已过期或被他人接管时返回 false。
	Refresh(ctx context.Context, name, owner string, ttl time.Duration) (bool, error)
	// Release 释放 owner 持有的运行租约；租约已过期或不属于 owner 时不修改。
	Release(ctx context.Context, name, owner string) error
	// States 批量返回全部任务的触发水位和运行状态。
	States(ctx context.Context) (map[string]TaskState, error)
}
