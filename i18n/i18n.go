package i18n

import (
	"context"
	"embed"
	"io/fs"
	"path"
	"strings"

	"github.com/cockroachdb/errors"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"go.yaml.in/yaml/v4"
	"golang.org/x/text/language"
)

// 内置语言标签。
const (
	LocaleZhCN = "zh-CN"
	LocaleEn   = "en"
)

//go:embed locales/*.yaml
var builtin embed.FS

// New 创建以 defaultLocale 为默认语言的消息目录。只启用 defaultLocale 和 sources 中实际出现的语言：
// 框架内置消息只加载这些语言中匹配的部分，不会扩大可协商的语言集合；随后按顺序加载 sources 中的
// YAML 翻译文件（文件名即语言标签，如 en.yaml），后加载的同名消息覆盖先加载的。
// 返回的 Bundle 应在处理请求前完成全部修改。
func New(defaultLocale string, sources ...fs.FS) (*goi18n.Bundle, error) {
	tag, err := language.Parse(defaultLocale)
	if err != nil {
		return nil, errors.Wrapf(err, "i18n: default locale %q", defaultLocale)
	}
	// 先解析应用翻译文件，收集应用实际启用的语言集合。
	allowed := []language.Tag{tag}
	seen := map[language.Tag]bool{tag: true}
	var appFiles []*goi18n.MessageFile
	for _, src := range sources {
		files, err := parseFS(src)
		if err != nil {
			return nil, err
		}
		for _, mf := range files {
			if !seen[mf.Tag] {
				seen[mf.Tag] = true
				allowed = append(allowed, mf.Tag)
			}
			appFiles = append(appFiles, mf)
		}
	}
	b := goi18n.NewBundle(tag)
	// 框架内置消息只为 allowed 中匹配的语言加载，避免单语言项目被协商到其他内置语言。
	builtinFiles, err := parseFS(builtin)
	if err != nil {
		return nil, err
	}
	for _, mf := range builtinFiles {
		for _, target := range allowed {
			if builtinMatches(mf.Tag, target) {
				if err := b.AddMessages(target, mf.Messages...); err != nil {
					return nil, errors.Wrapf(err, "i18n: load builtin %s", mf.Path)
				}
			}
		}
	}
	for _, mf := range appFiles {
		if err := b.AddMessages(mf.Tag, mf.Messages...); err != nil {
			return nil, errors.Wrapf(err, "i18n: load %s", mf.Path)
		}
	}
	return b, nil
}

// MustNew 与 New 相同，出错时 panic。用于嵌入程序的翻译文件：它们在编译时就确定了，
// 出错是构建的错误，可以在构造路由时直接加载：
//
//	router.Use(httpx.Defaults(cfg.HTTP, httpx.Languages(i18n.MustNew("zh-CN", locales.FS)))...)
func MustNew(defaultLocale string, sources ...fs.FS) *goi18n.Bundle {
	b, err := New(defaultLocale, sources...)
	if err != nil {
		panic(err)
	}
	return b
}

// yamlUnmarshalers 是解析 YAML 翻译文件所用的反序列化映射。
var yamlUnmarshalers = map[string]goi18n.UnmarshalFunc{"yaml": yaml.Unmarshal, "yml": yaml.Unmarshal}

// parseFS 读取并解析 src 中的全部 YAML 翻译文件。
func parseFS(src fs.FS) ([]*goi18n.MessageFile, error) {
	var files []*goi18n.MessageFile
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := strings.ToLower(path.Ext(p)); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		mf, err := goi18n.ParseMessageFileBytes(data, path.Base(p), yamlUnmarshalers)
		if err != nil {
			return errors.Wrapf(err, "i18n: load %s", p)
		}
		mf.Path = p
		files = append(files, mf)
		return nil
	})
	return files, err
}

// builtinMatches 报告框架内置语言 bt 是否应作为目标语言 target 的基础消息加载：
// 完全相同，或者主语言与文字系统相同（例如内置 en 匹配 en-US，内置 zh-CN 匹配 zh-Hans，不匹配 zh-TW）。
func builtinMatches(bt, target language.Tag) bool {
	if bt == target {
		return true
	}
	bBase, _ := bt.Base()
	tBase, _ := target.Base()
	bScript, _ := bt.Script()
	tScript, _ := target.Script()
	return bBase == tBase && bScript == tScript
}

// Templates 按与 New 相同的顺序读取框架内置消息和 sources 中的翻译文件，返回每种语言（文件名中的语言标签，
// 例如 zh-CN）中每个消息 ID 的原始模板，后加载的覆盖先加载的；有复数形式的消息取 other。
// 用于把消息交给不经过 Bundle 的程序，例如 tenon generate 为前端生成验证提示。
func Templates(sources ...fs.FS) (map[string]map[string]string, error) {
	unmarshal := map[string]goi18n.UnmarshalFunc{"yaml": yaml.Unmarshal, "yml": yaml.Unmarshal}
	out := map[string]map[string]string{}
	for _, src := range append([]fs.FS{builtin}, sources...) {
		err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if ext := strings.ToLower(path.Ext(p)); ext != ".yaml" && ext != ".yml" {
				return nil
			}
			data, err := fs.ReadFile(src, p)
			if err != nil {
				return err
			}
			mf, err := goi18n.ParseMessageFileBytes(data, path.Base(p), unmarshal)
			if err != nil {
				return errors.Wrapf(err, "i18n: load %s", p)
			}
			tag := mf.Tag.String()
			if out[tag] == nil {
				out[tag] = map[string]string{}
			}
			for _, m := range mf.Messages {
				out[tag][m.ID] = m.Other
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Match 按语言偏好在 b 中协商语言，匹配失败时返回默认语言。
// prefs 使用 Accept-Language 格式（如 "en-US,en;q=0.9"），多个值视为同一列表依次拼接，
// 可直接传入 HTTP 头或 gRPC metadata 中的全部取值。
func Match(b *goi18n.Bundle, prefs ...string) string {
	supported := b.LanguageTags()
	tags, _, err := language.ParseAcceptLanguage(strings.Join(prefs, ","))
	if err != nil || len(tags) == 0 {
		return supported[0].String()
	}
	_, index, confidence := language.NewMatcher(supported).Match(tags...)
	if confidence == language.No {
		return supported[0].String()
	}
	return supported[index].String()
}

// localeKey 是语言在 context 中的键。
type localeKey struct{}

// localeValue 是 context 中的语言和它的翻译器。
type localeValue struct {
	locale    string
	localizer *goi18n.Localizer
	isDefault bool
}

// WithLocale 返回携带 locale 及其 Localizer 的 context。
func WithLocale(ctx context.Context, b *goi18n.Bundle, locale string) context.Context {
	tags := b.LanguageTags()
	isDefault := len(tags) > 0 && (locale == "" || strings.EqualFold(locale, tags[0].String()))
	return context.WithValue(ctx, localeKey{}, localeValue{locale, goi18n.NewLocalizer(b, locale), isDefault})
}

// isDefaultLocale 报告 ctx 中设置的语言是否是语言目录的默认语言。
func isDefaultLocale(ctx context.Context) bool {
	v, _ := ctx.Value(localeKey{}).(localeValue)
	return v.isDefault
}

// Locale 返回 context 中的语言；未设置时返回空字符串。
func Locale(ctx context.Context) string {
	v, _ := ctx.Value(localeKey{}).(localeValue)
	return v.locale
}

// Localizer 返回 context 中的 go-i18n Localizer，用于复数等高级用法；未设置时返回 nil。
func Localizer(ctx context.Context) *goi18n.Localizer {
	v, _ := ctx.Value(localeKey{}).(localeValue)
	return v.localizer
}

// T 按 context 中的语言翻译消息 id，data 为模板数据（可为 nil）。
// 当前语言缺少该消息时使用默认语言的译文；context 未设置语言或默认语言也没有该消息时返回 id。
func T(ctx context.Context, id string, data any) string {
	l := Localizer(ctx)
	if l == nil {
		return id
	}
	if msg, _ := l.Localize(&goi18n.LocalizeConfig{MessageID: id, TemplateData: data}); msg != "" {
		return msg
	}
	return id
}
