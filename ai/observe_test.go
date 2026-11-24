package ai_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-tenon/tenon/ai"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestObserveTransportNonStream(t *testing.T) {
	t.Parallel()
	var got ai.CallEvent
	tr := ai.ObserveTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"model":"gpt-4o","choices":[{"message":{"content":"你好"}}],"usage":{"prompt_tokens":12,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":5},"completion_tokens_details":{"reasoning_tokens":3}}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req-1"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}), func(_ context.Context, ev ai.CallEvent) {
		got = ev
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.com/v1/chat/completions?foo=bar", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}

	if got.Stream || got.StatusCode != 200 || got.Model != "gpt-4o" || got.Content != "你好" {
		t.Fatalf("unexpected event: %+v", got)
	}
	if got.Endpoint != "https://api.example.com/v1/chat/completions" {
		t.Fatalf("endpoint = %q", got.Endpoint)
	}
	if got.PromptTokens != 12 || got.CompletionTokens != 8 || got.CachedTokens != 5 || got.ReasoningTokens != 3 {
		t.Fatalf("unexpected usage: %+v", got)
	}
	if got.Headers.Get("X-Request-Id") != "req-1" {
		t.Fatalf("missing header: %+v", got.Headers)
	}
}

func TestObserveTransportStream(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var got ai.CallEvent
	sse := strings.Join([]string{
		`data: {"model":"gpt-4o","choices":[{"delta":{"content":"你"}}]}`,
		``,
		`data: {"model":"gpt-4o","choices":[{"delta":{"content":"好世界"}}]}`,
		``,
		`data: {"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":15,"prompt_tokens_details":{"cached_tokens":10},"completion_tokens_details":{"reasoning_tokens":4}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	tr := ai.ObserveTransport(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	}), func(_ context.Context, ev ai.CallEvent) {
		calls.Add(1)
		got = ev
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.com/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("callback should wait until stream finishes")
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != sse {
		t.Fatalf("stream passthrough corrupted")
	}
	_ = resp.Body.Close()

	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if !got.Stream || got.Content != "你好世界" || got.PromptTokens != 20 || got.CompletionTokens != 15 || got.CachedTokens != 10 || got.ReasoningTokens != 4 {
		t.Fatalf("unexpected stream event: %+v", got)
	}
}

func TestObserveTransportBoundedLargeBody(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var got ai.CallEvent
	largeReq := `{"model":"gpt-4o","payload":"` + strings.Repeat("a", 512) + `"}`
	largeResp := `{"model":"gpt-4o","payload":"` + strings.Repeat("b", 512) + `"}`
	tr := ai.ObserveTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if string(raw) != largeReq {
			t.Errorf("request body truncated: got %d want %d", len(raw), len(largeReq))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(largeResp)),
		}, nil
	}), func(_ context.Context, ev ai.CallEvent) {
		calls.Add(1)
		got = ev
	}, ai.ObserveOptions{MaxBodyBytes: 64})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.com/v1/chat/completions", strings.NewReader(largeReq))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if string(raw) != largeResp {
		t.Fatalf("response passthrough corrupted: got %d want %d", len(raw), len(largeResp))
	}
	if calls.Load() != 1 || len(got.RequestBody) != 64 || len(got.ResponseBody) != 64 || got.Model != "gpt-4o" {
		t.Fatalf("unexpected bounded event: calls=%d reqLen=%d respLen=%d model=%q", calls.Load(), len(got.RequestBody), len(got.ResponseBody), got.Model)
	}
}
