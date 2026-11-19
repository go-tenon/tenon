package database

import (
	"context"
	"reflect"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// Timestamps 是实体表在 id 之后的 created_at、updated_at 两列，嵌入模型中使用，由 Bun 的钩子写入：
//
//	type Article struct {
//		bun.BaseModel `bun:"table:articles"`
//
//		ID uuid.UUID `bun:"id,pk"`
//		database.Timestamps
//		Title string `bun:"title,notnull"`
//	}
//
// 插入时 CreatedAt 为零值就填当前时间，UpdatedAt 为零值就取 CreatedAt；传入模型的更新（Model(&a)）刷新 UpdatedAt，
// Model((*Article)(nil)).Set(...) 这种没有模型实例的更新在 SET 中加上 updated_at，语句中不能再 Set("updated_at = ?")，否则同一列赋值两次，PostgreSQL 报错。
// 两种写法需要自己写入 updated_at：Model(&a).Column(...) 的列中带上 "updated_at"，Model(&a).Set(...) 加上 Set("updated_at = ?", ...)。
// 原生 SQL 不经过钩子。时间取 UTC，截到微秒，与 PostgreSQL 保存的精度相同，写入后的模型与再读出的值相等。
type Timestamps struct {
	CreatedAt time.Time `bun:"created_at,notnull,skipupdate" json:"created_at"`
	UpdatedAt time.Time `bun:"updated_at,notnull" json:"updated_at"`
}

var (
	_ bun.BeforeAppendModelHook = (*Timestamps)(nil)
	_ bun.BeforeUpdateHook      = (*Timestamps)(nil)
)

// BeforeAppendModel 在 Bun 用模型实例生成插入、更新语句之前填写时间：插入时填空的 CreatedAt、UpdatedAt，更新时刷新 UpdatedAt。
func (t *Timestamps) BeforeAppendModel(_ context.Context, query bun.Query) error {
	if !stamped(query) {
		return nil
	}
	switch query.(type) {
	case *bun.InsertQuery:
		if t.CreatedAt.IsZero() {
			t.CreatedAt = now()
		}
		if t.UpdatedAt.IsZero() {
			t.UpdatedAt = t.CreatedAt
		}
	case *bun.UpdateQuery:
		t.UpdatedAt = now()
	}
	return nil
}

// BeforeUpdate 在没有模型实例的更新（Model((*T)(nil))）的 SET 中加上 updated_at。
// 有模型实例时由 BeforeAppendModel 刷新字段：这时再加 SET 会让 Bun 只更新 SET 中的列。
func (*Timestamps) BeforeUpdate(_ context.Context, query *bun.UpdateQuery) error {
	if !stamped(query) {
		return nil
	}
	if v := reflect.ValueOf(query.GetModel().Value()); v.Kind() == reflect.Pointer && v.IsNil() {
		query.Set("updated_at = ?", now())
	}
	return nil
}

// stamped 报告 query 的表有 Timestamps 的两列。Go 把嵌入字段的方法提升到外层结构体，
// 外层用 bun:"-" 嵌入一个带 Timestamps 的模型时也带着这两个钩子，它的表却没有这两列，这时钩子什么也不做。
func stamped(query schema.Query) bool {
	m, ok := query.GetModel().(interface{ Table() *schema.Table })
	return ok && m.Table().HasField("created_at") && m.Table().HasField("updated_at")
}

// now 返回写入时间列的当前时间：UTC，截到微秒。
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
