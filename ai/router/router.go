// Package router 提供无状态的 AI 模型智能路由决策原语。
//
// 它基于模型能力画像、前置确定性规则、会话粘性策略以及 ai.Decider 分类器，
// 为每次对话请求选出有序的候选模型列表。包本身不绑定数据库或具体 HTTP 协议，
// 既可直接接入 AI 网关，也可配合 ai/chat 的 llmagent.WithModelSelector 使用。
package router

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/ai"
)

// Capability 是文本模型具备的可选能力标签，用于路由阶段的能力硬过滤。
type Capability string

const (
	// CapabilityVision 表示模型支持图像等多模态输入理解。
	CapabilityVision Capability = "vision"
	// CapabilityTools 表示模型支持工具调用（function calling）。
	CapabilityTools Capability = "tools"
	// CapabilityReasoning 表示模型支持深度推理与思考模式。
	CapabilityReasoning Capability = "reasoning"
	// CapabilityStructuredOutput 表示模型支持结构化 JSON Schema 输出。
	CapabilityStructuredOutput Capability = "structured_output"
)

// Candidate 描述一个可供路由选择的目标模型及其客观画像。
type Candidate struct {
	ID            string       // 模型唯一标识
	ContextWindow int          // 最大上下文窗口（token 数），0 表示不限制
	Capabilities  []Capability // 模型具备的能力标签列表
}

// HasCapability 报告候选模型 c 是否声明具备能力 cap。
func (c Candidate) HasCapability(cap Capability) bool {
	return slices.Contains(c.Capabilities, cap)
}

// Tier 是路由策略中的一个能力/成本档位，按 Rank 从低到高排列。
type Tier struct {
	Key         string   // 档位标识，在策略内唯一，例如 fast、balanced、strong
	Description string   // 自然语言说明，描述适合该档位的任务类型，供分类器判断
	Rank        int      // 能力等级序号，值越大能力越强；用于低置信度升档、故障向上兜底与只升不降会话策略
	Models      []string // 该档位按优先级排列的候选模型 ID 列表
}

// Rule 是前置确定性短路规则：满足全部已设定的非零条件时直接命中目标档位，跳过分类器调用。
type Rule struct {
	Name              string   // 规则名称，写入决策原因便于审计
	Tier              string   // 命中后指向的档位 Key
	MinTokens         int      // 估算输入 token 下限，0 表示不限
	MinTurns          int      // 对话轮次下限，0 表示不限
	HasImages         *bool    // 非 nil 时要求请求是否包含图片与之匹配
	HasTools          *bool    // 非 nil 时要求请求是否携带工具定义与之匹配
	ReasoningRequired *bool    // 非 nil 时要求请求是否显式开启推理与之匹配
	Keywords          []string // 任意关键词出现在最近用户消息中即匹配（不区分大小写）
	Pattern           string   // Go 正则表达式，命中最近用户消息即匹配；为空表示不限
}

// SessionMode 控制同一会话多轮请求之间的路由一致性策略。
type SessionMode string

const (
	// SessionStateless 每轮独立决策，不保留会话绑定。
	SessionStateless SessionMode = "stateless"
	// SessionSticky 首轮决策后锁定会话模型，后续同会话优先复用以命中上游上下文缓存。
	SessionSticky SessionMode = "sticky"
	// SessionEscalateOnly 同会话允许按更高档位升档，不降档，兼顾缓存与复杂任务升级。
	SessionEscalateOnly SessionMode = "escalate_only"
)

// defaultTimeout 是未指定 Timeout 时的单次分类器超时上限。
const defaultTimeout = 2 * time.Second

// defaultInstructions 是发给 ai.Decider 的默认路由分类指令。
const defaultInstructions = "根据请求的复杂度、所需推理深度、代码/工具任务难度和上下文特征，为本次对话选择性价比最合适的模型档位。"

// Policy 是一套完整的智能路由策略定义。
type Policy struct {
	Tiers         []Tier        // 档位列表，至少 1 个
	Rules         []Rule        // 前置确定性短路规则，按声明顺序匹配
	FallbackModel string        // 兜底模型 ID，分类器失败或档位不可用时回退
	Instructions  string        // 发给分类器的总体指令；为空时使用默认指令
	MinConfidence float64       // 分类器最低置信度（0~1），低于此值保守向更高 Rank 升档；0 表示不强制升档
	Timeout       time.Duration // 单次分类器调用超时上限；<= 0 时使用默认 2 秒
	SessionMode   SessionMode   // 会话粘性策略，为空时按 SessionStateless 处理
}

// Validate 检查路由策略的档位、规则和阈值是否合法。
func (p Policy) Validate() error {
	if len(p.Tiers) == 0 {
		return errors.New("ai/router: policy needs at least one tier")
	}
	if p.MinConfidence < 0 || p.MinConfidence > 1 {
		return errors.New("ai/router: min confidence must be between 0 and 1")
	}
	switch p.SessionMode {
	case "", SessionStateless, SessionSticky, SessionEscalateOnly:
	default:
		return errors.Newf("ai/router: unknown session mode %q", p.SessionMode)
	}
	tiers := make(map[string]bool, len(p.Tiers))
	for _, t := range p.Tiers {
		if t.Key == "" {
			return errors.New("ai/router: tier key is required")
		}
		if tiers[t.Key] {
			return errors.Newf("ai/router: duplicate tier key %q", t.Key)
		}
		if len(t.Models) == 0 {
			return errors.Newf("ai/router: tier %q needs at least one model", t.Key)
		}
		tiers[t.Key] = true
	}
	for i, r := range p.Rules {
		if !tiers[r.Tier] {
			return errors.Newf("ai/router: rule %d references unknown tier %q", i, r.Tier)
		}
		if r.Pattern != "" {
			if _, err := regexp.Compile(r.Pattern); err != nil {
				return errors.Wrapf(err, "ai/router: rule %d invalid pattern", i)
			}
		}
	}
	return nil
}

// Message 是提取后的单条精简对话消息，供摘要构造与规则匹配使用。
type Message struct {
	Role    string `json:"role"`               // system、user、assistant 或 tool
	Content string `json:"content"`            // 精简后的文本内容
	IsError bool   `json:"is_error,omitempty"` // 工具结果是否包含错误信号
}

// Request 是待路由请求的标准化特征输入。
type Request struct {
	EstInputTokens     int       // 估算输入 token 数
	TurnCount          int       // 对话轮次数
	HasImages          bool      // 是否包含图片等多模态内容
	HasTools           bool      // 是否携带工具定义
	ToolCount          int       // 工具定义数量
	InToolLoop         bool      // 是否处于工具调用循环中
	LastToolFailed     bool      // 最近一次工具调用是否返回错误
	StructuredOutput   bool      // 是否要求结构化 JSON 输出
	ReasoningRequested bool      // 是否显式要求推理/深度思考
	TierHint           string    // 调用方显式指定的档位 Key（例如请求头覆盖）
	SystemSummary      string    // 截断后的系统提示摘要
	Messages           []Message // 精简后的最近对话消息
	SessionTier        string    // 当前会话此前锁定的档位 Key；为空表示新会话
	SessionModel       string    // 当前会话此前锁定的模型 ID；为空表示新会话
}

// ReasonKind 标识路由决策的来源分类。
type ReasonKind string

const (
	// ReasonSticky 表示命中会话粘性锁定。
	ReasonSticky ReasonKind = "sticky"
	// ReasonHint 表示命中调用方显式指定的档位提示。
	ReasonHint ReasonKind = "hint"
	// ReasonRule 表示命中前置确定性规则。
	ReasonRule ReasonKind = "rule"
	// ReasonClassifier 表示由 ai.Decider 分类器选中。
	ReasonClassifier ReasonKind = "classifier"
	// ReasonEscalated 表示因低置信度、能力约束或会话升档策略升级到更高档位。
	ReasonEscalated ReasonKind = "escalated"
	// ReasonFallback 表示因分类器超时/报错/拒答或无可用候选而回退兜底模型。
	ReasonFallback ReasonKind = "fallback"
)

// Decision 是一次智能路由决策的结果。
type Decision struct {
	Tier            string             // 最终命中的档位 Key；直接回退兜底模型时可能为空
	Models          []string           // 按优先级排列、通过能力硬过滤的可用模型 ID 列表
	ReasonKind      ReasonKind         // 决策来源分类
	Reason          string             // 人类可读的简短决策原因，适合写入响应头和日志
	Confidence      float64            // 分类器置信度，未使用分类器时为 0
	Probabilities   map[string]float64 // 分类器给出的各档位概率分布，未使用分类器时为 nil
	Latency         time.Duration      // 本次路由决策总耗时
	InputTokens     int                // 分类器消耗的输入 token
	OutputTokens    int                // 分类器消耗的输出 token
	ClassifierModel string             // 分类器实际使用的模型名
	Escalated       bool               // 是否触发了升档
}

// compiledRule 保存预编译正则后的规则。
type compiledRule struct {
	rule    Rule
	pattern *regexp.Regexp
}

// Router 按 Policy、候选模型画像和可选的 ai.Decider 执行无状态路由决策。
type Router struct {
	policy     Policy
	tiers      []Tier               // 按 Rank 升序排列的档位副本
	tierByKey  map[string]int       // 档位 Key 到 tiers 索引的映射
	rules      []compiledRule       // 预编译的前置规则
	candidates map[string]Candidate // 模型 ID 到模型画像的映射
	hasCapPool map[Capability]bool  // 候选池中是否存在声明了某项能力的模型
	decider    ai.Decider           // 可选的分类器；为 nil 时仅按规则与默认档位选路
}

// New 用策略 p、候选模型画像 candidates 和可选分类器 decider 创建 Router。
// candidates 可以为空或仅覆盖部分模型；decider 为 nil 时只走规则与兜底逻辑。
func New(p Policy, candidates []Candidate, decider ai.Decider) (*Router, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.SessionMode == "" {
		p.SessionMode = SessionStateless
	}
	if p.Timeout <= 0 {
		p.Timeout = defaultTimeout
	}
	tiers := make([]Tier, len(p.Tiers))
	for i, t := range p.Tiers {
		t.Models = slices.Clone(t.Models)
		tiers[i] = t
	}
	slices.SortStableFunc(tiers, func(a, b Tier) int {
		return a.Rank - b.Rank
	})
	tierByKey := make(map[string]int, len(tiers))
	for i, t := range tiers {
		tierByKey[t.Key] = i
	}
	rules := make([]compiledRule, len(p.Rules))
	for i, r := range p.Rules {
		var re *regexp.Regexp
		if r.Pattern != "" {
			re = regexp.MustCompile(r.Pattern)
		}
		rules[i] = compiledRule{rule: r, pattern: re}
	}
	candMap := make(map[string]Candidate, len(candidates))
	hasCapPool := make(map[Capability]bool)
	for _, c := range candidates {
		c.Capabilities = slices.Clone(c.Capabilities)
		candMap[c.ID] = c
		for _, cap := range c.Capabilities {
			hasCapPool[cap] = true
		}
	}
	return &Router{
		policy:     p,
		tiers:      tiers,
		tierByKey:  tierByKey,
		rules:      rules,
		candidates: candMap,
		hasCapPool: hasCapPool,
		decider:    decider,
	}, nil
}

// Decide 为请求 req 执行路由决策，返回最终选中的档位、有序候选模型列表及决策元数据。
// 分类器超时、出错或拒答时不返回 error，而是自动降级回退到兜底模型并记录在 Decision.Reason 中。
func (r *Router) Decide(ctx context.Context, req Request) Decision {
	start := time.Now()
	finish := func(d Decision) Decision {
		d.Latency = time.Since(start)
		return d
	}

	// 1. 会话粘性（sticky）：若锁定模型满足本轮请求的硬约束，直接复用以命中上下文缓存。
	if r.policy.SessionMode == SessionSticky && req.SessionModel != "" && r.eligible(req.SessionModel, req) {
		tierKey := req.SessionTier
		if _, ok := r.tierByKey[tierKey]; !ok {
			tierKey = r.tierOfModel(req.SessionModel)
		}
		models := r.buildChain(tierKey, req, req.SessionModel)
		if len(models) > 0 {
			return finish(Decision{
				Tier:       tierKey,
				Models:     models,
				ReasonKind: ReasonSticky,
				Reason:     "sticky:" + tierKey,
			})
		}
	}

	// 2. 收集满足硬约束的可用档位。
	viable := r.viableTierIndexes(req)
	if len(viable) == 0 {
		// 所有模型都未能通过硬过滤时，按原始策略兜底，避免直接无模型可用。
		return finish(r.fallbackDecision(req, "no_eligible_candidate"))
	}

	var (
		chosenIdx  = -1
		reasonKind ReasonKind
		reason     string
		conf       float64
		probs      map[string]float64
		inTokens   int
		outTokens  int
		clfModel   string
		escalated  bool
	)

	// 3. 调用方显式档位提示（请求头覆盖）。
	if req.TierHint != "" {
		if idx, ok := r.tierByKey[req.TierHint]; ok {
			chosenIdx, escalated = r.resolveViableIndex(idx, viable)
			reasonKind = ReasonHint
			reason = "hint:" + r.tiers[chosenIdx].Key
			if escalated {
				reasonKind = ReasonEscalated
				reason = fmt.Sprintf("hint:%s->%s", req.TierHint, r.tiers[chosenIdx].Key)
			}
		}
	}

	// 4. 前置确定性短路规则。
	if chosenIdx < 0 {
		if matched, ok := r.matchRule(req); ok {
			idx := r.tierByKey[matched.Tier]
			chosenIdx, escalated = r.resolveViableIndex(idx, viable)
			reasonKind = ReasonRule
			label := matched.Name
			if label == "" {
				label = matched.Tier
			}
			reason = "rule:" + label
			if escalated {
				reasonKind = ReasonEscalated
				reason = fmt.Sprintf("rule:%s->%s", label, r.tiers[chosenIdx].Key)
			}
		}
	}

	// 5. 仅剩一个可行档位或未配置分类器时，跳过分类器直接确定档位。
	if chosenIdx < 0 && (len(viable) == 1 || r.decider == nil) {
		chosenIdx = viable[0]
		if len(viable) == 1 && len(r.tiers) > 1 {
			reasonKind = ReasonEscalated
			reason = "capability:" + r.tiers[chosenIdx].Key
			escalated = chosenIdx > 0
		} else {
			reasonKind = ReasonRule
			reason = "default:" + r.tiers[chosenIdx].Key
		}
	}

	// 6. 调用 ai.Decider 分类器对可行档位进行语义分类。
	if chosenIdx < 0 {
		res, err := r.classify(ctx, req, viable)
		if err != nil {
			d := r.fallbackDecision(req, "classifier_error:"+err.Error())
			return finish(d)
		}
		inTokens, outTokens, clfModel = res.InputTokens, res.OutputTokens, res.Model
		ans := res.Answers["tier"]
		if ans.Refused {
			d := r.fallbackDecision(req, "classifier_refused")
			d.InputTokens, d.OutputTokens, d.ClassifierModel = inTokens, outTokens, clfModel
			return finish(d)
		}
		idx, ok := r.tierByKey[ans.Choice]
		if !ok {
			d := r.fallbackDecision(req, "classifier_unknown_tier:"+ans.Choice)
			d.InputTokens, d.OutputTokens, d.ClassifierModel = inTokens, outTokens, clfModel
			return finish(d)
		}
		probs = make(map[string]float64, len(viable))
		for _, vi := range viable {
			key := r.tiers[vi].Key
			probs[key] = ans.ProbabilityOf(key)
		}
		conf = max(ans.Confidence, ans.ProbabilityOf(ans.Choice))
		chosenIdx = idx
		reasonKind = ReasonClassifier
		reason = fmt.Sprintf("classifier:%s(%.2f)", r.tiers[chosenIdx].Key, conf)

		// 置信度低于阈值时保守向高一档升档。
		if r.policy.MinConfidence > 0 && conf < r.policy.MinConfidence {
			if higher, ok := nextHigher(chosenIdx, viable); ok {
				origKey := r.tiers[chosenIdx].Key
				chosenIdx = higher
				escalated = true
				reasonKind = ReasonEscalated
				reason = fmt.Sprintf("low_confidence:%s(%.2f)->%s", origKey, conf, r.tiers[chosenIdx].Key)
			}
		}
	}

	// 7. 会话只升不降（escalate_only）：若本轮判定低于会话已有档位且原档位仍满足硬约束，则保持原档位。
	preferModel := ""
	if r.policy.SessionMode == SessionEscalateOnly && req.SessionTier != "" {
		if prevIdx, ok := r.tierByKey[req.SessionTier]; ok && slices.Contains(viable, prevIdx) {
			if chosenIdx < prevIdx {
				chosenIdx = prevIdx
				reasonKind = ReasonSticky
				reason = "sticky_floor:" + r.tiers[chosenIdx].Key
				escalated = false
				if req.SessionModel != "" && r.eligible(req.SessionModel, req) {
					preferModel = req.SessionModel
				}
			} else if chosenIdx > prevIdx {
				escalated = true
				reasonKind = ReasonEscalated
				reason = fmt.Sprintf("escalate:%s->%s", req.SessionTier, r.tiers[chosenIdx].Key)
			} else if req.SessionModel != "" && r.eligible(req.SessionModel, req) {
				preferModel = req.SessionModel
			}
		}
	}

	tierKey := r.tiers[chosenIdx].Key
	return finish(Decision{
		Tier:            tierKey,
		Models:          r.buildChain(tierKey, req, preferModel),
		ReasonKind:      reasonKind,
		Reason:          reason,
		Confidence:      conf,
		Probabilities:   probs,
		InputTokens:     inTokens,
		OutputTokens:    outTokens,
		ClassifierModel: clfModel,
		Escalated:       escalated,
	})
}

// eligible 检查模型 modelID 是否满足请求 req 的能力与上下文窗口硬约束。
// 若候选池中没有任何模型声明某项能力，则忽略该能力维度的硬过滤，避免未配置标签时误杀全部模型。
func (r *Router) eligible(modelID string, req Request) bool {
	c, ok := r.candidates[modelID]
	if !ok {
		return true
	}
	if req.EstInputTokens > 0 && c.ContextWindow > 0 && req.EstInputTokens > c.ContextWindow {
		return false
	}
	if req.HasImages && r.hasCapPool[CapabilityVision] && !c.HasCapability(CapabilityVision) {
		return false
	}
	if req.HasTools && r.hasCapPool[CapabilityTools] && !c.HasCapability(CapabilityTools) {
		return false
	}
	if req.ReasoningRequested && r.hasCapPool[CapabilityReasoning] && !c.HasCapability(CapabilityReasoning) {
		return false
	}
	if req.StructuredOutput && r.hasCapPool[CapabilityStructuredOutput] && !c.HasCapability(CapabilityStructuredOutput) {
		return false
	}
	return true
}

// viableTierIndexes 返回按 Rank 升序排列、至少包含一个满足硬约束模型的档位索引。
func (r *Router) viableTierIndexes(req Request) []int {
	out := make([]int, 0, len(r.tiers))
	for i, t := range r.tiers {
		for _, id := range t.Models {
			if r.eligible(id, req) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// resolveViableIndex 在 viable 中找到不低于 target 的最小可用档位索引；若更高档位均不可用，回退到最高可用档位。
func (r *Router) resolveViableIndex(target int, viable []int) (idx int, escalated bool) {
	if slices.Contains(viable, target) {
		return target, false
	}
	for _, vi := range viable {
		if vi > target {
			return vi, true
		}
	}
	return viable[len(viable)-1], false
}

// nextHigher 返回 viable 中严格大于 current 的最小档位索引。
func nextHigher(current int, viable []int) (int, bool) {
	for _, vi := range viable {
		if vi > current {
			return vi, true
		}
	}
	return 0, false
}

// matchRule 按顺序检查前置确定性规则，返回第一条命中的规则。
func (r *Router) matchRule(req Request) (Rule, bool) {
	userText := latestUserText(req.Messages)
	lowerText := strings.ToLower(userText)
	for _, cr := range r.rules {
		rule := cr.rule
		if rule.MinTokens > 0 && req.EstInputTokens < rule.MinTokens {
			continue
		}
		if rule.MinTurns > 0 && req.TurnCount < rule.MinTurns {
			continue
		}
		if rule.HasImages != nil && req.HasImages != *rule.HasImages {
			continue
		}
		if rule.HasTools != nil && req.HasTools != *rule.HasTools {
			continue
		}
		if rule.ReasoningRequired != nil && req.ReasoningRequested != *rule.ReasoningRequired {
			continue
		}
		if len(rule.Keywords) > 0 {
			matched := false
			for _, kw := range rule.Keywords {
				if kw != "" && strings.Contains(lowerText, strings.ToLower(kw)) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if cr.pattern != nil && !cr.pattern.MatchString(userText) {
			continue
		}
		return rule, true
	}
	return Rule{}, false
}

// latestUserText 返回最近一条用户消息的文本；没有用户消息时返回最后一条消息内容。
func latestUserText(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && strings.TrimSpace(msgs[i].Content) != "" {
			return msgs[i].Content
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content
	}
	return ""
}

// routingPayload 是发送给 ai.Decider 的去噪结构化材料。
type routingPayload struct {
	Signals        routingSignals `json:"signals"`
	SystemSummary  string         `json:"system_summary,omitempty"`
	RecentMessages []Message      `json:"recent_messages,omitempty"`
}

// routingSignals 汇总与任务难度相关的结构化元信号。
type routingSignals struct {
	EstInputTokens     int  `json:"est_input_tokens"`
	TurnCount          int  `json:"turn_count"`
	HasImages          bool `json:"has_images"`
	HasTools           bool `json:"has_tools"`
	ToolCount          int  `json:"tool_count,omitempty"`
	InToolLoop         bool `json:"in_tool_loop,omitempty"`
	LastToolFailed     bool `json:"last_tool_failed,omitempty"`
	StructuredOutput   bool `json:"structured_output,omitempty"`
	ReasoningRequested bool `json:"reasoning_requested,omitempty"`
}

// classify 在 Policy.Timeout 内调用 ai.Decider，从 viable 档位中选出最匹配的一项。
func (r *Router) classify(ctx context.Context, req Request, viable []int) (ai.Decision, error) {
	tctx, cancel := context.WithTimeout(ctx, r.policy.Timeout)
	defer cancel()
	options := make([]ai.ChoiceOption, len(viable))
	for i, vi := range viable {
		t := r.tiers[vi]
		options[i] = ai.ChoiceOption{Value: t.Key, Description: t.Description}
	}
	instructions := r.policy.Instructions
	if strings.TrimSpace(instructions) == "" {
		instructions = defaultInstructions
	}
	payload := routingPayload{
		Signals: routingSignals{
			EstInputTokens:     req.EstInputTokens,
			TurnCount:          req.TurnCount,
			HasImages:          req.HasImages,
			HasTools:           req.HasTools,
			ToolCount:          req.ToolCount,
			InToolLoop:         req.InToolLoop,
			LastToolFailed:     req.LastToolFailed,
			StructuredOutput:   req.StructuredOutput,
			ReasoningRequested: req.ReasoningRequested,
		},
		SystemSummary:  req.SystemSummary,
		RecentMessages: req.Messages,
	}
	return r.decider.Decide(tctx, ai.DecisionRequest{
		Input:     payload,
		Questions: []ai.Question{ai.Choice("tier", instructions, options...)},
	})
}

// buildChain 按「preferModel → 命中档位 → 更高档位 → 更低档位 → 兜底模型」的顺序生成去重候选模型链。
func (r *Router) buildChain(tierKey string, req Request, preferModel string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(id string, checkEligible bool) {
		if id == "" || seen[id] {
			return
		}
		if checkEligible && !r.eligible(id, req) {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(preferModel, true)
	idx, ok := r.tierByKey[tierKey]
	if ok {
		for _, id := range r.tiers[idx].Models {
			add(id, true)
		}
		for i := idx + 1; i < len(r.tiers); i++ {
			for _, id := range r.tiers[i].Models {
				add(id, true)
			}
		}
		for i := idx - 1; i >= 0; i-- {
			for _, id := range r.tiers[i].Models {
				add(id, true)
			}
		}
	}
	add(r.policy.FallbackModel, true)
	if len(out) == 0 {
		add(r.policy.FallbackModel, false)
	}
	return out
}

// fallbackDecision 构造回退决策：优先使用 Policy.FallbackModel，其次按从高到低的中间档位补齐容灾链。
func (r *Router) fallbackDecision(req Request, cause string) Decision {
	var out []string
	seen := make(map[string]bool)
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	if r.policy.FallbackModel != "" && r.eligible(r.policy.FallbackModel, req) {
		add(r.policy.FallbackModel)
	}
	mid := len(r.tiers) / 2
	for i := mid; i < len(r.tiers); i++ {
		for _, id := range r.tiers[i].Models {
			if r.eligible(id, req) {
				add(id)
			}
		}
	}
	for i := mid - 1; i >= 0; i-- {
		for _, id := range r.tiers[i].Models {
			if r.eligible(id, req) {
				add(id)
			}
		}
	}
	if len(out) == 0 {
		add(r.policy.FallbackModel)
		for _, t := range r.tiers {
			for _, id := range t.Models {
				add(id)
			}
		}
	}
	tierKey := ""
	if len(out) > 0 {
		tierKey = r.tierOfModel(out[0])
	}
	return Decision{
		Tier:       tierKey,
		Models:     out,
		ReasonKind: ReasonFallback,
		Reason:     "fallback:" + cause,
	}
}

// tierOfModel 返回包含 modelID 的第一个档位 Key；未命中时返回空字符串。
func (r *Router) tierOfModel(modelID string) string {
	for _, t := range r.tiers {
		if slices.Contains(t.Models, modelID) {
			return t.Key
		}
	}
	return ""
}

// Summarize 从原始消息列表中提取去噪后的系统摘要和最近几轮对话，控制发往分类器的总字符预算。
// maxRunes <= 0 时使用默认值 1500。
func Summarize(messages []Message, maxRunes int) (systemSummary string, recent []Message) {
	if maxRunes <= 0 {
		maxRunes = 1500
	}
	sysBudget := min(240, maxRunes/4)
	var sysParts []string
	var nonSys []Message
	for _, m := range messages {
		text := strings.TrimSpace(m.Content)
		if text == "" && !m.IsError {
			continue
		}
		if m.Role == "system" {
			sysParts = append(sysParts, text)
			continue
		}
		nonSys = append(nonSys, Message{Role: m.Role, Content: text, IsError: m.IsError})
	}
	if len(sysParts) > 0 {
		systemSummary = truncateRunes(strings.Join(sysParts, "\n"), sysBudget)
	}
	remain := max(maxRunes-utf8.RuneCountInString(systemSummary), 200)
	// 优先保留末尾最多 4 条非系统消息，按倒序分配字符预算确保最后一条用户消息最完整。
	start := max(len(nonSys)-4, 0)
	tail := nonSys[start:]
	picked := make([]Message, 0, len(tail))
	for i := len(tail) - 1; i >= 0 && remain > 0; i-- {
		m := tail[i]
		limit := remain
		if i < len(tail)-1 {
			limit = min(limit, maxRunes/4)
		}
		m.Content = truncateRunes(m.Content, limit)
		remain -= utf8.RuneCountInString(m.Content)
		picked = append(picked, m)
	}
	slices.Reverse(picked)
	return systemSummary, picked
}

// EstimateTokens 按中英文混合经验比例粗略估算文本的 token 数，供上下文窗口过滤和规则匹配使用。
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	var ascii, nonASCII int
	for _, r := range text {
		if r < 128 {
			ascii++
		} else {
			nonASCII++
		}
	}
	// ASCII 约 4 字符 1 token，中日韩等非 ASCII 字符约 1.5 token。
	return (ascii+3)/4 + (nonASCII*3+1)/2
}

// truncateRunes 将字符串截断到最多 maxRunes 个 Unicode 字符；超长时保留首尾片段并用省略号连接。
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 10 {
		return string(runes[:maxRunes])
	}
	head := (maxRunes - 3) * 2 / 3
	tail := maxRunes - 3 - head
	return string(runes[:head]) + "..." + string(runes[len(runes)-tail:])
}
