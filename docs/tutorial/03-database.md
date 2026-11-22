# 3. 数据库与迁移

这一章给程序加上 `migrate` 命令，用第一个迁移创建文章表。表结构的每次变更都写成一个迁移文件，随代码提交，每个数据库执行同样的文件。

<a id="commands"></a>
## 迁移命令

迁移文件放在 `migrations` 目录中，嵌入程序，部署时不用另外复制。新建 `migrations/embed.go`：

```go title="migrations/embed.go"
// Package migrations 嵌入应用的 SQL 迁移，部署时只需要一个二进制。
package migrations

import "embed"

// FS 是全部迁移。模式写成 *：还没有迁移时也能编译；goose 只读取其中的 .sql 文件。
//
//go:embed *
var FS embed.FS
```

在 `cmd/blog/main.go` 中加上 `migrate` 命令：

```go title="cmd/blog/main.go"
// Command blog 是博客的入口。
package main

import (
    "github.com/go-tenon/tenon/command"
    "github.com/go-tenon/tenon/command/httpcmd"
    "github.com/go-tenon/tenon/command/migratecmd"
    "github.com/go-tenon/tenon/database"
    "github.com/go-tenon/tenon/httpx"

    "example.com/blog/internal/bootstrap"
    "example.com/blog/migrations"
)

func main() {
    root := command.Root("blog", "博客")
    root.AddCommand(
        command.Serve(bootstrap.Open, bootstrap.Parts),
        command.Config[bootstrap.Config](),
        httpcmd.Health(func(cfg bootstrap.Config) httpx.Config { return cfg.HTTP }),
        // migrate up、status、down、make
        migratecmd.New(migrations.FS, func(cfg bootstrap.Config) database.Config { return cfg.Database }),
    )
    // openapi、routes、client：导出接口文档、列出路由、生成客户端，go tool tenon 调用它们。
    root.AddCommand(httpcmd.Commands(bootstrap.DocRouter)...)
    command.Execute(root)
}
```

`migratecmd.New` 的第二个参数从应用的配置中取出数据库的配置段。命令有 `migrate make <名称>`（新建迁移文件）、`migrate up`（执行未执行的迁移）、`migrate status`（查看状态）、`migrate down`（撤销最近一次迁移）。

<a id="first-migration"></a>
## 第一个迁移

```bash
go mod tidy
go run ./cmd/blog migrate make create_articles_table
```

`migrate make` 输出新文件的路径，例如 `migrations/2026_11_13_080000_create_articles_table.sql`。文件名以创建时的 UTC 时间开头，执行顺序就是创建顺序；下文的文件名以你生成的为准。名称写成 `create_…_table` 时，文件中已有建表语句的骨架，把它补全：

```sql title="migrations/2026_11_13_080000_create_articles_table.sql"
-- +goose Up
CREATE TABLE articles (
    id         uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    title      text NOT NULL,
    slug       text NOT NULL,
    content    text NOT NULL,
    CONSTRAINT articles_slug_key UNIQUE (slug)
);
COMMENT ON TABLE articles IS '博客文章';
COMMENT ON COLUMN articles.id IS '主键';
COMMENT ON COLUMN articles.created_at IS '创建时间';
COMMENT ON COLUMN articles.updated_at IS '更新时间';
COMMENT ON COLUMN articles.title IS '文章标题';
COMMENT ON COLUMN articles.slug IS 'URL 别名，全局唯一';
COMMENT ON COLUMN articles.content IS '文章正文';

-- +goose Down
DROP TABLE articles;
```

- `-- +goose Up` 下是这次变更，`-- +goose Down` 下是撤销它的语句，`migrate down` 执行后者。
- `id` 是 UUID，由程序生成。
- `created_at`、`updated_at` 由程序写入，类型是 `timestamptz`。
- `UNIQUE (slug)` 是唯一约束 `articles_slug_key`，第 4 章靠它拒绝重复的 slug。

<a id="running"></a>
## 执行迁移

```bash
go run ./cmd/blog migrate up
go run ./cmd/blog migrate status
```

```text
VERSION         STATE    APPLIED AT           FILE
20261113080000  applied  2026-11-13 08:01:12  2026_11_13_080000_create_articles_table.sql
```

每个迁移在一个事务中执行，出错时整体回滚，修正 SQL 后再运行 `migrate up`。已经提交或部署的迁移不再修改，改表结构时写新的迁移，第 7 章就这样给文章表加列。

<a id="troubleshooting"></a>
## 常见问题

- **`migrate up` 连接不上数据库**：PostgreSQL 没有运行，在项目根目录运行 `docker compose up -d --wait`，见[第 2 章](02-infrastructure.md#infrastructure)。
- **想改刚执行的迁移**：迁移还没有提交、别人没有执行过时，先 `migrate down`，修改文件后再 `migrate up`。
