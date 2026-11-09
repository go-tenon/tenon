// Package i18n 是多语言：消息目录、确定每个请求的语言、按语言翻译消息。
//
// 基于 github.com/nicksnyder/go-i18n（CLDR 复数规则、模板参数）。go-kit 自带 zh-CN（默认语言）与 en。
//
// # 消息
//
// 消息文件为 YAML，文件名是语言标签，例如 locales/zh-CN.yaml：
//
//	account.email_taken: 该邮箱已注册
//	export.done: "导出完成：{{.File}}"
//	notes.count:
//	  one: "{{.Count}} 条笔记"
//	  other: "{{.Count}} 条笔记"
//
// 消息 ID 以模块名开头。go-kit 的包在 import 时（init）把自己的消息加入默认目录；
// 产品用 Load 加载自己的消息，后加载的同名消息覆盖先加载的，因此产品可以改写 go-kit 的任何文字。
//
// # 语言
//
// Middleware 为每个请求确定语言：lang Cookie（用户在页面上切换）→ Accept-Language 头 → 默认语言。
// 没有请求的场景（发邮件、异步通知）用 WithLocale 指定收件人的语言。
//
//	i18n.T(ctx, "account.email_taken")
//	i18n.T(ctx, "export.done", map[string]any{"File": name})
package i18n

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"sync"

	"github.com/cockroachdb/errors"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"go.yaml.in/yaml/v4"
	"golang.org/x/text/language"
)

// DefaultLocale 是默认语言，也是找不到对应语言的消息时的兜底。
const DefaultLocale = "zh-CN"

// CookieName 是保存用户所选语言的 Cookie。
const CookieName = "lang"

var (
	mu      sync.RWMutex
	bundle  = newBundle()
	matcher language.Matcher
	tags    []language.Tag
)

func newBundle() *goi18n.Bundle {
	b := goi18n.NewBundle(language.MustParse(DefaultLocale))
	b.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)
	return b
}

//go:embed locales/*.yaml
var locales embed.FS

func init() { MustLoad(locales) }

// Load 加载 fsys 中所有 *.yaml 消息文件（可以在子目录中），文件名为语言标签，如 en.yaml、zh-CN.yaml。
// 已存在的同名消息被覆盖。应在开始处理请求之前调用。
func Load(fsys fs.FS) error {
	mu.Lock()
	defer mu.Unlock()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".yaml" {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if _, err := bundle.ParseMessageFileBytes(data, path.Base(p)); err != nil {
			return errors.Wrapf(err, "i18n: load %s", p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	tags = bundle.LanguageTags()
	matcher = language.NewMatcher(tags)
	return nil
}

// MustLoad 与 Load 相同，出错时 panic。供包的 init 使用。
func MustLoad(fsys fs.FS) {
	if err := Load(fsys); err != nil {
		panic(err)
	}
}

// Locales 返回已加载的语言，第一个是默认语言。
func Locales() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = t.String()
	}
	return out
}

type localeKey struct{}

// WithLocale 返回使用 locale 的 context。locale 为空或不支持时使用最接近的语言或默认语言。
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeKey{}, Match(locale))
}

// Locale 返回 ctx 的语言，没有设置时为 DefaultLocale。
func Locale(ctx context.Context) string {
	if l, ok := ctx.Value(localeKey{}).(string); ok {
		return l
	}
	return DefaultLocale
}

// Match 返回已加载的语言中与 preferred（可以是 Accept-Language 的格式，按顺序优先）最匹配的一个。
func Match(preferred ...string) string {
	mu.RLock()
	defer mu.RUnlock()
	var want []language.Tag
	for _, p := range preferred {
		if p == "" {
			continue
		}
		parsed, _, err := language.ParseAcceptLanguage(p)
		if err == nil {
			want = append(want, parsed...)
		}
	}
	if len(want) == 0 {
		return DefaultLocale
	}
	_, i, conf := matcher.Match(want...)
	if conf == language.No {
		return DefaultLocale
	}
	return tags[i].String()
}

// T 按 ctx 的语言翻译消息 id。data 为模板参数；其中的 Count 键同时用于选择复数形式。
// 当前语言没有该消息时使用默认语言的，都没有时返回 id 本身。
func T(ctx context.Context, id string, data ...map[string]any) string {
	return Translate(Locale(ctx), id, data...)
}

// Translate 按 locale 翻译消息 id，见 T。
func Translate(locale, id string, data ...map[string]any) string {
	cfg := &goi18n.LocalizeConfig{MessageID: id}
	if len(data) > 0 && data[0] != nil {
		cfg.TemplateData = data[0]
		if c, ok := data[0]["Count"]; ok {
			cfg.PluralCount = c
		}
	}
	mu.RLock()
	l := goi18n.NewLocalizer(bundle, locale, DefaultLocale)
	s, err := l.Localize(cfg)
	mu.RUnlock()
	if err != nil && s == "" {
		return id
	}
	return s
}

// Has 报告是否存在消息 id（任何语言）。
func Has(id string) bool {
	return Translate(DefaultLocale, id) != id
}

// Middleware 确定请求的语言并放入 context：lang Cookie → Accept-Language → 默认语言。
// 响应带 Content-Language 头。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var prefs []string
		if c, err := r.Cookie(CookieName); err == nil {
			prefs = append(prefs, c.Value)
		}
		prefs = append(prefs, r.Header.Get("Accept-Language"))
		locale := Match(prefs...)
		w.Header().Set("Content-Language", locale)
		w.Header().Add("Vary", "Accept-Language")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localeKey{}, locale)))
	})
}

// SetLocale 在响应中设置 lang Cookie，记住用户选择的语言（一年）。
func SetLocale(w http.ResponseWriter, locale string) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: Match(locale), Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
