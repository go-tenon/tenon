package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
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
func (t *observeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	cloned := req.Clone(ctx)

	var reqBody []byte
	var reqModel string
	if cloned.Body != nil {
		raw, err := io.ReadAll(cloned.Body)
		_ = cloned.Body.Close()
		if err != nil {
			return nil, err
		}
		cloned.Body = io.NopCloser(bytes.NewReader(raw))
		cloned.ContentLength = int64(len(raw))
		reqBody = truncateBytes(raw, t.maxBody)
		var head struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(raw, &head) == nil {
			reqModel = head.Model
		}
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

	data, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	ev.Duration = time.Since(start)
	if readErr != nil {
		ev.Err = readErr
		t.fn(context.WithoutCancel(ctx), ev)
		return nil, readErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	ev.ResponseBody = truncateBytes(data, t.maxBody)
	parseCompletionEnvelope(data, &ev)
	t.fn(context.WithoutCancel(ctx), ev)
	return resp, nil
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
func (s *sseObserveBody) consume(chunk []byte) {
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
			if len(ch.Delta.Content) > remain {
				s.content.WriteString(ch.Delta.Content[:remain])
			} else {
				s.content.WriteString(ch.Delta.Content)
			}
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

// truncateBytes 将切片截断至不超过 limit 字节。
func truncateBytes(b []byte, limit int) []byte {
	if len(b) <= limit {
		return b
	}
	return b[:limit]
}
