package webhook

import (
	"context"
	"reflect"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// Event 声明一种事件：Name 是事件类型，端点按它订阅；T 是事件内容，编码为 JSON 后放在请求体的 data 中。
// 在包级变量中声明，用 Send 发送，用 Document 写入 OpenAPI 文档：
//
//	var Published = webhook.Event[PublishedHook]{Name: "article.published", Summary: "文章发布后推送"}
type Event[T any] struct {
	Name    string // 事件类型，例如 article.published
	Summary string // 一句话说明，写入 OpenAPI 文档
}

// Send 把事件推送给 subject 所在租户中订阅了它的端点：为每个端点写入一条投递记录，
// 并在 db 中发布投递任务 Deliver。db 通常是业务修改所在的事务，事务提交后才会投递，回滚时一起取消。
// 没有端点订阅时什么也不写。
func (e Event[T]) Send(ctx context.Context, db bun.IDB, subject identity.Subject, data T) error {
	return send(ctx, db, subject, e.Name, data)
}

// describe 返回事件的名称、说明和数据类型。
func (e Event[T]) describe() description {
	return description{name: e.Name, summary: e.Summary, data: reflect.TypeFor[T]()}
}

// Documented 是可以写入 OpenAPI 文档的事件，由 Event 实现。
type Documented interface {
	describe() description
}

// description 是一种事件的名称、说明和数据类型。
type description struct {
	name, summary string
	data          reflect.Type
}
