# 入门教程

这个教程从一个空目录开始，一步步做出一个博客：作者登录后写文章，草稿只有自己能看到，发布后所有人都能读到。每一章结束时项目都能运行，代码都是完整的文件，可以直接复制。

学完之后，你会用到一个常规项目需要的大部分东西：配置与装配、数据库与迁移、接口与校验、错误提示、React 页面、登录与授权。之后需要缓存、队列、文件上传、部署等功能时，查阅对应的参考页即可，它们不依赖这个博客。

1. [创建项目](01-project.md)：生成项目，启动开发
2. [基础设施与装配](02-infrastructure.md)：用 Docker 启动服务，配置，装配应用，健康检查
3. [数据库与迁移](03-database.md)：第一张表
4. [文章接口](04-api.md)：模型、动作、校验、业务错误、分页
5. [文章页面](05-pages.md)：生成的客户端，TanStack Query，显示错误
6. [作者登录](06-login.md)：邮箱和密码，HttpOnly cookie，命令行建号
7. [草稿与发布](07-drafts.md)：给表加列，授权，事务中检查

读完后可以继续阅读：[缓存](../libraries/cache.md)、[队列](../libraries/queue.md)、[定时任务](../libraries/scheduling.md)、[文件存储](../libraries/filesystem.md)、[测试](../testing/overview.md)、[部署](../installation/deployment.md)。
