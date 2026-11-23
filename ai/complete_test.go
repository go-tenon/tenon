package ai_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/ai/aitest"
)

// TestCompleteJSON 检查 CompleteJSON 解码普通 JSON、带代码块围栏的 JSON，以及错误传播。
func TestCompleteJSON(t *testing.T) {
	t.Parallel()
	type answer struct {
		Language string `json:"language"`
		Text     string `json:"text"`
	}

	c := aitest.NewCompleter(
		`{"language":"zh-CN","text":"你好"}`,
		"```json\n{\"language\":\"en\",\"text\":\"hello\"}\n```",
		`not json`,
	)
	req := ai.CompleteRequest{
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "translate"}},
	}

	got, err := ai.CompleteJSON[answer](context.Background(), c, req)
	if err != nil {
		t.Fatalf("plain json: %v", err)
	}
	if got.Language != "zh-CN" || got.Text != "你好" {
		t.Fatalf("unexpected plain json: %+v", got)
	}

	got, err = ai.CompleteJSON[answer](context.Background(), c, req)
	if err != nil {
		t.Fatalf("fenced json: %v", err)
	}
	if got.Language != "en" || got.Text != "hello" {
		t.Fatalf("unexpected fenced json: %+v", got)
	}

	if _, err := ai.CompleteJSON[answer](context.Background(), c, req); err == nil {
		t.Fatal("invalid json: expected error")
	}

	wantErr := errors.New("upstream down")
	c.FailWith(wantErr)
	if _, err := ai.CompleteJSON[answer](context.Background(), c, req); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
