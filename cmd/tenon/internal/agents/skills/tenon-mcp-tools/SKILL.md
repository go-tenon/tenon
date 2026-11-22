---
name: tenon-mcp-tools
description: 把 Tenon 应用的业务动作作为 MCP 工具提供给 Claude、Cursor 等 AI 客户端（mcpx）：业务动作的 Tool 方法、tools.go 中登记工具和权限、挂到 /mcp、本地 stdio 运行与测试。让应用的用户在 AI 客户端中调用应用的功能时使用。
---

# MCP 工具

`mcpx` 把业务动作作为 MCP 工具提供出去，认证和权限与 JSON 接口相同。这是应用给自己的用户提供的 AI 工具，与开发时助手用的 MCP 服务（tenon、gopls 等）无关。先读 `tenon/docs/ai/mcp.md`。

## 步骤

1. **工具方法**：业务动作的 `Tool(ctx, Args) (*Out, error)`，参数类型的标签与 REST 的请求体相同，`doc` 标签写清楚每个参数的含义和来源（例如“不知道 ID 时先用 article_search 查找”）。见 `tenon/docs/ai/mcp.md#arguments-and-results`。
2. **登记**：模块的 `tools.go` 中 `mcpx.Register(srv, mcpx.Tool{Name: "article_publish", Title: "发布文章", Description: "...", Permissions: []string{"articles:write"}}, NewPublish(db).Tool)`。只读的工具加 `ReadOnly: true`。见 `tenon/docs/ai/mcp.md#registering-tools`、`tenon/docs/ai/mcp.md#annotations`。
3. **挂到 HTTP**：`mcpx.New(mcpx.Options{Name, Version, Instructions})` 创建服务，调用各模块的 `Tools`，用 `mcpx.Handler` 挂到 `/mcp`，见 `tenon/docs/ai/mcp.md#serving`、`tenon/docs/ai/mcp.md#authentication`。
4. **本地 stdio**：加入 `mcpcmd.New`，本机的 AI 客户端以子进程运行 `mcp serve`；`mcp tools` 列出登记的工具，见 `tenon/docs/ai/mcp.md#stdio`。
5. **测试**：见 `tenon/docs/ai/mcp.md#testing`。

## 要点

- `Description` 必填，写清楚工具做什么、什么时候用、有什么限制：模型只能从这里和参数说明了解工具。
- 工具的粒度比 REST 接口粗，按模型要完成的任务设计，例如“发布并通知订阅者”是一个工具。
- 工具名小写字母开头，只有小写字母、数字和下划线；不合法、重复或缺少说明时 `Register` 直接 panic。
- 业务错误交给模型让它调整，内部错误只告诉它“内部错误”，完整的错误写进日志，见 `tenon/docs/ai/mcp.md#errors`。
- 主体没有权限的工具既看不到也调不了；给 AI 用的令牌只给需要的权限，见 `tenon/docs/security/api-tokens.md#personal-access-tokens`。
- claude.ai 这类远程连接器需要 OAuth 授权服务器，见 `tenon/docs/security/oauth-server.md`。
