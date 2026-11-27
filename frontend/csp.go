package frontend

import (
	"net/url"
	"slices"
	"strings"
)

// CSP 是 HTML 响应的 Content-Security-Policy。由 DefaultPolicy 构造后按需修改字段，再调用 String 序列化。
type CSP struct {
	DefaultSrc     []string // default-src，默认 'self'
	ScriptSrc      []string // script-src，默认 'self' 和本次请求的 nonce
	StyleSrc       []string // style-src，默认 'self' 'unsafe-inline'
	ImgSrc         []string // img-src，默认 'self' data:
	MediaSrc       []string // media-src，默认空（不输出）
	ConnectSrc     []string // connect-src，默认 'self'
	WorkerSrc      []string // worker-src，默认空（不输出）
	BaseURI        []string // base-uri，默认 'none'
	FormAction     []string // form-action，默认 'self'
	FrameAncestors []string // frame-ancestors，默认 'none'
	ObjectSrc      []string // object-src，默认 'none'
}

// DefaultPolicy 返回带本次请求 nonce 的默认 CSP，序列化结果与 DefaultCSP 相同。
// 应用可以在此基础上用 AddImgSrc、AddConnectSrc 或直接设置 FrameAncestors 等字段。
func DefaultPolicy(nonce string) CSP {
	return CSP{
		DefaultSrc:     []string{"'self'"},
		ScriptSrc:      []string{"'self'", "'nonce-" + nonce + "'"},
		StyleSrc:       []string{"'self'", "'unsafe-inline'"},
		ImgSrc:         []string{"'self'", "data:"},
		ConnectSrc:     []string{"'self'"},
		BaseURI:        []string{"'none'"},
		FormAction:     []string{"'self'"},
		FrameAncestors: []string{"'none'"},
		ObjectSrc:      []string{"'none'"},
	}
}

// Origin 从 http 或 https 地址中取出 origin（scheme://host）；相对路径或无效地址返回空字符串。
// 常用于把 imgproxy.url、对象存储公开地址等配置转成 CSP 的来源。
func Origin(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// AddImgSrc 向 img-src 追加非空且尚未存在的来源。
func (c *CSP) AddImgSrc(sources ...string) { c.ImgSrc = appendUniqueSources(c.ImgSrc, sources...) }

// AddMediaSrc 向 media-src 追加非空且尚未存在的来源。
func (c *CSP) AddMediaSrc(sources ...string) {
	c.MediaSrc = appendUniqueSources(c.MediaSrc, sources...)
}

// AddConnectSrc 向 connect-src 追加非空且尚未存在的来源。
func (c *CSP) AddConnectSrc(sources ...string) {
	c.ConnectSrc = appendUniqueSources(c.ConnectSrc, sources...)
}

// AddWorkerSrc 向 worker-src 追加非空且尚未存在的来源。
func (c *CSP) AddWorkerSrc(sources ...string) {
	c.WorkerSrc = appendUniqueSources(c.WorkerSrc, sources...)
}

// String 返回序列化后的 Content-Security-Policy 头的值。空指令会跳过。
func (c CSP) String() string {
	directives := []struct {
		name   string
		values []string
	}{
		{"default-src", c.DefaultSrc},
		{"script-src", c.ScriptSrc},
		{"style-src", c.StyleSrc},
		{"img-src", c.ImgSrc},
		{"media-src", c.MediaSrc},
		{"connect-src", c.ConnectSrc},
		{"worker-src", c.WorkerSrc},
		{"base-uri", c.BaseURI},
		{"form-action", c.FormAction},
		{"frame-ancestors", c.FrameAncestors},
		{"object-src", c.ObjectSrc},
	}
	parts := make([]string, 0, len(directives))
	for _, d := range directives {
		if len(d.values) == 0 {
			continue
		}
		parts = append(parts, d.name+" "+strings.Join(d.values, " "))
	}
	return strings.Join(parts, "; ")
}

// appendUniqueSources 把非空且尚未出现的来源追加到 dst。
func appendUniqueSources(dst []string, sources ...string) []string {
	for _, s := range sources {
		s = strings.TrimSpace(s)
		if s == "" || slices.Contains(dst, s) {
			continue
		}
		dst = append(dst, s)
	}
	return dst
}
