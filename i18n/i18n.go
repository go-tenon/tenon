package i18n

import (
	"cmp"
	"context"
	"io/fs"
	"path"
	"slices"
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

// New 按优先级加载消息并校验 RequiredIDs，返回原生 Bundle。
// 调用方在开始处理请求前完成目录修改。
func New(opts Options) (*goi18n.Bundle, error) {
	def := opts.Default
	if def == "" {
		def = LocaleZhCN
	}
	defTag, err := language.Parse(def)
	if err != nil {
		return nil, errors.Wrapf(err, "i18n: default locale %q", def)
	}

	layers := slices.Clone(opts.Layers)
	slices.SortStableFunc(layers, func(a, b Layer) int { return cmp.Compare(a.Priority, b.Priority) })

	b := goi18n.NewBundle(defTag)
	b.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)
	b.RegisterUnmarshalFunc("yml", yaml.Unmarshal)

	// 按优先级、语言和消息 ID 记录来源。
	seen := map[Priority]map[string]map[string]string{}
	for _, layer := range layers {
		if layer.FS == nil {
			return nil, errors.Newf("i18n: layer %s requires a filesystem", layerLabel(layer))
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
		locale := mf.Tag.String()
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

// MatchAcceptLanguage 返回匹配的目录语言，匹配失败时返回默认语言。
func MatchAcceptLanguage(b *goi18n.Bundle, header string) string {
	supported := b.LanguageTags()
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(tags) == 0 {
		return supported[0].String()
	}
	_, index, confidence := language.NewMatcher(supported).Match(tags...)
	if confidence == language.No {
		return supported[0].String()
	}
	return supported[index].String()
}
