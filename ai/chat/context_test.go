package chat_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	topenai "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/ai/chat"
)

// TestConversationContextAndUsage 检查 Runs 在请求模型时把当前对话写入 context（供自定义 RoundTripper 注入网关会话头），
// 并在最终 EventDone 事件中汇总本次运行的 token 用量。
func TestConversationContextAndUsage(t *testing.T) {
	t.Parallel()

	var gotSessionHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSessionHeader = r.Header.Get("X-Omirouter-Session")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1",
			"object":"chat.completion",
			"created":1700000000,
			"model":"demo",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{
				"prompt_tokens":120,
				"completion_tokens":35,
				"total_tokens":155,
				"prompt_tokens_details":{"cached_tokens":40},
				"completion_tokens_details":{"reasoning_tokens":12}
			}
		}`))
	}))
	defer srv.Close()

	hc := &http.Client{
		Transport: rtFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			if conv, ok := chat.ConversationFromContext(req.Context()); ok && conv.ID != "" {
				clone.Header.Set("X-Omirouter-Session", conv.ID)
			}
			return http.DefaultTransport.RoundTrip(clone)
		}),
	}

	m := chat.NewModel(
		ai.ModelConfig{Model: "demo", BaseURL: srv.URL + "/v1", APIKey: "sk-test", MaxRetries: -1},
		topenai.WithOpenAIOptions(option.WithHTTPClient(hc)),
	)
	ag := llmagent.New("assistant",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}),
	)
	runs := chat.NewRuns("app", ag, inmemory.NewSessionService())
	defer func() { _ = runs.Close() }()

	run, err := runs.Start(t.Context(), "user-1", "sess-42", model.NewUserMessage("hi"))
	require.NoError(t, err)
	<-run.Done()

	assert.Equal(t, "sess-42", gotSessionHeader)

	var done *chat.Done
	for ev := range run.Events(t.Context(), "") {
		if ev.Name == chat.EventDone {
			d := ev.Data.(chat.Done)
			done = &d
		}
	}
	require.NotNil(t, done)
	require.NotNil(t, done.Usage)
	assert.Equal(t, 120, done.Usage.PromptTokens)
	assert.Equal(t, 40, done.Usage.CachedTokens)
	assert.Equal(t, 35, done.Usage.CompletionTokens)
	assert.Equal(t, 12, done.Usage.ReasoningTokens)
}

// rtFunc 将普通函数适配为 http.RoundTripper。
type rtFunc func(*http.Request) (*http.Response, error)

// RoundTrip 实现 http.RoundTripper。
func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
