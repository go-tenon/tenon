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
	e := enum.New("article.status", draft, published)
	if e.Name() != "ArticleStatus" || e.Prefix() != "article.status" || e.MessageID(draft) != "article.status.draft" {
		t.Fatalf("name %q prefix %q", e.Name(), e.Prefix())
	}
	if !e.Valid(published) || e.Valid("archived") || len(e.Values()) != 2 || e.Strings()[1] != "published" {
		t.Fatalf("values %q", e.Values())
	}
	if got := enum.New("order.payment_state", status("paid")).Name(); got != "OrderPaymentState" {
		t.Errorf("name %q", got)
	}

	bundle := i18n.MustNew("zh-CN", fstest.MapFS{"zh-CN.yaml": {Data: []byte("article.status.draft: 草稿\n")}})
	ctx := i18n.WithLocale(context.Background(), bundle, "zh-CN")
	if got := e.Label(ctx, draft); got != "草稿" {
		t.Errorf("label %q", got)
	}
	// 没有翻译、或 context 中没有语言时返回取值本身。
	if got := e.Label(ctx, published); got != "published" {
		t.Errorf("untranslated label %q", got)
	}
	if got := e.Label(context.Background(), draft); got != "draft" {
		t.Errorf("label without locale %q", got)
	}
}

func TestNewPanics(t *testing.T) {
	for name, f := range map[string]func(){
		"bad prefix": func() { enum.New("Article.Status", draft) },
		"no values":  func() { enum.New[status]("article.status") },
		"empty":      func() { enum.New("article.status", draft, "") },
		"duplicate":  func() { enum.New("article.status", draft, draft) },
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
