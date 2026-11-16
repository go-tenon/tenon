package lock_test

import (
	"context"
	"time"

	"github.com/go-tenon/tenon/lock"
)

// prefixed 给锁名加上前缀，让每次契约测试使用独立的锁名，多次测试可以共用同一个存储。
type prefixed struct {
	lock.Store
	prefix string
}

func (p prefixed) Acquire(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	return p.Store.Acquire(ctx, p.prefix+name, owner, ttl)
}

func (p prefixed) Refresh(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	return p.Store.Refresh(ctx, p.prefix+name, owner, ttl)
}

func (p prefixed) Release(ctx context.Context, name, owner string) (bool, error) {
	return p.Store.Release(ctx, p.prefix+name, owner)
}

func (p prefixed) Held(ctx context.Context, name string) (bool, error) {
	return p.Store.Held(ctx, p.prefix+name)
}
