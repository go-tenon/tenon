// Package lock 提供带过期时间的原子锁，保证同一时刻只有一个进程或实例执行某段逻辑。
//
// Locker 在 Store 之上提供加锁、等待、续期和释放；子包 pglock 提供 PostgreSQL 存储，
// locktest 提供测试用的内存存储。
//
//	locker := lock.New(pglock.New(db))
//	err := locker.Do(ctx, "reports:daily", 10*time.Minute, func(ctx context.Context) error {
//		return reports.Generate(ctx)
//	})
package lock
