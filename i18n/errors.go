package i18n

import (
	"context"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/go-tenon/tenon/errorx"
)

// ErrorMessage 翻译带错误码的错误 err：以 errorx.CodeOf(err) 为消息 ID、errorx.PublicOf(err) 为模板数据，
// 按 ctx 中的语言翻译，当前语言缺少译文时使用默认语言的译文。它与入口无关，HTTP 响应、MCP 工具结果
// 和其他入口用它得到同一句提示。
//
// 没有错误码、ctx 未设置语言或默认语言也没有译文时 msg 为空，由入口换成自己的通用描述，
// 例如 httpx.ErrorDetail 换成 HTTP 状态描述。missing 报告当前语言缺少这条译文（ctx 未设置语言时为 false），
// 入口可以据此记录漏翻的错误码。
func ErrorMessage(ctx context.Context, err error) (msg string, missing bool) {
	code := errorx.CodeOf(err)
	if code == "" {
		return "", false
	}
	l := Localizer(ctx)
	if l == nil {
		return "", false
	}
	msg, lerr := l.Localize(&goi18n.LocalizeConfig{MessageID: code, TemplateData: errorx.PublicOf(err)})
	return msg, lerr != nil
}
