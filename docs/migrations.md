# 数据库：迁移

- [简介](#introduction)
- [编写迁移](#writing-migrations)
    - [迁移文件结构](#migration-structure)
- [嵌入迁移文件](#embedding-migrations)
- [运行迁移](#running-migrations)
    - [创建迁移器](#creating-a-provider)
    - [迁移命令](#migration-commands)
- [修改已发布的迁移](#published-migrations)
    - [在 CI 中检查](#checking-in-ci)
    - [零停机变更](#zero-downtime-changes)
- [非事务迁移失败后的恢复](#recovering-non-transactional-migrations)

<a id="introduction"></a>
## 简介

迁移就像是数据库的版本控制：它记录了表结构的每一次变化，让团队成员和每个部署环境都能把数据库升级到相同的结构。

mygoravel 使用 [goose](https://github.com/pressly/goose) 管理迁移。迁移是应用目录中的普通 SQL 文件，它们会被嵌入到二进制中，在发布时通过命令显式执行——API 服务启动时**不会**自动执行迁移。

<a id="writing-migrations"></a>
## 编写迁移

在应用的 `migrations` 目录中新建一个 SQL 文件。文件名以一个唯一、递增的 UTC 时间戳开头，后面跟上描述，例如 `20261111090000_create_notes.sql`。

你也可以使用 goose 的命令行工具生成文件：

```bash
goose -dir migrations create create_notes sql
```

<a id="migration-structure"></a>
### 迁移文件结构

每个迁移文件包含两部分：`Up` 描述如何应用这次变更，`Down` 描述如何撤销它：

```sql
-- +goose Up
CREATE TABLE notes (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL,
    title text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE notes;
```

默认情况下，每个迁移文件在一个事务中执行，失败时整体回滚。

<a id="embedding-migrations"></a>
## 嵌入迁移文件

为了让部署时只需要一个二进制文件，请用 `embed` 把迁移文件嵌入进来。在 `migrations` 目录的上一级创建一个 Go 文件，例如 `migrations_embed.go`：

```go
package main

import "embed"

//go:embed migrations/*.sql
var migrationFiles embed.FS
```

`//go:embed` 的路径相对于这个 Go 文件所在的目录。每次新增迁移文件后，都需要重新构建应用。

<a id="running-migrations"></a>
## 运行迁移

<a id="creating-a-provider"></a>
### 创建迁移器

使用 `migrate.NewProvider` 创建一个 goose 迁移器（Provider）。下面是一个完整的函数，它执行所有尚未应用的迁移：

```go
package migrations

import (
    "context"
    "io/fs"

    "github.com/ryan053857/mygoravel/database/migrate"
    "github.com/uptrace/bun"
)

func Up(ctx context.Context, db *bun.DB, files fs.FS) error {
    provider, err := migrate.NewProvider(db.DB, files)
    if err != nil {
        return err
    }
    _, err = provider.Up(ctx)
    return err
}
```

`NewProvider` 要求传入的文件系统**根目录**直接包含迁移文件。对于上一节嵌入的 `migrationFiles`，需要先取出 `migrations` 子目录：

```go
files, err := migrate.Sub(migrationFiles, "migrations")
```

`NewProvider` 返回的是原生的 `*goose.Provider`，你可以直接调用它的 `Up`、`Down`、`Status` 等方法。它已经配置好了以下行为：

- 使用 PostgreSQL 的会话锁，保证多个实例同时执行迁移时也只会串行运行；
- 只使用 SQL 文件，不使用 goose 的全局 Go 迁移注册。

版本记录表、事务和锁都由 goose 管理。框架还提供了 `migrate.Up(ctx, provider)`，它在调用 `provider.Up` 的基础上为错误附加了上下文信息。

<a id="migration-commands"></a>
### 迁移命令

推荐使用 `command.NewMigrate` 为应用添加迁移命令，它会创建 `migrate up`、`migrate status` 和 `migrate down` 三个子命令：

```go
root.AddCommand(command.NewMigrate(
    func(ctx context.Context, cmd *cobra.Command, args []string) error {
        return withProvider(ctx, cmd, func(p *goose.Provider) error {
            _, err := p.Up(ctx)
            return err
        })
    },
    statusFunc, // 调用 p.Status(ctx)，把结果编码为 JSON 输出
    downFunc,   // 调用 p.Down(ctx)，回滚最近一次迁移
))
```

这里的 `withProvider` 是应用自己的辅助函数：它只读取配置文件中的 `database` 段，打开数据库连接，创建 Provider，执行传入的函数，最后关闭连接。

之后，你就可以在部署流程中执行：

```bash
./bin/api migrate up
```

迁移成功后，再启动依赖新表结构的 API 服务。完整的发布流程见[部署](deployment.md#running-migrations)。

<a id="published-migrations"></a>
## 修改已发布的迁移

一旦迁移被发布到任何环境，就**不要再修改它**。已经执行过的迁移不会再次执行，修改它只会让不同环境的数据库结构变得不一致。如果需要修正，请新建一个迁移。

<a id="checking-in-ci"></a>
### 在 CI 中检查

为了防止有人不小心修改了已发布的迁移，你可以在 CI 中把当前的迁移目录与某个已发布版本（例如上一个发布标签）进行比较：

```go
err := migrate.CheckPublished("migrations", "baseline/migrations")
```

如果你的迁移已经以 `fs.FS` 的形式存在，可以使用 `migrate.CheckFS(currentFS, baselineFS)`。

检查会确认基线中的每个迁移文件在当前目录中都存在，并且内容逐字节相同；当前目录中新增的文件是允许的。

<a id="zero-downtime-changes"></a>
### 零停机变更

滚动发布时，新旧两个版本的应用会同时运行一段时间，它们必须都能使用当前的数据库结构。因此，破坏性的变更（例如重命名列）应该分多次发布完成：

1. 先添加新结构（例如新增一列），新旧代码都能正常工作；
2. 部署使用新结构的应用代码；
3. 在后续的版本中，移除不再使用的旧结构。

<a id="recovering-non-transactional-migrations"></a>
## 非事务迁移失败后的恢复

某些操作不能在事务中执行，例如 `CREATE INDEX CONCURRENTLY`。这时需要在迁移文件中使用 goose 的 `-- +goose NO TRANSACTION` 指令。

> [!WARNING]
> 非事务迁移失败时，可能已有部分语句生效，而迁移的版本号却没有更新。此时直接重新执行可能会失败或造成重复的变更。

遇到这种情况，请按以下步骤恢复：

1. 查询数据库的实际状态，确认哪些语句已经执行；
2. 根据实际结构，手动清理或补全变更；
3. 确认数据库处于迁移前或迁移后的一致状态后，再重新执行迁移。

为了降低风险，建议每个非事务迁移只包含一条语句。
