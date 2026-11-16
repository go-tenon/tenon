package theme_test

import (
	"strings"
	"testing"

	"github.com/go-tenon/tenon/theme"
)

const tokens = `{
  "color": {
    "brand": { "$type": "color", "$value": "oklch(0.6279 0.2577 29.23)", "$extensions": { "tenon.dark": "#3b82f6" } },
    "background": { "$type": "color", "$value": "#ffffff", "$extensions": { "tenon.dark": "#0a0a0a" } },
    "success": { "$type": "color", "$value": { "colorSpace": "srgb", "components": [0.086, 0.639, 0.29] } },
    "warning": { "$type": "color", "$value": "#facc15" }
  },
  "radius": { "$type": "dimension", "$value": { "value": 0.75, "unit": "rem" } },
  "font": { "$type": "fontFamily", "$value": ["Inter Variable", "system-ui", "sans-serif"] },
  "density": { "$type": "string", "$value": "compact" }
}`

func TestParse(t *testing.T) {
	tok, err := theme.Parse([]byte(tokens))
	if err != nil {
		t.Fatal(err)
	}
	brand := tok.Colors["brand"]
	// oklch 换算为 sRGB：oklch(0.6279 0.2577 29.23) 是纯红色。
	if got := brand.Light.Hex(); got != "#ff0000" {
		t.Errorf("brand light hex = %s", got)
	}
	if brand.Light.CSS != "oklch(0.6279 0.2577 29.23)" || brand.Dark.CSS != "#3b82f6" {
		t.Errorf("brand css = %q / %q", brand.Light.CSS, brand.Dark.CSS)
	}
	if got := tok.Colors["success"].Light.Hex(); got != "#16a34a" {
		t.Errorf("success hex = %s", got)
	}
	// 没有写的前景色按对比度取白色或接近黑色：红色上用白字，黄色上用黑字。
	if got := tok.Colors["brand-foreground"].Light.CSS; got != "#ffffff" {
		t.Errorf("brand foreground = %s", got)
	}
	if got := tok.Colors["warning-foreground"].Light.CSS; got != "#0a0a0a" {
		t.Errorf("warning foreground = %s", got)
	}
	if tok.Radius != "0.75rem" || tok.Font != `"Inter Variable", system-ui, sans-serif` || tok.Density != "compact" {
		t.Errorf("radius %q font %q density %q", tok.Radius, tok.Font, tok.Density)
	}
}

func TestParseRejects(t *testing.T) {
	for name, doc := range map[string]string{
		"no brand":      `{"color": {"background": {"$value": "#fff"}}}`,
		"unknown color": `{"color": {"brand": {"$value": "#fff"}, "accent2": {"$value": "#fff"}}}`,
		"bad color":     `{"color": {"brand": {"$value": "red"}}}`,
		"bad hex":       `{"color": {"brand": {"$value": "#12"}}}`,
		"bad radius":    `{"color": {"brand": {"$value": "#fff"}}, "radius": {"$value": "1em"}}`,
		"bad density":   `{"color": {"brand": {"$value": "#fff"}}, "density": {"$value": "airy"}}`,
		"style escape":  `{"color": {"brand": {"$value": "#fff"}}, "font": {"$value": "x;}</style>"}}`,
		"not json":      `{`,
	} {
		if _, err := theme.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOutputs(t *testing.T) {
	tok := theme.MustParse([]byte(tokens))
	light, dark := tok.CSSVars()
	if light["primary"] != "oklch(0.6279 0.2577 29.23)" || dark["primary"] != "#3b82f6" || light["ring"] != light["primary"] {
		t.Errorf("primary light %q dark %q", light["primary"], dark["primary"])
	}
	if light["radius"] != "0.75rem" || light["font-sans"] == "" || light["success"] == "" || dark["background"] != "#0a0a0a" {
		t.Errorf("light %v dark %v", light, dark)
	}
	if _, ok := light["muted"]; ok {
		t.Error("tokens that are not written must not override the defaults")
	}
	css := tok.CSS()
	for _, want := range []string{":root {\n", "  --primary: oklch(0.6279 0.2577 29.23);\n", ".dark {\n", "  --primary: #3b82f6;\n"} {
		if !strings.Contains(css, want) {
			t.Errorf("css missing %q:\n%s", want, css)
		}
	}

	a := tok.Antd()
	// antd 只接受 sRGB：颜色都换算为十六进制；运营后台默认只取品牌色和圆角（12px）。
	if a.Brand.Light["colorPrimary"] != "#ff0000" || a.Brand.Dark["colorPrimary"] != "#3b82f6" || a.Brand.Light["borderRadius"] != 12.0 {
		t.Errorf("brand %v", a.Brand)
	}
	if _, ok := a.Brand.Light["colorSuccess"]; ok {
		t.Errorf("brand tokens include more than brand and radius: %v", a.Brand.Light)
	}
	if a.Full.Light["colorSuccess"] != "#16a34a" || a.Full.Light["colorBgBase"] != "#ffffff" || a.Full.Dark["colorBgBase"] != "#0a0a0a" || a.Density != "compact" {
		t.Errorf("full %v density %s", a.Full, a.Density)
	}
	ts := tok.TS()
	for _, want := range []string{"export const cssVars = {", "export const antd = {", `"colorPrimary": "#ff0000"`, `"density": "compact"`} {
		if !strings.Contains(ts, want) {
			t.Errorf("ts missing %q:\n%s", want, ts)
		}
	}
}

func TestOKLCHGamut(t *testing.T) {
	for in, want := range map[string]string{
		"oklch(1 0 0)":                     "#ffffff",
		"oklch(0 0 0)":                     "#000000",
		"oklch(50% 0 0)":                   "#636363",
		"oklch(0.7 0.4 140)":               "#00cd00", // 超出色域的分量截到 0 到 1
		"oklch(0.6279 0.2577 29.23 / 0.5)": "#ff000080",
	} {
		tok, err := theme.Parse([]byte(`{"color": {"brand": {"$value": "` + in + `"}}}`))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := tok.Colors["brand"].Light.Hex(); got != want {
			t.Errorf("%s = %s, want %s", in, got, want)
		}
	}
}
