// Package cleanup 提供小型资源清理栈。
//
// Stack 的零值可直接使用：Add 登记具名清理函数，Close 按登记逆序执行一次。
// 它不管理应用、依赖解析或服务运行，也不支持关闭过程中继续登记或并发关闭。
//
//	var stack cleanup.Stack
//	defer func() { err = stack.CloseWith(context.Background(), err) }()
//
//	db, err := openDB()
//	if err != nil { return err }
//	stack.Add("db", func(ctx context.Context) error { return db.Close() })
package cleanup
