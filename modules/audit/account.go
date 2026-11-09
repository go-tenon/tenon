package audit

import (
	"context"
	"uuid"

	"github.com/ryan053857/go-kit/modules/account"
)

var accountSummaries = map[string]string{
	account.EventRegistered:      "注册账号",
	account.EventLogin:           "登录",
	account.EventLoginFailed:     "登录失败",
	account.EventLogout:          "退出登录",
	account.EventSessionRevoked:  "注销登录设备",
	account.EventTokenReused:     "检测到凭证被重复使用，已注销该登录设备",
	account.EventPasswordChanged: "修改密码",
	account.EventPasswordReset:   "通过邮件重置密码",
	account.EventEmailVerified:   "验证邮箱",
	account.EventMFAEnabled:      "启用两步验证",
	account.EventMFADisabled:     "关闭两步验证",
	account.EventAPIKeyCreated:   "创建 API Key",
	account.EventAPIKeyRevoked:   "删除 API Key",
	account.EventIdentityLinked:  "绑定第三方账号",
	account.EventIdentityRemoved: "解绑第三方账号",
	account.EventDisabled:        "账号被停用",
	account.EventEnabled:         "账号被启用",
}

// AccountEvent 把账号模块的事件写入审计日志，注册到 account.Service.OnEvent：
//
//	users.OnEvent(audits.AccountEvent)
//
// 事件的账号即操作者；账号被停用、启用时操作者为当前登录用户（管理员），账号为操作对象。
func (s *Service) AccountEvent(ctx context.Context, e account.Event) {
	entry := Entry{
		Action:       e.Type,
		ActorID:      e.UserID,
		ResourceType: "user",
		ResourceID:   e.UserID.String(),
		Summary:      accountSummaries[e.Type],
		Data:         map[string]any{},
		Failed:       e.Type == account.EventLoginFailed || e.Type == account.EventTokenReused,
	}
	if e.UserID == uuid.Nil() {
		entry.ResourceID = ""
	}
	switch e.Type {
	case account.EventDisabled, account.EventEnabled:
		entry.ActorID = uuid.Nil()
	case account.EventLogin, account.EventLogout, account.EventSessionRevoked, account.EventTokenReused:
		entry.ResourceType, entry.ResourceID = "session", e.SessionID.String()
	case account.EventAPIKeyCreated, account.EventAPIKeyRevoked:
		entry.ResourceType, entry.ResourceID = "api_key", e.SubjectID.String()
	}
	if e.Email != "" {
		entry.Data["email"] = e.Email
	}
	if e.Provider != "" {
		entry.Data["provider"] = e.Provider
	}
	s.Log(ctx, entry)
}
