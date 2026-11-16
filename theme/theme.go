// Package theme 读取设计令牌：品牌只在一处定义（项目的 theme/tokens.json），生成三套界面的主题——
// 服务端页面（web/ui，见 web.Theme.WithTokens）、shadcn 的前端（CSS 变量）和 antd 的前端（ThemeConfig 的令牌）。
//
// 令牌采用 W3C Design Tokens（DTCG）格式，暗色的值写在 $extensions 的 "tenon.dark" 中：
//
//	{
//	  "color": {
//	    "brand":   { "$type": "color", "$value": "oklch(0.51 0.23 277)", "$extensions": { "tenon.dark": "oklch(0.68 0.16 277)" } },
//	    "success": { "$type": "color", "$value": "#16a34a" }
//	  },
//	  "radius":  { "$type": "dimension", "$value": "0.5rem" },
//	  "font":    { "$type": "fontFamily", "$value": ["Inter", "system-ui", "sans-serif"] },
//	  "density": { "$type": "string", "$value": "comfortable" }
//	}
//
// 只有 color.brand 是必须的；没有写的令牌沿用各套界面自己的默认值。
package theme

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
)

// DarkExtension 是 $extensions 中写暗色值的键。
const DarkExtension = "tenon.dark"

// Colors 是令牌中的颜色名称：品牌色、中性色、语义色。
var Colors = []string{
	"brand", "brand-foreground",
	"background", "foreground", "muted", "muted-foreground", "border",
	"success", "success-foreground", "warning", "warning-foreground", "error", "error-foreground",
}

// Shade 是一个颜色令牌的亮色和暗色值；没有写暗色时 Dark 与 Light 相同。
type Shade struct {
	Light, Dark Color
}

// Tokens 是解析后的设计令牌。
type Tokens struct {
	// Colors 的键是 Colors 中的名称，只包含令牌中写了的（以及由它们算出的前景色）。
	Colors map[string]Shade
	// Radius 是圆角的基准，例如 0.5rem；没有写时为空。
	Radius string
	// Font 是正文字体，CSS font-family 的值；没有写时为空。
	Font string
	// Density 是 comfortable（默认）或 compact。
	Density string
}

// MustParse 与 Parse 相同，出错时 panic，用于嵌入程序的令牌文件。
func MustParse(data []byte) *Tokens {
	t, err := Parse(data)
	if err != nil {
		panic(err)
	}
	return t
}

// Parse 解析 DTCG 格式的令牌。brand、success、warning、error 没有写对应的 *-foreground 时，
// 白字的对比度达到 3:1 时前景色用白色，否则用接近黑色。
func Parse(data []byte) (*Tokens, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, errors.Wrap(err, "theme: tokens are not JSON")
	}
	t := &Tokens{Colors: map[string]Shade{}, Density: "comfortable"}
	colors, _ := doc["color"].(map[string]any)
	for name, raw := range colors {
		if strings.HasPrefix(name, "$") {
			continue
		}
		if !slices.Contains(Colors, name) {
			return nil, errors.Newf("theme: color.%s is not one of %v", name, Colors)
		}
		shade, err := parseShade(raw)
		if err != nil {
			return nil, errors.Wrapf(err, "theme: color.%s", name)
		}
		t.Colors[name] = shade
	}
	if _, ok := t.Colors["brand"]; !ok {
		return nil, errors.New("theme: color.brand is required")
	}
	for _, name := range []string{"brand", "success", "warning", "error"} {
		base, ok := t.Colors[name]
		if _, has := t.Colors[name+"-foreground"]; ok && !has {
			t.Colors[name+"-foreground"] = Shade{Light: foreground(base.Light), Dark: foreground(base.Dark)}
		}
	}
	if raw, ok := doc["radius"]; ok {
		v, err := dimension(value(raw))
		if err != nil {
			return nil, errors.Wrap(err, "theme: radius")
		}
		t.Radius = v
	}
	if raw, ok := doc["font"]; ok {
		v, err := fontFamily(value(raw))
		if err != nil {
			return nil, errors.Wrap(err, "theme: font")
		}
		t.Font = v
	}
	if raw, ok := doc["density"]; ok {
		v, _ := value(raw).(string)
		if v != "comfortable" && v != "compact" {
			return nil, errors.Newf("theme: density %q is not comfortable or compact", v)
		}
		t.Density = v
	}
	if err := t.validate(); err != nil {
		return nil, err
	}
	return t, nil
}

// value 返回令牌的 $value；直接写值（不是 {"$value": …} 对象）时返回它本身。
func value(raw any) any {
	if m, ok := raw.(map[string]any); ok {
		if v, ok := m["$value"]; ok {
			return v
		}
	}
	return raw
}

func parseShade(raw any) (Shade, error) {
	light, err := parseColor(value(raw))
	if err != nil {
		return Shade{}, err
	}
	shade := Shade{Light: light, Dark: light}
	if m, ok := raw.(map[string]any); ok {
		if ext, ok := m["$extensions"].(map[string]any); ok {
			if d, ok := ext[DarkExtension]; ok {
				dark, err := parseColor(d)
				if err != nil {
					return Shade{}, errors.Wrap(err, "dark")
				}
				shade.Dark = dark
			}
		}
	}
	return shade, nil
}

// dimension 接受 "0.5rem"、"8px"，或者 DTCG 的对象 {"value": 0.5, "unit": "rem"}。
func dimension(v any) (string, error) {
	switch v := v.(type) {
	case string:
		if _, ok := toPixels(v); !ok {
			return "", errors.Newf("%q: use a number with px or rem", v)
		}
		return v, nil
	case map[string]any:
		n, ok1 := v["value"].(float64)
		unit, ok2 := v["unit"].(string)
		if !ok1 || !ok2 || (unit != "px" && unit != "rem") {
			return "", errors.Newf("%v: use {\"value\": number, \"unit\": \"px\" | \"rem\"}", v)
		}
		return trim(n) + unit, nil
	}
	return "", errors.Newf("%v: not a dimension", v)
}

// toPixels 把 px、rem（按 16px）换算为像素。
func toPixels(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	for _, u := range []struct {
		unit  string
		scale float64
	}{{"px", 1}, {"rem", 16}} {
		if n, ok := strings.CutSuffix(s, u.unit); ok {
			v, err := strconv.ParseFloat(n, 64)
			return v * u.scale, err == nil && v >= 0
		}
	}
	return 0, false
}

// fontFamily 接受字符串或字体名的数组，返回 CSS font-family 的值；含空格的名称加引号。
func fontFamily(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case []any:
		names := make([]string, 0, len(v))
		for _, n := range v {
			s, ok := n.(string)
			if !ok || s == "" {
				return "", errors.Newf("%v: font names must be strings", v)
			}
			if strings.Contains(s, " ") && !strings.HasPrefix(s, `"`) {
				s = strconv.Quote(s)
			}
			names = append(names, s)
		}
		return strings.Join(names, ", "), nil
	}
	return "", errors.Newf("%v: not a font family", v)
}

// validate 拒绝会闭合 <style> 或 CSS 块的字符：令牌会被写进页面的 <style>。
func (t *Tokens) validate() error {
	bad := func(s string) bool { return strings.ContainsAny(s, "<>{};") }
	if bad(t.Radius) || bad(t.Font) {
		return errors.New("theme: radius and font must not contain <>{};")
	}
	for name, s := range t.Colors {
		if bad(s.Light.CSS) || bad(s.Dark.CSS) {
			return errors.Newf("theme: color.%s must not contain <>{};", name)
		}
	}
	return nil
}

// cssVars 是令牌颜色对应的 shadcn CSS 变量（省略 --）。success、warning 是 shadcn 之外另加的变量。
var cssVars = map[string][]string{
	"brand":              {"primary", "ring", "sidebar-primary", "sidebar-ring", "chart-1"},
	"brand-foreground":   {"primary-foreground", "sidebar-primary-foreground"},
	"background":         {"background", "card", "popover", "sidebar"},
	"foreground":         {"foreground", "card-foreground", "popover-foreground", "sidebar-foreground"},
	"muted":              {"muted", "secondary", "accent", "sidebar-accent"},
	"muted-foreground":   {"muted-foreground"},
	"border":             {"border", "input", "sidebar-border"},
	"success":            {"success"},
	"success-foreground": {"success-foreground"},
	"warning":            {"warning"},
	"warning-foreground": {"warning-foreground"},
	"error":              {"destructive"},
	"error-foreground":   {"destructive-foreground"},
}

// CSSVars 返回亮色和暗色的 shadcn CSS 变量（键省略 --），web/ui 和 shadcn 的前端共用；
// 亮色中还有 radius 和 font-sans。
func (t *Tokens) CSSVars() (light, dark map[string]string) {
	light, dark = map[string]string{}, map[string]string{}
	for name, shade := range t.Colors {
		for _, v := range cssVars[name] {
			light[v] = shade.Light.CSS
			dark[v] = shade.Dark.CSS
		}
	}
	if t.Radius != "" {
		light["radius"] = t.Radius
	}
	if t.Font != "" {
		light["font-sans"] = t.Font
	}
	return light, dark
}

// CSS 返回 shadcn 的前端用的样式表：:root 中是亮色，.dark 中是暗色。
func (t *Tokens) CSS() string {
	light, dark := t.CSSVars()
	var b strings.Builder
	b.WriteString("/* 由 theme/tokens.json 生成，不要修改。 */\n")
	block := func(selector string, vars map[string]string) {
		b.WriteString(selector + " {\n")
		for _, k := range slices.Sorted(maps.Keys(vars)) {
			fmt.Fprintf(&b, "  --%s: %s;\n", k, vars[k])
		}
		b.WriteString("}\n")
	}
	block(":root", light)
	block(".dark", dark)
	return b.String()
}

// antdColors 是令牌颜色对应的 antd 令牌。
var antdColors = map[string]string{
	"brand":      "colorPrimary",
	"background": "colorBgBase",
	"foreground": "colorTextBase",
	"border":     "colorBorder",
	"success":    "colorSuccess",
	"warning":    "colorWarning",
	"error":      "colorError",
}

// AntdTokens 是亮色和暗色的 antd 令牌。
type AntdTokens struct {
	Light map[string]any `json:"light"`
	Dark  map[string]any `json:"dark"`
}

// Antd 是 antd 的主题令牌，交给 ConfigProvider 的 theme.token；暗色另配 antd 的 darkAlgorithm，
// Density 为 compact 时配 compactAlgorithm。颜色都是十六进制：antd 不接受 oklch。
type Antd struct {
	// Brand 只有品牌色和圆角：运营后台默认用它，其余保持 antd 的紧凑风格。
	Brand AntdTokens `json:"brand"`
	// Full 是全部令牌。
	Full    AntdTokens `json:"full"`
	Density string     `json:"density"`
}

// Antd 返回 antd 的主题令牌。
func (t *Tokens) Antd() Antd {
	tokens := func(dark bool, brandOnly bool) map[string]any {
		m := map[string]any{}
		for name, key := range antdColors {
			if brandOnly && name != "brand" {
				continue
			}
			if s, ok := t.Colors[name]; ok {
				c := s.Light
				if dark {
					c = s.Dark
				}
				m[key] = c.Hex()
			}
		}
		if px, ok := toPixels(t.Radius); ok {
			m["borderRadius"] = px
		}
		if t.Font != "" && !brandOnly {
			m["fontFamily"] = t.Font
		}
		return m
	}
	return Antd{
		Brand:   AntdTokens{Light: tokens(false, true), Dark: tokens(true, true)},
		Full:    AntdTokens{Light: tokens(false, false), Dark: tokens(true, false)},
		Density: t.Density,
	}
}

// TS 返回前端的主题包（packages/theme）的 index.ts：令牌的值和 antd 的令牌，不依赖 antd。
func (t *Tokens) TS() string {
	light, dark := t.CSSVars()
	j := func(v any) string {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	}
	var b strings.Builder
	b.WriteString("// 由 theme/tokens.json 生成，不要修改。修改令牌后运行 go tool tenon generate（tenon dev 会自动运行）。\n\n")
	b.WriteString("/** shadcn 的 CSS 变量（省略 --），与 tokens.css 相同。 */\n")
	fmt.Fprintf(&b, "export const cssVars = %s as const\n\n", j(map[string]any{"light": light, "dark": dark}))
	b.WriteString("/**\n * antd 的主题令牌，交给 ConfigProvider 的 theme.token；暗色另配 theme.darkAlgorithm，\n")
	b.WriteString(" * density 为 compact 时配 theme.compactAlgorithm。brand 只有品牌色和圆角（运营后台默认用它），full 是全部令牌。\n */\n")
	fmt.Fprintf(&b, "export const antd = %s as const\n", j(t.Antd()))
	return b.String()
}
