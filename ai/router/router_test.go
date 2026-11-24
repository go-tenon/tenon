package router_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/ai/aitest"
	"github.com/go-tenon/tenon/ai/router"
)

func boolPtr(v bool) *bool { return &v }

func samplePolicy() router.Policy {
	return router.Policy{
		Tiers: []router.Tier{
			{Key: "strong", Description: "复杂推理、架构设计与难题", Rank: 3, Models: []string{"claude-opus", "gpt-5"}},
			{Key: "fast", Description: "简单问答、翻译和润色", Rank: 1, Models: []string{"deepseek-chat"}},
			{Key: "balanced", Description: "常规编码与多步分析", Rank: 2, Models: []string{"claude-sonnet"}},
		},
		FallbackModel: "claude-sonnet",
		MinConfidence: 0.7,
		Timeout:       time.Second,
	}
}

func sampleCandidates() []router.Candidate {
	return []router.Candidate{
		{ID: "deepseek-chat", ContextWindow: 32_000, Capabilities: []router.Capability{router.CapabilityTools}},
		{ID: "claude-sonnet", ContextWindow: 200_000, Capabilities: []router.Capability{router.CapabilityTools, router.CapabilityVision}},
		{ID: "claude-opus", ContextWindow: 200_000, Capabilities: []router.Capability{router.CapabilityTools, router.CapabilityVision, router.CapabilityReasoning}},
		{ID: "gpt-5", ContextWindow: 400_000, Capabilities: []router.Capability{router.CapabilityTools, router.CapabilityVision, router.CapabilityReasoning}},
	}
}

func TestPolicyValidate(t *testing.T) {
	t.Parallel()
	_, err := router.New(router.Policy{}, nil, nil)
	assert.ErrorContains(t, err, "at least one tier")

	p := samplePolicy()
	p.MinConfidence = 1.5
	_, err = router.New(p, nil, nil)
	assert.ErrorContains(t, err, "min confidence")

	p = samplePolicy()
	p.SessionMode = "invalid"
	_, err = router.New(p, nil, nil)
	assert.ErrorContains(t, err, "unknown session mode")

	p = samplePolicy()
	p.Rules = []router.Rule{{Name: "bad", Tier: "missing"}}
	_, err = router.New(p, nil, nil)
	assert.ErrorContains(t, err, `unknown tier "missing"`)

	p = samplePolicy()
	p.Rules = []router.Rule{{Name: "bad-regex", Tier: "strong", Pattern: "["}}
	_, err = router.New(p, nil, nil)
	assert.ErrorContains(t, err, "invalid pattern")
}

func TestDecideClassifierAndLowConfidenceEscalation(t *testing.T) {
	t.Parallel()
	decider := aitest.NewDecider(
		map[string]ai.Answer{
			"tier": {Choice: "fast", Confidence: 0.92, Probabilities: []float64{0.92, 0.06, 0.02}},
		},
		map[string]ai.Answer{
			"tier": {Choice: "fast", Confidence: 0.52, Probabilities: []float64{0.52, 0.38, 0.10}},
		},
	)
	r, err := router.New(samplePolicy(), sampleCandidates(), decider)
	require.NoError(t, err)

	// 高置信度命中 fast，容灾链按 fast -> balanced -> strong 排列。
	d := r.Decide(t.Context(), router.Request{Messages: []router.Message{{Role: "user", Content: "你好"}}})
	assert.Equal(t, "fast", d.Tier)
	assert.Equal(t, router.ReasonClassifier, d.ReasonKind)
	assert.Equal(t, []string{"deepseek-chat", "claude-sonnet", "claude-opus", "gpt-5"}, d.Models)
	assert.False(t, d.Escalated)
	assert.InDelta(t, 0.92, d.Confidence, 1e-6)

	// 置信度低于 0.7 自动向更高 Rank 升档至 balanced。
	d = r.Decide(t.Context(), router.Request{Messages: []router.Message{{Role: "user", Content: "有点模糊的问题"}}})
	assert.Equal(t, "balanced", d.Tier)
	assert.Equal(t, router.ReasonEscalated, d.ReasonKind)
	assert.True(t, d.Escalated)
	assert.Equal(t, []string{"claude-sonnet", "claude-opus", "gpt-5", "deepseek-chat"}, d.Models)
}

func TestDecideCapabilityFilteringSkipsClassifierWhenSingleTierLeft(t *testing.T) {
	t.Parallel()
	decider := aitest.NewDecider()
	r, err := router.New(samplePolicy(), sampleCandidates(), decider)
	require.NoError(t, err)

	// 显式要求 reasoning 时只有 strong 档位满足，直接跳过分类器调用。
	d := r.Decide(t.Context(), router.Request{ReasoningRequested: true})
	assert.Equal(t, "strong", d.Tier)
	assert.Equal(t, []string{"claude-opus", "gpt-5"}, d.Models)
	assert.Empty(t, decider.Requests())

	// 带图片且超过 250k tokens 时，只剩 gpt-5 满足 400k 窗口与视觉能力。
	d = r.Decide(t.Context(), router.Request{HasImages: true, EstInputTokens: 250_000})
	assert.Equal(t, "strong", d.Tier)
	assert.Equal(t, []string{"gpt-5"}, d.Models)
	assert.Empty(t, decider.Requests())
}

func TestDecideRulesAndHint(t *testing.T) {
	t.Parallel()
	p := samplePolicy()
	p.Rules = []router.Rule{
		{Name: "long-context", Tier: "balanced", MinTokens: 50_000},
		{Name: "sql-debug", Tier: "strong", Keywords: []string{"deadlock", "explain analyze"}},
		{Name: "stack-trace", Tier: "strong", Pattern: `panic:\s+`},
		{Name: "vision-tools", Tier: "balanced", HasImages: boolPtr(true), HasTools: boolPtr(true)},
	}
	decider := aitest.NewDecider()
	r, err := router.New(p, sampleCandidates(), decider)
	require.NoError(t, err)

	// 显式 hint 优先级高于规则与分类器。
	d := r.Decide(t.Context(), router.Request{TierHint: "strong"})
	assert.Equal(t, "strong", d.Tier)
	assert.Equal(t, router.ReasonHint, d.ReasonKind)

	// 关键词命中规则。
	d = r.Decide(t.Context(), router.Request{Messages: []router.Message{{Role: "user", Content: "PostgreSQL EXPLAIN ANALYZE 很慢"}}})
	assert.Equal(t, "strong", d.Tier)
	assert.Equal(t, router.ReasonRule, d.ReasonKind)
	assert.Equal(t, "rule:sql-debug", d.Reason)

	// 正则命中规则。
	d = r.Decide(t.Context(), router.Request{Messages: []router.Message{{Role: "user", Content: "goroutine 1: panic: runtime error"}}})
	assert.Equal(t, "strong", d.Tier)
	assert.Equal(t, "rule:stack-trace", d.Reason)

	// 规则指向的档位若不满足硬约束，自动向上升档。
	p.Rules = []router.Rule{{Name: "force-fast", Tier: "fast", HasImages: boolPtr(true)}}
	r2, err := router.New(p, sampleCandidates(), decider)
	require.NoError(t, err)
	d = r2.Decide(t.Context(), router.Request{HasImages: true})
	assert.Equal(t, "balanced", d.Tier)
	assert.Equal(t, router.ReasonEscalated, d.ReasonKind)
	assert.Empty(t, decider.Requests())
}

func TestDecideSessionStickyAndEscalateOnly(t *testing.T) {
	t.Parallel()
	// Sticky：满足硬约束时直接锁定原模型，不调用分类器。
	p := samplePolicy()
	p.SessionMode = router.SessionSticky
	decider := aitest.NewDecider(
		map[string]ai.Answer{"tier": {Choice: "balanced", Confidence: 0.9, Probabilities: []float64{0.9, 0.1}}},
	)
	r, err := router.New(p, sampleCandidates(), decider)
	require.NoError(t, err)

	d := r.Decide(t.Context(), router.Request{SessionTier: "fast", SessionModel: "deepseek-chat"})
	assert.Equal(t, router.ReasonSticky, d.ReasonKind)
	assert.Equal(t, "fast", d.Tier)
	assert.Equal(t, "deepseek-chat", d.Models[0])
	assert.Empty(t, decider.Requests())

	// 若新一轮带图片而锁定模型不支持 vision，自动打破粘性重新决策。
	d = r.Decide(t.Context(), router.Request{SessionTier: "fast", SessionModel: "deepseek-chat", HasImages: true})
	assert.Equal(t, "balanced", d.Tier)
	assert.Equal(t, "claude-sonnet", d.Models[0])
	assert.Len(t, decider.Requests(), 1)

	// EscalateOnly：只升不降。
	p.SessionMode = router.SessionEscalateOnly
	decider2 := aitest.NewDecider(
		map[string]ai.Answer{"tier": {Choice: "fast", Confidence: 0.95, Probabilities: []float64{0.95, 0.03, 0.02}}},
		map[string]ai.Answer{"tier": {Choice: "strong", Confidence: 0.91, Probabilities: []float64{0.04, 0.05, 0.91}}},
	)
	r2, err := router.New(p, sampleCandidates(), decider2)
	require.NoError(t, err)

	// 分类器判 fast 但会话已在 balanced，保持 balanced 并优先复用会话模型。
	d = r2.Decide(t.Context(), router.Request{SessionTier: "balanced", SessionModel: "claude-sonnet"})
	assert.Equal(t, "balanced", d.Tier)
	assert.Equal(t, router.ReasonSticky, d.ReasonKind)
	assert.Equal(t, "claude-sonnet", d.Models[0])

	// 分类器判 strong 高于 balanced，允许升档。
	d = r2.Decide(t.Context(), router.Request{SessionTier: "balanced", SessionModel: "claude-sonnet"})
	assert.Equal(t, "strong", d.Tier)
	assert.Equal(t, router.ReasonEscalated, d.ReasonKind)
	assert.True(t, d.Escalated)
	assert.Equal(t, "claude-opus", d.Models[0])

	// 已处于最高可用 Rank 时跳过分类器直接沿用会话档位，不消耗分类器调用。
	d = r2.Decide(t.Context(), router.Request{SessionTier: "strong", SessionModel: "claude-opus"})
	assert.Equal(t, "strong", d.Tier)
	assert.Equal(t, router.ReasonSticky, d.ReasonKind)
	assert.Equal(t, "claude-opus", d.Models[0])
	assert.Len(t, decider2.Requests(), 2)

	// 显式 TierHint 和确定性规则优先级高于 SessionSticky。
	pRule := samplePolicy()
	pRule.SessionMode = router.SessionSticky
	pRule.Rules = []router.Rule{{Name: "sql-debug", Tier: "strong", Keywords: []string{"deadlock"}}}
	r3, err := router.New(pRule, sampleCandidates(), aitest.NewDecider())
	require.NoError(t, err)
	d = r3.Decide(t.Context(), router.Request{SessionTier: "fast", SessionModel: "deepseek-chat", TierHint: "strong"})
	assert.Equal(t, "strong", d.Tier)
	assert.Equal(t, router.ReasonHint, d.ReasonKind)
	d = r3.Decide(t.Context(), router.Request{
		SessionTier: "fast", SessionModel: "deepseek-chat",
		Messages: []router.Message{{Role: "user", Content: "遇到 deadlock 怎么排查"}},
	})
	assert.Equal(t, "strong", d.Tier)
	assert.Equal(t, router.ReasonRule, d.ReasonKind)
}

func TestDecideEqualRankAndStrictCandidates(t *testing.T) {
	t.Parallel()
	// 同 Rank 档位在低置信度时向更高 Rank 升档，而非横向跳到同 Rank 兄弟档位。
	p := router.Policy{
		Tiers: []router.Tier{
			{Key: "fast_a", Rank: 1, Models: []string{"m-a"}},
			{Key: "fast_b", Rank: 1, Models: []string{"m-b"}},
			{Key: "strong", Rank: 2, Models: []string{"m-strong"}},
		},
		MinConfidence:    0.8,
		StrictCandidates: true,
	}
	cands := []router.Candidate{
		{ID: "m-a"},
		{ID: "m-b"},
		{ID: "m-strong"},
	}
	decider := aitest.NewDecider(map[string]ai.Answer{
		"tier": {Choice: "fast_a", Confidence: 0.4, Probabilities: []float64{0.4, 0.3, 0.3}},
	})
	r, err := router.New(p, cands, decider)
	require.NoError(t, err)
	d := r.Decide(t.Context(), router.Request{})
	assert.Equal(t, "strong", d.Tier)
	assert.True(t, d.Escalated)

	// StrictCandidates 拒绝未在候选池中声明的模型。
	pStrict := router.Policy{
		Tiers: []router.Tier{
			{Key: "undeclared", Rank: 1, Models: []string{"ghost-model"}},
			{Key: "known", Rank: 2, Models: []string{"m-strong"}},
		},
		StrictCandidates: true,
	}
	rStrict, err := router.New(pStrict, cands, nil)
	require.NoError(t, err)
	d = rStrict.Decide(t.Context(), router.Request{})
	assert.Equal(t, "known", d.Tier)
	assert.Equal(t, []string{"m-strong"}, d.Models)
}

type slowDecider struct{ delay time.Duration }

func (s slowDecider) Decide(ctx context.Context, _ ai.DecisionRequest) (ai.Decision, error) {
	select {
	case <-time.After(s.delay):
		return ai.Decision{}, nil
	case <-ctx.Done():
		return ai.Decision{}, ctx.Err()
	}
}

func TestDecideFallbackOnErrorTimeoutAndRefusal(t *testing.T) {
	t.Parallel()
	// 分类器报错时回退到 FallbackModel。
	decider := aitest.NewDecider()
	decider.FailWith(errors.New("upstream 503"))
	r, err := router.New(samplePolicy(), sampleCandidates(), decider)
	require.NoError(t, err)

	d := r.Decide(t.Context(), router.Request{})
	assert.Equal(t, router.ReasonFallback, d.ReasonKind)
	assert.Equal(t, "claude-sonnet", d.Models[0])
	assert.Contains(t, d.Reason, "upstream 503")
	assert.ErrorContains(t, d.ClassifierErr, "upstream 503")

	// 分类器超时时在 Timeout 内快速回退。
	p := samplePolicy()
	p.Timeout = 30 * time.Millisecond
	rSlow, err := router.New(p, sampleCandidates(), slowDecider{delay: time.Second})
	require.NoError(t, err)
	d = rSlow.Decide(t.Context(), router.Request{})
	assert.Equal(t, router.ReasonFallback, d.ReasonKind)
	assert.Equal(t, "claude-sonnet", d.Models[0])
	assert.Less(t, d.Latency, 300*time.Millisecond)

	// 分类器拒答时回退。
	refuser := aitest.NewDecider(map[string]ai.Answer{"tier": {Refused: true}})
	rRefuse, err := router.New(samplePolicy(), sampleCandidates(), refuser)
	require.NoError(t, err)
	d = rRefuse.Decide(t.Context(), router.Request{})
	assert.Equal(t, router.ReasonFallback, d.ReasonKind)
	assert.Equal(t, "fallback:classifier_refused", d.Reason)
}

func TestSummarizeAndEstimateTokens(t *testing.T) {
	t.Parallel()
	msgs := []router.Message{
		{Role: "system", Content: "你是一个严格的代码审查助手，请仔细检查并发安全问题。"},
		{Role: "user", Content: "第一轮消息"},
		{Role: "assistant", Content: "第一轮回复"},
		{Role: "user", Content: "第二轮消息"},
		{Role: "assistant", Content: "第二轮回复"},
		{Role: "user", Content: "最终问题：分析这段锁竞争代码"},
	}
	sys, recent := router.Summarize(msgs, 120)
	assert.NotEmpty(t, sys)
	require.NotEmpty(t, recent)
	assert.Equal(t, "最终问题：分析这段锁竞争代码", recent[len(recent)-1].Content)

	assert.Greater(t, router.EstimateTokens("你好世界 hello world"), 5)
	assert.Equal(t, 0, router.EstimateTokens(""))
}
