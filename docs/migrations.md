# 数据库迁移

[文档首页](README.md) / 数据库

迁移记录表结构如何随应用演进。mygoravel 使用应用自己的 SQL 目录和原生 goose.Provider，应用发布时显式执行迁移。

本章内容：

- [编写迁移](#writing)
- [将 SQL 放进二进制](#embed)
- [构造 Provider](#provider)
- [执行命令](#commands)
- [发布后的变更](#published-migrations)
- [非事务迁移失败](#recovery)

<a id="writing"></a>

## 编写迁移

在应用的 `migrations` 目录新增一个 SQL 文件。文件名使用唯一、递增的 UTC 时间戳，例如 `20261111090000_create_notes.sql`：

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

Up 定义前进变更，Down 定义对应回退。也可以用 goose 官方 create 工具创建文件。

同一秒有多个变更时选择下一个有效时刻。已有迁移保持原样，新变更使用追加文件。

<a id="embed"></a>

## 将 SQL 放进二进制

在迁移目录旁创建文件，例如 `migrations_embed.go`：

```go
package main

import "embed"

//go:embed migrations/*.sql
var migrationFiles embed.FS
```

嵌入路径相对于这个 Go 文件。新增 SQL 后重新构建应用。

<a id="provider"></a>

## 构造 Provider

下面是迁移执行函数的完整示例。文件系统根目录应直接包含迁移 SQL：

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

对上面的 embed.FS，先调用 `migrate.Sub(migrationFiles, "migrations")` 获得根目录。NewProvider 配置 PostgreSQL session lock，并禁用全局 Go 迁移注册；版本表、事务与锁由 goose 管理。

Provider 返回的是原生 `*goose.Provider`。可直接使用 `Up`、`Status`、`Down` 等方法；`migrate.Up` 只是附加错误上下文的便利函数。

<a id="commands"></a>

## 执行命令

把 Provider 方法连接到应用选择安装的 Cobra 命令。例如，在 `command.NewMigrate` 的 up 回调中调用 `provider.Up(ctx)`，在 status 回调中取得 `provider.Status(ctx)` 并编码为 JSON。

这些回调负责读取数据库配置、打开连接和关闭资源。回调只解码数据库和迁移所需配置段。

迁移在发布步骤中执行，成功后启动依赖新结构的 HTTP 应用。命令工厂的具体用法见[应用命令](commands.md)。

<a id="published-migrations"></a>

## 发布后的变更

已发布迁移保持原样，修复使用新的迁移。滚动发布时先增加新旧应用都能使用的结构，再部署应用，最后在后续版本移除旧结构。

在应用的 CI 中，选择明确的发布 tag 或已发布文件目录作为基线。使用 `migrate.CheckPublished(currentDir, baselineDir)` 或 `migrate.CheckFS(currentFS, baselineFS)` 比较已有迁移是否存在且字节一致；当前目录可以追加文件。

这项检查验证迁移源码的相对路径与字节内容。首次发布前，可以用旧迁移 fixture 演练升级。

<a id="recovery"></a>

## 非事务迁移失败

某些操作需要使用 goose 的 `NO TRANSACTION` 指令。失败时可能已有部分 DDL 生效，而迁移版本没有推进。

先查询实际数据库状态，确定哪些语句已经执行，再决定清理、修复或继续的步骤。恢复步骤依据实际结构与迁移版本共同确定。仓库集成测试包含部分 DDL 成功后失败、检查状态、人工恢复再执行的演练。

---

[上一篇：数据库](database.md) · [下一篇：缓存](cache.md)
