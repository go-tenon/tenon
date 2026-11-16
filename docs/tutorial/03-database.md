# 3. 数据库与迁移

数据库已经启动了，但里面还是空的。这一章，我们创建博客的第一张表：文章表。

**这一章要做的：** 给程序加上 `migrate` 命令，写第一个迁移文件，用它创建文章表。

阅读前需要了解：[第 2 章](02-infrastructure.md)；SQL 的 `CREATE TABLE` 语句；[embed](../concepts/go-primer.md#embed)。

- [为什么用迁移](#why)
- [迁移命令](#commands)
- [第一个迁移](#first-migration)
- [执行迁移](#running)
- [常见问题](#troubleshooting)
- [小结](#summary)

<a id="why"></a>
## 为什么用迁移

你可以连上数据库，手动执行一条 `CREATE TABLE`。但这样会遇到问题：同事的电脑、测试环境、生产环境的数据库都要再手动执行一遍；以后改了表结构，每个数据库都要记得改，漏了一个，程序就会因为找不到字段而出错；也没有办法知道每个数据库现在是什么状态。

**迁移**解决这些问题：表结构的每一次变更写成一个 SQL 文件，放在代码仓库中，和代码一起提交。程序按文件名的顺序执行它们，并在数据库中记录执行到了哪一个，下次只执行新的文件。每个数据库只要执行一次 `migrate up`，就会变成和代码一致的结构。

<a id="commands"></a>
## 迁移命令

迁移文件放在 `migrations` 目录中，并且用 `embed` 打包进程序，部署时不需要另外复制这个目录。

新建 `migrations/embed.go`，把这个目录中的文件打包进程序：

```go title="migrations/embed.go"
// Package migrations 嵌入应用的 SQL 迁移，部署时只需要一个二进制。
package migrations

import "embed"

// FS 是全部迁移。模式写成 *：还没有迁移时也能编译；goose 只读取其中的 .sql 文件。
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

`migratecmd.New` 创建 `migrate` 命令。第一个参数是迁移文件；第二个参数是一个函数，告诉它从应用的配置中取出哪一段是数据库的配置。这样程序就有了四个命令：

- `migrate make <名称>`：新建一个迁移文件；
- `migrate up`：执行还没有执行的迁移；
- `migrate status`：查看每个迁移的状态；
- `migrate down`：撤销最近一次迁移。

<a id="first-migration"></a>
## 第一个迁移

```bash
go mod tidy
go run ./cmd/api migrate make create_articles_table
```

`go mod tidy` 下载新导入的包需要的依赖。`migrate make` 输出新文件的路径，例如 `migrations/20261113080000_create_articles_table.sql`。

文件名开头是创建时的 UTC 时间，所以新的迁移总是排在已有的后面，执行顺序就是创建的顺序。你生成的文件名和这里不同，下文中的文件名以你生成的为准。名称写成 `create_…_table` 的形式时，文件中已经有建表语句的骨架。把它补全：

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

这个文件分成两部分：

- **`-- +goose Up`** 下面是这次变更：创建 `articles` 表。
- **`-- +goose Down`** 下面是撤销这次变更：删除这张表。`migrate down` 执行它，开发时写错了可以撤销重来。

表中的每一列：

- `id uuid PRIMARY KEY`：文章的 ID，**主键**唯一标识一行。UUID 是一个随机生成的 128 位标识，由程序生成，不需要数据库自增。
- `title`、`content`：标题和正文。`NOT NULL` 表示这一列不能为空。
- `slug`：文章在网址中的名字，例如 `hello-world`。最后一行的 `UNIQUE (slug)` 是**唯一约束**：数据库保证不会有两篇文章的 slug 相同，即使两个请求同时写入也不会。
- `created_at`、`updated_at`：创建和修改的时间，由程序写入。`timestamptz` 是带时区的时间，保存的是一个确定的时刻，不会因为服务器的时区不同而出错。

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

`migrate up` 执行了这个迁移，`migrate status` 显示它已经执行（`applied`）。再运行一次 `migrate up`，什么也不会发生，因为没有新的迁移。

每个迁移在一个**事务**中执行。事务把几条 SQL 语句变成一个整体：要么全部生效，要么全部不生效。迁移中途出错时，已经执行的部分会被撤销（回滚），数据库不会停在“改了一半”的状态。

已经执行过的迁移不要再修改：其他数据库已经执行过它，不会再执行修改后的版本。以后要改表结构，写一个新的迁移，第 7 章就会这样给文章表加列。

<a id="troubleshooting"></a>
## 常见问题

**`migrate up` 报错连接不上数据库**

PostgreSQL 没有运行。在项目根目录运行 `docker compose up -d --wait`，见[第 2 章](02-infrastructure.md#infrastructure)。

**SQL 写错了，迁移执行失败**

迁移在事务中执行，失败时整体回滚，数据库没有变化。修正 SQL 文件后再运行 `migrate up`。

**迁移执行成功了，但想改一下表结构**

开发时，如果这个迁移还没有提交、别人没有执行过，可以先 `migrate down` 撤销，修改文件后再 `migrate up`。已经提交或部署的迁移，写一个新的迁移来修改。

<a id="summary"></a>
## 小结

这一章，你：

- 知道了为什么用迁移管理表结构：变更写在文件中，每个数据库执行同样的文件，结构始终一致；
- 给程序加上了 `migrate` 命令；
- 写了第一个迁移，创建了文章表。

文章表已经建好了。下一章，我们为它写接口：创建、查询、修改、删除文章。

深入了解：[迁移](../database/migrations.md)、[数据库](../database/quick-start.md)。
