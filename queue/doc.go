// Package queue 是基于 NATS JetStream 的异步任务队列，任务和业务数据在同一个事务中发布。
//
// 发布：Topic.Publish 把任务写入 PostgreSQL 的 queue_outbox 表。传入事务时任务随事务提交，回滚时一起取消。
// 延迟任务（Delay、At）保存在同一张表中，到期后由 Worker 批量转发到 JetStream 并从表中删除；
// 多个 Worker 通过 FOR UPDATE SKIP LOCKED 并行转发，互不冲突。表由 Migrations 创建。
//
// 处理：Worker 消费已注册 topic 的任务，交给处理函数；同一个应用的 Worker 共用每个 topic 的 consumer，每个任务只处理一次。
// 处理失败的任务由 JetStream 原生按退避时间延迟重投；次数用完后转入 queue_failed_jobs。
// NotFound、InvalidArgument 这类业务错误重试也不会成功，直接转入，见 IsPermanent。
//
// Topic 把 topic 名称和 payload 类型声明在一起，发布和处理都通过它：
//
//	var SendMail = queue.Topic[mail.Message]("blog.mail.send")
//
//	// 发布
//	err := SendMail.Publish(ctx, tx, msg)
//
//	// 处理：nc 是 natsx.Connect 返回的连接，与广播等组件共用
//	w := queue.NewWorker(db, nc, cfg, logger)
//	SendMail.Handle(w, mailer.Send)
//	err := w.Run(ctx)
//
// 每个任务至少处理一次：进程崩溃、租约过期或转发后提交失败时任务会被重新处理，处理函数需要能安全地重复执行。
// 任务之间不保证顺序。
package queue
