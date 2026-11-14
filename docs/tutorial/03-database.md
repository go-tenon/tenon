# 3. 数据库与迁移

数据库已经启动了，但里面还是空的。这一章，我们用迁移创建博客的第一张表：文章表。

- [迁移命令](#commands)
- [第一个迁移](#first-migration)
- [执行迁移](#running)

<a id="commands"></a>
## 迁移命令

表结构由迁移管理：每次变更写一个 SQL 文件，按顺序执行，每个数据库都记录着执行到了哪一个。迁移文件嵌入在程序中，部署时不需要另外带上 SQL 文件。

让我们先把迁移命令加到应用中。新建 `migrations/embed.go`，把这个目录中的迁移嵌入程序：

```go title="migrations/embed.go"
// Package migrations 嵌入应用的 SQL 迁移，部署时只需要一个二进制。
package migrations

import "embed"

// FS 是全部迁移。模式写成 * 而不是 *.sql：还没有迁移时也能编译，embed.go 不是 .sql，不会被当作迁移。
//
//go:embed *
var FS embed.FS
```

在 `cmd/api/main.go` 中加上 `migrate` 命令：

```go title="cmd/api/main.go"
// Command api 是博客的入口。
package main

import (
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/command/httpcmd"
    "github.com/go-tenon/tenon/command/migratecmd"
    "github.com/go-tenon/tenon/database"

    "example.com/blog/internal/bootstrap"
    "example.com/blog/migrations"
)

func main() {
    root := command.Root("api", "博客")
    root.AddCommand(
        command.Run("serve", "启动 HTTP 服务", bootstrap.Serve),
        command.Config[bootstrap.Config](),
        // migrate up、status、down、make
        migratecmd.New(migrations.FS, func(cfg bootstrap.Config) database.Config { return cfg.Database }),
    )
    // openapi、routes、client：导出接口文档、列出路由、生成客户端，go tool tenon 调用它们。
    root.AddCommand(httpcmd.Commands(bootstrap.DocRouter)...)
    command.Execute(root)
}
```

`migratecmd.New` 的第二个参数从应用的配置中取出数据库的配置。应用就有了 `migrate make`、`up`、`status`、`down` 四个命令。

<a id="first-migration"></a>
## 第一个迁移

```bash
go mod tidy
go run ./cmd/api migrate make create_articles_table
```

命令输出新文件的路径，例如 `migrations/20261113080000_create_articles_table.sql`。文件名开头是当前的 UTC 时间，新迁移总是排在已有的后面；下文中的文件名以你生成的为准。名称以 `create_…_table` 的形式写时，文件中已经有建表语句的骨架。把它补全：

```sql title="migrations/20261113080000_create_articles_table.sql"
-- +goose Up
CREATE TABLE articles (
    id         uuid PRIMARY KEY,
    title      text NOT NULL,
    slug       text NOT NULL,
    content    text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT articles_slug_key UNIQUE (slug)
);

-- +goose Down
DROP TABLE articles;
```

`Up` 是这次变更，`Down` 是撤销它。`slug` 是文章在网址中的名字，例如 `hello-world`，唯一约束保证不会重复。时间列不设默认值，由应用写入。

<a id="running"></a>
## 执行迁移

```bash
go run ./cmd/api migrate up
go run ./cmd/api migrate status
```

```text
VERSION         STATE    APPLIED AT           FILE
20261113080000  applied  2026-11-13 08:01:12  20261113080000_create_articles_table.sql
```

每个迁移在一个事务中执行，失败时整体回滚。已经执行过的迁移不要再修改，之后的变更写新的迁移，第 7 章会给文章表加列。

文章表已经建好了。下一章，我们为它写接口。

深入了解：[迁移](../database/migrations.md)、[数据库](../database/quick-start.md)。
