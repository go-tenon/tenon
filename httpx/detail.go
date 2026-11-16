package httpx

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/i18n"
)

// ErrorDetail 返回面向用户的错误描述：业务错误用 i18n.ErrorMessage 的译文，没有译文时，
// 以及其他错误，一律返回 StatusOf(err) 的状态描述，内部错误信息只留在日志中。
//
// 业务错误码会写入访问日志的 code 字段；有 Locale 但当前语言缺少译文时另外标记 untranslated。
func ErrorDetail(ctx context.Context, err error) string {
	if code := errorx.CodeOf(err); code != "" {
		AddAccessLogAttrs(ctx, slog.String("code", code))
	}
	msg, missing := i18n.ErrorMessage(ctx, err)
	if missing {
		AddAccessLogAttrs(ctx, slog.Bool("untranslated", true))
	}
	if msg == "" {
		return http.StatusText(StatusOf(err))
	}
	return msg
}
