# 数据库：迁移

迁移就像是数据库的版本控制：它记录了表结构的每一次变化，让团队中的每个人、每个部署环境都能把数据库升级到相同的结构。

Tenon 使用 [goose](https://github.com/pressly/goose) 管理迁移。迁移是应用目录中的普通 SQL 文件，它们被嵌入到二进制中，在发布时通过命令**显式**执行。迁移是发布中的一个独立步骤，由 `migrate up` 执行一次：多个实例同时启动时，由谁执行、失败了怎么办都很清楚。

阅读前需要了解：SQL 的 `CREATE TABLE`、`ALTER TABLE`；[入门教程第 3 章](../tutorial/03-database.md)写过第一个迁移；[embed](../concepts/go-primer.md#embed)。

- [示例用法](#introduction)
- [编写迁移](#writing-migrations)
    - [迁移文件结构](#migration-structure)
- [嵌入迁移文件](#embedding-migrations)
- [发布框架迁移](#publishing-framework-migrations)
- [运行迁移](#running-migrations)
    - [迁移命令](#migration-commands)
    - [在代码中执行迁移](#creating-a-provider)
- [填充数据](#seeding)
- [修改已发布的迁移](#published-migrations)
    - [在 CI 中检查](#checking-in-ci)
    - [零停机变更](#zero-downtime-changes)
- [非事务迁移失败后的恢复](#recovering-non-transactional-migrations)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="introduction"></a>
## 示例用法

用 `migrate make` 新建一个迁移：

```bash
go run ./cmd/api migrate make create_articles_table   # 新建 migrations/2026_11_13_080000_create_articles_table.sql
go run ./cmd/api migrate up                            # 执行全部未执行的迁移
go run ./cmd/api migrate status
```

在生成的文件中写上建表和回滚的 SQL：

```sql
-- +goose Up
CREATE TABLE articles (
    id    uuid PRIMARY KEY,
    title text NOT NULL,
    slug  text NOT NULL UNIQUE
);

-- +goose Down
DROP TABLE articles;
```

`migrate up` 执行全部还没有执行过的迁移，`migrate status` 列出每个迁移的状态。下面详细介绍怎样编写、嵌入和运行迁移。

<a id="writing-migrations"></a>
## 编写迁移

用 `migrate make` 新建迁移。名称使用 snake_case，以动作开头、表名结尾：

```bash
go run ./cmd/api migrate make create_articles_table
```

命令输出新文件的路径，例如 `migrations/2026_11_11_090000_create_articles_table.sql`。文件名和 Laravel 一样，是 UTC 时间加名称；时间保证晚于目录中已有的迁移，所以新迁移总是排在最后。命令只生成文件；`--dir` 默认是 `migrations`。

命令会从名称推断表名并生成骨架：

| 名称 | 生成的骨架 |
| --- | --- |
| `create_articles_table` | `CREATE TABLE articles`，带 UUID 主键 `id`、`created_at`、`updated_at`；`Down` 中 `DROP TABLE articles` |
| `add_published_at_to_articles_table`、`drop_summary_from_articles_table` | `ALTER TABLE articles`，补全后才能执行 |
| 其他名称，例如 `backfill_article_slugs` | 只有 goose 注解 |

去掉下划线的时间 `20261111090000` 是迁移的版本，数据库用它记录哪些迁移已经执行，`migrate status` 的 `VERSION` 列显示的就是它。迁移目录中的 `.sql` 文件都要按这个格式命名，否则 `migrate make` 和 `migrate publish` 会报错。

<a id="migration-structure"></a>
### 迁移文件结构

每个迁移文件包含两部分：`Up` 描述如何应用这次变更，`Down` 描述如何撤销它。下面是补全后的 `create_articles_table`：

在生成的文件中写上建表和回滚的 SQL：

```sql
-- +goose Up
CREATE TABLE articles (
    id           uuid PRIMARY KEY,
    author_id    text NOT NULL,
    title        text NOT NULL,
    slug         text NOT NULL,
    content      text NOT NULL,
    status       text NOT NULL DEFAULT 'draft',
    published_at timestamptz,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    deleted_at   timestamptz,
    CONSTRAINT articles_slug_key UNIQUE (slug)
);

-- +goose Down
DROP TABLE articles;
```

这里显式声明了三个时间字段，与[文章模型](quick-start.md#defining-models)保持一致。时间列不设数据库默认值，`created_at` 和 `updated_at` 由[模型钩子](quick-start.md#timestamps)写入；允许为空的 `deleted_at` 配合 Bun 的[软删除标签](advanced.md#soft-deletes)使用。表结构只由迁移修改。

默认情况下，每个迁移文件在一个事务中执行，失败时整体回滚。

<a id="embedding-migrations"></a>
## 嵌入迁移文件

为了让部署时只需要一个二进制文件，请用 `embed` 把迁移文件嵌入进来。在 `migrations` 目录中创建 `embed.go`：

```go
// Package migrations 嵌入应用的 SQL 迁移文件。
package migrations

import "embed"

// 模式写成 *：还没有迁移时也能编译；goose 只读取其中的 .sql 文件。
//
//go:embed *
var FS embed.FS
```

这样 `migrations` 就成了一个普通的 Go 包，命令行和测试都可以通过 `migrations.FS` 使用这些文件。每次新增迁移文件后，都需要重新构建应用。

<a id="publishing-framework-migrations"></a>
## 发布框架迁移

文件上传等可选功能自带迁移 SQL。使用这些功能时，把迁移发布到你的应用目录，审查并纳入版本控制，然后交给应用现有的 goose 迁移流程执行。

发布命令需要显式注册。下面是加入已有命令装配代码的片段：`root` 是 `command.Root` 返回的根命令，`migrations.FS` 是应用嵌入的迁移，`dbConfig` 是应用命令装配中从配置取出数据库配置的函数。需要导入 `command/migratecmd` 和 `filesystem` 包：

```go
migrationCmd := migratecmd.New(migrations.FS, dbConfig)
migrationCmd.AddCommand(migratecmd.Publish(
    filesystem.Migrations(),
))
root.AddCommand(migrationCmd)
```

在应用根目录执行：

```bash
go run ./cmd/api migrate publish --dir migrations
```

`--dir` 默认是当前工作目录下的 `migrations`。命令把迁移文件原样复制过来，文件名和内容都不变，只输出本次新复制的文件名；再次运行且没有新增迁移时，输出为空。

功能包中的迁移和你自己的迁移命名方式相同，例如 `filesystem` 包的：

```text
migrations/2026_11_10_114451_create_files.sql
```

目录中已有的迁移会跳过：你可以在首次执行前调整发布出来的 SQL，再次发布时你的修改保持原样；但不要给它改名，否则会被再复制一份。升级框架后再次运行命令，只会复制新增的迁移。发布之前先检查全部文件，有文件名不合格式、版本与目录中其他迁移重复等问题时，一个文件也不写。

发布出来的迁移时间通常早于你自己最近的迁移。和 Laravel 一样，`migrate up` 会执行所有还没有执行过的迁移，不管它们比已执行的迁移早还是晚，见[运行迁移](#migration-commands)。功能包之间的依赖由时间保证：例如 `sheet/exports` 引用 `operation` 的表，它的迁移时间晚于 `operation` 的迁移。

> [!NOTE]
> 发布完成后，需要审查和提交 SQL，再重新构建应用，将新文件嵌入二进制。然后运行 `migrate up`。已经在任何环境执行过的迁移保持原样，后续调整一律新增迁移。`CheckPublished` 只检查应用自身的历史基线。

自定义命令或工具也可以直接调用 `migrate.Publish("migrations", sources...)`，`sources` 是功能包的 `Migrations()`。

<a id="running-migrations"></a>
## 运行迁移

<a id="migration-commands"></a>
### 迁移命令

在[装配命令行](../general/commands.md#declaring-the-cli)时加入 `command/migratecmd` 包的 `migratecmd.New`，应用就有了 `migrate up`、`migrate status`、`migrate down` 和[新建迁移](#writing-migrations)的 `migrate make`：

```go
root.AddCommand(migratecmd.New(migrations.FS, func(cfg settings.Config) database.Config {
    return cfg.Database
}))
```

之后，你就可以在部署流程中执行：

```bash
./bin/api migrate up
./bin/api migrate status
```

和 Laravel 一样，`migrate up` 按时间顺序执行所有还没有执行过的迁移，即使它比已经执行过的迁移更早，例如升级框架后发布的功能包迁移，或者两个分支各自新建、合并后时间交错的迁移。`migrate down` 回滚最后执行的那一个迁移。

命令负责加载配置、连接数据库和关闭连接。数据库连不上时，命令以 `ExitUnavailable` 退出，部署脚本可以据此重试。各命令的输出见 [migrate 命令](../general/commands.md#migrate-command)。

迁移成功后，再启动依赖新表结构的 API 服务。完整的发布流程见[部署](../installation/deployment.md#running-migrations)。

<a id="creating-a-provider"></a>
### 在代码中执行迁移

在测试或自定义命令中，可以用 `migrate.NewProvider` 创建一个 goose 迁移器（Provider），然后调用它的方法：

```go
provider, err := migrate.NewProvider(db, migrations.FS)
if err != nil {
    return err
}
err = migrate.Up(ctx, provider)
```

其中 `db` 是 `database.Open` 打开的 `*bun.DB`。`NewProvider` 要求文件系统的**根目录**直接包含迁移文件，`migrations.FS` 正好满足这个要求。如果你的迁移文件嵌入在某个子目录中，可以先用 `migrate.Sub(files, "migrations")` 取出子目录。

`NewProvider` 返回的是原生的 `*goose.Provider`，你可以直接调用它的 `Up`、`Down`、`Status` 等方法。它已经配置好了以下行为：

- 使用 PostgreSQL 会话锁，保证多个实例同时执行迁移时也只会串行运行；
- 只使用 SQL 文件，不使用 goose 的全局 Go 迁移注册；
- 执行所有还没有执行过的迁移，包括比已执行的迁移更早的，见[迁移命令](#migration-commands)；
- goose 把文件名中第一个下划线之前的数字当作版本，所以它看到的 `2026_11_17_080000_create_articles_table.sql` 名为 `20261117080000_create_articles_table.sql`。`Status` 等方法返回的 `Source.Path` 也是这个名字，用 `migrate.FileName(path)` 换回目录中的文件名。

版本记录表、事务和锁都由 goose 管理。`migrate.Up(ctx, provider)` 在调用 `provider.Up` 的基础上为错误附加了上下文信息。`migrate` 命令使用的也是同一个迁移器。

<a id="seeding"></a>
## 填充数据

数据库中的初始数据分为两类，放在不同的地方：

- **每个环境都必须有的数据**，例如角色或分类，写进迁移的 `Up` 部分。它们和表结构一起受版本控制，在每个环境中只执行一次。
- **只在开发和演示时需要的数据**，例如几篇示例文章，写成一个应用自己的 `seed` 命令。

下面的 `seed` 命令以 alice 的身份创建并发布两篇文章。它用 `command.Bind` 声明，`db` 在命令执行时才打开，见[只在执行时装配的命令](../concepts/dependencies.md#deferred-commands)：

```go
func seed(db *bun.DB) *cobra.Command {
    return &cobra.Command{
        Use:   "seed",
        Short: "写入开发用的示例数据",
        Args:  command.NoArgs,
        RunE: func(cmd *cobra.Command, _ []string) error {
            create, publish := article.NewCreate(db), article.NewPublish(db)
            alice := identity.Subject{Type: user.Type, ID: "alice"}
            for _, c := range []article.Changes{
                {Title: "Hello World", Slug: "hello-world", Content: "……"},
                {Title: "Second Post", Slug: "second-post", Content: "……"},
            } {
                item, err := create.Handle(cmd.Context(), alice, c)
                if errors.Is(err, article.ErrSlugTaken) {
                    continue // 之前已经写入过
                }
                if err != nil {
                    return err
                }
                if _, err := publish.Handle(cmd.Context(), alice, item.ID); err != nil {
                    return err
                }
            }
            return nil
        },
    }
}
```

示例数据通过[业务动作](../concepts/actions.md)写入，这样它们和真实请求创建的数据经过相同的验证与规则，表结构变化时随业务动作一起更新。slug 的唯一约束让命令可以重复执行：已经存在的文章会被跳过。

`seed` 组合了 `Create` 和 `Publish` 两个动作，直接调用它们的 `Handle`。在本地准备数据库：

```bash
go run ./cmd/api migrate up
go run ./cmd/api seed
```

`seed` 只用于开发环境。需要大量随机数据时，可以用 [gofakeit](https://github.com/brianvoe/gofakeit) 生成 `article.Changes`。测试各自准备数据，每个测试在自己的数据库中创建，见[测试业务动作](../testing/database.md#testing-actions)。

<a id="published-migrations"></a>
## 修改已发布的迁移

一旦迁移被发布到任何环境，它就**固定下来**。已经执行过的迁移只执行一次，修改它会让不同环境的数据库结构变得不一致。需要修正时，请新建一个迁移。

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
3. 在后续的版本中，移除已经弃用的旧结构。

以把 `articles.body` 改名为 `content` 为例，一次 `ALTER TABLE articles RENAME COLUMN body TO content` 会让还在运行的旧版本立即找不到 `body` 列。改为分三次发布：

| 发布 | 迁移 | 代码 |
| --- | --- | --- |
| 第一次 | 新增 `content` 列，把 `body` 的数据复制过去 | 同时写 `body` 和 `content`，读 `body` |
| 第二次 | 无 | 只读写 `content` |
| 第三次 | 删除 `body` 列 | 不变 |

每一次发布时，同时运行的新旧两个版本用到的列都存在。

<a id="recovering-non-transactional-migrations"></a>
## 非事务迁移失败后的恢复

某些操作必须在事务之外执行，例如 `CREATE INDEX CONCURRENTLY`。这时需要在迁移文件中使用 goose 的 `-- +goose NO TRANSACTION` 指令。

> [!WARNING]
> 非事务迁移失败时，可能已有部分语句生效，而迁移的版本号却没有更新。此时直接重新执行可能会失败或造成重复的变更。

遇到这种情况，请按以下步骤恢复：

1. 查询数据库的实际状态，确认哪些语句已经执行；
2. 根据实际结构，手动清理或补全变更；
3. 确认数据库处于迁移前或迁移后的一致状态后，再重新执行迁移。

为了降低风险，建议每个非事务迁移只包含一条语句。

<a id="troubleshooting"></a>
## 常见问题

**修改了一个已经执行过的迁移，`migrate up` 没有任何变化**

数据库记录着这个迁移已经执行过，不会再执行。开发时可以先 `migrate down` 撤销再 `migrate up`；已经发布的迁移不要修改，写一个新的迁移，见[修改已发布的迁移](#published-migrations)。

**新建的迁移文件没有被执行**

迁移文件通过 `embed` 打包进程序，修改后要重新编译：`go run` 会自动编译，用 `./bin/api` 时要先重新构建。另外检查文件是否在 `migrations` 目录中、扩展名是否是 `.sql`。

**`migrate up` 报错 `migration "…" must be named like 2026_11_17_123456_create_articles_table.sql`**

迁移目录中有不符合格式的 `.sql` 文件。用 `migrate make` 新建迁移，或者按 `年_月_日_时分秒_名称.sql` 给文件改名。

**迁移执行到一半失败了**

普通的迁移在事务中执行，失败时整体回滚，修正 SQL 后再执行即可。使用了 `-- +goose NO TRANSACTION` 的迁移见[非事务迁移失败后的恢复](#recovering-non-transactional-migrations)。

<a id="api-reference"></a>
## API 参考

命令见[migrate 命令](../general/commands.md#migrate-command)。包 `github.com/go-tenon/tenon/database/migrate`：

### NewProvider、Up

```go
func NewProvider(db *bun.DB, fsys fs.FS) (*goose.Provider, error)
func Up(ctx context.Context, p *goose.Provider) error
```

用嵌入的迁移文件创建 goose 的 Provider；`Up` 执行全部未执行的迁移。用法见[在代码中执行迁移](#creating-a-provider)。

### FileName

```go
func FileName(path string) string
```

把 goose 返回的迁移路径换成迁移目录中的文件名，例如 `20261117080000_create_articles_table.sql` 换成 `2026_11_17_080000_create_articles_table.sql`。见[在代码中执行迁移](#creating-a-provider)。

### Create

```go
func Create(dir, name string) (string, error)
```

在 `dir` 中新建以 UTC 时间开头的迁移文件，例如 `2026_11_17_080000_create_articles_table.sql`，返回路径。`migrate make` 调用它。

### Publish

```go
func Publish(dir string, sources ...fs.FS) ([]string, error)
```

把功能包自带的迁移原样复制到应用的 `dir` 中，跳过目录中已有的迁移，返回新写入的文件。见[发布框架迁移](#publishing-framework-migrations)。

### CheckPublished、CheckFS

```go
func CheckPublished(dir, baseline string) error
func CheckFS(live, baseline fs.FS) error
```

比较当前的迁移和基线（例如主分支上的版本），已经发布的迁移被修改或删除时返回错误。见[在 CI 中检查](#checking-in-ci)。
