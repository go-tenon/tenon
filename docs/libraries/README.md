# 库参考

框架提供的基础库，按需使用。HTTP 接口、后台任务和命令行都可以使用它们。

- [缓存](cache.md)：cachex 与 Memcached
- [原子锁](locks.md)：基于 Redis 的分布式锁
- [限流](rate-limiting.md)：按键限制请求或操作的频率
- [广播](broadcasting.md)：跨实例发布消息
- [实时频道](realtime.md)：基于 Centrifuge 的 WebSocket 频道
- [文件存储](filesystem.md)：本地磁盘与 S3，files 表，上传与确认
- [文件的常见用法与上传接口](file-uploads.md)：替换文件、缩略图、REST 上传接口
- [客户端直传](direct-uploads.md)：签名地址，单次与分片上传到 S3
- [邮件](mail.md)：装配发送器、编写模板、发送邮件
- [邮件服务商](mail-providers.md)：SMTP、Resend、SES，多个服务商自动切换
- [短信](sms.md)：发送短信，模板与服务商
- [生成 PDF](pdf.md)：用 templ 和 CSS 生成 PDF
- [出站请求](outbound.md)：安全地请求外部 HTTP 服务
- [Redis](redis.md)：rueidis 客户端与使用约定
- [NATS](nats.md)：广播与在线成员共用的 NATS 连接
