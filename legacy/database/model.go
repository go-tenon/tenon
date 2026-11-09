package database

import (
	"context"
	"time"
	"uuid"

	"github.com/uptrace/bun"
)

// Model 是所有表共用的主键与时间戳字段，嵌入到产品的模型中：
//
//	type User struct {
//		bun.BaseModel `bun:"table:users"`
//		database.Model
//
//		Email string `bun:"email,notnull"`
//	}
//
// 通过 bun 的 BeforeAppendModel hook 自动填充：
//
//	INSERT  ID 为零值时生成 UUIDv7；CreatedAt、UpdatedAt 为零值时设为当前时间
//	UPDATE  UpdatedAt 设为当前时间
//
// hook 只修改结构体字段，只有写入 SQL 的列才会生效：用 Column 只更新部分列时要把
// "updated_at" 一并列上；用 Set、Table 而不经过模型更新时需自己设置 updated_at
// （或按包文档为该表加触发器兜底）。
//
// 只追加、不更新的表（日志、一次性令牌等）用 CreatedModel；
// 自然主键表、关联表等不嵌入，直接定义自己的字段。
//
// 时间截断到微秒，与 Postgres timestamptz 的精度一致，写入后再读出的值与结构体相等。
//
// 产品模型如果自己实现了 BeforeAppendModel，会遮蔽本方法，需要在其中显式调用
// m.Model.BeforeAppendModel(ctx, query)。
type Model struct {
	ID        uuid.UUID `bun:"id,pk,type:uuid"`
	CreatedAt time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp"`
	UpdatedAt time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp"`
}

var _ bun.BeforeAppendModelHook = (*Model)(nil)

// BeforeAppendModel 实现 bun.BeforeAppendModelHook。
func (m *Model) BeforeAppendModel(_ context.Context, query bun.Query) error {
	switch query.(type) {
	case *bun.InsertQuery:
		if m.ID == uuid.Nil() {
			m.ID = uuid.NewV7()
		}
		if m.CreatedAt.IsZero() {
			m.CreatedAt = now()
		}
		if m.UpdatedAt.IsZero() {
			m.UpdatedAt = m.CreatedAt
		}
	case *bun.UpdateQuery:
		m.UpdatedAt = now()
	}
	return nil
}

// now 返回截断到微秒的当前时间（同时去掉单调时钟读数）。
func now() time.Time {
	return time.Now().Truncate(time.Microsecond)
}

// CreatedModel 是只追加、不更新的表使用的主键与创建时间字段，例如审计日志、一次性令牌。
// INSERT 时 ID 为零值则生成 UUIDv7，CreatedAt 为零值则设为当前时间。
type CreatedModel struct {
	ID        uuid.UUID `bun:"id,pk,type:uuid"`
	CreatedAt time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp"`
}

var _ bun.BeforeAppendModelHook = (*CreatedModel)(nil)

// BeforeAppendModel 实现 bun.BeforeAppendModelHook。
func (m *CreatedModel) BeforeAppendModel(_ context.Context, query bun.Query) error {
	if _, ok := query.(*bun.InsertQuery); ok {
		if m.ID == uuid.Nil() {
			m.ID = uuid.NewV7()
		}
		if m.CreatedAt.IsZero() {
			m.CreatedAt = now()
		}
	}
	return nil
}
