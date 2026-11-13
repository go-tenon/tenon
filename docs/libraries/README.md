# 库参考

框架提供的基础库，按需使用。

- [缓存](cache.md)：cachex 与 Memcached
- [原子锁](locks.md)：基于 Redis 的分布式锁
- [限流](rate-limiting.md)：按键限制请求或操作的频率
- [队列](queue.md)：NSQ 与数据库发件箱，后台任务
- [定时任务](scheduling.md)：按计划执行任务，多实例去重
- [广播](broadcasting.md)：跨实例发布消息
- [实时频道](realtime.md)：基于 Centrifuge 的 WebSocket 频道
- [文件存储](filesystem.md)：本地磁盘与 S3，files 表，上传与确认
- [文件的常见用法与上传接口](file-uploads.md)：替换文件、缩略图、REST 上传接口
- [邮件](mail.md)：发送邮件，多个服务商切换
- [短信](sms.md)：发送短信，模板与服务商
- [生成 PDF](pdf.md)：用 templ 和 CSS 生成 PDF
- [出站请求](outbound.md)：安全地请求外部 HTTP 服务
- [Redis](redis.md)：rueidis 客户端与使用约定
