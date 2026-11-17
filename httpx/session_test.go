package httpx_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/go-tenon/tenon/httpx"
)

// 会话 cookie 的属性：HttpOnly、Secure、SameSite=Lax、Path=/，__Host- 前缀的要求全部满足。
func TestSessionCookie(t *testing.T) {
	t.Parallel()
	c := httpx.SessionCookie{}.Set("tok", 30*24*time.Hour)
	if c.Name != "__Host-session" || c.Value != "tok" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" || c.MaxAge != 30*24*3600 {
		t.Fatalf("cookie %+v", c)
	}
	if err := c.Valid(); err != nil {
		t.Fatal(err)
	}
	if c := (httpx.SessionCookie{}).Clear(); c.Name != httpx.DefaultSessionCookieName || c.MaxAge != -1 || c.Value != "" || !c.Secure || c.Path != "/" {
		t.Fatalf("clear %+v", c)
	}
	header := "theme=dark; __Host-session=tok; __Host-ops=staff; lang=zh"
	if got := (httpx.SessionCookie{}).Token(header); got != "tok" {
		t.Fatalf("token %q", got)
	}
	// 同一个源下的另一套账号体系用自己的名称，两个 cookie 互不干扰。
	ops := httpx.SessionCookie{Name: "__Host-ops"}
	if got := ops.Token(header); got != "staff" || ops.Set("s", time.Hour).Name != "__Host-ops" {
		t.Fatalf("ops token %q", got)
	}
	if got := (httpx.SessionCookie{}).Token("session=tok"); got != "" {
		t.Fatalf("other cookie read as session: %q", got)
	}
	for _, name := range []string{"session", "__Host-", "__Host-a b", "__Secure-x"} {
		if (httpx.SessionCookie{Name: name}).Validate() == nil {
			t.Errorf("%q accepted", name)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		site, origin, host string
		want               bool
	}{
		{"same-origin", "", "example.com", true},
		{"same-site", "https://example.com", "example.com", false}, // Sec-Fetch-Site 优先：同站的其他子域名也不算
		{"cross-site", "https://example.com", "example.com", false},
		{"none", "", "example.com", false}, // 地址栏直接打开的写请求不存在，按跨站处理
		{"", "https://example.com", "example.com", true},
		{"", "http://localhost:8080", "localhost:8080", true},
		{"", "https://evil.example", "example.com", false},
		{"", "null", "example.com", false},
		{"", "", "example.com", false},
	} {
		if got := httpx.SameOrigin(c.site, c.origin, c.host); got != c.want {
			t.Errorf("SameOrigin(%q, %q, %q) = %v", c.site, c.origin, c.host, got)
		}
	}
}
