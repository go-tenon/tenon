package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/ai/openai"
)

// TestComplete 检查 Client.Complete 按标准 OpenAI Chat Completions 协议发送参数并返回回答，以及使用自定义 HTTPClient。
func TestComplete(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		gotHeader = r.Header.Get("X-Test-Trace")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"text\":\"你好\"}"}}]}`))
	}))
	defer srv.Close()

	hc := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.Header.Set("X-Test-Trace", "trace-1")
			return http.DefaultTransport.RoundTrip(clone)
		}),
	}

	temp := 0.2
	c := openai.New(ai.ModelConfig{
		Model:      "translate-fast",
		BaseURL:    srv.URL + "/v1",
		APIKey:     "sk-test",
		MaxRetries: -1,
	}, openai.WithHTTPClient(hc))

	type out struct {
		Text string `json:"text"`
	}
	res, err := ai.CompleteJSON[out](context.Background(), c, ai.CompleteRequest{
		Messages: []ai.Message{
			{Role: ai.RoleSystem, Content: "Translate to Chinese."},
			{Role: ai.RoleUser, Content: "hello"},
		},
		Temperature:     &temp,
		MaxTokens:       128,
		ReasoningEffort: "none",
		JSONSchema: &ai.JSONSchema{
			Name:   "translation",
			Strict: true,
			Schema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		},
	})
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if res.Text != "你好" {
		t.Fatalf("text = %q, want 你好", res.Text)
	}
	if gotHeader != "trace-1" {
		t.Fatalf("header X-Test-Trace = %q, want trace-1", gotHeader)
	}
	if gotBody["model"] != "translate-fast" || gotBody["reasoning_effort"] != "none" || gotBody["temperature"] != 0.2 || gotBody["max_tokens"] != float64(128) {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
	rf, _ := gotBody["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format = %+v", rf)
	}
}

// roundTripFunc 将普通函数适配为 http.RoundTripper。
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip 实现 http.RoundTripper。
func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
