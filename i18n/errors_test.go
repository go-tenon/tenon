package i18n_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/i18n"
)

func TestErrorMessage(t *testing.T) {
	app := yamlFS(map[string]string{
		"en.yaml":    "article.locked: Article {{.title}} is locked\narticle.en_only: English only\n",
		"zh-CN.yaml": "article.locked: 文章《{{.title}}》已锁定\n",
	})
	locked := errorx.Define(errorx.Conflict, "article.locked")
	enOnly := errorx.Define(errorx.Conflict, "article.en_only")
	missing := errorx.Define(errorx.Conflict, "article.missing")
	inline := errorx.Define(errorx.Conflict, "article.slug_taken", "slug“{{.slug}}”已被使用")
	inlineOverridden := errorx.Define(errorx.Conflict, "article.locked", "默认锁定文案")
	zh := withLocale(t, "zh-CN", "en", app)
	zhDefault := withLocale(t, "zh-CN", "zh-CN")
	enInZhApp := withLocale(t, "en", "zh-CN", app)

	for name, tc := range map[string]struct {
		ctx         context.Context
		err         error
		msg         string
		missingWant bool
	}{
		"translated":               {zh, errors.Wrap(locked.New(errorx.Public("title", "Go")), "publish"), "文章《Go》已锁定", false},
		"default language":         {zh, enOnly.New(), "English only", true},
		"no translation":           {zh, missing.New(), "", true},
		"no code":                  {zh, errors.New("boom"), "", false},
		"no locale":                {context.Background(), locked.New(), "", false},
		"no locale inline":         {context.Background(), inline.New(errorx.Public("slug", "hi")), "slug“hi”已被使用", false},
		"builtin framework":        {zh, errorx.Define(errorx.Unauthenticated, "auth.unauthenticated").New(), "请先认证", false},
		"inline default locale":    {zhDefault, inline.New(errorx.Public("slug", "go")), "slug“go”已被使用", false},
		"inline other locale miss": {enInZhApp, inline.New(errorx.Public("slug", "go")), "slug“go”已被使用", true},
		"inline overridden by fs":  {zh, inlineOverridden.New(errorx.Public("title", "Go")), "文章《Go》已锁定", false},
	} {
		msg, miss := i18n.ErrorMessage(tc.ctx, tc.err)
		if msg != tc.msg || miss != tc.missingWant {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", name, msg, miss, tc.msg, tc.missingWant)
		}
	}
}
