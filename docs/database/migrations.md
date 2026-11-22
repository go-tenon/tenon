# 迁移

迁移是 `migrations` 目录中的 [goose](https://github.com/pressly/goose) SQL 文件，嵌入二进制，部署时由 `migrate up` 显式执行，成功后再启动依赖新表结构的服务。

包：`database/migrate`、`command/migratecmd`。

<a id="introduction"></a>
## 示例

```bash
go run ./cmd/blog migrate make create_articles_table   # 新建 migrations/2026_11_13_080000_create_articles_table.sql
go run ./cmd/blog migrate up                            # 执行全部未执行的迁移
go run ./cmd/blog migrate status                        # 列出每个迁移的状态
```

在生成的文件中补全 `-- +goose Up` 和 `-- +goose Down` 两部分，见[迁移文件结构](#migration-structure)。

<a id="writing-migrations"></a>
## 用 `migrate make` 新建迁移

- 名称用 snake_case，以动作开头、表名结尾。`--dir` 默认是 `migrations`。
- 文件名是 UTC 时间加名称，例如 `2026_11_11_090000_create_articles_table.sql`，时间晚于目录中已有的迁移。去掉下划线的 `20261111090000` 是迁移的版本。
- 迁移目录中的 `.sql` 文件都按 `年_月_日_时分秒_名称.sql` 命名，否则 `migrate` 的各个命令报错。

| 名称 | 生成的骨架 |
| --- | --- |
| `create_articles_table` | `CREATE TABLE articles`，带 `id`、`created_at`、`updated_at` 与 `COMMENT ON`；`Down` 中 `DROP TABLE` |
| `add_published_at_to_articles_table`、`drop_summary_from_articles_table` | `ALTER TABLE articles`，补全后才能执行 |
| 其他名称 | 只有 goose 注解 |

<a id="migration-structure"></a>
### 迁移文件结构：`Up` 和 `Down`

`-- +goose Up` 之后是应用变更的 SQL，`-- +goose Down` 之后是撤销它的 SQL。与[文章模型](database.md#defining-models)对应的迁移：

```sql
-- +goose Up
CREATE TABLE articles (
    id           uuid PRIMARY KEY,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    deleted_at   timestamptz,
    author_id    text NOT NULL,
    title        text NOT NULL,
    slug         text NOT NULL,
    content      text NOT NULL,
    status       text NOT NULL DEFAULT 'draft',
    published_at timestamptz
);
CREATE UNIQUE INDEX articles_slug_key ON articles (slug) WHERE deleted_at IS NULL;
COMMENT ON TABLE articles IS '文章';
COMMENT ON COLUMN articles.id IS '主键';
COMMENT ON COLUMN articles.created_at IS '创建时间';
COMMENT ON COLUMN articles.updated_at IS '更新时间';
COMMENT ON COLUMN articles.deleted_at IS '软删除时间；为空表示未删除';
COMMENT ON COLUMN articles.author_id IS '作者 ID';
COMMENT ON COLUMN articles.title IS '文章标题';
COMMENT ON COLUMN articles.slug IS 'URL 别名，未删除文章中唯一';
COMMENT ON COLUMN articles.content IS '文章正文';
COMMENT ON COLUMN articles.status IS '发布状态：draft 或 published';
COMMENT ON COLUMN articles.published_at IS '发布时间；草稿为空';

-- +goose Down
DROP TABLE articles;
```

- 表结构只由迁移修改。
- 每个文件默认在一个事务中执行，失败时整体回滚；要在事务之外执行的见[非事务迁移](#recovering-non-transactional-migrations)。
- `deleted_at` 用于[软删除](database.md#soft-deletes)，所以 slug 的唯一索引只约束未删除的行。

<a id="timestamp-columns"></a>
### 时间列与注释：`created_at`、`updated_at`、`deleted_at`、`COMMENT`

- 以 `id` 为主键的表，前三列依次是 `id`、`created_at`、`updated_at`，两个时间列都是 `timestamptz NOT NULL`，模型中嵌入 [`database.Timestamps`](database.md#timestamps) 对应它们。只追加的表（例如日志）同样声明 `updated_at`。
- 软删除的表，`deleted_at` 是第四列，允许为空。
- 以其他列为主键的表（关联表等）按需要声明时间列。
- 时间列不设数据库默认值，由应用写入：测试的假时钟只对 Go 有效，见[控制时间](../testing/testing.md#controlling-time)。
- 每张表和每个字段都要写非空的 `COMMENT ON TABLE` 与 `COMMENT ON COLUMN`（加列迁移同样为新列写 `COMMENT ON COLUMN`）。
- 迁移测试中调用 `dbtest.CheckSchema(t, db)`，自动检查时间列以及所有表和字段的 `COMMENT`；只需单独检查注释时调用 `dbtest.CheckComments(t, db)`。

<a id="embedding-migrations"></a>
## 嵌入迁移文件

在 `migrations` 目录中创建 `embed.go`，命令和测试通过 `migrations.FS` 使用迁移：

```go
// Package migrations 嵌入应用的 SQL 迁移文件。
package migrations

import "embed"

// 模式写成 *：还没有迁移时也能编译；goose 只读取其中的 .sql 文件。
//
//go:embed *
var FS embed.FS
```

迁移在编译时嵌入：新增或修改迁移后重新构建，否则 `./bin/blog migrate up` 看不到新文件（`go run` 会自动编译）。

<a id="publishing-framework-migrations"></a>
## 发布框架迁移：`migratecmd.Publish`

文件上传、队列等功能包自带迁移。用它们时把迁移发布到应用的迁移目录，审查后提交，由 `migrate up` 执行。注册 `publish` 子命令，传入各功能包的 `Migrations()`；`root` 是根命令，`dbConfig` 从配置取出 `database.Config`：

```go
sources := []fs.FS{filesystem.Migrations()}
migrationCmd := migratecmd.New(migrations.FS, dbConfig)
migrationCmd.AddCommand(migratecmd.Publish(sources...), migratecmd.Verify(sources...))
root.AddCommand(migrationCmd)
```

```bash
go run ./cmd/blog migrate publish --dir migrations   # 输出本次新复制的文件名
go run ./cmd/blog migrate verify --dir migrations    # 检查是否漏发新迁移或内容偏离框架
```

- 文件名和内容原样复制。目录中已有的迁移跳过，所以首次执行前可以修改发布出来的 SQL；不给它改名，否则会再复制一份。
- 升级框架后运行 `migrate publish` 复制新增的迁移；在 CI 中运行 `migrate verify` 检查是否有漏发布的框架迁移或与框架内容不一致的文件。
- 发布出来的迁移时间通常早于应用最近的迁移，`migrate up` 照样执行它们。发布后重新构建，再运行 `migrate up`。

自定义工具可以直接调用 `migrate.Publish("migrations", sources...)` 和 `migrate.Verify("migrations", sources...)`。

<a id="migration-commands"></a>
## 迁移命令：`migratecmd.New`

在[装配命令行](../general/commands.md#declaring-the-cli)时加入 `migratecmd.New`，应用就有了 `migrate up`、`status`、`down`、`make`。`root` 是根命令，`settings.Config` 是应用的配置结构：

```go
root.AddCommand(migratecmd.New(migrations.FS, func(cfg settings.Config) database.Config {
    return cfg.Database
}))
```

- `migrate up` 按时间顺序执行所有还没执行过的迁移，包括比已执行的更早的，例如升级框架后发布的迁移、两个分支合并后时间交错的迁移。`migrate down` 回滚最后执行的一个。
- 数据库连不上时以 `ExitUnavailable` 退出，部署脚本据此重试。输出见 [migrate 命令](../general/commands.md#migrate-command)。
- `migratecmd.Check` 让 `migrate up` 在迁移之前运行自检，见[自检](../installation/doctor.md)。
- 多个数据库见[多个数据库](database.md#multiple-databases)；发布流程见[部署](../installation/deployment.md#running-migrations)。

<a id="creating-a-provider"></a>
## 在代码中执行迁移：`migrate.NewProvider`

`db` 是 `database.Open` 打开的 `*bun.DB`：

```go
provider, err := migrate.NewProvider(db, migrations.FS)
if err != nil {
    return err
}
err = migrate.Up(ctx, provider)
```

- 文件系统的根目录直接包含迁移文件；在子目录中时先用 `migrate.Sub(files, "migrations")` 取出。
- 返回原生的 `*goose.Provider`，与 `migrate` 命令用同一个配置：会话锁让多个实例的迁移串行执行，执行所有未执行的迁移（包括更早的）。
- `Status` 返回的 `Source.Path` 是去掉下划线的文件名，用 `migrate.FileName(path)` 换回目录中的文件名。
- 测试中用 `dbtest.Open(t, migrations.FS)`，见[数据库测试](../testing/testing.md#database-tests)。

<a id="seeding"></a>
## 填充数据：`seed` 命令

- **每个环境都要有、写入后不变的数据**（分类、地区代码）：写进迁移的 `Up`。
- **由代码决定的数据**（预置角色）：应用启动时同步，见[预置角色](../security/authorization.md#preset-roles)。
- **开发和演示用的数据**：写成应用自己的 `seed` 命令，通过[业务动作](../concepts/actions.md)写入，数据经过相同的校验与规则。

`seed` 用 `command.Bind` 声明，`db` 在命令执行时才打开，见[只在执行时装配的命令](../concepts/dependencies.md#deferred-commands)：

```go
func seed(db *bun.DB) *cobra.Command {
    return &cobra.Command{
        Use:  "seed",
        Args: command.NoArgs,
        RunE: func(cmd *cobra.Command, _ []string) error {
            alice := identity.Subject{Type: user.Type, ID: "alice"}
            _, err := article.NewCreate(db).Handle(cmd.Context(), alice,
                article.Changes{Title: "Hello World", Slug: "hello-world", Content: "……"})
            if errors.Is(err, article.ErrSlugTaken) {
                return nil // 已经写入过，命令可以重复执行
            }
            return err
        },
    }
}
```

本地依次运行 `migrate up` 和 `seed`。测试各自准备数据，见[测试业务动作](../testing/testing.md#testing-actions)。

<a id="published-migrations"></a>
## 不修改已发布的迁移

迁移在任何环境执行过之后就固定下来，需要修正时新建一个迁移：已执行的迁移不会再执行，修改它只会让各环境的表结构不一致。还没发布的迁移，可以 `migrate down` 后修改再 `migrate up`。

<a id="checking-in-ci"></a>
### 在 CI 中检查：`migrate.CheckPublished`

与上一个发布版本的迁移目录比较，基线中的文件被修改或删除时返回错误，新增文件允许：

```go
err := migrate.CheckPublished("migrations", "baseline/migrations")
```

迁移已经是 `fs.FS` 时用 `migrate.CheckFS(currentFS, baselineFS)`。

<a id="zero-downtime-changes"></a>
### 零停机变更

滚动发布时新旧版本同时运行，它们用到的列都要存在，破坏性的变更分几次发布。把 `articles.body` 改名为 `content`：

| 发布 | 迁移 | 代码 |
| --- | --- | --- |
| 第一次 | 新增 `content` 列，复制 `body` 的数据 | 同时写 `body` 和 `content`，读 `body` |
| 第二次 | 无 | 只读写 `content` |
| 第三次 | 删除 `body` 列 | 不变 |

<a id="recovering-non-transactional-migrations"></a>
## 非事务迁移：`-- +goose NO TRANSACTION`

`CREATE INDEX CONCURRENTLY` 这类语句要在事务之外执行，迁移文件中写 `-- +goose NO TRANSACTION`，每个非事务迁移只写一条语句。

> [!WARNING]
> 非事务迁移失败时，部分语句可能已生效而版本没有记录。先查询实际结构，手动清理或补全到一致的状态，再重新执行。
