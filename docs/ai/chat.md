# AI 助手

`ai/chat` 包用于构建对话式的 AI 助手：模型边想边答，需要时调用应用提供的工具，回答以[服务端推送](../http/sse.md)实时显示给用户。

agent 本身由 [trpc-agent-go](https://github.com/trpc-group/trpc-agent-go) 提供：模型、工具、提示词、记忆、多 agent 协作都直接使用它的 API，见[它的文档](https://trpc-group.github.io/trpc-agent-go/)。`ai/chat` 补上 Web 应用还需要的部分：

- **后台运行**：一次回答可能持续几分钟，在 HTTP 请求之外运行。用户刷新页面或网络断开后，生成过程照常继续，重新连接后接着收到剩余的内容。
- **中途插话**：模型还在查资料时，用户可以随时继续发消息，模型照常完成手头的工作。
- **审批**：退款、发布这类操作，模型调用工具后先停下，由人确认后才执行。审批可以在几小时后、在另一个进程中进行。
- **前端协议**：实现 Vercel AI SDK 的 UI 消息流协议，前端直接使用 `useChat`。

几个术语：**agent** 是能自己决定下一步做什么的模型程序，它可以调用你提供的**工具**（Go 函数）来查数据、执行操作；一次**运行**是 agent 回答一个问题的全过程，可能包含多轮思考和工具调用。

阅读前需要了解：[服务端推送](../http/sse.md)；[依赖装配](../concepts/dependencies.md)；React 和 Vercel AI SDK 的 `useChat`（前端部分）。按对话选择 agent、使用应用自己的消息表、多模态、部署等见 [AI 助手进阶](chat-advanced.md)。
- **图片、语音和向量**：生成图片、合成和识别语音、计算向量、重排，接口统一，测试时有替身。
- **决策**：用专门的决策模型做分类、路由和打分，得到概率。

后两项在 `ai` 包中，与 `ai/chat` 相互独立：只用到它们的应用保持精简。

- [示例用法](#example-usage)
- [准备数据表](#preparing-tables)
- [装配](#assembling)
    - [配置模型](#configuring-models)
- [对话接口](#conversation-api)
    - [发送图片和文件](#files)
    - [停止回答](#stopping)
    - [中途插话](#steering)
    - [断线重连](#reconnecting)
- [审批](#approval)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面创建一个写作助手，在接口中开始一次回答，并把生成过程流式推给前端：

```go
agent := llmagent.New("writer",
    llmagent.WithModel(chat.NewModel(cfg.AI)),
    llmagent.WithInstruction("你是写作助手。回答简洁。"),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
)
runs := chat.NewRuns("app", agent, sessions)

// 接口：开始一次回答，把事件流式推给前端
run, err := runs.Submit(ctx, sub.ID, req)
if err != nil {
    return nil, err
}
return chat.UIStream(run), nil
```

回答在后台运行，页面刷新或断线后用 `chat.UIResume` 接着读；需要审批的工具调用先暂停，等用户批准。

`Submit` 在后台开始一次运行就返回，回答的内容通过 `UIStream` 以 Vercel AI SDK 的协议推给前端。即使用户刷新了页面，生成过程也照常继续，用 `UIResume` 就能接着读到剩余的内容。下面先准备数据表，再逐步介绍装配、对话接口和审批。

<a id="preparing-tables"></a>
## 准备数据表

对话的消息保存在数据库中，模型每次回答时都会读出完整的历史。先[发布](../database/migrations.md#publishing-framework-migrations)这些表的迁移：

```go
migrationCmd.AddCommand(migratecmd.Publish(
    chat.Migrations(),
))
```

这些表的名称都以 `agent_` 开头，表结构与 trpc-agent-go 的 PostgreSQL 会话存储完全一致，框架的测试会在每次升级 trpc-agent-go 时检查这一点。

<a id="assembling"></a>
## 装配

助手由三部分组成：模型、带工具的 agent，以及运行它的 `chat.Runs`。在入口打开共用资源的地方创建它们：

```go
chat.SetLogger(slog.Default())

sessions, err := chat.NewSessions(db)
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

- `chat.SetLogger` 让 trpc-agent-go 的日志写进应用的日志，带 `component=ai` 字段，可以[单独设置级别](../general/logging.md#component-levels)。
- `chat.NewSessions` 把对话保存在应用的 PostgreSQL 中，直接使用应用的连接池，连接池由应用自己关闭。
- `chat.RequireApproval` 列出需要审批的工具，见[审批](#approval)。
- `runs.Shutdown` 让进程停止时进行中的回答说完，见[部署](chat-advanced.md#deployment)；`runs.Close` 取消剩下的运行并关闭 runner。它们登记在数据库之后，所以会先于数据库关闭。

工具是普通的 Go 函数，用 trpc-agent-go 的 `function.NewFunctionTool` 包装，参数和返回值按 JSON 编码，字段说明会告诉模型如何使用它：

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
### 配置模型

`ai.ModelConfig` 描述 OpenAI 兼容接口上的一个模型，DeepSeek、通义千问、Kimi、OpenAI 等都提供这种接口。它只是一个普通的结构体，`chat.NewModel` 按传进来的值创建模型，不关心这些值从哪里来。

模型固定时，把它加进应用配置：

```go
type Config struct {
    // ...
    AI ai.ModelConfig `yaml:"ai"`
}
```

```yaml
ai:
  model: deepseek-chat
  base_url: https://api.deepseek.com
  api_key: ${DEEPSEEK_API_KEY}
  max_retries: 2 # 可省略，默认就是 2；-1 表示不重试
```

模型请求和普通的 HTTP 请求一样，遇到暂时的失败会重试几次，仍然失败就直接返回错误：

- 连接失败、`408`、`409`、`429` 和 `5xx` 时重试。服务端给出 `Retry-After`（1 分钟以内）就按它等待，否则指数退避：0.5 秒起，每次翻倍，最长 8 秒。
- 重试用完后返回错误。对话中，这次回答以 `error` 事件结束，用户可以重新发送；在[队列](../background/queue.md)任务中调用模型时，失败由任务的重试策略接着处理。
- 模型已经开始流式返回内容后出错时直接结束：已经推给用户的半段回答无法撤回，这次回答以错误结束。

`max_retries` 同样作用于[图片、语音和向量](chat-advanced.md#media)的请求。

如果模型由管理员在后台维护，或者让用户在对话中挑选，就不在启动时固定模型，改用 `llmagent.WithModelSelector`。它在每次调用模型之前执行，从应用自己的数据中查出这一次使用的模型：

```go
agent := llmagent.New("writer",
    llmagent.WithModelSelector(func(ctx context.Context, inv *agent.Invocation) (model.Model, error) {
        p, err := settings.WriterModel(ctx, inv.Session.UserID) // 应用自己的表和规则
        if err != nil {
            return nil, err
        }
        return chat.NewModel(ai.ModelConfig{Model: p.Model, BaseURL: p.BaseURL, APIKey: p.APIKey}), nil
    }),
    // ...
)
```

`chat.NewModel` 不访问网络，各模型共用进程的 HTTP 连接池，每次调用时创建也没有额外开销。后台修改后，下一次调用模型就使用新的配置；审批后继续和插话后补跑的那一轮同样经过这里。

Claude、Gemini 等其他接口的模型直接使用 trpc-agent-go 的对应包创建，`ai/chat` 的其余部分保持原样。

<a id="conversation-api"></a>
## 对话接口

助手的接口使用 [Vercel AI SDK](https://ai-sdk.dev) 的 [UI 消息流协议](https://ai-sdk.dev/docs/ai-sdk-ui/stream-protocol)，前端直接使用它的 `useChat`（React、Vue、Svelte 都有），流式显示、工具调用、审批和断线续传都现成可用。

每个对话用一个 ID 标识，由 `useChat` 生成。对话属于当前登录的编辑，`Runs` 的方法都以主体 ID 和对话 ID 定位它，一个编辑无法读取其他人的对话。

服务端提供五个接口，各接口的请求和响应见[客户端协议](../http/protocol.md#ai-chat)：

```go
type ChatInput struct {
    Body chat.UIRequest
}

type ConversationInput struct {
    ID string `path:"id"`
}

type SteerInput struct {
    ID   string `path:"id"`
    Body struct {
        Text string `json:"text" minLength:"1" maxLength:"4000"`
    }
}

type HistoryOutput struct {
    Body []chat.UIMessage
}

// POST /assistant/chat：发送消息或审批结果，响应是这次回答的消息流。
func (h *Handlers) Chat(ctx context.Context, in *ChatInput) (*huma.StreamResponse, error) {
    sub, _ := identity.FromContext(ctx)
    run, err := h.runs.Submit(ctx, sub.Ref().String(), in.Body)
    if err != nil {
        return nil, err
    }
    return chat.UIStream(run), nil
}

// GET /assistant/chat/{id}/stream：断线后接上进行中的回答。
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
        return nil, err // 没有进行中的回答时直接返回
    }
    run.Cancel()
    return nil, nil
}

// GET /assistant/chat/{id}/messages：打开页面时加载对话历史。
func (h *Handlers) History(ctx context.Context, in *ConversationInput) (*HistoryOutput, error) {
    sub, _ := identity.FromContext(ctx)
    messages, err := h.runs.UIMessages(ctx, sub.Ref().String(), in.ID)
    return &HistoryOutput{messages}, err
}
```

`Runs` 的 `userID` 传 `sub.Ref().String()`，例如 `user:42`：只传 `sub.ID` 时，不同账号体系中 ID 相同的两个人会共用对话。

两个流式接口用 `rest.RegisterSSE` 注册，`chat.UIEvents` 描述消息流，用于生成 OpenAPI 文档：

```go
rest.RegisterSSE(api, op, chat.UIEvents, h.Chat)
```

前端按这些接口配置 `useChat`：

```tsx
import { chatControls, chatTransport } from "@go-tenon/client";
import { session } from "./client"; // createSession 创建的会话，见客户端代码

const authed = session.fetch(fetch); // 同源发送会话 cookie，401 时会话变为未登录
const chat = chatControls('/api/v1/assistant/chat', authed);

const { messages, sendMessage, addToolApprovalResponse } = useChat({
  id: conversationId,
  messages: initialMessages, // await chat.messages(conversationId)
  resume: true,
  // 历史保存在服务端，chatTransport 只发送最后一条消息；响应不是 2xx 时抛出 HttpError，describeError 可以描述它
  transport: new DefaultChatTransport(chatTransport('/api/v1/assistant/chat', authed)),
  sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
});
```

React 前端可以直接用 `@go-tenon/ui/chat` 的 [`useAssistantChat`](../frontend/features.md#ai-chat)，它按上面的方式配置好 `useChat`，还处理了插话和停止。

`Runs.Submit` 在后台开始一次运行后立即返回，运行独立于请求，请求结束后照常继续。它也独立于 [`httpx.Timeout`](../http/middleware.md#request-timeout)，助手的路由和其他 API 装在同一组中间件下即可。对话正在回答时再次提交，`Submit` 返回 `chat.ErrBusy`（`409`）。

<a id="files"></a>
### 发送图片和文件

`useChat` 的 `sendMessage` 可以带上文件，`Submit` 把它们和文字一起交给模型，模型直接看到图片、听到音频、读到 PDF。使用 `@go-tenon/ui/chat` 时，`send(text, files)` 在发送前检查文件的类型和大小，`useAssistantComposer` 管理输入框中待发送的附件，见[附件](../frontend/features.md#chat-attachments)。直接使用 `useChat` 时：

```tsx
<form onSubmit={(e) => {
  e.preventDefault();
  sendMessage({ text, files }); // files 是 <input type="file" multiple> 的 FileList
}}>
```

`useChat` 把每个文件变成一个文件片段（`{"type": "file", "mediaType": "image/png", "filename": "cat.png", "url": "data:…"}`），`Submit` 按 `mediaType` 转换：

| `mediaType` | 交给模型的内容 |
| --- | --- |
| `image/*` | 图片 |
| `audio/*` | 音频；Chat Completions 接口只接受 wav 和 mp3 的文件内容，不接受链接 |
| `video/*` | 视频；Chat Completions 接口不支持，模型只看到一句说明，说明省略了视频 |
| 其他，例如 `application/pdf` | 文件 |

模型能否理解这些内容取决于模型本身：给不支持图片的模型发图片，模型服务通常会返回错误。消息可以只有文件、没有文字。

文件片段的 `url` 有两种写法：

- **data URL**：`useChat` 发送 `FileList` 时的默认做法，文件内容在请求体中，比文件大约三分之一。默认的[请求体上限](../http/requests.md#body-size)是 1 MiB，要按允许的文件大小放宽，`@go-tenon/ui/chat` 的默认限制对应 8 MiB：

  ```yaml
  http:
    body_limit:
      max_bytes: 8388608 # 8 MiB
  ```

  文件内容也随对话一起保存在对话存储中，页面打开时 `UIMessages` 原样返回。
- **http(s) 链接**：文件先[上传](../libraries/direct-uploads.md)到 S3 等处，消息中只放链接。链接原样交给模型服务，由模型服务下载，所以它必须能从公网访问，例如有效期足够长的签名地址；应用的服务端不访问它。适合较大的文件，请求体和对话存储都保持精简。

其他写法（例如 `blob:`）和缺少 `mediaType` 的文件返回 `422`。不使用 `useChat` 时，自己构造带 `ContentParts` 的 `model.Message` 交给 `Runs.Start`，例如用 `msg.AddImageData(data, "auto", "png")`。

<a id="attachments-in-tools"></a>
#### 在工具中使用附件

模型能看到图片，但调用工具时只能传 JSON 参数，传不了图片本身。用户发来一张照片问“有没有类似的商品”，工具要拿到这张照片才能[以图搜图](vector.md#image-queries)。

为此，`Runs` 在用户消息的每个附件前面写上 `[attachment 3f2a9c1e7b04]`，模型调用工具时把这个 ID 作为参数传入，工具用 `chat.FindAttachment` 取出附件：

```go
type SimilarArgs struct {
    AttachmentID string `json:"attachment_id" jsonschema:"description=用户发送的图片，消息中 [attachment …] 里的 ID"`
}

func NewSimilarProducts(vectors *vector.Store) tool.Tool {
    return function.NewFunctionTool(func(ctx context.Context, args SimilarArgs) ([]product.Summary, error) {
        a, err := chat.FindAttachment(ctx, args.AttachmentID)
        if err != nil {
            return nil, err // 错误交给模型
        }
        hits, err := product.Products.Search(ctx, vectors, vector.Query{
            Input:      vector.Content{vector.MediaPart(a.Media)},
            GroupByKey: true,
        })
        // ……
    }, function.WithName("similar_products"), function.WithDescription("按用户发送的图片查找相似的商品"))
}
```

- ID 由附件的内容计算，同一个附件在对话中始终是同一个 ID，之前几轮发送的附件也能找到。`chat.Attachments(ctx)` 列出对话中所有的附件。
- 标签只给模型看：`UIMessages` 返回的历史中没有它，调用方传入的消息也不会被修改。`Start`、`Submit`、`Send`、`Steer`、`Append` 收到的消息，以及 `UseHistory` 读出的历史，都会加上标签，应用自己保存的消息不用处理。
- 以链接发送的附件没有内容，`Attachment.URL` 是它的地址。
- ID 不存在时返回 `chat.ErrAttachmentNotFound`，错误作为工具的结果交给模型，模型可以据此改正 ID。工具收到的 ID 带着 `[attachment …]` 外壳时也能找到。

<a id="stopping"></a>
### 停止回答

用户点击「停止」时，前端调用停止接口：

```tsx
const { status } = useChat({ /* 同上 */ });

async function stopAnswer() {
  await chat.stop(conversationId);
}

{(status === 'submitted' || status === 'streaming') && <button onClick={stopAnswer}>停止</button>}
```

`stop()` 只断开浏览器的连接。回答在服务端后台运行，连接断开后照常继续：模型继续生成、继续调用工具、继续消耗 token，刷新页面后回答又接着出现。停止接口在服务端取消这次运行，消息流以 `abort` 结束，`useChat` 随之自动回到空闲状态。

停止之后：

- 已经生成的内容保留在对话中，用户可以接着发消息；
- 模型的请求和正在执行的工具收到取消的 `ctx`，工具应当把它传给数据库查询和外部调用，才能尽快停下；已经完成的工具调用（例如已经发出的邮件）保持生效；
- 部署了[多个实例](chat-advanced.md#multiple-instances)时，停止请求落在哪个实例上都可以，`Cancel` 转发给运行所在的实例，立即生效；
- 回答已经结束时调用停止接口直接返回，前端可以随时调用。

<a id="steering"></a>
### 中途插话

回答进行中，`useChat` 的 `sendMessage` 会等这次回答结束后才发送。要让模型在回答过程中就看到新消息，前端改为调用插话接口：

```ts
import { describeError } from "@go-tenon/client";

try {
  await chat.steer(conversationId, text);
} catch (err) {
  // 回答已经结束：作为新消息发送
  if (describeError(err).code === "ai.finished") sendMessage({ text });
  else throw err;
}
```

- 模型正在调用工具时，消息在这批工具调用结束后、模型下一次思考之前进入对话。模型会同时看到工具的结果和这条新消息。
- 模型已经在给出最终回答时，它先答完，然后接着回答这条消息。

两种情况下，消息都完整送达，模型照常完成当前的步骤。消息进入对话时，回答中出现一个数据片段 `data-steering`（`data` 是 `{"text": "…"}`），`useChat` 把它作为回答的一个 part，前端把它显示为一条用户消息，历史中也是如此。对话没有在回答时（包括请求到达时回答恰好结束），`Runs.Steer` 返回 `chat.ErrFinished`，客户端得到 `409`，错误码 `ai.finished`，前端改用 `sendMessage`。部署了多个实例时，从其他实例插话的结果相同。

<a id="reconnecting"></a>
### 断线重连

`useChat` 设置了 `resume: true` 时，页面打开和网络恢复后会请求 `GET {api}/{id}/stream`。回答还在进行时，`chat.UIResume` 从头重放它，`useChat` 用重放的内容重建这条回答；没有进行中的回答时返回 `204`。

已经完成的内容由 `Runs.UIMessages` 从对话存储中读出，作为 `useChat` 的初始消息。

<a id="approval"></a>
## 审批

`chat.RequireApproval` 列出的工具，模型调用时先等待审批。运行在其他工具执行完后停下，消息流为每个调用发出 `tool-approval-request`，`useChat` 中这个工具片段的状态变为 `approval-requested`，前端显示确认按钮：

```tsx
{part.state === 'approval-requested' && (
  <>
    <button onClick={() => addToolApprovalResponse({ id: part.approval.id, approved: true })}>发布</button>
    <button onClick={() => addToolApprovalResponse({ id: part.approval.id, approved: false, reason: '暂不发布' })}>取消</button>
  </>
)}
```

所有调用都有了决定后，`useChat` 自动把它们提交给 `POST /assistant/chat`。`Runs.Submit` 对批准的调用执行工具，对拒绝的调用把理由告诉模型，然后对话继续运行，新的回答接在同一条消息后面。

待审批的调用保存在对话中，所以审批可以在任何时候、在任何一个实例上进行。页面重新打开时，`Runs.UIMessages` 返回的历史中这些调用仍然处于 `approval-requested` 状态。不使用 `useChat` 时，用 `Runs.Pending` 读出它们，用 `Runs.Approve` 和 `Runs.Reject` 逐个处理。

对话等待审批期间，`Submit` 返回 `chat.ErrAwaitingApproval`（`409`），因为模型要先得到调用结果，才能回答新问题。等待审批前已经插入、还没有进入对话的消息，在审批之后的运行中送达。

<a id="troubleshooting"></a>
## 常见问题

**提交消息时返回 409，`chat.ErrBusy`**

这个对话正在回答中。等回答结束再提交，或者用[中途插话](#steering)把消息插进当前的回答。

**插话时返回 `409 ai.finished`**

对话没有在回答，包括请求到达时回答恰好结束。客户端改为普通地提交这条消息。

**对话一直停着，提交新消息返回 `chat.ErrAwaitingApproval`**

模型调用了需要审批的工具，在等人确认。先批准或拒绝，见[审批](#approval)。

**刷新页面后，回答从头开始显示，或者没有继续**

回答在后台运行，重新连接后会接着收到剩余的内容。检查前端是否按[断线重连](#reconnecting)的方式恢复对话。

**模型调用失败，提示认证错误**

模型服务商的 API Key 或地址配置不对，见[配置模型](#configuring-models)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/ai/chat`。

### NewRuns

```go
func NewRuns(app string, ag agent.Agent, sessions session.Service, opts ...Option) *Runs
func RequireApproval(tools ...tool.CallableTool) Option
func Cluster(hub *broadcast.Hub, locker *lock.Locker) Option
func AgentFor(choose func(ctx context.Context, c Conversation) (agent.Agent, error)) Option
func KeepFinished(d time.Duration) Option
```

`RequireApproval` 列出需要审批的工具；`Cluster` 让多个实例共同承担对话，见[多个实例](chat-advanced.md#multiple-instances)；`AgentFor` 按对话选择 agent。

### Runs

```go
func (rs *Runs) Submit(ctx context.Context, userID string, req UIRequest, opts ...agent.RunOption) (*Run, error)
func (rs *Runs) Start(ctx context.Context, userID, sessionID string, msg model.Message, opts ...agent.RunOption) (*Run, error)
func (rs *Runs) Send(ctx context.Context, userID, sessionID string, msg model.Message, opts ...agent.RunOption) (*Run, error)
func (rs *Runs) Steer(ctx context.Context, userID, sessionID string, msg model.Message) error
func (rs *Runs) Get(ctx context.Context, userID, sessionID string) (*Run, error)
func (rs *Runs) Pending(ctx context.Context, userID, sessionID string) ([]ToolCall, error)
func (rs *Runs) Approve(ctx context.Context, userID, sessionID, callID string, opts ...agent.RunOption) (*Run, error)
func (rs *Runs) Reject(ctx context.Context, userID, sessionID, callID, reason string, opts ...agent.RunOption) (*Run, error)
func (rs *Runs) UIMessages(ctx context.Context, userID, sessionID string) ([]UIMessage, error)
func (rs *Runs) Shutdown(ctx context.Context) error
func (rs *Runs) Close() error
```

| 方法 | 说明 |
| --- | --- |
| `Submit` | 处理前端发来的 Vercel AI SDK 请求，开始一次回答 |
| `Start`、`Send` | 在代码中开始一次回答 |
| `Steer` | 回答进行中插话；没有进行中的回答时返回 `ErrFinished`（`409 ai.finished`） |
| `Get` | 进行中的回答，断线后接着读 |
| `Pending`、`Approve`、`Reject` | 审批工具调用 |
| `UIMessages` | 对话的历史消息，供页面显示 |
| `Shutdown`、`Close` | 停止时等进行中的回答说完；取消剩下的 |

### 流

```go
func UIStream(run *Run) *huma.StreamResponse
func UIResume(run *Run) *huma.StreamResponse
func WriteUIStream(w http.ResponseWriter, r *http.Request, run *Run) error
```

按 Vercel AI SDK 的 UI 消息流协议推送事件。

### 其他

```go
func NewModel(cfg ai.ModelConfig, opts ...openai.Option) model.Model
func NewSessions(db *bun.DB, opts ...tpg.ServiceOpt) (session.Service, error)
func EraseUser(ctx context.Context, db bun.IDB, userID string) error
func ExportUser(ctx context.Context, db bun.IDB, userID string) (any, error)
func SetLogger(l *slog.Logger)
func Migrations() fs.FS
```

`NewModel` 用 OpenAI 兼容接口创建模型；`NewSessions` 把对话保存在应用的 PostgreSQL 中，`EraseUser`、`ExportUser` 删除、导出一个用户的全部对话，见[注销账号](../features/privacy.md#framework-sources)和[导出个人数据](../features/data-export.md#sources)。媒体、向量、重排和决策的接口在 `ai` 包中，见[媒体](chat-advanced.md#media)和[决策](chat-advanced.md#decisions)。
