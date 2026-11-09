package i18n_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/ryan053857/go-kit/i18n"
)

func init() {
	i18n.MustLoad(fstest.MapFS{
		"locales/zh-CN.yaml": {Data: []byte("test.hello: \"你好，{{.Name}}\"\ntest.items:\n  other: \"{{.Count}} 项\"\ntest.only_zh: 只有中文\n")},
		"locales/en.yaml":    {Data: []byte("test.hello: \"Hello, {{.Name}}\"\ntest.items:\n  one: \"{{.Count}} item\"\n  other: \"{{.Count}} items\"\n")},
	})
}

func TestTranslate(t *testing.T) {
	ctx := context.Background()
	en := i18n.WithLocale(ctx, "en-US")
	for _, tc := range []struct {
		ctx  context.Context
		id   string
		data map[string]any
		want string
	}{
		{ctx, "test.hello", map[string]any{"Name": "小明"}, "你好，小明"},
		{en, "test.hello", map[string]any{"Name": "Bob"}, "Hello, Bob"},
		{en, "test.items", map[string]any{"Count": 1}, "1 item"},
		{en, "test.items", map[string]any{"Count": 3}, "3 items"},
		{en, "test.only_zh", nil, "只有中文"}, // 缺少翻译时用默认语言
		{en, "test.missing", nil, "test.missing"},
		{en, "error.internal", nil, "Something went wrong. Please try again later."},
	} {
		if got := i18n.T(tc.ctx, tc.id, tc.data); got != tc.want {
			t.Errorf("T(%s, %s) = %q, want %q", i18n.Locale(tc.ctx), tc.id, got, tc.want)
		}
	}
	if !i18n.Has("test.hello") || i18n.Has("test.missing") {
		t.Error("Has")
	}
	if l := i18n.Locales(); l[0] != i18n.DefaultLocale {
		t.Errorf("Locales = %v", l)
	}
}

func TestMatch(t *testing.T) {
	for in, want := range map[string]string{
		"":                     "zh-CN",
		"en":                   "en",
		"en-GB,en;q=0.8":       "en",
		"zh-TW":                "zh-CN",
		"fr-FR":                "zh-CN",
		"fr;q=0.9, en;q=0.8":   "en",
		"zh-Hans-CN;q=0.9, en": "en",
		"garbage;;;":           "zh-CN",
	} {
		if got := i18n.Match(in); got != want {
			t.Errorf("Match(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMiddleware(t *testing.T) {
	var got string
	h := i18n.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = i18n.Locale(r.Context())
	}))
	do := func(accept string, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Accept-Language", accept)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: cookie})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("en-US,en;q=0.9", ""); got != "en" || rec.Header().Get("Content-Language") != "en" {
		t.Errorf("accept-language: %q", got)
	}
	if do("en", "zh-CN"); got != "zh-CN" {
		t.Errorf("cookie should win: %q", got)
	}
	if do("", ""); got != "zh-CN" {
		t.Errorf("default: %q", got)
	}

	rec := httptest.NewRecorder()
	i18n.SetLocale(rec, "en-US")
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Value != "en" {
		t.Errorf("SetLocale cookie = %v", c)
	}
}
