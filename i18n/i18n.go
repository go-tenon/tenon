package i18n

import (
	"context"
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

// Priority 表示消息层优先级；数值越大覆盖优先级越高。
type Priority int

const (
	PriorityFramework Priority = 0
	PriorityModule    Priority = 1
	PriorityProduct   Priority = 2
)

// Layer 是一层消息来源。同一 Priority 内重复 ID 报错；更高 Priority 可覆盖。
type Layer struct {
	Priority Priority
	FS       fs.FS
	// Name 仅用于错误信息，可空。
	Name string
}

// Options 控制 New。
type Options struct {
	// Default 默认语言；空则 LocaleZhCN。
	Default string
	// Layers 按任意顺序传入；加载时先按 Priority 升序，同级按切片顺序。
	Layers []Layer
	// RequiredIDs 若非空，则默认语言必须包含这些消息 ID。
	RequiredIDs []string
}

// Bundle 是冻结后的消息目录，可安全并发读取。
type Bundle struct {
	bundle     *goi18n.Bundle
	defaultTag language.Tag
	matcher    language.Matcher
	tags       []language.Tag
}

// New 构建并冻结 Bundle。
func New(opts Options) (*Bundle, error) {
	def := opts.Default
	if def == "" {
		def = LocaleZhCN
	}
	defTag, err := language.Parse(def)
	if err != nil {
		return nil, errors.Wrapf(err, "i18n: default locale %q", def)
	}

	layers := append([]Layer(nil), opts.Layers...)
	sortLayers(layers)

	b := goi18n.NewBundle(defTag)
	b.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)
	b.RegisterUnmarshalFunc("yml", yaml.Unmarshal)

	// priority -> locale -> id -> layer name；同一语言、同一优先级下 ID 不可重复。
	seen := map[Priority]map[string]map[string]string{}
	for _, layer := range layers {
		if layer.FS == nil {
			continue
		}
		if seen[layer.Priority] == nil {
			seen[layer.Priority] = map[string]map[string]string{}
		}
		entries, err := loadLayer(b, layer)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			byLocale := seen[layer.Priority]
			if byLocale[e.locale] == nil {
				byLocale[e.locale] = map[string]string{}
			}
			if prev, ok := byLocale[e.locale][e.id]; ok {
				return nil, errors.Newf("i18n: duplicate message id %q for locale %s at priority %d (%s and %s)", e.id, e.locale, layer.Priority, prev, layerLabel(layer))
			}
			byLocale[e.locale][e.id] = layerLabel(layer)
		}
	}

	out := &Bundle{
		bundle:     b,
		defaultTag: defTag,
		tags:       b.LanguageTags(),
	}
	out.matcher = language.NewMatcher(out.tags)

	if len(opts.RequiredIDs) > 0 {
		if err := out.checkRequired(opts.RequiredIDs); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func layerLabel(l Layer) string {
	if l.Name != "" {
		return l.Name
	}
	return "layer"
}

func sortLayers(layers []Layer) {
	// 稳定：Priority 升序，同 Priority 保持原顺序。
	for i := 1; i < len(layers); i++ {
		j := i
		for j > 0 && layers[j-1].Priority > layers[j].Priority {
			layers[j-1], layers[j] = layers[j], layers[j-1]
			j--
		}
	}
}

type msgRef struct {
	locale string
	id     string
}

func loadLayer(b *goi18n.Bundle, layer Layer) ([]msgRef, error) {
	var out []msgRef
	err := fs.WalkDir(layer.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := strings.ToLower(path.Ext(p))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		data, err := fs.ReadFile(layer.FS, p)
		if err != nil {
			return err
		}
		mf, err := b.ParseMessageFileBytes(data, path.Base(p))
		if err != nil {
			return errors.Wrapf(err, "i18n: load %s from %s", p, layerLabel(layer))
		}
		locale := strings.TrimSuffix(path.Base(p), path.Ext(p))
		if mf.Tag != language.Und {
			locale = mf.Tag.String()
		}
		for _, m := range mf.Messages {
			out = append(out, msgRef{locale: locale, id: m.ID})
		}
		return nil
	})
	return out, err
}

func (b *Bundle) checkRequired(ids []string) error {
	loc := goi18n.NewLocalizer(b.bundle, b.defaultTag.String())
	var missing []string
	for _, id := range ids {
		if _, err := loc.Localize(&goi18n.LocalizeConfig{MessageID: id}); err != nil {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return errors.Newf("i18n: default locale %s missing messages: %s", b.defaultTag, strings.Join(missing, ", "))
	}
	return nil
}

// Localizer 返回指定语言的 go-i18n Localizer；空或不支持时回退到默认语言。
func (b *Bundle) Localizer(locale string) *goi18n.Localizer {
	tag := b.Match(locale)
	return goi18n.NewLocalizer(b.bundle, tag)
}

// Match 将 locale 匹配到已加载语言；空则返回默认语言。
func (b *Bundle) Match(locale string) string {
	if locale == "" {
		return b.defaultTag.String()
	}
	tag, _ := language.Parse(locale)
	m, _, _ := b.matcher.Match(tag)
	return m.String()
}

// Default 返回默认语言标签。
func (b *Bundle) Default() string { return b.defaultTag.String() }

// Locales 返回已加载语言；第一个不一定是默认语言，使用 Default 取默认。
func (b *Bundle) Locales() []string {
	out := make([]string, len(b.tags))
	for i, t := range b.tags {
		out[i] = t.String()
	}
	return out
}

// Unwrap 返回底层 *goi18n.Bundle，供需要原生 API 的调用方使用。
func (b *Bundle) Unwrap() *goi18n.Bundle { return b.bundle }

type localeKey struct{}

// WithLocale 将语言标记写入 context。
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeKey{}, locale)
}

// Locale 读取 context 中的语言标记；未设置时返回空字符串。
func Locale(ctx context.Context) string {
	if l, ok := ctx.Value(localeKey{}).(string); ok {
		return l
	}
	return ""
}

// MatchAcceptLanguage 按 Accept-Language 与默认语言选择标签。
func (b *Bundle) MatchAcceptLanguage(header string) string {
	if header == "" {
		return b.defaultTag.String()
	}
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(tags) == 0 {
		return b.defaultTag.String()
	}
	m, _, _ := b.matcher.Match(tags...)
	return m.String()
}
