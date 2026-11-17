# 后台任务

不由请求触发、在后台运行的工作。队列 worker 和定时任务调度器与 HTTP 服务一样，是应用的常驻部分：可以和 HTTP 服务运行在同一个进程中，也可以单独部署，见[在一个进程中运行多个部分](../general/commands.md#running-together)。只运行它们，就是一个没有 HTTP 接口的纯后台服务。

- [队列](queue.md)：NATS JetStream 与数据库发件箱，发布任务，启动 worker
- [队列：失败任务与监控](queue-monitoring.md)：失败任务的重试与删除，日志、状态命令与运维接口
- [定时任务](scheduling.md)：按计划执行任务，多实例去重
