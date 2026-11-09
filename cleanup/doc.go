// Package cleanup 提供串行使用的资源清理栈。
//
// Stack 的零值可直接使用。Add 登记清理函数，Close 按登记逆序执行一次。
// 入口拥有 Stack，通过 defer 覆盖运行结束和构建失败后的资源释放。
//
//	var stack cleanup.Stack
//	defer func() { err = stack.CloseWith(context.Background(), err) }()
//
//	db, err := openDB()
//	if err != nil { return err }
//	stack.MustAdd("db", func(ctx context.Context) error { return db.Close() })
package cleanup
