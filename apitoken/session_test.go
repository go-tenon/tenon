package apitoken_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
)

// Session 的 JSON 是客户端协议的一部分：字段名固定，refresh_token 为空时省略。
func TestSession(t *testing.T) {
	t.Parallel()
	exp := time.Date(2026, 11, 11, 8, 10, 0, 0, time.UTC)
	pair := apitoken.Pair{Access: "a1", Refresh: "r1", ExpiresAt: exp, RefreshExpiresAt: exp.Add(time.Hour)}

	b, _ := json.Marshal(apitoken.NewSession(pair))
	if string(b) != `{"access_token":"a1","refresh_token":"r1","expires_at":"2026-11-11T08:10:00Z"}` {
		t.Errorf("body %s", b)
	}

	s, cookie := refreshCookie.Session(pair)
	b, _ = json.Marshal(s)
	if string(b) != `{"access_token":"a1","expires_at":"2026-11-11T08:10:00Z"}` {
		t.Errorf("cookie session body %s", b)
	}
	if cookie == nil || cookie.Value != "r1" || !cookie.HttpOnly {
		t.Errorf("cookie %+v", cookie)
	}
	// 宽限期内的重复刷新没有新的 refresh token：响应体省略它，也不写 cookie。
	grace := apitoken.Pair{Access: "a2", ExpiresAt: exp}
	if s, cookie := refreshCookie.Session(grace); cookie != nil || s.RefreshToken != "" {
		t.Errorf("grace %+v %+v", s, cookie)
	}
	if b, _ := json.Marshal(apitoken.NewSession(grace)); string(b) != `{"access_token":"a2","expires_at":"2026-11-11T08:10:00Z"}` {
		t.Errorf("grace body %s", b)
	}

	var req apitoken.RefreshRequest
	if err := json.Unmarshal([]byte(`{"refresh_token":"r1"}`), &req); err != nil || req.RefreshToken != "r1" {
		t.Errorf("refresh request %+v %v", req, err)
	}
}
