package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// CallEvent 是一次模型 HTTP 调用（非流式 JSON 或流式 SSE）的观测记录。
type CallEvent struct {
	Endpoint         string        // 请求地址（不含查询参数）
	Model            string        // 请求体中的 model 字段
	Stream           bool          // 是否为流式 SSE 响应
	StatusCode       int           // HTTP 状态码，网络失败时为 0
	Duration         time.Duration // 完整耗时（流式为到流结束/关闭为止）
	Headers          http.Header   // 响应头克隆，供应用读取自定义网关计量头
	RequestBody      []byte        // 请求体原始字节（最多截断至 MaxBodyBytes）
	ResponseBody     []byte        // 非流式响应体原始字节（最多截断至 MaxBodyBytes）；流式响应为空
	Content          string        // 模型生成的文本内容（流式自动拼接 delta.content，最多截断至 MaxBodyBytes）
	PromptTokens     int           // 输入 token 数
	CompletionTokens int           // 输出 token 数
	ReasoningTokens  int           // 思考/推理 token 数
	CachedTokens     int           // 命中缓存的输入 token 数
	Err              error         // 网络错误、读取错误或非 2xx 状态错误
}

// ObserveOptions 调整 ObserveTransport 的采集行为。
type ObserveOptions struct {
	MaxBodyBytes int // 请求体与响应文本的最大采集字节数；不大于 0 时默认 4 MiB
}

const defaultObserveMaxBody = 4 << 20

// ObserveTransport 包装 HTTP RoundTripper，统一观测非流式 JSON 与流式 SSE 的模型调用并回调 fn。
// 非流式响应在 RoundTrip 返回前完成采集；流式 SSE 响应包装 resp.Body，在流读到 EOF 或 Close 时完成采集并仅回调一次。
// fn 在独立于请求取消的上下文中执行；base 为 nil 时使用 http.DefaultTransport；fn 为 nil 时 panic。
func ObserveTransport(base http.RoundTripper, fn func(context.Context, CallEvent), opts ...ObserveOptions) http.RoundTripper {
	if fn == nil {
		panic("ai: nil observe callback")
	}
	if base == nil {
		base = http.DefaultTransport
	}
	maxBody := defaultObserveMaxBody
	for _, o := range opts {
		if o.MaxBodyBytes > 0 {
			maxBody = o.MaxBodyBytes
		}
	}
	return &observeTransport{base: base, fn: fn, maxBody: maxBody}
}

// observeTransport 实现 http.RoundTripper，采集非流式和流式模型调用。
type observeTransport struct {
	base    http.RoundTripper
	fn      func(context.Context, CallEvent)
	maxBody int
}

// RoundTrip 发送模型请求并采集非流式或流式 SSE 响应的用量与内容。
// 请求体与非流式响应体按 MaxBodyBytes 设限采集：超过上限时仅保留前缀并流式透传剩余字节，避免大载荷引发内存暴涨。
func (t *observeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	cloned := req.Clone(ctx)

	var reqBody []byte
	var reqModel string
	if cloned.Body != nil {
		origBody := cloned.Body
		prefix, err := io.ReadAll(io.LimitReader(origBody, int64(t.maxBody)+1))
		if err != nil {
			_ = origBody.Close()
			return nil, err
		}
		if len(prefix) <= t.maxBody {
			_ = origBody.Close()
			cloned.Body = io.NopCloser(bytes.NewReader(prefix))
			cloned.ContentLength = int64(len(prefix))
			reqBody = prefix
		} else {
			cloned.Body = &joinedReadCloser{r: io.MultiReader(bytes.NewReader(prefix), origBody), closer: origBody}
			reqBody = prefix[:t.maxBody]
		}
		reqModel = extractJSONModel(reqBody)
	}

	ev := CallEvent{
		Endpoint:    cleanEndpoint(cloned),
		Model:       reqModel,
		RequestBody: reqBody,
	}

	start := time.Now()
	resp, err := t.base.RoundTrip(cloned)
	if err != nil {
		ev.Duration = time.Since(start)
		ev.Err = err
		t.fn(context.WithoutCancel(ctx), ev)
		return nil, err
	}

	ev.StatusCode = resp.StatusCode
	ev.Headers = resp.Header.Clone()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		ev.Err = fmt.Errorf("ai: unexpected status %d", resp.StatusCode)
	}

	if isEventStream(resp.Header.Get("Content-Type")) {
		ev.Stream = true
		resp.Body = &sseObserveBody{
			rc:      resp.Body,
			ctx:     context.WithoutCancel(ctx),
			start:   start,
			ev:      ev,
			maxBody: t.maxBody,
			fn:      t.fn,
		}
		return resp, nil
	}

	origRespBody := resp.Body
	prefix, readErr := io.ReadAll(io.LimitReader(origRespBody, int64(t.maxBody)+1))
	if readErr != nil {
		_ = origRespBody.Close()
		ev.Duration = time.Since(start)
		ev.Err = readErr
		t.fn(context.WithoutCancel(ctx), ev)
		return nil, readErr
	}
	if len(prefix) <= t.maxBody {
		_ = origRespBody.Close()
		ev.Duration = time.Since(start)
		resp.Body = io.NopCloser(bytes.NewReader(prefix))
		ev.ResponseBody = prefix
		parseCompletionEnvelope(prefix, &ev)
		t.fn(context.WithoutCancel(ctx), ev)
		return resp, nil
	}
	// 非流式响应体超出采集上限时流式透传剩余内容，读完或关闭时回调一次，不将整块响应缓存在内存中。
	ev.ResponseBody = prefix[:t.maxBody]
	resp.Body = &passthroughObserveBody{
		r:      io.MultiReader(bytes.NewReader(prefix), origRespBody),
		closer: origRespBody,
		ctx:    context.WithoutCancel(ctx),
		start:  start,
		ev:     ev,
		fn:     t.fn,
	}
	return resp, nil
}

// joinedReadCloser 把前缀缓冲区和底层流拼为一个 ReadCloser。
type joinedReadCloser struct {
	r      io.Reader
	closer io.Closer
}

// Read 从拼接流中读取字节。
func (j *joinedReadCloser) Read(p []byte) (int, error) { return j.r.Read(p) }

// Close 关闭底层流。
func (j *joinedReadCloser) Close() error { return j.closer.Close() }

// passthroughObserveBody 透传超过 MaxBodyBytes 的非流式响应体，在 EOF 或 Close 时回调一次观测函数。
type passthroughObserveBody struct {
	r      io.Reader
	closer io.Closer
	ctx    context.Context
	start  time.Time
	ev     CallEvent
	fn     func(context.Context, CallEvent)
	once   sync.Once
}

// Read 透传响应字节并在结束时上报观测事件。
func (b *passthroughObserveBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil {
		if err != io.EOF {
			b.ev.Err = err
		}
		b.finish()
	}
	return n, err
}

// Close 关闭底层响应体并确保触发一次观测回调。
func (b *passthroughObserveBody) Close() error {
	err := b.closer.Close()
	b.finish()
	return err
}

// finish 记录完整耗时并触发一次观测回调。
func (b *passthroughObserveBody) finish() {
	b.once.Do(func() {
		b.ev.Duration = time.Since(b.start)
		b.fn(b.ctx, b.ev)
	})
}

// extractJSONModel 从完整或截断的 JSON 请求体中尽力提取顶层 model 字段。
func extractJSONModel(raw []byte) string {
	var head struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &head) == nil && head.Model != "" {
		return head.Model
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return ""
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return ""
		}
		key, ok := k.(string)
		if !ok {
			return ""
		}
		if key == "model" {
			var m string
			if dec.Decode(&m) == nil {
				return m
			}
			return ""
		}
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return ""
		}
	}
	return ""
}

// sseObserveBody 包装 SSE 响应体，边透传字节流边解析文本增量与 usage，在 EOF 或 Close 时触发一次回调。
type sseObserveBody struct {
	rc      io.ReadCloser
	ctx     context.Context
	start   time.Time
	ev      CallEvent
	maxBody int
	fn      func(context.Context, CallEvent)

	pending bytes.Buffer
	content bytes.Buffer
	once    sync.Once
}

// Read 透传底层 SSE 数据流并解析已完成的行。
func (s *sseObserveBody) Read(p []byte) (int, error) {
	n, err := s.rc.Read(p)
	if n > 0 {
		s.consume(p[:n])
	}
	if err != nil {
		if err != io.EOF {
			s.ev.Err = err
		}
		s.finish()
	}
	return n, err
}

// Close 关闭底层流并确保触发一次观测回调。
func (s *sseObserveBody) Close() error {
	err := s.rc.Close()
	s.finish()
	return err
}

// consume 解析新增的 SSE 字节片段，按行提取 data: JSON 载荷中的文本与用量。
// 对缺少换行的超长单行设限丢弃，防止畸形上游流占满内存。
func (s *sseObserveBody) consume(chunk []byte) {
	maxPending := max(s.maxBody, 1<<20)
	if s.pending.Len()+len(chunk) > maxPending {
		s.pending.Reset()
		return
	}
	s.pending.Write(chunk)
	for {
		line, err := s.pending.ReadString('\n')
		if err != nil {
			// 未遇到换行，写回缓冲区等待下一次 Read。
			s.pending.WriteString(line)
			return
		}
		s.handleLine(line)
	}
}

// handleLine 解析单行 SSE data 负载，累计文本增量并更新最终 usage。
func (s *sseObserveBody) handleLine(line string) {
	line = strings.TrimSpace(line)
	data, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return
	}
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	var chunk sseChunkEnvelope
	if json.Unmarshal([]byte(data), &chunk) != nil {
		return
	}
	if chunk.Model != "" && s.ev.Model == "" {
		s.ev.Model = chunk.Model
	}
	for _, ch := range chunk.Choices {
		if ch.Delta.Content != "" && s.content.Len() < s.maxBody {
			remain := s.maxBody - s.content.Len()
			s.content.WriteString(truncateUTF8(ch.Delta.Content, remain))
		}
	}
	if chunk.Usage != nil {
		applyUsage(chunk.Usage, &s.ev)
	}
}

// finish 刷新缓冲区剩余行并向回调报告完整流式调用事件（仅执行一次）。
func (s *sseObserveBody) finish() {
	s.once.Do(func() {
		if s.pending.Len() > 0 {
			sc := bufio.NewScanner(&s.pending)
			for sc.Scan() {
				s.handleLine(sc.Text())
			}
		}
		s.ev.Content = s.content.String()
		s.ev.Duration = time.Since(s.start)
		s.fn(s.ctx, s.ev)
	})
}

// usageEnvelope 描述 OpenAI 兼容响应中的 token 用量结构。
type usageEnvelope struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// sseChunkEnvelope 是流式 SSE 单个 JSON 片段的最小结构。
type sseChunkEnvelope struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *usageEnvelope `json:"usage"`
}

// parseCompletionEnvelope 从非流式 OpenAI 兼容 JSON 响应中提取文本与用量。
func parseCompletionEnvelope(data []byte, ev *CallEvent) {
	var env struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *usageEnvelope `json:"usage"`
	}
	if json.Unmarshal(data, &env) != nil {
		return
	}
	if env.Model != "" && ev.Model == "" {
		ev.Model = env.Model
	}
	if len(env.Choices) > 0 {
		ev.Content = env.Choices[0].Message.Content
	}
	if env.Usage != nil {
		applyUsage(env.Usage, ev)
	}
}

// applyUsage 将 usageEnvelope 填入 CallEvent。
func applyUsage(u *usageEnvelope, ev *CallEvent) {
	if u.PromptTokens > 0 {
		ev.PromptTokens = u.PromptTokens
	}
	if u.CompletionTokens > 0 {
		ev.CompletionTokens = u.CompletionTokens
	}
	if u.CompletionTokensDetails.ReasoningTokens > 0 {
		ev.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	if u.PromptTokensDetails.CachedTokens > 0 {
		ev.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
}

// isEventStream 报告 Content-Type 是否为 SSE 事件流。
func isEventStream(ct string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "text/event-stream")
}

// truncateUTF8 按最多 maxBytes 字节截断 s，停在完整的 UTF-8 字符边界上。
func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// cleanEndpoint 返回不含查询参数和用户凭据的请求地址。
func cleanEndpoint(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	u := *req.URL
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String()
}
