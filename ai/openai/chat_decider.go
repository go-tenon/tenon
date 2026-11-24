package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/ai"
)

// ChatDecider 通过 OpenAI 兼容的 POST {BaseURL}/chat/completions 接口实现 ai.Decider。
// 它把决策问题编码为 JSON 输出指令，适用于未提供专用决策端点的普通文本对话模型。
type ChatDecider struct {
	client *Client
}

var _ ai.Decider = (*ChatDecider)(nil)

// ChatDecider 返回基于当前客户端 /chat/completions 接口的 ai.Decider 实现。
func (c *Client) ChatDecider() *ChatDecider { return &ChatDecider{client: c} }

// chatDecisionResponse 是 /chat/completions 接口返回的最小响应结构。
type chatDecisionResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// chatDecisionPayload 是要求对话模型输出的 JSON 结构。
type chatDecisionPayload struct {
	Answers map[string]chatDecisionAnswer `json:"answers"`
}

// chatDecisionAnswer 是对话模型对单个决策问题输出的 JSON 结构。
type chatDecisionAnswer struct {
	Refused       bool            `json:"refused"`
	Probability   float64         `json:"probability"`
	Choice        string          `json:"choice"`
	Level         *int            `json:"level"`
	Score         *float64        `json:"score"`
	Confidence    *float64        `json:"confidence"`
	Probabilities json.RawMessage `json:"probabilities"`
}

// Decide 实现 ai.Decider：将决策材料与问题作为对话消息发送，解析模型返回的 JSON 答案与 token 用量。
func (d *ChatDecider) Decide(ctx context.Context, req ai.DecisionRequest) (ai.Decision, error) {
	if err := req.Validate(); err != nil {
		return ai.Decision{}, err
	}
	userContent, err := chatDecisionUserContent(req)
	if err != nil {
		return ai.Decision{}, err
	}
	body := map[string]any{
		"model":       d.client.model,
		"temperature": 0,
		"response_format": map[string]string{
			"type": "json_object",
		},
		"messages": []map[string]any{
			{"role": "system", "content": chatDecisionSystemPrompt(req.Questions)},
			{"role": "user", "content": userContent},
		},
	}
	var resp chatDecisionResponse
	if err := d.client.client.Post(ctx, "chat/completions", body, &resp); err != nil {
		return ai.Decision{}, errors.Wrap(err, "ai/openai: chat decide")
	}
	if len(resp.Choices) == 0 {
		return ai.Decision{}, errors.New("ai/openai: chat decide returned no choices")
	}
	content := stripJSONFence(resp.Choices[0].Message.Content)
	var payload chatDecisionPayload
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return ai.Decision{}, errors.Wrap(err, "ai/openai: parse chat decision json")
	}
	out := ai.Decision{
		Answers:      make(map[string]ai.Answer, len(req.Questions)),
		Model:        resp.Model,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}
	for _, q := range req.Questions {
		raw, ok := payload.Answers[q.Name]
		if !ok {
			return ai.Decision{}, errors.Newf("ai/openai: chat decide has no answer for question %q", q.Name)
		}
		ans, err := chatDecisionAnswerOf(q, raw)
		if err != nil {
			return ai.Decision{}, err
		}
		out.Answers[q.Name] = ans
	}
	return out, nil
}

// chatDecisionSystemPrompt 按问题列表生成简洁明确的系统提示词，规定每个问题的 JSON 输出字段。
func chatDecisionSystemPrompt(qs []ai.Question) string {
	var b strings.Builder
	b.WriteString("You are a strict decision classifier. Evaluate the user input against every question below and return ONLY a single valid JSON object with top-level key \"answers\". Do not include explanations or markdown fences.\n\n")
	b.WriteString("Required JSON format:\n{\"answers\": {\"<question_name>\": <answer_object>}}\n\n")
	b.WriteString("Answer schemas by question kind:\n")
	b.WriteString("- yes_no: {\"refused\": false, \"probability\": <0..1>, \"confidence\": <0..1>}\n")
	b.WriteString("- choice: {\"refused\": false, \"choice\": \"<one_of_option_values>\", \"confidence\": <0..1>, \"probabilities\": {\"<option_value>\": <0..1>, ...}}\n")
	b.WriteString("- score: {\"refused\": false, \"level\": <0..N-1>, \"confidence\": <0..1>, \"probabilities\": [<p0>, <p1>, ...]}\n")
	b.WriteString("If you must refuse a question, return {\"refused\": true}.\n\nQuestions:\n")
	for _, q := range qs {
		fmt.Fprintf(&b, "\n- name: %q, kind: %s\n  instructions: %s\n", q.Name, q.Kind, q.Instructions)
		switch q.Kind {
		case ai.YesNoQuestion:
			if q.IfTrue != "" {
				fmt.Fprintf(&b, "  if_true: %s\n", q.IfTrue)
			}
			if q.IfFalse != "" {
				fmt.Fprintf(&b, "  if_false: %s\n", q.IfFalse)
			}
		case ai.ChoiceQuestion:
			b.WriteString("  options:\n")
			for _, o := range q.Options {
				if o.Description != "" {
					fmt.Fprintf(&b, "    - %q: %s\n", o.Value, o.Description)
				} else {
					fmt.Fprintf(&b, "    - %q\n", o.Value)
				}
			}
		case ai.ScoreQuestion:
			b.WriteString("  levels (0-based from lowest to highest):\n")
			for i, l := range q.Levels {
				if l.Description != "" {
					fmt.Fprintf(&b, "    - %d (%s): %s\n", i, l.Label, l.Description)
				} else {
					fmt.Fprintf(&b, "    - %d (%s)\n", i, l.Label)
				}
			}
		}
	}
	return b.String()
}

// chatDecisionUserContent 把决策请求的材料与图片编码为 chat/completions 的 user content。
func chatDecisionUserContent(req ai.DecisionRequest) (any, error) {
	var text string
	if s, ok := req.Input.(string); ok {
		text = s
	} else {
		raw, err := json.Marshal(req.Input)
		if err != nil {
			return nil, errors.Wrap(err, "ai/openai: marshal decision input")
		}
		text = string(raw)
	}
	if len(req.Images) == 0 {
		return text, nil
	}
	parts := []map[string]any{{"type": "text", "text": text}}
	for _, img := range req.Images {
		if img.Name != "" {
			parts = append(parts, map[string]any{"type": "text", "text": img.Name + ":"})
		}
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": img.DataURL()}})
	}
	return parts, nil
}

// stripJSONFence 去掉模型偶尔附加在 JSON 外层的 Markdown 代码块围栏。
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

// clamp01 把概率与置信度规范到 [0, 1] 区间内。
func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// chatDecisionAnswerOf 将对话模型返回的单个答案结构校验并转换为 ai.Answer。
// 模型未给出 confidence 或 probabilities 时保留 HasConfidence=false，不伪造 1.0 置信度。
func chatDecisionAnswerOf(q ai.Question, a chatDecisionAnswer) (ai.Answer, error) {
	if a.Refused {
		return ai.NewAnswer(q, ai.Answer{Refused: true}), nil
	}
	hasConf := a.Confidence != nil
	var conf float64
	if hasConf {
		conf = clamp01(*a.Confidence)
	}
	switch q.Kind {
	case ai.YesNoQuestion:
		return ai.NewAnswer(q, ai.Answer{
			Probability:   clamp01(a.Probability),
			Confidence:    conf,
			HasConfidence: hasConf,
		}), nil

	case ai.ChoiceQuestion:
		chosen := -1
		for i, o := range q.Options {
			if o.Value == a.Choice {
				chosen = i
				break
			}
		}
		if chosen < 0 {
			return ai.Answer{}, errors.Newf("ai/openai: chat decide chose unknown option %q for question %q", a.Choice, q.Name)
		}
		var probs []float64
		var byKey map[string]float64
		if len(a.Probabilities) > 0 && json.Unmarshal(a.Probabilities, &byKey) == nil && len(byKey) > 0 {
			probs = make([]float64, len(q.Options))
			for i, o := range q.Options {
				probs[i] = clamp01(byKey[o.Value])
			}
			hasConf = true
			if a.Confidence == nil {
				conf = probs[chosen]
			}
		} else if hasConf {
			probs = make([]float64, len(q.Options))
			probs[chosen] = conf
			if len(q.Options) > 1 {
				rest := (1 - conf) / float64(len(q.Options)-1)
				for i := range probs {
					if i != chosen {
						probs[i] = rest
					}
				}
			}
		}
		return ai.NewAnswer(q, ai.Answer{
			Choice:        a.Choice,
			Probabilities: probs,
			Confidence:    conf,
			HasConfidence: hasConf,
		}), nil

	case ai.ScoreQuestion:
		n := len(q.Levels)
		var probs []float64
		var arr []float64
		hasProbs := len(a.Probabilities) > 0 && json.Unmarshal(a.Probabilities, &arr) == nil && len(arr) == n
		if hasProbs {
			probs = make([]float64, n)
			for i, v := range arr {
				probs[i] = clamp01(v)
			}
			hasConf = true
		} else if a.Level != nil {
			if *a.Level < 0 || *a.Level >= n {
				return ai.Answer{}, errors.Newf("ai/openai: chat decide returned level %d out of range for question %q", *a.Level, q.Name)
			}
			if hasConf {
				probs = make([]float64, n)
				probs[*a.Level] = conf
				if n > 1 {
					rest := (1 - conf) / float64(n-1)
					for i := range probs {
						if i != *a.Level {
							probs[i] = rest
						}
					}
				}
			}
		} else {
			return ai.Answer{}, errors.Newf("ai/openai: chat decide missing level or probabilities for score question %q", q.Name)
		}
		var score float64
		switch {
		case a.Score != nil:
			score = *a.Score
		case len(probs) > 0:
			var sum, weighted float64
			for i, p := range probs {
				sum += p
				weighted += float64(i) * p
			}
			if sum > 0 {
				score = weighted / sum
			}
		case a.Level != nil:
			score = float64(*a.Level)
		}
		return ai.NewAnswer(q, ai.Answer{
			Score:         score,
			Probabilities: probs,
			Confidence:    conf,
			HasConfidence: hasConf,
		}), nil

	default:
		return ai.Answer{}, errors.Newf("ai/openai: unsupported decision question kind %q", q.Kind)
	}
}
