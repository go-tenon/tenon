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

// New 构建并冻结 Bundle。
func New(opts Options) (*goi18n.Bundle, error) {
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

	for _, id := range opts.RequiredIDs {
		found := false
		for _, locales := range seen {
			if _, ok := locales[defTag.String()][id]; ok {
				found = true
			}
		}
		if !found {
			return nil, errors.Newf("i18n: default locale missing message %s", id)
		}
	}
	return b, nil
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

// MatchAcceptLanguage negotiates loaded languages, with explicit default fallback.
func MatchAcceptLanguage(b *goi18n.Bundle, header string) string {
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(tags) == 0 {
		return b.LanguageTags()[0].String()
	}
	m, _, confidence := language.NewMatcher(b.LanguageTags()).Match(tags...)
	if confidence == language.No {
		return b.LanguageTags()[0].String()
	}
	return m.String()
}
