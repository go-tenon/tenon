// Package database 约定产品访问 Postgres 的方式：[bun] 作为查询构造器，
// [pgx] 作为驱动，[goose] 管理 SQL 迁移，主键使用 UUIDv7。要求 Postgres 18 及以上（内置 uuidv7()）。
//
// 本包只提供：
//
//	Config / Open / New            连接池默认值、DSN 打码、启动时 Ping
//	Model / CreatedModel           公共字段（id、created_at、updated_at）及其自动填充
//	NewMigrator / Migrate          带 advisory lock 的 goose，多实例同时启动也安全
//	MigrationSource                go-kit 模块随包发布的迁移
//	PublishMigrations              把模块迁移复制到产品的迁移目录
//	CreateMigration                新建产品迁移文件
//	IsUniqueViolation              判断唯一约束冲突
//
// 使用 go-kit 的 app 包时，以上大部分由 app 调用，产品通过子命令操作：
//
//	go run . migrate create create_orders   新建迁移
//	go run . migrate publish                发布已注册模块的迁移
//	go run . migrate up | down | status
//
// 查询、事务、关联等直接使用 bun 的 API，本包不做包装。
//
// # 启动
//
// 不使用 app 包时，产品在 main 中显式组装：
//
//	db, err := database.Open(ctx, cfg.Database)
//	if err != nil { ... }
//	defer db.Close()
//
//	// 可选：查询日志。普通查询为 Debug，超过阈值的慢查询为 Warn。
//	// 出错的查询会返回给调用方处理，这里降为 Debug，避免与业务日志重复。
//	db.AddQueryHook(bunslog.NewQueryHook( // github.com/uptrace/bun/extra/bunslog
//		bunslog.WithSlowQueryThreshold(200*time.Millisecond),
//		bunslog.WithErrorQueryLogLevel(slog.LevelDebug),
//	))
//
//	if err := database.Migrate(ctx, db.DB, migrations.FS); err != nil { ... }
//
//	srv := httpx.NewServer(cfg.HTTP, r)
//	srv.ReadyCheck = db.PingContext
//
// # 模型
//
// 模型嵌入 Model，表名由 bun.BaseModel 的标签指定：
//
//	type User struct {
//		bun.BaseModel `bun:"table:users"`
//		database.Model
//
//		Email string `bun:"email,notnull"`
//	}
//
//	u := &User{Email: "a@example.com"}
//	_, err := db.NewInsert().Model(u).Exec(ctx) // 自动填充 u.ID、u.CreatedAt、u.UpdatedAt
//
// 主键在应用中生成，插入前即已知，批量插入也不需要 RETURNING。
// 查询不到记录时 bun 返回 sql.ErrNoRows。
//
// 需要在事务中执行的函数接收 bun.IDB，*bun.DB 与 bun.Tx 都实现了它：
//
//	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
//		return createUser(ctx, tx, u)
//	})
//
// # 迁移
//
// 产品只有一个迁移目录，全部迁移（产品自己的与 go-kit 模块发布过来的）都在这里，
// 用 embed 打包进二进制，共用一张 goose 版本表：
//
//	migrations/
//		migrations.go                                   package migrations; //go:embed *.sql; var FS embed.FS
//		20261109120000_session_0001_create_sessions.sql  由 PublishMigrations 从 go-kit 复制
//		20261110093000_create_orders.sql                 产品自己的迁移
//
// 文件名以时间戳为版本号（YYYYMMDDHHMMSS），CreateMigration 与 PublishMigrations 保证新文件的
// 版本号大于目录中已有的最大版本号。go-kit 模块的迁移在 go-kit 中只按 0001、0002 排序，
// 发布时才获得版本号；复制过来的文件归产品所有，可以审阅、修改后提交。
//
// 普通实体表按以下模板创建，created_at、updated_at 紧跟在 id 之后，其后是业务字段。
// 主键与时间戳的默认值供手写 SQL 使用，通过 bun 插入时由应用填充：
//
//	-- +goose Up
//	CREATE TABLE orders (
//		id         uuid PRIMARY KEY DEFAULT uuidv7(),
//		created_at timestamptz NOT NULL DEFAULT now(),
//		updated_at timestamptz NOT NULL DEFAULT now(),
//		sku        text NOT NULL
//	);
//
//	-- +goose Down
//	DROP TABLE orders;
//
// 只追加的表（对应 CreatedModel）去掉 updated_at；自然主键表、关联表按需定义。
//
// # updated_at 触发器（可选）
//
// Model 的 hook 只在通过模型更新时生效。某张表经常被 Column 部分更新、Set 或手写 SQL 修改时，
// 产品可以为它加触发器兜底：本次 UPDATE 没有改动 updated_at 时设为当前事务的开始时间。
// go-kit 自己的表不依赖这个触发器。
//
//	-- +goose StatementBegin
//	CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
//	BEGIN
//		IF NEW.updated_at IS NOT DISTINCT FROM OLD.updated_at THEN
//			NEW.updated_at = now();
//		END IF;
//		RETURN NEW;
//	END;
//	$$;
//	-- +goose StatementEnd
//
//	CREATE TRIGGER orders_set_updated_at BEFORE UPDATE ON orders
//		FOR EACH ROW EXECUTE FUNCTION set_updated_at();
//
// [bun]: https://bun.uptrace.dev
// [pgx]: https://github.com/jackc/pgx
// [goose]: https://github.com/pressly/goose
package database
