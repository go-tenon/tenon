package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

var (
	secretRe   = regexp.MustCompile(`name="secret" value="([A-Z2-7]+)"`)
	recoveryRe = regexp.MustCompile(`<span>([a-z2-9]{5}-[a-z2-9]{5})</span>`)
	tokenRe    = regexp.MustCompile(`sk_[A-Za-z0-9_-]{20,}`)
	noticeRe   = regexp.MustCompile(`href="(/notifications/[0-9a-f-]{36})"`)
)

func register(t *testing.T, c *browser, name, email string) {
	t.Helper()
	form := url.Values{"name": {name}, "email": {email}, "password": {"password1"}}.Encode()
	expect(t, c.do("POST", "/register", form), http.StatusSeeOther)
}

// TestSecurityPages 覆盖两步验证、API Key、通知、管理后台与审计日志的页面。
func TestSecurityPages(t *testing.T) {
	a := newTestApp(t)
	c := &browser{t: t, h: a.Router}
	register(t, c, "Admin", "admin@example.com")

	// 注册后收到欢迎通知；点击后标记已读并跳转。
	rec := c.do("GET", "/notifications", "")
	expect(t, rec, 200, "欢迎使用")
	link := noticeRe.FindStringSubmatch(rec.Body.String())
	if link == nil {
		t.Fatalf("no notification link:\n%s", rec.Body)
	}
	rec = c.do("GET", link[1], "")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/security" {
		t.Fatalf("open notification = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	expect(t, c.do("GET", "/notifications/bell", ""), 200, `hx-get="/notifications/bell"`)

	// 邮箱未验证；无效的验证链接。
	expect(t, c.do("GET", "/settings", ""), 200, "未验证", "重新发送验证邮件")
	expect(t, c.do("GET", "/verify-email?token=bogus", ""), 400, "验证链接无效")

	// 启用两步验证：生成密钥 → 输错验证码 → 输对后显示恢复码。
	expect(t, c.do("GET", "/settings/security", ""), 200, "两步验证", "API Key", "安全日志", "注册账号")
	rec = c.do("POST", "/settings/mfa/setup", "", "HX-Request", "true")
	expect(t, rec, 200, "data:image/png;base64,")
	secret := secretRe.FindStringSubmatch(rec.Body.String())[1]
	otpURL := "otpauth://totp/x?secret=" + secret
	expect(t, c.do("POST", "/settings/mfa/enable", url.Values{"code": {"000000"}, "secret": {secret}, "url": {otpURL}}.Encode(),
		"HX-Request", "true"), 200, "验证码错误", "data:image/png;base64,")
	code := totpNow(t, secret)
	rec = c.do("POST", "/settings/mfa/enable", url.Values{"code": {code}, "secret": {secret}, "url": {otpURL}}.Encode(), "HX-Request", "true")
	expect(t, rec, 200, "请保存以下恢复码")
	var codes []string
	for _, m := range recoveryRe.FindAllStringSubmatch(rec.Body.String(), -1) {
		codes = append(codes, m[1])
	}
	if len(codes) != 10 {
		t.Fatalf("recovery codes = %v", codes)
	}

	// API Key：创建后只显示一次；可以访问 API，但不能访问敏感接口。
	rec = c.do("POST", "/settings/api-keys", "name=CI", "HX-Request", "true")
	expect(t, rec, 200, "只显示这一次", "CI")
	key := tokenRe.FindString(rec.Body.String())
	api := &browser{t: t, h: a.Router, bearer: key}
	expect(t, api.do("GET", "/api/v1/notes", ""), 200)
	expect(t, api.do("GET", "/api/v1/me/api-keys", ""), 403)

	// 重新登录：密码正确后要求两步验证，恢复码可以登录。
	expect(t, c.do("POST", "/logout", ""), http.StatusSeeOther)
	rec = c.do("POST", "/login", "email=admin@example.com&password=password1&next=/notifications")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login/mfa?next=%2Fnotifications" {
		t.Fatalf("login = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	expect(t, c.do("GET", "/", ""), http.StatusSeeOther) // 尚未登录
	expect(t, c.do("GET", "/login/mfa", ""), 200, "两步验证")
	expect(t, c.do("POST", "/login/mfa", "code=000000"), 422, "验证码错误")
	rec = c.do("POST", "/login/mfa", "code="+codes[0]+"&next=/notifications")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/notifications" {
		t.Fatalf("mfa = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	expect(t, c.do("GET", "/settings/security", ""), 200, "已启用", "剩余恢复码 9 个", "启用两步验证")

	// 管理后台：修改站点名称并关闭注册。
	expect(t, c.do("GET", "/admin/settings", ""), 200, "站点名称", "开放注册")
	expect(t, c.do("POST", "/admin/settings", "name="), 422, "站点名称为")
	expect(t, c.do("POST", "/admin/settings", "name=新站点"), http.StatusSeeOther)
	expect(t, c.do("GET", "/", ""), 200, "<title>笔记 · 新站点</title>")
	expect(t, (&browser{t: t, h: a.Router}).do("GET", "/register", ""), 403, "暂未开放注册")
	expect(t, c.do("GET", "/admin/audit", ""), 200, "account.login", "account.mfa_enabled", "setting.update")
	expect(t, c.do("GET", "/admin/audit?action=account.login_failed", ""), 200, "登录失败")

	// 普通用户看不到管理后台。
	expect(t, c.do("POST", "/admin/settings", "name=新站点&registration_open=on"), http.StatusSeeOther)
	user := &browser{t: t, h: a.Router}
	register(t, user, "Bob", "bob@example.com")
	expect(t, user.do("GET", "/admin/settings", ""), 404)
}

// TestAsyncExport 覆盖后台导出：创建任务 → 消息处理 → 站内通知 → 下载。
func TestAsyncExport(t *testing.T) {
	a := newTestApp(t)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Bus.Run(ctx); close(done) }()
	t.Cleanup(func() { stop(); <-done })
	<-a.Bus.Running()

	c := &browser{t: t, h: a.Router}
	register(t, c, "Alice", "alice@example.com")
	expect(t, c.do("POST", "/notes", "text=hello", "HX-Request", "true"), 200)
	expect(t, c.do("POST", "/notes/export-jobs", "format=xlsx", "HX-Request", "true"), 200, "已开始导出")

	var body string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		body = c.do("GET", "/notifications", "").Body.String()
		if strings.Contains(body, "导出完成") {
			break
		}
	}
	if !strings.Contains(body, "导出完成：笔记-") {
		t.Fatalf("no export notification:\n%s", body)
	}
	link := noticeRe.FindStringSubmatch(body)[1] // 最新的通知在最前
	rec := c.do("GET", link, "")
	download := rec.Header().Get("Location")
	if !strings.HasPrefix(download, "/exports/") {
		t.Fatalf("notification target = %s", download)
	}
	rec = c.do("GET", download, "")
	expect(t, rec, 200)
	if !strings.HasPrefix(rec.Body.String(), "PK") || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("download = %v", rec.Header())
	}

	// API 也能查询导出任务。
	rec = c.do("GET", "/api/v1/exports", "")
	var jobs []struct{ Status string }
	_ = json.Unmarshal(rec.Body.Bytes(), &jobs)
	if len(jobs) != 1 || jobs[0].Status != "done" {
		t.Errorf("jobs = %s", rec.Body)
	}
}

func totpNow(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}
