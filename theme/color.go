package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
)

// Color 是一个颜色：在 sRGB 中的分量（0 到 1）和不透明度，以及原样的 CSS 写法。
type Color struct {
	// CSS 是写进 CSS 变量的值：令牌中写的是 oklch 时保持 oklch，否则是十六进制。
	CSS string
	// R、G、B 是 sRGB 分量，超出色域的值已经截到 0 到 1。
	R, G, B float64
	// Alpha 是不透明度，1 表示不透明。
	Alpha float64
}

// Hex 返回 #rrggbb，不透明度不是 1 时为 #rrggbbaa。antd 的令牌只接受这类 sRGB 写法，不接受 oklch。
func (c Color) Hex() string {
	b := func(v float64) string { return fmt.Sprintf("%02x", int(math.Round(clamp(v)*255))) }
	s := "#" + b(c.R) + b(c.G) + b(c.B)
	if c.Alpha < 1 {
		s += b(c.Alpha)
	}
	return s
}

// luminance 是 WCAG 的相对亮度。
func (c Color) luminance() float64 {
	lin := func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// contrast 是两个颜色的 WCAG 对比度。
func contrast(a, b Color) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

var (
	white = Color{CSS: "#ffffff", R: 1, G: 1, B: 1, Alpha: 1}
	black = Color{CSS: "#0a0a0a", R: 10.0 / 255, G: 10.0 / 255, B: 10.0 / 255, Alpha: 1}
)

// foreground 返回放在 c 上的文字颜色：白字的对比度达到 3:1（WCAG 对界面组件和大号文字的要求）时用白字，
// 与大多数品牌按钮一致；否则（例如黄色）用接近黑色。
func foreground(c Color) Color {
	if contrast(c, white) >= 3 {
		return white
	}
	return black
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// parseColor 解析令牌中的颜色：#rgb、#rrggbb、#rrggbbaa、oklch(L C H [/ A])，
// 或者 DTCG 的对象 {"colorSpace": "srgb" | "oklch", "components": [...], "alpha": ...}。
func parseColor(v any) (Color, error) {
	switch v := v.(type) {
	case string:
		s := strings.TrimSpace(v)
		switch {
		case strings.HasPrefix(s, "#"):
			return parseHex(s)
		case strings.HasPrefix(strings.ToLower(s), "oklch(") && strings.HasSuffix(s, ")"):
			return parseOKLCHString(s)
		}
		return Color{}, errors.Newf("color %q: use #rrggbb or oklch(L C H)", s)
	case map[string]any:
		space, _ := v["colorSpace"].(string)
		comps, _ := v["components"].([]any)
		if len(comps) != 3 {
			return Color{}, errors.Newf("color %v: components must have 3 numbers", v)
		}
		var n [3]float64
		for i, c := range comps {
			f, ok := c.(float64)
			if !ok {
				return Color{}, errors.Newf("color %v: components must be numbers", v)
			}
			n[i] = f
		}
		alpha := 1.0
		if a, ok := v["alpha"].(float64); ok {
			alpha = a
		}
		switch space {
		case "srgb":
			c := Color{R: clamp(n[0]), G: clamp(n[1]), B: clamp(n[2]), Alpha: clamp(alpha)}
			c.CSS = c.Hex()
			return c, nil
		case "oklch":
			return oklch(n[0], n[1], n[2], alpha), nil
		}
		return Color{}, errors.Newf("color %v: colorSpace must be srgb or oklch", v)
	}
	return Color{}, errors.Newf("color %v: not a color", v)
}

func parseHex(s string) (Color, error) {
	h := strings.TrimPrefix(s, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 && len(h) != 8 {
		return Color{}, errors.Newf("color %q: hex must have 3, 6 or 8 digits", s)
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return Color{}, errors.Newf("color %q: not hex", s)
	}
	alpha := 1.0
	if len(h) == 8 {
		alpha = float64(n&0xff) / 255
		n >>= 8
	}
	c := Color{R: float64(n>>16&0xff) / 255, G: float64(n>>8&0xff) / 255, B: float64(n&0xff) / 255, Alpha: alpha}
	c.CSS = c.Hex()
	return c, nil
}

// parseOKLCHString 解析 oklch(L C H [/ A])：L 可以是 0 到 1 或百分比，H 是角度（可以带 deg）。
func parseOKLCHString(s string) (Color, error) {
	inner := strings.TrimSpace(s[len("oklch(") : len(s)-1])
	alpha := 1.0
	if main, a, ok := strings.Cut(inner, "/"); ok {
		inner = main
		v, err := number(strings.TrimSpace(a), 1)
		if err != nil {
			return Color{}, errors.Newf("color %q: %v", s, err)
		}
		alpha = v
	}
	parts := strings.Fields(inner)
	if len(parts) != 3 {
		return Color{}, errors.Newf("color %q: oklch needs L C H", s)
	}
	l, err := number(parts[0], 1)
	if err != nil {
		return Color{}, errors.Newf("color %q: %v", s, err)
	}
	c, err := number(parts[1], 0.4)
	if err != nil {
		return Color{}, errors.Newf("color %q: %v", s, err)
	}
	h, err := number(strings.TrimSuffix(parts[2], "deg"), 1)
	if err != nil {
		return Color{}, errors.Newf("color %q: %v", s, err)
	}
	color := oklch(l, c, h, alpha)
	color.CSS = s
	return color, nil
}

// number 解析数字；百分比按 percent 对应 100% 换算，例如 L 的 100% 是 1，C 的 100% 是 0.4。
func number(s string, percent float64) (float64, error) {
	if p, ok := strings.CutSuffix(s, "%"); ok {
		v, err := strconv.ParseFloat(p, 64)
		return v / 100 * percent, err
	}
	return strconv.ParseFloat(s, 64)
}

// oklch 把 OKLCH 换算为 sRGB（https://bottosson.github.io/posts/oklab/），超出色域的分量截到 0 到 1。
func oklch(l, c, h, alpha float64) Color {
	rad := h * math.Pi / 180
	a, b := c*math.Cos(rad), c*math.Sin(rad)
	l1 := l + 0.3963377774*a + 0.2158037573*b
	m1 := l - 0.1055613458*a - 0.0638541728*b
	s1 := l - 0.0894841775*a - 1.2914855480*b
	l3, m3, s3 := l1*l1*l1, m1*m1*m1, s1*s1*s1
	lr := 4.0767416621*l3 - 3.3077115913*m3 + 0.2309699292*s3
	lg := -1.2684380046*l3 + 2.6097574011*m3 - 0.3413193965*s3
	lb := -0.0041960863*l3 - 0.7034186147*m3 + 1.7076147010*s3
	gamma := func(v float64) float64 {
		if v <= 0.0031308 {
			return clamp(12.92 * v)
		}
		return clamp(1.055*math.Pow(v, 1/2.4) - 0.055)
	}
	color := Color{R: gamma(lr), G: gamma(lg), B: gamma(lb), Alpha: clamp(alpha)}
	color.CSS = fmt.Sprintf("oklch(%s %s %s)", trim(l), trim(c), trim(h))
	if alpha < 1 {
		color.CSS = fmt.Sprintf("oklch(%s %s %s / %s)", trim(l), trim(c), trim(h), trim(alpha))
	}
	return color
}

func trim(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
