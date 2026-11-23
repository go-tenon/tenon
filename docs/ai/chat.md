# AI 对话

用 `ai/chat` 构建 AI 对话（AI 助手）：回答在后台运行，以 Vercel AI SDK 的 UI 消息流推给前端，支持中途插话、断线续传和工具调用的审批。单次补全、生成图片、语音、向量、重排和决策在 `ai` 包中，与 `ai/chat` 相互独立。

包：`ai`、`ai/chat`、`ai/openai`、`ai/openrouter`、`ai/dashscope`、`ai/systemone`、`ai/router`、`ai/aitest`、`ai/chat/chattest`。

agent、模型、工具、记忆直接使用 [trpc-agent-go](https://trpc-group.github.io/trpc-agent-go/) 的 API。线路约定见[客户端协议](../http/protocol.md#ai-chat)，React 前端用 [`useAssistantChat`](../frontend/features.md#ai-chat)。

<a id="example-usage"></a>
## 示例

`cfg.AI` 是 `ai.ModelConfig`，`sessions` 见[装配](#assembling)，`sub` 是 `identity.FromContext` 取得的主体：

```go
agent := llmagent.New("writer",
    llmagent.WithModel(chat.NewModel(cfg.AI)),
    llmagent.WithInstruction("你是写作助手。回答简洁。"),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
)
runs := chat.NewRuns("app", agent, sessions)

// 接口中：开始一次回答，把事件流式推给前端
run, err := runs.Submit(ctx, sub.Ref().String(), req)
if err != nil {
    return nil, err
}
return chat.UIStream(run), nil
```

`Submit` 在后台开始运行后立即返回。请求结束、页面刷新或断线后运行照常继续。

<a id="preparing-tables"></a>
## 发布迁移

对话保存在 `agent_` 开头的表中，表结构与 trpc-agent-go 的 PostgreSQL 会话存储一致。[发布](../database/migrations.md#publishing-framework-migrations)迁移：

```go
migrationCmd.AddCommand(migratecmd.Publish(chat.Migrations()))
```

<a id="assembling"></a>
## 装配 `chat.Runs`

在入口打开共用资源的地方创建，`db` 是入口打开的 `*bun.DB`，`stack` 是清理资源的栈：

```go
chat.SetLogger(slog.Default()) // trpc-agent-go 的日志带 component=ai

sessions, err := chat.NewSessions(db) // 使用应用的连接池
if err != nil {
    return nil, err
}

publish := assistant.PublishTool(article.NewPublish(db))
agent := llmagent.New("writer",
    llmagent.WithModel(chat.NewModel(cfg.AI)),
    llmagent.WithInstruction("你是写作助手，帮编辑查找文章和发布草稿。回答简洁。"),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
    llmagent.WithTools([]tool.Tool{assistant.SearchTool(article.NewSearch(db)), publish}),
)
runs := chat.NewRuns("app", agent, sessions, chat.RequireApproval(publish))
stack.MustAdd("ai", func(ctx context.Context) error {
    // 等进行中的回答说完，最多 1 分钟，之后取消剩下的
    drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
    defer cancel()
    return errors.Join(runs.Shutdown(drain), runs.Close(), sessions.Close())
})
```

登记在数据库之后，先于数据库关闭。`chat.RequireApproval` 见[审批](#approval)，`Shutdown` 见[部署](#deployment)。

工具是用 `function.NewFunctionTool` 包装的 Go 函数，参数和返回值按 JSON 编码，`jsonschema` 中的说明交给模型：

```go
type SearchArgs struct {
    Query string `json:"query" jsonschema:"description=搜索关键词"`
}

func SearchTool(search *article.Search) tool.CallableTool {
    return function.NewFunctionTool(func(ctx context.Context, args SearchArgs) ([]article.Summary, error) {
        return search.Handle(ctx, args.Query, 10)
    }, function.WithName("search_articles"), function.WithDescription("按关键词搜索已发布的文章"))
}
```

<a id="configuring-models"></a>
### 配置模型：`ai.ModelConfig`

`ai.ModelConfig` 是 OpenAI 兼容接口（DeepSeek、通义千问、Kimi、OpenAI 等）上的一个模型，放进应用配置：

```yaml
ai:
  model: deepseek-chat
  base_url: https://api.deepseek.com
  api_key: ${DEEPSEEK_API_KEY}
  max_retries: 2 # 默认 2；-1 不重试
```

连接失败、`408`、`409`、`429` 和 `5xx` 时按 `Retry-After` 或指数退避（0.5 秒起，最长 8 秒）重试，用完后对话以 `error` 事件结束。模型开始流式返回后出错不重试。

模型由管理员维护或由用户挑选时，用 `llmagent.WithModelSelector` 在每次调用模型前查出配置。`chat.NewModel` 不访问网络、共用连接池，每次创建没有额外开销：

```go
agent := llmagent.New("writer",
    llmagent.WithModelSelector(func(ctx context.Context, inv *agent.Invocation) (model.Model, error) {
        p, err := settings.WriterModel(ctx, inv.Session.UserID) // 应用自己的表
        if err != nil {
            return nil, err
        }
        return chat.NewModel(ai.ModelConfig{Model: p.Model, BaseURL: p.BaseURL, APIKey: p.APIKey}), nil
    }),
    // ...
)
```

Claude、Gemini 等其他接口的模型用 trpc-agent-go 的对应包创建。`Runs` 在请求模型时把当前对话写入 `ctx`，通过 `openai.WithOpenAIOptions(option.WithHTTPClient(hc))` 注入的 `http.RoundTripper` 可用 `chat.ConversationFromContext(ctx)` 取出对话 ID 写入网关会话头；`EventDone` 的 `Done.Usage` 汇总本次运行累计的 token 用量。

<a id="conversation-api"></a>
## 对话接口

一个对话有五个接口，请求和响应见[客户端协议](../http/protocol.md#ai-chat)。`h.runs` 是装配好的 `*chat.Runs`，对话 ID 由前端生成：

```go
type ChatInput struct{ Body chat.UIRequest }
type ConversationInput struct {
    ID string `path:"id"`
}
type SteerInput struct {
    ID   string `path:"id"`
    Body struct {
        Text string `json:"text" minLength:"1" maxLength:"4000"`
    }
}
type HistoryOutput struct{ Body []chat.UIMessage }

// POST /assistant/chat：发送消息或审批结果，响应是这次回答的消息流。
func (h *Handlers) Chat(ctx context.Context, in *ChatInput) (*huma.StreamResponse, error) {
    sub, _ := identity.FromContext(ctx)
    run, err := h.runs.Submit(ctx, sub.Ref().String(), in.Body)
    if err != nil {
        return nil, err
    }
    return chat.UIStream(run), nil
}

// GET /assistant/chat/{id}/stream：断线后接上进行中的回答，没有时返回 204。
func (h *Handlers) Resume(ctx context.Context, in *ConversationInput) (*huma.StreamResponse, error) {
    sub, _ := identity.FromContext(ctx)
    run, err := h.runs.Get(ctx, sub.Ref().String(), in.ID)
    if err != nil {
        return nil, err
    }
    return chat.UIResume(run), nil
}

// POST /assistant/chat/{id}/steer：回答进行中插话。
func (h *Handlers) Steer(ctx context.Context, in *SteerInput) (*struct{}, error) {
    sub, _ := identity.FromContext(ctx)
    return nil, h.runs.Steer(ctx, sub.Ref().String(), in.ID, model.NewUserMessage(in.Body.Text))
}

// POST /assistant/chat/{id}/stop：停止进行中的回答。
func (h *Handlers) Stop(ctx context.Context, in *ConversationInput) (*struct{}, error) {
    sub, _ := identity.FromContext(ctx)
    run, err := h.runs.Get(ctx, sub.Ref().String(), in.ID)
    if err != nil || run == nil {
        return nil, err
    }
    run.Cancel()
    return nil, nil
}

// GET /assistant/chat/{id}/messages：打开页面时加载历史。
func (h *Handlers) History(ctx context.Context, in *ConversationInput) (*HistoryOutput, error) {
    sub, _ := identity.FromContext(ctx)
    messages, err := h.runs.UIMessages(ctx, sub.Ref().String(), in.ID)
    return &HistoryOutput{messages}, err
}
```

- `userID` 传 `sub.Ref().String()`（例如 `user:42`）：只传 `sub.ID` 时，不同账号体系中 ID 相同的两个人会共用对话。
- 流式接口用 `rest.RegisterSSE(api, op, chat.UIEvents, h.Chat)` 注册。
- 运行独立于请求和 [`httpx.Timeout`](../http/middleware.md#request-timeout)，助手的路由和其他 API 用同一组中间件。

<a id="errors"></a>
### 错误码

| 错误 | 响应 | 出现时机 |
| --- | --- | --- |
| `chat.ErrBusy` | `409 ai.busy` | 对话正在回答时 `Submit`、`Start`、`Append`；新消息改用[插话](#steering) |
| `chat.ErrAwaitingApproval` | `409 ai.awaiting_approval` | 有等待[审批](#approval)的调用时发送新消息 |
| `chat.ErrFinished` | `409 ai.finished` | 插话时回答已经结束，客户端改为发送新消息 |
| `chat.ErrNotPending` | `404 ai.not_pending` | 审批的调用不存在或已经处理过 |
| `chat.ErrBadRequest` | `422 ai.bad_request` | 请求体不合法，例如文件的 `url` 写法不支持 |
| `chat.ErrShuttingDown` | `503 ai.shutting_down` | 本实例正在停止 |
| `chat.ErrNoWorker` | `503 ai.no_worker` | 使用 `RunOnWorkers` 时没有 worker |

<a id="files"></a>
### 发送图片和文件

`Submit` 把 `useChat` 发来的文件片段按 `mediaType` 交给模型：`image/*` 是图片，`audio/*` 是音频（Chat Completions 只接受 wav 和 mp3 的内容），`video/*` 只给模型一句省略了视频的说明，其他（例如 PDF）是文件。模型不支持时模型服务通常返回错误。

文件片段的 `url` 有两种写法，其他写法和缺少 `mediaType` 返回 `422`：

- **data URL**：`useChat` 的默认做法，比文件大约三分之一，随对话保存。默认的[请求体上限](../http/requests.md#body-size)是 1 MiB，`useAssistantChat` 的默认限制要求放宽到 8 MiB：

  ```yaml
  http:
    body_limit:
      max_bytes: 8388608
  ```

- **http(s) 链接**：文件先[上传](../libraries/filesystem.md)，消息中放签名地址。链接由模型服务下载，必须能从公网访问。

在代码中发送时，把带 `ContentParts` 的 `model.Message` 交给 `Runs.Start`，例如 `msg.AddImageData(data, "auto", "png")`。

<a id="attachments-in-tools"></a>
#### 在工具中使用附件：`chat.FindAttachment`

`Runs` 在用户消息的每个附件前写上 `[attachment 3f2a9c1e7b04]`。模型调用工具时传这个 ID，工具用 `chat.FindAttachment` 取出附件，例如[以图搜图](vector.md#image-queries)：

```go
type SimilarArgs struct {
    AttachmentID string `json:"attachment_id" jsonschema:"description=消息中 [attachment …] 里的 ID"`
}

func NewSimilarProducts(vectors *vector.Store) tool.Tool {
    return function.NewFunctionTool(func(ctx context.Context, args SimilarArgs) ([]product.Summary, error) {
        a, err := chat.FindAttachment(ctx, args.AttachmentID)
        if err != nil {
            return nil, err // ErrAttachmentNotFound 交给模型，模型可以改正 ID
        }
        // 用 a.Media 检索；以链接发送的附件没有内容，a.URL 是它的地址
    }, function.WithName("similar_products"), function.WithDescription("按用户发送的图片查找相似的商品"))
}
```

ID 由附件内容计算，之前几轮的附件也能找到，`chat.Attachments(ctx)` 列出全部。标签只给模型看，`UIMessages` 的历史中没有它。

<a id="stopping"></a>
### 停止回答

客户端断开连接不会停止回答，停止接口调用 `Run.Cancel`，消息流以 `abort` 结束。已生成的内容保留在对话中；模型请求和工具收到取消的 `ctx`，工具要把它传给数据库和外部调用才能尽快停下，已经完成的工具调用保持生效。

<a id="steering"></a>
### 中途插话：`Runs.Steer`

`Runs.Steer` 把消息送进进行中的回答，模型照常完成当前步骤：正在调用工具时，消息在这批调用结束后进入对话；正在给出最终回答时，先答完再回答它。消息进入对话时消息流中出现 `data-steering` 片段。回答已经结束时返回 `chat.ErrFinished`，前端改为发送新消息。

<a id="reconnecting"></a>
### 断线重连

`useChat` 设置 `resume: true` 时，页面打开和网络恢复后请求恢复接口，`chat.UIResume` 从头重放进行中的回答。运行结束后保留 1 分钟供重连读取，用 `chat.KeepFinished` 修改。已完成的内容由 `Runs.UIMessages` 读出。

<a id="approval"></a>
## 审批工具调用：`chat.RequireApproval`

退款、发布这类操作，用 `chat.RequireApproval(tools...)` 要求模型调用后先由人确认；工具由 [`AgentFor`](#choosing-agents) 按对话提供时用 `chat.RequireApprovalOf(names...)` 按名称列出。

- 运行在其他工具执行完后停下，消息流为每个调用发出 `tool-approval-request`，前端用 [`useAssistantChat`](../frontend/features.md#ai-chat) 的 `pending` 和 `respond` 显示和提交。
- 审批结果发给 `POST /assistant/chat`，`Submit` 执行批准的调用，把拒绝的理由告诉模型，全部有了决定后对话继续；还有调用未决定时 `Submit` 返回 `nil`，`UIStream` 推送一个空的回答。
- 待审批的调用保存在对话中，几小时后、在任何实例上都能审批。不用 `useChat` 时用 `Runs.Pending`、`Runs.Approve`、`Runs.Reject`。
- 等待审批期间发送新消息返回 `chat.ErrAwaitingApproval`：模型要先得到调用结果。

<a id="choosing-agents"></a>
## 按对话选择 agent：`chat.AgentFor`

每个对话由不同的 agent 负责时（例如每个「AI 员工」有自己的模型、指令和工具），`NewRuns` 的 agent 传 `nil`，用 `chat.AgentFor` 选出：

```go
runs := chat.NewRuns("app", nil, sessions,
    chat.AgentFor(func(ctx context.Context, c chat.Conversation) (agent.Agent, error) {
        emp, err := employees.ForConversation(ctx, c.ID) // 应用自己的表
        if err != nil {
            return nil, err
        }
        return llmagent.New(emp.AgentName,
            llmagent.WithModel(chat.NewModel(emp.Model)),
            llmagent.WithInstruction(emp.Instruction),
            llmagent.WithTools(emp.Tools()),
        ), nil
    }),
    chat.RequireApprovalOf("refund_order"),
)
```

- 每一轮请求模型之前调用，修改配置后下一轮生效；返回错误时这一轮以 `error` 事件结束。
- 同一个员工的 agent 名称保持不变：trpc-agent-go 把其他名称的 agent 的回答当作其他参与者的话。
- 只有模型不同时用 [`llmagent.WithModelSelector`](#configuring-models)。

<a id="appending-messages"></a>
## 加入其他消息：`Runs.Append`

人写的回复、系统通知、其他渠道的消息，用 `Runs.Append` 写进对话，模型之后能看到：

```go
err := runs.Append(ctx, userID, conversationID, "staff:42", model.NewAssistantMessage("已为你补发"))
```

- agent 用 `llmagent.WithAddContextPrefix(true)` 创建时，模型看到 `[staff:42] said:` 前缀，才知道是谁说的。
- `UIMessages` 把它作为单独一条返回，`metadata.author` 是作者。
- 对话正在回答时返回 `chat.ErrBusy`，有待审批的调用时返回 `chat.ErrAwaitingApproval`。
- 对话中还没有用户消息时只能加入 `user` 消息：trpc-agent-go 丢弃第一条用户消息之前的内容。
- 已打开的页面由应用通过[广播](../libraries/broadcasting.md)通知后重新读取历史。

<a id="own-history"></a>
## 使用应用自己的消息表：`chat.UseHistory`

群聊、客服会话的消息已经在应用的表中时，用 `chat.UseHistory` 让 `Runs` 直接读写它，`sessions` 传 `nil`：

```go
runs := chat.NewRuns("app", agent, nil, chat.UseHistory(chatHistory{db}))
```

- 每一轮之前用 `Load` 读出上下文，运行中产生的消息逐条交给 `Save`。停止、插话、审批、多实例的用法不变。
- `UIMessages` 和 `Append` 返回 `chat.ErrNeedsSessions`，页面显示应用自己的消息。
- 多人共享的对话 `userID` 由应用约定，例如空字符串或租户 ID；权限由应用在调用前检查。

<a id="history-store"></a>
### 实现 `chat.HistoryStore`

`Load` 返回消息和游标（例如读到的最大序号），这一轮产生的消息交给 `Save` 时 `Entry.After` 是这个游标。`Load` 按**逻辑顺序**返回：`Save` 保存的消息排在它的 `After` 之后。模型回答期间其他人的发言因此排在回答之后，下一轮接着回应；按写入顺序时它们会被漏掉。消息表多存一列 `after_seq`：

```go
func (h chatHistory) Load(ctx context.Context, c chat.Conversation) (chat.History, error) {
    var rows []ChatMessage
    err := h.db.NewSelect().Model(&rows).Where("conversation_id = ?", c.ID).
        OrderExpr("COALESCE(after_seq, seq), after_seq IS NOT NULL, seq").Scan(ctx)
    if err != nil {
        return chat.History{}, err
    }
    var out chat.History
    var last int64
    for _, r := range rows {
        out.Messages = append(out.Messages, r.ModelMessage()) // 群成员的发言转为 user 消息，前加名字
        last = max(last, r.Seq)
    }
    out.Cursor = strconv.FormatInt(last, 10)
    return out, nil
}

func (h chatHistory) Save(ctx context.Context, c chat.Conversation, e chat.Entry) error {
    row := NewChatMessage(c.ID, e.Message, e.RunID)
    if e.After != "" {
        after, err := strconv.ParseInt(e.After, 10, 64)
        if err != nil {
            return err
        }
        row.AfterSeq = &after
    }
    return h.insert(ctx, row) // 在事务中锁住会话，分配下一个 seq
}
```

- 带工具调用的 assistant 消息和工具结果原样保存、原样返回：待审批的调用由它们算出。
- `Load` 的最后一条是模型的回答时，这一轮不请求模型。

<a id="group-chat"></a>
### 群聊中的 AI：`Runs.Notify`

群成员的发言由应用写入消息表，需要 AI 回应时（例如被 @）调用 `runs.Notify(ctx, "", conversationID)`：

- AI 没有在回答时开始一次运行；正在回答时（任何实例上）记下通知，结束后再运行一轮，多次通知只多运行一轮。
- 有待审批的调用时返回 `chat.ErrAwaitingApproval`。
- 返回的运行用 `chat.UIStream` 推送，群成员用 `runs.Get(ctx, "", conversationID)` 接上；保存后的消息由应用[广播](../libraries/broadcasting.md)。

<a id="complete"></a>
## 单次补全和结构化输出：`ai.Completer`

翻译、摘要、提取这类无状态单次调用使用 `ai.Completer`，与 `ai/chat` 独立、不引入对话运行时：

```go
type Translation struct {
    Language string `json:"language"`
    Text     string `json:"text"`
}

c := openai.New(cfg.Translation, openai.WithHTTPClient(hc))
res, err := ai.CompleteJSON[Translation](ctx, c, ai.CompleteRequest{
    Messages: []ai.Message{
        {Role: ai.RoleSystem, Content: "识别语言并翻译为中文。"},
        {Role: ai.RoleUser, Content: text},
    },
    ReasoningEffort: "none",
    JSONSchema: &ai.JSONSchema{
        Name:   "translation",
        Strict: true,
        Schema: translationSchema,
    },
})
```

- `openai.New` 发出标准 OpenAI `/chat/completions` 请求；`ai.CompleteJSON` 自动剥离模型偶尔附带的 Markdown 代码块围栏再解码。
- 计量、费用采集、会话头透传等横切关注点通过 `openai.WithHTTPClient` 注入自定义 `http.RoundTripper` 统一处理。

<a id="media"></a>
## 生成图片、语音和向量

`ai` 包的五个接口各有一个方法：`ai.Imager`（生成图片）、`ai.Speaker`（合成语音）、`ai.Transcriber`（识别语音）、`ai.Embedder`（计算向量）、`ai.Reranker`（重排）。实现它们的包：`ai/openai`（OpenAI 兼容接口，各家支持的能力以厂商文档为准）、`ai/openrouter`、`ai/dashscope`（阿里云百炼，见[图片和视频向量](#image-embeddings)）。`New` 接收 `ai.ModelConfig`，不访问网络。`cfg.*` 是 `ai.ModelConfig`：

```go
img, err := openai.New(cfg.Cover).Image(ctx, ai.ImageRequest{Prompt: "封面：" + post.Title, Aspect: ai.Landscape})
audio, err := openai.New(cfg.Voice).Speak(ctx, ai.SpeechRequest{Text: post.Body, Voice: "alloy"})
text, err := openai.New(cfg.Dictation).Transcribe(ctx, ai.TranscriptionRequest{Audio: rec, Language: "zh"})

// 一次传入多项；查询用 ai.ForQuery，文档用 ai.ForDocument（默认）
vecs, err := openai.New(cfg.Embedding, openai.WithBatchSize(10)).Embed(ctx, ai.EmbedRequest{
    Inputs: []ai.Content{ai.Text(post.Title), ai.Text(post.Body)},
})

// 按语义对任意类型的列表排序，取前 5 项
posts, err = ai.RerankBy(ctx, openai.New(cfg.Rerank), ai.Text(query), posts, func(p article.Summary) ai.Content {
    return ai.Text(p.Title)
}, 5)
```

- `ai.Media` 是内容和 MIME 类型。`ai.Content` 是文字、图片或二者组合，模型不接受时返回 `ai.ErrUnsupportedInput`。
- 厂商自己的参数用 `openai.WithRequestOptions` 追加；`Client.OpenAI()` 返回底层的 openai-go 客户端。
- 重排调用 `POST {BaseURL}/rerank`（Cohere、Jina 的格式），只接受文字。
- 保存和检索向量见[向量检索](vector.md)。生成图片和语音较慢，放进[队列](../background/queue.md)执行。

<a id="image-embeddings"></a>
### 图片和视频向量：`ai/dashscope`

以文搜图、以图搜图用百炼的多模态向量模型，`BaseURL` 填兼容接口的地址：

```go
emb := dashscope.New(cfg.Embedding, dashscope.WithDimensions(1024)) // Model: qwen3-vl-embedding

// 标题和主图融合为一个向量
docs, err := emb.Embed(ctx, ai.EmbedRequest{Inputs: []ai.Content{{ai.TextPart(p.Title), ai.MediaPart(cover)}}})
query, err := emb.Embed(ctx, ai.EmbedRequest{Inputs: []ai.Content{ai.Text("红色连衣裙")}, Purpose: ai.ForQuery})

// 多模态重排（qwen3-vl-rerank）
ranked, err := dashscope.New(cfg.Rerank).Rerank(ctx, ai.Content{ai.MediaPart(photo)}, candidates, 10)
```

- 每项是文字、图片、视频，或文字加一张图片（一段视频）；没有音频向量。超过 20 项时自动分批。
- 文字和图片融合只有 `qwen3-vl-embedding`、`qwen2.5-vl-embedding` 支持，其他模型返回 `ai.ErrUnsupportedInput`。`qwen2.5-vl-embedding` 必须用 `dashscope.WithBatchSize(1)`。
- 跨模态的相似度偏低（文字与视频实测 0.1～0.4），按排名取结果，不用固定阈值。

<a id="decisions"></a>
## 分类和打分：`ai.Decider`

工单分给哪个团队、评论是不是垃圾信息、问题有多严重，交给决策模型，它回答定义好的问题并给出概率：

| 问题 | 创建 | 回答 |
| --- | --- | --- |
| 是非题 | `ai.YesNo(name, instructions)`，`.Criteria(成立, 不成立)` | `Probability`；`Yes(threshold)` |
| 选择题 | `ai.Choice(name, instructions, options...)` | `Choice`；`ProbabilityOf(value)` |
| 打分题 | `ai.Score(name, instructions, levels...)` | `Score`；`Level()`、`Label()` |

```go
res, err := decider.Decide(ctx, ai.DecisionRequest{
    Input: ticket, // 字符串按文本发送，结构体按 JSON 发送
    Questions: []ai.Question{
        ai.Choice("team", "应该由哪个团队处理？",
            ai.ChoiceOption{Value: "billing", Description: "支付、退款和账单"},
            ai.ChoiceOption{Value: "technical", Description: "产品故障和集成"}),
        ai.YesNo("page", "是否需要立即通知值班人员？"),
    },
})
if err != nil {
    return err
}
team, page := res.Answers["team"].Choice, res.Answers["page"].Yes(0.8)
```

- 概率在不同模型之间不可比，阈值用自己的样本为每个模型确定。拒答时 `Refused` 为 true。
- 从业务数据中选一项用 `ai.DecideBy`，概率低于阈值或拒答时返回 false。
- `decider` 用 `openai.New`（`/decisions`）、`systemone.New`（`/systemone`，百炼、TypeSafe）或 `openai.New(cfg).ChatDecider()`（经 `/chat/completions` 输出 JSON，适用于普通文本模型）创建；`dashscope.New`、`openrouter.New` 也实现 `ai.Decider`。

<a id="routing"></a>
## 智能路由：`ai/router`

按请求特征、模型能力画像与 `ai.Decider` 分类器为每次对话选出有序候选模型列表，可配合 [`llmagent.WithModelSelector`](#configuring-models) 或自建网关使用：

```go
r, err := router.New(router.Policy{
    Tiers: []router.Tier{
        {Key: "fast", Description: "简单问答、翻译与润色", Rank: 1, Models: []string{"qwen-turbo"}},
        {Key: "strong", Description: "复杂编码、架构设计与深度推理", Rank: 2, Models: []string{"claude-sonnet"}},
    },
    FallbackModel: "claude-sonnet",
    MinConfidence: 0.7,
    SessionMode:   router.SessionEscalateOnly,
}, candidates, decider)

sys, recent := router.Summarize(msgs, 1500)
d := r.Decide(ctx, router.Request{
    EstInputTokens: router.EstimateTokens(prompt),
    SystemSummary:  sys,
    Messages:       recent,
})
// d.Models 是按优先级排列且通过能力硬过滤的候选模型 ID 列表
```

- `Candidate` 声明模型的上下文窗口与能力（`CapabilityVision`、`CapabilityTools`、`CapabilityReasoning`、`CapabilityStructuredOutput`），不满足请求硬约束的候选会被自动过滤。
- `Policy.Rules` 定义前置确定性短路规则；命中规则或仅剩一个可行档位时跳过分类器。
- 分类器超时、出错或拒答时自动回退到 `FallbackModel`，置信度低于 `MinConfidence` 时自动向更高 `Rank` 升档。
- `SessionMode` 支持 `SessionStateless`、`SessionSticky`（锁定首轮模型以命中上游上下文缓存）与 `SessionEscalateOnly`（同会话只升不降）。

<a id="deployment"></a>
## 部署

- **停机**：`runs.Shutdown` 拒绝新的回答（`503 ai.shutting_down`），等进行中的回答说完，超时才取消。这段时间计入停止宽限期，见[平滑停止](../installation/deployment.md#graceful-shutdown)。多实例时浏览器重连到其他实例继续接收。
- **后台任务**：生成摘要这类工作放进[队列](../background/queue.md)，在处理函数中调用 trpc-agent-go 的 `runner.Run`。任务至少执行一次，工具要能安全重复执行。

<a id="multiple-instances"></a>
### 多个实例：`chat.Cluster`

部署多个实例时加上 `chat.Cluster`，同一个对话的请求可以落在任何实例上。`hub` 是[广播](../libraries/broadcasting.md#creating-the-hub)的 Hub，`locker` 是[原子锁](../libraries/locks.md)：

```go
runs := chat.NewRuns("app", agent, sessions, chat.RequireApproval(publish), chat.Cluster(hub, locker))
```

- 运行所在的实例持有对话的锁，每个对话同一时刻只运行一次。
- 其他实例上的 `Runs.Get` 返回运行的镜像，`Steer` 和 `Cancel` 转发给运行所在的实例。
- 运行所在的实例意外退出时，锁在 30 秒内过期，镜像以 `error` 事件结束。

<a id="running-on-workers"></a>
### 在 worker 中运行：`chat.RunOnWorkers`

再加上 `chat.RunOnWorkers()`，回答交给 worker 进程运行，HTTP 实例部署和扩缩容不影响回答。worker 在处理[队列](../background/queue.md#running-workers)的同时调用 `runs.Serve`，`a.jobs` 返回原来的队列 worker 函数：

```go
func (a *App) Worker(d *command.Daemon) (func(context.Context) error, error) {
    jobs, err := a.jobs(d)
    if err != nil {
        return nil, err
    }
    return func(ctx context.Context) error {
        return command.Concurrently(ctx, jobs, a.runs.Serve)
    }, nil
}
```

- `Submit`、`Start`、`Notify`、`Approve`、`Reject` 把请求转交给一个 worker，返回运行的镜像，其余用法不变。
- 请求不排队：没有 worker 在 `Serve` 时返回 `chat.ErrNoWorker`，前端稍后重试。
- 传入 `agent.RunOption` 时返回 `chat.ErrRunOptions`，设置放在 agent 上。

<a id="testing"></a>
## 测试：`chattest` 和 `aitest`

`chattest.NewModel` 按脚本回复，代替 `chat.NewModel` 测试工具调用、插话和审批；会话存储用 trpc-agent-go 的 `inmemory.NewSessionService()`：

```go
m := chattest.NewModel(
    chattest.Calls(chattest.Call{Name: "search_articles", Args: assistant.SearchArgs{Query: "Go"}}),
    chattest.Text("找到 3 篇相关文章"),
)
// m.Requests() 返回模型收到的请求
```

`ai/aitest` 中是其他能力的替身：

```go
img := aitest.NewImager()           // 每次返回 aitest.PNG
emb := aitest.NewEmbedder(1024)     // 相同的内容得到相同的向量
tr := aitest.NewTranscriber("你好") // 依次返回预设的文字
dc := aitest.NewDecider()           // 是非题 0.5，选择题选第一项，打分题最低一级
```

还有 `NewCompleter`、`NewSpeaker`、`NewReranker`。预设结果用完后返回错误；`Requests()` 返回收到的请求，`FailWith(err)` 让之后的请求失败。`emb.Same(a, b)` 让一段描述和一张图片得到相同的向量，用于测试以文搜图。
