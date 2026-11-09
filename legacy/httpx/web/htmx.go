package web

import (
	"encoding/json"
	"net/http"
	"strings"
)

// HTMXVersion 是 Vendor 中内置的 htmx 版本。
const HTMXVersion = "2.0.11"

// IsHTMX 报告 r 是否由 htmx 发起（请求头 HX-Request: true）。
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// IsPartial 报告 r 是否只需要返回页面片段：由 htmx 发起，
// 且不是 hx-boost 导航（HX-Boosted），也不是浏览历史恢复（HX-History-Restore-Request），
// 后两者 htmx 都需要完整页面。
func IsPartial(r *http.Request) bool {
	return IsHTMX(r) &&
		r.Header.Get("HX-Boosted") != "true" &&
		r.Header.Get("HX-History-Restore-Request") != "true"
}

// Redirect 让浏览器跳转到 url：htmx 请求设置 HX-Redirect 并响应 200（htmx 会整页跳转），
// 普通请求响应 303 See Other。常用于表单提交成功后。
func Redirect(w http.ResponseWriter, r *http.Request, url string) {
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// Trigger 通过 HX-Trigger 响应头让 htmx 在浏览器触发事件 name，detail 为事件的 detail（可为 nil）。
// 多次调用会合并到同一个响应头中。需在写入响应头之前调用。
//
//	web.Trigger(w, "toast", map[string]string{"message": "保存成功"})
//	// 页面中：<div hx-on:toast="showToast(event.detail.message)">
func Trigger(w http.ResponseWriter, name string, detail any) error {
	events := map[string]any{}
	if prev := w.Header().Get("HX-Trigger"); prev != "" {
		if strings.HasPrefix(strings.TrimSpace(prev), "{") {
			if err := json.Unmarshal([]byte(prev), &events); err != nil {
				return err
			}
		} else {
			// 简单形式："event1, event2"
			for e := range strings.SplitSeq(prev, ",") {
				if e = strings.TrimSpace(e); e != "" {
					events[e] = nil
				}
			}
		}
	}
	events[name] = detail
	b, err := json.Marshal(events)
	if err != nil {
		return err
	}
	w.Header().Set("HX-Trigger", string(b))
	return nil
}
