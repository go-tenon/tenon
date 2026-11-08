package httpclient

import (
	"io"
	"net/http"
	"strconv"

	"github.com/shellphy-kit/tools/errs"
)

// maxErrorBody 是 StatusError 保留的响应体最大字节数。
const maxErrorBody = 1 << 10

// maxDrain 是出错时为复用连接而丢弃的响应体最大字节数，超过则直接关闭连接。
const maxDrain = 64 << 10

// StatusError 表示下游返回了非 2xx 响应。
type StatusError struct {
	Method     string
	Host       string
	Path       string
	StatusCode int
	// Body 为响应体的前 1KB，便于排查；可能被截断。
	Body []byte
}

func (e *StatusError) Error() string {
	s := e.Method + " " + e.Host + e.Path + ": " +
		strconv.Itoa(e.StatusCode) + " " + http.StatusText(e.StatusCode)
	if len(e.Body) > 0 {
		s += ": " + string(e.Body)
	}
	return s
}

// Check 在 resp 为 2xx 时返回 nil，调用方继续读取并关闭 resp.Body。
//
// 否则读取最多 1KB 响应体存入 *StatusError、关闭 resp.Body，并按状态码分类：
//
//	429             errs.ResourceExhausted
//	502, 503, 504   errs.Unavailable
//	其他            未分类（errs.Internal）
//
// 下游的 4xx 不会映射成本服务的同名分类，例如下游 404 并不意味着本服务应返回 404；
// 需要时由业务层用 errs.Wrap 重新分类。
func Check(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))

	se := &StatusError{StatusCode: resp.StatusCode, Body: body}
	if req := resp.Request; req != nil {
		se.Method = req.Method
		se.Host = req.URL.Host
		se.Path = req.URL.Path
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return errs.Wrap(se, errs.ResourceExhausted, "")
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return errs.Wrap(se, errs.Unavailable, "")
	}
	return se
}
