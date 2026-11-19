---
name: tenon-database
description: Tenon 应用中与 PostgreSQL 有关的工作：第一次接入数据库（配置、打开连接池、迁移命令）、用 migrate make 新建迁移、定义 Bun 模型、写查询和事务、处理唯一约束等错误、提交后执行的操作。加表、加列、写存储或查询时使用。
---

# 数据库与迁移

数据库是 PostgreSQL，查询用 Bun（`*bun.DB`），迁移用 goose 的 SQL 文件。先读 `.tenon/docs/database/quick-start.md` 和 `.tenon/docs/database/migrations.md`。

## 第一次接入数据库

`tenon init` 生成的项目把数据保存在内存中。换成 PostgreSQL：

1. 在应用的 `Config` 中加上 `Database database.Config \`yaml:"database"\``，在 `config/app.yaml` 中写 `database.dsn`（可以用 `${DATABASE_URL}`），见 `.tenon/docs/database/quick-start.md#configuration`。
2. 在入口的 `open` 中用 `database.Open(ctx, cfg.Database)` 打开连接池，登记到 `cleanup.Stack` 中在退出时关闭，再传给需要它的业务动作，见 `.tenon/docs/concepts/dependencies.md#assembling-the-application`。
3. 新建 `migrations/embed.go`，用 `//go:embed *` 嵌入迁移文件，见 `.tenon/docs/database/migrations.md#embedding-migrations`。
4. 在根命令中加入 `migratecmd.New(migrations.FS, func(cfg Config) database.Config { return cfg.Database })`，应用就有了 `migrate up/down/status/make`，见 `.tenon/docs/database/migrations.md#migration-commands`。
5. 本机用 Docker 启动 PostgreSQL，例如入门教程第 2 章的 `compose.yaml`：`.tenon/docs/tutorial/02-infrastructure.md`。

## 加表或改表

1. `go run ./cmd/<应用> migrate make create_articles_table` 生成迁移文件，补全 `-- +goose Up` 和 `-- +goose Down` 两部分。名称以动作开头、表名结尾，见 `.tenon/docs/database/migrations.md#writing-migrations`。
2. 表结构只由迁移修改。以 `id` 为主键的表前三列依次是 `id`、`created_at`、`updated_at`（`timestamptz NOT NULL`），软删除的 `deleted_at` 是第四列，见 `.tenon/docs/database/migrations.md#timestamp-columns`。时间列不设数据库默认值。
3. `go run ./cmd/<应用> migrate up` 执行，`migrate status` 查看。
4. 在模块的 `<模块>.go` 中定义模型（`bun:"table:articles"` 等标签），字段按列的顺序写，`ID` 之后嵌入 `database.Timestamps`，见 `.tenon/docs/database/quick-start.md#defining-models`。它在插入、`Model(&x)` 更新全部列和 `Model((*T)(nil)).Set(...)` 时写入时间；`Column(...)` 的列中带上 `updated_at`，传入模型的 `Set(...)`、upsert 和原生 SQL 自己写，见 `.tenon/docs/database/quick-start.md#timestamps`。
5. 已经发布（在别处执行过）的迁移不再修改，新建一个迁移，见 `.tenon/docs/database/migrations.md#published-migrations`。

## 查询与事务

- 存储和查询函数接收 `bun.IDB`，连接池和事务都能传入；自己开启事务的业务动作接收 `*bun.DB`。
- 没有找到时 Bun 返回 `sql.ErrNoRows`，转换成模块的业务错误（例如 `ErrNotFound.New()`），见 `.tenon/docs/database/quick-start.md#handling-not-found`。
- 唯一约束等冲突转换成业务错误，见 `.tenon/docs/database/quick-start.md#constraint-errors`。
- 软删除的表用 `WHERE deleted_at IS NULL` 的部分唯一索引代替唯一约束，子表的数据在同一个事务中一起处理，见 `.tenon/docs/database/advanced.md#soft-deletes`。
- 几条写入要一起成功时用 `database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error { ... })`，先读后改的行用 `For("UPDATE")` 加锁，见 `.tenon/docs/database/quick-start.md#transactions`。
- 回滚时撤销不了的操作（删除缓存、广播）用 `tx.AfterCommit` 登记，见 `.tenon/docs/database/quick-start.md#after-commit`。
- 队列任务和业务数据在同一个事务中发布，见技能 `tenon-background`。

## 要点

- SQL 中的时间由 Go 算出后作为参数传入（`time.Now().UTC()`），不用 `NOW()`、`CURRENT_TIMESTAMP`：测试中的假时钟只对 Go 有效。
- 测试用 `dbtest.Open(t, migrations.FS)` 为每个测试建一个已执行迁移的临时库，需要环境变量 `POSTGRES_TEST_URL`，见 `.tenon/docs/testing/database.md#creating-test-databases`。
- 框架的功能包（队列、令牌等）自带迁移，用 `migratecmd.Publish` 发布到项目的迁移目录，见 `.tenon/docs/database/migrations.md#publishing-framework-migrations`。
- 慢查询、软删除、只读副本见 `.tenon/docs/database/advanced.md`。
