// Package database 约定产品访问 Postgres 的方式：[bun] 作为查询构造器，
// [pgx] 作为驱动，[goose] 管理 SQL 迁移，所有表使用 UUIDv7 主键与
// created_at、updated_at 时间戳。要求 Postgres 18 及以上（内置 uuidv7()）。
//
// 本包只提供：
//
//	Config / Open       连接池默认值、DSN 打码、启动时 Ping
//	Model               id、created_at、updated_at 三个公共字段及其自动填充
//	Migrate             带 advisory lock 的 goose Up，多实例同时启动也安全
//	IsUniqueViolation   判断唯一约束冲突
//
// 查询、事务、关联等直接使用 bun 的 API，本包不做包装。
//
// # 启动
//
// 产品在 main 中显式组装：
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
// 迁移文件放在产品的 migrations 包中，用 embed 打包进二进制：
//
//	migrations/
//		migrations.go          package migrations; //go:embed *.sql; var FS embed.FS
//		00001_init.sql
//		00002_create_users.sql
//
// 第一个迁移创建维护 updated_at 的触发器函数。Model 的 hook 只在通过模型更新全部列时生效，
// 触发器兜底处理 Column 部分更新、Set、批量更新与手写 SQL：本次 UPDATE 没有改动
// updated_at 时，设为当前事务的开始时间。
//
//	-- +goose Up
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
//	-- +goose Down
//	DROP FUNCTION set_updated_at();
//
// 之后每张表按以下模板创建。主键与时间戳的默认值供手写 SQL 使用，通过 bun 插入时由应用填充：
//
//	-- +goose Up
//	CREATE TABLE users (
//		id         uuid PRIMARY KEY DEFAULT uuidv7(),
//		created_at timestamptz NOT NULL DEFAULT now(),
//		updated_at timestamptz NOT NULL DEFAULT now(),
//		email      text NOT NULL UNIQUE
//	);
//	CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
//		FOR EACH ROW EXECUTE FUNCTION set_updated_at();
//
//	-- +goose Down
//	DROP TABLE users;
//
// 创建迁移文件、回滚、查看状态使用 goose 命令行。用 go run 指定版本运行，与 go.mod 中的
// goose 库版本保持一致（不使用 go.mod 的 tool 指令，它会把 goose 支持的所有数据库驱动加入依赖）：
//
//	export GOOSE_DRIVER=postgres GOOSE_DBSTRING=$DATABASE_URL GOOSE_MIGRATION_DIR=migrations
//	go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 create -s create_users sql
//	go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 status
//	go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 down
//
// -s 使用连续编号（00001、00002……）。两个分支各自新增迁移时会出现重复编号，
// goose 会报错拒绝执行，合并时改号即可。
//
// [bun]: https://bun.uptrace.dev
// [pgx]: https://github.com/jackc/pgx
// [goose]: https://github.com/pressly/goose
package database
