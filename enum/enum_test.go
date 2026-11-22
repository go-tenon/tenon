package enum_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/i18n"
)

type status string

const (
	draft     status = "draft"
	published status = "published"
)

func TestEnum(t *testing.T) {
	t.Parallel()
	e := enum.New("article.status", enum.Item(draft, "草稿"), enum.Item(published))
	if e.Name() != "ArticleStatus" || e.Prefix() != "article.status" || e.MessageID(draft) != "article.status.draft" {
		t.Fatalf("name %q prefix %q", e.Name(), e.Prefix())
	}
	if !e.Valid(published) || e.Valid("archived") || len(e.Values()) != 2 || e.Strings()[1] != "published" {
		t.Fatalf("values %q", e.Values())
	}
	if got := enum.New("order.payment_state", enum.Item(status("paid"), "已支付")).Name(); got != "OrderPaymentState" {
		t.Errorf("name %q", got)
	}
	if got := e.DefaultLabels(); len(got) != 1 || got["draft"] != "草稿" {
		t.Errorf("default labels %v", got)
	}

	// 翻译文件中的译文覆盖默认显示文字。
	bundle := i18n.MustNew("zh-CN", fstest.MapFS{"en.yaml": {Data: []byte("article.status.draft: Draft\n")}})
	enCtx := i18n.WithLocale(context.Background(), bundle, "en")
	if got := e.Label(enCtx, draft); got != "Draft" {
		t.Errorf("translated label %q", got)
	}
	// 没有翻译但有默认显示文字时使用默认显示文字（包括 context 未设置语言时）。
	zhCtx := i18n.WithLocale(context.Background(), bundle, "zh-CN")
	if got := e.Label(zhCtx, draft); got != "草稿" {
		t.Errorf("default label %q", got)
	}
	if got := e.Label(context.Background(), draft); got != "草稿" {
		t.Errorf("default label without locale %q", got)
	}
	// 既没有翻译也没有默认显示文字时返回取值本身。
	if got := e.Label(zhCtx, published); got != "published" {
		t.Errorf("untranslated label %q", got)
	}
}

func TestNewPanics(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]func(){
		"bad prefix":      func() { enum.New("Article.Status", enum.Item(draft)) },
		"no values":       func() { enum.New[status]("article.status") },
		"empty":           func() { enum.New("article.status", enum.Item(draft), enum.Item(status(""))) },
		"duplicate":       func() { enum.New("article.status", enum.Item(draft), enum.Item(draft)) },
		"multiple labels": func() { enum.Item(draft, "a", "b") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}
