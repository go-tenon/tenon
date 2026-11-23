package ai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cockroachdb/errors"
)

// Role 是单次文本补全消息的角色。
type Role string

// 单次文本补全消息的角色。
const (
	RoleSystem    Role = "system"    // 系统提示词。
	RoleUser      Role = "user"      // 用户输入。
	RoleAssistant Role = "assistant" // 模型回复（用于少样本示例）。
)

// Message 是单次补全请求中的一条消息。
type Message struct {
	Role    Role   `json:"role"`    // 角色：RoleSystem、RoleUser 或 RoleAssistant。
	Content string `json:"content"` // 消息文本内容。
}

// JSONSchema 描述要求模型遵循的 JSON Schema 结构化输出约束。
type JSONSchema struct {
	Name        string         // Schema 名称，例如 translation。
	Description string         // 可选的 Schema 说明。
	Schema      map[string]any // JSON Schema 定义。
	Strict      bool           // 是否要求模型严格遵循 Schema。
}

// CompleteRequest 是一次无状态文本或结构化 JSON 补全请求。
type CompleteRequest struct {
	Messages        []Message   // 按顺序发送的消息列表，不能为空。
	Temperature     *float64    // 采样温度；为 nil 时使用模型默认值。
	MaxTokens       int         // 生成的最大 token 数；不大于 0 时使用模型默认值。
	ReasoningEffort string      // 推理思考力度，例如 "none"、"low"、"medium"、"high"；为空时使用模型默认值。
	JSONSchema      *JSONSchema // 非空时要求模型按该 JSON Schema 输出 JSON 对象。
}

// Completer 执行一次无状态的文本或结构化 JSON 补全，返回模型生成的文本内容。
type Completer interface {
	Complete(ctx context.Context, req CompleteRequest) (string, error)
}

// CompleteJSON 调用 c 执行 req，并把模型返回的 JSON 文本解码为 T。
// 模型用 Markdown 代码块围栏包裹 JSON 时自动剥离围栏后再解码。
func CompleteJSON[T any](ctx context.Context, c Completer, req CompleteRequest) (T, error) {
	var zero T
	text, err := c.Complete(ctx, req)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal([]byte(stripCodeFence(text)), &out); err != nil {
		return zero, errors.Wrap(err, "ai: decode json completion")
	}
	return out, nil
}

// stripCodeFence 去掉模型偶尔包在 JSON 外层的 Markdown 代码块围栏。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}
