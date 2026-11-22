package i18n

import (
	"context"
	"strings"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"

	"github.com/go-tenon/tenon/errorx"
)

// fallbackLocalizer 用于渲染 errorx.Define 附带的源语言默认提示模板，与 go-i18n 的模板语法一致。
var fallbackLocalizer = goi18n.NewLocalizer(goi18n.NewBundle(language.Und))

// ErrorMessage 翻译带错误码的错误 err：以 errorx.CodeOf(err) 为消息 ID、errorx.PublicOf(err) 为模板数据，
// 按 ctx 中的语言翻译，当前语言缺少译文时依次回退到默认语言的译文和 errorx.Define 附带的源语言默认提示。
// 它与入口无关，HTTP 响应、MCP 工具结果和其他入口用它得到同一句提示。
//
// 没有错误码，或者既没有译文也没有默认提示时 msg 为空，由入口换成自己的通用描述，
// 例如 httpx.ErrorDetail 换成 HTTP 状态描述。missing 报告当前语言缺少这条译文（当前语言就是默认语言且
// errorx.Define 已提供默认提示时为 false），入口可以据此记录漏翻的错误码。
func ErrorMessage(ctx context.Context, err error) (msg string, missing bool) {
	code := errorx.CodeOf(err)
	if code == "" {
		return "", false
	}
	fallback := errorx.MessageOf(err)
	data := errorx.PublicOf(err)
	l := Localizer(ctx)
	if l == nil {
		if fallback == "" {
			return "", false
		}
		return renderFallback(code, fallback, data), false
	}
	msg, lerr := l.Localize(&goi18n.LocalizeConfig{MessageID: code, TemplateData: data})
	if lerr == nil {
		return msg, false
	}
	if msg != "" {
		// 当前语言缺少译文，go-i18n 回退到了语言目录的默认语言译文。
		return msg, true
	}
	if fallback == "" {
		return "", true
	}
	// 语言目录中没有译文，回退到 errorx.Define 上的源语言默认提示。
	// 当前请求语言就是默认语言时，源语言提示即完整译文，不标记为漏翻。
	return renderFallback(code, fallback, data), !isDefaultLocale(ctx)
}

// renderFallback 用 data 渲染 errorx.Define 附带的默认提示模板 fallback。
func renderFallback(code, fallback string, data map[string]any) string {
	if !strings.Contains(fallback, "{{") {
		return fallback
	}
	msg, _ := fallbackLocalizer.Localize(&goi18n.LocalizeConfig{
		DefaultMessage: &goi18n.Message{ID: code, Other: fallback},
		TemplateData:   data,
	})
	if msg != "" {
		return msg
	}
	return fallback
}
