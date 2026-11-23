package openai

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/ai"
)

var _ ai.Completer = (*Client)(nil)

// chatCompletionResponse 是 POST {BaseURL}/chat/completions 的最小响应结构。
type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete 实现 ai.Completer，通过标准 OpenAI POST {BaseURL}/chat/completions 执行单次文本或结构化 JSON 补全。
func (c *Client) Complete(ctx context.Context, req ai.CompleteRequest) (string, error) {
	if len(req.Messages) == 0 {
		return "", errors.New("ai/openai: messages are required")
	}
	body := map[string]any{
		"model":    c.model,
		"messages": req.Messages,
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.ReasoningEffort != "" {
		body["reasoning_effort"] = req.ReasoningEffort
	}
	if req.JSONSchema != nil {
		js := map[string]any{
			"name":   req.JSONSchema.Name,
			"strict": req.JSONSchema.Strict,
			"schema": req.JSONSchema.Schema,
		}
		if req.JSONSchema.Description != "" {
			js["description"] = req.JSONSchema.Description
		}
		body["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": js,
		}
	}
	var resp chatCompletionResponse
	if err := c.client.Post(ctx, "chat/completions", body, &resp); err != nil {
		return "", errors.Wrap(err, "ai/openai: complete")
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("ai/openai: complete returned no choices")
	}
	return resp.Choices[0].Message.Content, nil
}
