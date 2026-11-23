package openai_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-tenon/tenon/ai"
)

func TestChatDecider(t *testing.T) {
	t.Parallel()
	s := newServer(t, func(w http.ResponseWriter, r request) {
		writeJSON(w, map[string]any{
			"model": "gpt-4.1-nano",
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": "```json\n" + `{
					  "answers": {
					    "urgent": {"refused": false, "probability": 0.91, "confidence": 0.88},
					    "department": {"refused": false, "choice": "billing", "confidence": 0.94, "probabilities": {"billing": 0.94, "technical": 0.06}},
					    "severity": {"refused": false, "level": 1, "confidence": 0.8, "probabilities": [0.2, 0.8]}
					  }
					}` + "\n```",
				},
			}},
			"usage": map[string]any{"prompt_tokens": 210, "completion_tokens": 45},
		})
	})

	res, err := s.client("gpt-4.1-nano").ChatDecider().Decide(t.Context(), ai.DecisionRequest{
		Input:     "我被重复扣款了",
		Questions: ticketQuestions,
	})
	require.NoError(t, err)
	assert.Equal(t, "gpt-4.1-nano", res.Model)
	assert.Equal(t, 210, res.InputTokens)
	assert.Equal(t, 45, res.OutputTokens)
	assert.True(t, res.Answers["urgent"].Yes(0.9))
	assert.Equal(t, "billing", res.Answers["department"].Choice)
	assert.Equal(t, []float64{0.94, 0.06}, res.Answers["department"].Probabilities)
	assert.Equal(t, 0.94, res.Answers["department"].Confidence)
	assert.Equal(t, 1, res.Answers["severity"].Level())
	assert.Equal(t, "阻塞", res.Answers["severity"].Label())
	assert.InDelta(t, 0.8, res.Answers["severity"].Score, 1e-6)

	r := s.last()
	assert.Equal(t, "/v1/chat/completions", r.Path)
	assert.Equal(t, "gpt-4.1-nano", r.JSON["model"])
	assert.Equal(t, float64(0), r.JSON["temperature"])
	assert.Equal(t, map[string]any{"type": "json_object"}, r.JSON["response_format"])
}

func TestChatDeciderFallbackProbabilitiesAndImages(t *testing.T) {
	t.Parallel()
	s := newServer(t, func(w http.ResponseWriter, r request) {
		writeJSON(w, map[string]any{
			"model": "qwen-turbo",
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"answers": {"tier": {"choice": "strong", "confidence": 0.85}, "damaged": {"refused": true}}}`,
				},
			}},
		})
	})

	res, err := s.client("qwen-turbo").ChatDecider().Decide(t.Context(), ai.DecisionRequest{
		Input:  map[string]any{"prompt": "分析这张架构图"},
		Images: []ai.Media{{Data: []byte("img"), MIME: "image/png", Name: "arch"}},
		Questions: []ai.Question{
			ai.Choice("tier", "选择档位", ai.ChoiceOption{Value: "fast"}, ai.ChoiceOption{Value: "strong"}),
			ai.YesNo("damaged", "是否损坏？"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "strong", res.Answers["tier"].Choice)
	assert.InDelta(t, 0.85, res.Answers["tier"].ProbabilityOf("strong"), 1e-6)
	assert.InDelta(t, 0.15, res.Answers["tier"].ProbabilityOf("fast"), 1e-6)
	assert.True(t, res.Answers["damaged"].Refused)

	msgs := s.last().JSON["messages"].([]any)
	user := msgs[1].(map[string]any)
	parts := user["content"].([]any)
	assert.Len(t, parts, 3)
	assert.Equal(t, "arch:", parts[1].(map[string]any)["text"])
}

func TestChatDeciderErrors(t *testing.T) {
	t.Parallel()
	reply := `{"answers": {}}`
	s := newServer(t, func(w http.ResponseWriter, r request) {
		writeJSON(w, map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": reply}}},
		})
	})
	d := s.client("m").ChatDecider()

	_, err := d.Decide(t.Context(), ai.DecisionRequest{Input: "x"})
	assert.ErrorContains(t, err, "at least one question")

	_, err = d.Decide(t.Context(), ai.DecisionRequest{Input: "x", Questions: ticketQuestions[:1]})
	assert.ErrorContains(t, err, `no answer for question "urgent"`)

	reply = `{"answers": {"department": {"choice": "unknown"}}}`
	_, err = d.Decide(t.Context(), ai.DecisionRequest{Input: "x", Questions: ticketQuestions[1:2]})
	assert.ErrorContains(t, err, `unknown option "unknown"`)

	reply = `not-json`
	_, err = d.Decide(t.Context(), ai.DecisionRequest{Input: "x", Questions: ticketQuestions[:1]})
	assert.ErrorContains(t, err, "parse chat decision json")
}
