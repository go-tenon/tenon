package rest

import (
	"reflect"
	"sync"

	"github.com/go-tenon/tenon/contract"
)

// schemaNames 生成 OpenAPI 中的 schema 名，具名类型按 contract.SchemaName 命名，例如 article.Changes 为 ArticleChanges。
//
// 匿名类型使用 Huma 给出的 hint。输入、输出类型中匿名结构体字段的 hint 是外层类型名加字段名，
// 例如 article.RejectInput 的匿名 Body 为 RejectInputBody；Register 事先用 bodies 登记带包名的名称
// ArticleRejectInputBody，comment.RejectInput 的匿名 Body 则是 CommentRejectInputBody。
type schemaNames struct {
	mu   sync.Mutex
	anon map[reflect.Type]string
}

// bodies 为 types 中匿名结构体字段的类型登记名称：外层类型的 schema 名加字段名。
func (n *schemaNames) bodies(types ...reflect.Type) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, t := range types {
		t = deref(t)
		if t.Kind() != reflect.Struct || t.Name() == "" {
			continue
		}
		for _, f := range reflect.VisibleFields(t) {
			ft := deref(f.Type)
			if ft.Kind() != reflect.Struct || ft.Name() != "" {
				continue
			}
			if _, ok := n.anon[ft]; !ok {
				n.anon[ft] = contract.SchemaName(t, "") + f.Name
			}
		}
	}
}

// name 返回类型 t 的 schema 名称：匿名类型按 hint 命名，同一个匿名类型总是得到同一个名称。
func (n *schemaNames) name(t reflect.Type, hint string) string {
	if deref(t).Name() == "" {
		n.mu.Lock()
		name, ok := n.anon[deref(t)]
		n.mu.Unlock()
		if ok {
			return name
		}
	}
	return contract.SchemaName(t, hint)
}

// deref 去掉 t 的全部指针，返回元素类型。
func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
