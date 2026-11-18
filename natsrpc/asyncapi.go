package natsrpc

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/nats-io/nats.go/micro"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/identity"
)

// schemaPrefix 是 AsyncAPI 文档中命名类型的引用前缀，与 OpenAPI 文档相同；名称同样由 contract.SchemaName 得出，
// 同一个 Go 类型在两份文档中名字和结构都相同。
const schemaPrefix = "#/components/schemas/"

// problemSchema 是错误响应体在 components.schemas 中的名字。
const problemSchema = "TenonRPCProblem"

// 请求、成功响应和错误响应的请求头，见 docs/rpc/protocol.md。
var (
	requestHeaders = map[string]any{
		"type": "object",
		"properties": map[string]any{
			HeaderAuthorization:  map[string]any{"type": "string", "description": "Bearer <token>"},
			HeaderAcceptLanguage: map[string]any{"type": "string", "description": "错误提示的语言，例如 zh-CN"},
			HeaderRequestID:      map[string]any{"type": "string", "description": "可选，1–128 个不含空格的可打印 ASCII 字符"},
			HeaderTimeout:        map[string]any{"type": "string", "description": "可选，调用方还愿意等待的毫秒数"},
			"traceparent":        map[string]any{"type": "string", "description": "可选，W3C Trace Context"},
			"tracestate":         map[string]any{"type": "string", "description": "可选，W3C Trace Context"},
		},
	}
	responseHeaders = map[string]any{
		"type": "object",
		"properties": map[string]any{
			HeaderRequestID:       map[string]any{"type": "string", "description": "这次调用的 ID"},
			HeaderContentLanguage: map[string]any{"type": "string", "description": "响应文案使用的语言"},
		},
	}
	errorHeaders = map[string]any{
		"type":     "object",
		"required": []string{HeaderErrorCode, HeaderError},
		"properties": map[string]any{
			HeaderErrorCode:       map[string]any{"type": "string", "description": "状态码，例如 409"},
			HeaderError:           map[string]any{"type": "string", "description": "状态码的标准描述，例如 Conflict"},
			HeaderRequestID:       map[string]any{"type": "string", "description": "这次调用的 ID"},
			HeaderContentLanguage: map[string]any{"type": "string", "description": "响应文案使用的语言"},
			HeaderRetryAfter:      map[string]any{"type": "string", "description": "建议的等待秒数，出现在 429 和部分 503 中"},
		},
	}
)

// problemJSON 是错误响应体（Problem）的 schema，字段与 HTTP 接口的错误响应相同。手写而不由类型生成，
// 字段错误内联在其中，不占用 components.schemas 中的其他名字。
var problemJSON = map[string]any{
	"type":     "object",
	"required": []string{"title", "status"},
	"properties": map[string]any{
		"title":  map[string]any{"type": "string", "description": "状态码的标准描述"},
		"status": map[string]any{"type": "integer", "description": "状态码，与 HTTP 接口相同"},
		"detail": map[string]any{"type": "string", "description": "展示给用户的文案，按 Accept-Language 翻译，不用于判断"},
		"code":   map[string]any{"type": "string", "description": "稳定的错误码，只有业务错误包含"},
		"params": map[string]any{"type": "object", "additionalProperties": true, "description": "业务错误的公开参数"},
		"errors": map[string]any{
			"type":        "array",
			"description": "字段错误的明细，只有请求格式错误包含",
			"items": map[string]any{
				"type":     "object",
				"required": []string{"message", "location"},
				"properties": map[string]any{
					"message":  map[string]any{"type": "string", "description": "按请求的语言翻译的提示"},
					"location": map[string]any{"type": "string", "description": "出错的位置，例如 body.items[0].name"},
					"value":    map[string]any{"description": "服务端收到的值"},
					"code":     map[string]any{"type": "string", "description": "违反的规则，例如 validation.min_length"},
				},
			},
		},
	},
}

// AsyncAPI 返回描述全部端点的 AsyncAPI 3.0 文档（JSON），见 docs/rpc/protocol.md 的接口描述：
// 每个端点是一个带 reply 的 receive 操作，参数和返回值的 JSON Schema 在 components.schemas 中，
// 与 OpenAPI 文档用同样的规则生成；扩展字段 x-tenon-permissions、x-required-permissions 与 OpenAPI 文档相同，
// 另有 x-tenon-idempotent 和 x-tenon-public。
//
// 它只读取登记的端点，不需要 NATS 连接，可以用零值的依赖构造 Server 后调用。
func (s *Server) AsyncAPI() ([]byte, error) {
	s.mu.Lock()
	endpoints := slices.Clone(s.endpoints)
	s.mu.Unlock()
	slices.SortFunc(endpoints, func(a, b *endpoint) int { return strings.Compare(a.spec.Name, b.spec.Name) })

	registry := huma.NewMapRegistry(schemaPrefix, contract.SchemaName)
	channels := map[string]any{}
	operations := map[string]any{}
	messages := map[string]any{
		"error": map[string]any{
			"name":        "error",
			"title":       "错误",
			"contentType": "application/json",
			"headers":     errorHeaders,
			"payload":     map[string]any{"$ref": schemaPrefix + problemSchema},
		},
	}
	for _, ep := range endpoints {
		request := map[string]any{
			"name":        ep.name + ".request",
			"contentType": "application/json",
			"headers":     requestHeaders,
			"payload":     registry.Schema(ep.inType, true, ep.name+"Request"),
		}
		response := map[string]any{
			"name":        ep.name + ".response",
			"contentType": "application/json",
			"headers":     responseHeaders,
		}
		if !ep.empty {
			response["payload"] = registry.Schema(ep.outType, true, ep.name+"Response")
		}
		messages[ep.name+".request"] = request
		messages[ep.name+".response"] = response

		reply := ep.name + ".reply"
		channels[ep.name] = map[string]any{
			"address":  ep.subject,
			"messages": map[string]any{"request": map[string]any{"$ref": "#/components/messages/" + ep.name + ".request"}},
		}
		// 回复地址是请求的 reply subject，由调用方的 NATS 客户端生成。
		channels[reply] = map[string]any{
			"address": nil,
			"messages": map[string]any{
				"response": map[string]any{"$ref": "#/components/messages/" + ep.name + ".response"},
				"error":    map[string]any{"$ref": "#/components/messages/error"},
			},
		}
		op := map[string]any{
			"action":   "receive",
			"channel":  map[string]any{"$ref": "#/channels/" + ep.name},
			"messages": []any{map[string]any{"$ref": "#/channels/" + ep.name + "/messages/request"}},
			"reply": map[string]any{
				"channel": map[string]any{"$ref": "#/channels/" + reply},
				"messages": []any{
					map[string]any{"$ref": "#/channels/" + reply + "/messages/response"},
					map[string]any{"$ref": "#/channels/" + reply + "/messages/error"},
				},
			},
			"bindings":               map[string]any{"nats": map[string]any{"queue": micro.DefaultQueueGroup, "bindingVersion": "0.1.0"}},
			"x-required-permissions": slices.Clone(ep.spec.Permissions),
			"x-tenon-idempotent":     ep.spec.Idempotent,
			"x-tenon-public":         ep.spec.Public,
		}
		if ep.spec.Description != "" {
			op["description"] = ep.spec.Description
		}
		if ep.spec.Permissions == nil {
			op["x-required-permissions"] = []string{}
		}
		operations[ep.name] = op
	}
	schemas := map[string]any{}
	for name, schema := range registry.Map() {
		schemas[name] = schema
	}
	schemas[problemSchema] = problemJSON

	info := map[string]any{"title": s.opts.Prefix, "version": s.opts.Version}
	if s.opts.Description != "" {
		info["description"] = s.opts.Description
	}
	doc := map[string]any{
		"asyncapi":           "3.0.0",
		"info":               info,
		"defaultContentType": "application/json",
		"channels":           channels,
		"operations":         operations,
		"components":         map[string]any{"schemas": schemas, "messages": messages},
	}
	if len(s.opts.Permissions) > 0 {
		doc["x-tenon-permissions"] = slices.SortedFunc(slices.Values(s.opts.Permissions), func(a, b identity.Permission) int {
			return strings.Compare(a.Name, b.Name)
		})
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	return data, errors.Wrap(err, "natsrpc: encode AsyncAPI")
}
