---
name: tenon-background
description: Tenon 应用的后台任务：用 queue.Topic 声明队列任务、在业务事务中发布、在 worker 中处理、失败与重试；用 schedule 登记定时任务。把耗时的工作（发邮件、生成报表、调用外部服务）移到后台，或者每天、每小时执行某件事时使用。
---

# 后台任务

队列的任务保存在 NATS JetStream 中，经 PostgreSQL 的发件箱发布，所以任务和业务数据在同一个事务中提交。定时任务由调度器按 cron 表达式执行。先读 `tenon/docs/background/queue.md` 或 `tenon/docs/background/scheduling.md`。

## 队列任务

1. **准备**：NATS 2.15+ 开启 JetStream；发布框架的队列迁移 `queue.Migrations()`（创建 `queue_outbox` 和 `queue_failed_jobs` 表）。见 `tenon/docs/background/queue.md#preparing`。
2. **声明 topic**：`var PublishAt = queue.Topic[uuid.UUID]("articles.publish")`，名称和 payload 类型绑在一起，发布方和处理方共用这一个声明，放在业务动作的文件中。见 `tenon/docs/background/queue.md#publishing-jobs`。
3. **在事务中发布**：`PublishAt.Publish(ctx, tx, item.ID)`，与业务数据一起提交；事务回滚时任务不存在。延迟执行用 `queue.At(t)`，见 `tenon/docs/background/queue.md#delayed-jobs`。
4. **处理**：业务动作的 `Job(ctx, payload) error` 方法，以系统身份调用 `Handle`，见 `tenon/docs/concepts/actions.md#as-jobs`。
5. **登记**：模块的 `jobs.go` 中 `func RegisterJobs(w *queue.Worker, db *bun.DB) { PublishAt.Handle(w, NewPublish(db).Job) }`，worker 装配时调用，见 `tenon/docs/background/queue.md#running-workers`。
6. **失败**：返回错误时稍后重试；重试结果不会变的业务错误直接转入失败任务表。处理函数要能安全地重复执行（例如已经发布过就视为完成）。见 `tenon/docs/background/queue.md#failures-and-retries`。

## 定时任务

1. 任务是 `func(ctx context.Context) error`，业务动作的 `Handle` 正好是这个签名时直接登记。
2. 发布 `pgschedule.Migrations()`，用 `schedule.New(pgschedule.New(db), cfg.Schedule)` 创建调度器；在模块的 `tasks.go` 中：`s.Add("articles:publish-scheduled", "* * * * *", NewPublishScheduled(db).Handle, schedule.Describe("发布到了预定时间的文章"))`，见 `tenon/docs/background/scheduling.md#defining-tasks`。
3. 多个实例时同一个任务只执行一次，避免重叠见 `tenon/docs/background/scheduling.md#multiple-instances`、`tenon/docs/background/scheduling.md#preventing-overlaps`。
4. 运行调度器见 `tenon/docs/background/scheduling.md#running-the-scheduler`。

## 要点

- worker 和调度器是应用的运行部分，`serve` 运行全部部分，也可以单独部署，见 `tenon/docs/concepts/dependencies.md#running-parts`。
- 多租户应用中 payload 要带上租户，见 `tenon/docs/security/tenancy.md#tenant-jobs`。
- 队列的测试工具和监控见 `tenon/docs/background/queue.md#testing`、`tenon/docs/background/queue.md#failed-jobs`。
- 耗时、带进度、用户要看到结果的操作（导入、批量处理）用后台操作，见 `tenon/docs/features/operations.md`。
