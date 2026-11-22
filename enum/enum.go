// Package enum 声明带显示文字的枚举：一组字符串取值，以及源语言的默认显示文字和按语言翻译的显示文字。
//
// 每个取值在定义处可直接附带源语言的默认显示文字；多语言应用在翻译文件中用 <前缀>.<取值> 覆盖它：
//
//	type Status string
//
//	const (
//	    Draft     Status = "draft"
//	    Published Status = "published"
//	)
//
//	var Statuses = enum.New("article.status",
//	    enum.Item(Draft, "草稿"),
//	    enum.Item(Published, "已发布"),
//	)
//
//	// locales/en.yaml
//	// article.status.draft: Draft
//	// article.status.published: Published
//
// JSON API 中的类型用 contract.EnumSchema 声明为 OpenAPI 的命名类型，生成的客户端得到取值和各语言的显示文字。
package enum

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/go-tenon/tenon/i18n"
)

// prefixPattern 是消息前缀：以点分隔的若干段，每段以小写字母开头，由小写字母、数字和下划线组成。
var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// Definition 是不带类型参数的枚举，*Enum 实现它，供 contract.EnumSchema 等只需要名称、取值和默认显示文字的地方使用。
type Definition interface {
	Name() string
	Prefix() string
	Strings() []string
	DefaultLabels() map[string]string
}

var _ Definition = (*Enum[string])(nil)

// Entry 是枚举的一项：取值和可选的源语言默认显示文字。
type Entry[T ~string] struct {
	Value T      // 取值
	Label string // 源语言的默认显示文字；为空时只从翻译文件读取
}

// Item 构造一条枚举项：value 是取值，label 可选指定源语言的默认显示文字；传入多条文字时 panic。
func Item[T ~string](value T, label ...string) Entry[T] {
	if len(label) > 1 {
		panic(fmt.Sprintf("enum: Item(%q) accepts at most one default label, got %d", value, len(label)))
	}
	var l string
	if len(label) == 1 {
		l = label[0]
	}
	return Entry[T]{Value: value, Label: l}
}

// Enum 是一个枚举：取值按声明的顺序排列。可以并发使用。
type Enum[T ~string] struct {
	prefix string
	values []T
	labels map[string]string
}

// New 创建前缀为 prefix 的枚举，items 是全部枚举项，按显示的顺序排列。prefix 不合法、没有取值、取值为空或重复时 panic：
// 它们是在代码中写死的声明，出错是编程错误。
func New[T ~string](prefix string, items ...Entry[T]) *Enum[T] {
	if !prefixPattern.MatchString(prefix) {
		panic(fmt.Sprintf("enum: prefix %q must be dot-separated lowercase segments, e.g. article.status", prefix))
	}
	if len(items) == 0 {
		panic(fmt.Sprintf("enum: %s has no values", prefix))
	}
	values := make([]T, len(items))
	var labels map[string]string
	for i, it := range items {
		if it.Value == "" || slices.Contains(values[:i], it.Value) {
			panic(fmt.Sprintf("enum: %s has an empty or duplicate value %q", prefix, it.Value))
		}
		values[i] = it.Value
		if it.Label != "" {
			if labels == nil {
				labels = make(map[string]string, len(items))
			}
			labels[string(it.Value)] = it.Label
		}
	}
	return &Enum[T]{prefix: prefix, values: values, labels: labels}
}

// Prefix 返回显示文字的消息前缀，例如 article.status。
func (e *Enum[T]) Prefix() string { return e.prefix }

// Name 返回由前缀得出的类型名，例如 article.status 得到 ArticleStatus，用作 OpenAPI 中的命名类型。
func (e *Enum[T]) Name() string {
	var b strings.Builder
	for _, seg := range strings.FieldsFunc(e.prefix, func(r rune) bool { return r == '.' || r == '_' }) {
		r := []rune(seg)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

// Values 返回全部取值，按声明的顺序。
func (e *Enum[T]) Values() []T { return slices.Clone(e.values) }

// Strings 返回全部取值的字符串形式，按声明的顺序。
func (e *Enum[T]) Strings() []string {
	out := make([]string, len(e.values))
	for i, v := range e.values {
		out[i] = string(v)
	}
	return out
}

// DefaultLabels 返回每个取值附带的源语言默认显示文字的浅拷贝；都没有附带时返回 nil。
func (e *Enum[T]) DefaultLabels() map[string]string {
	return maps.Clone(e.labels)
}

// Valid 报告 v 是否是其中的一个取值。
func (e *Enum[T]) Valid(v T) bool { return slices.Contains(e.values, v) }

// MessageID 返回 v 的显示文字的消息 ID，例如 article.status.draft。
func (e *Enum[T]) MessageID(v T) string { return e.prefix + "." + string(v) }

// Label 按 ctx 中的语言返回 v 的显示文字（见 i18n.T）；翻译文件中没有时回退到定义时附带的源语言默认显示文字，
// 都没有时返回 v 本身。
func (e *Enum[T]) Label(ctx context.Context, v T) string {
	id := e.MessageID(v)
	if s := i18n.T(ctx, id, nil); s != id {
		return s
	}
	if s := e.labels[string(v)]; s != "" {
		return s
	}
	return string(v)
}
