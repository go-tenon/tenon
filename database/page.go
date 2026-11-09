package database

import (
	"context"
	"fmt"
	"reflect"
	"uuid"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// Cursor 是游标分页的参数：返回 ID 小于 Before 的记录（UUIDv7 主键即创建时间更早），最多 Limit 条。
type Cursor struct {
	Before uuid.UUID // 零值表示第一页
	Limit  int       // <= 0 时为 20，最大 100
}

// ParseCursor 解析 rest.PageInput 的 before 与 limit，before 无效时视为第一页。
func ParseCursor(before string, limit int) Cursor {
	id, _ := uuid.Parse(before)
	return Cursor{Before: id, Limit: limit}
}

// Paginate 按主键 id 倒序执行 q 并扫描到 dest（*[]T），返回下一页的游标（没有更多时为零值）。
// q 的模型须以 id（UUIDv7）为主键。
//
//	var logs []Log
//	next, err := database.Paginate(ctx, db.NewSelect().Model(&logs).Where("actor_id = ?", id), &logs, c)
func Paginate[T any](ctx context.Context, q *bun.SelectQuery, dest *[]T, c Cursor) (next uuid.UUID, err error) {
	limit := c.Limit
	switch {
	case limit <= 0:
		limit = 20
	case limit > 100:
		limit = 100
	}
	if c.Before != uuid.Nil() {
		q = q.Where("?TableAlias.id < ?", c.Before)
	}
	if err := q.OrderExpr("?TableAlias.id DESC").Limit(int64(limit + 1)).Scan(ctx); err != nil {
		return uuid.Nil(), errors.Wrap(err, "database: paginate")
	}
	if len(*dest) > limit {
		*dest = (*dest)[:limit]
		next = idOf((*dest)[limit-1])
	}
	return next, nil
}

// idOf 返回记录的主键：嵌入 Model 或 CreatedModel 的结构体，或者有 uuid.UUID 类型 ID 字段的结构体。
func idOf(v any) uuid.UUID {
	rv := reflect.Indirect(reflect.ValueOf(v))
	if rv.Kind() == reflect.Struct {
		if f := rv.FieldByName("ID"); f.IsValid() {
			if id, ok := f.Interface().(uuid.UUID); ok {
				return id
			}
		}
	}
	panic(fmt.Sprintf("database: %T has no uuid.UUID ID field", v))
}
