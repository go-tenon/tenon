# 本地化

按请求的语言翻译给人看的文案：错误响应的 `detail`、校验提示和应用自己的文案。前端的界面文字见[界面文字](../frontend/i18n.md)。

包：`i18n`。

错误码和分类不随语言变化：客户端按 `code` 判断错误，把 `detail` 直接展示给用户。`i18n` 基于 [go-i18n](https://github.com/nicksnyder/go-i18n)，`i18n.New` 返回原生的 `*goi18n.Bundle`（下文叫语言目录），请求选定的语言放在 context 中。

<a id="example"></a>
## 示例

翻译文件放在项目的 `locales` 目录中，由这个文件嵌入程序：

```go
// Package locales 嵌入应用的翻译：业务错误码对应给用户看的提示。
package locales

import "embed"

//go:embed *.yaml
var FS embed.FS
```

```yaml
# locales/zh-CN.yaml
article.not_found: 文章不存在
```

装配 HTTP 服务时把语言目录交给默认中间件，`router` 是应用的 chi 路由，`cfg.HTTP` 是 [HTTP 配置](../http/middleware.md#http-configuration)：

```go
router.Use(httpx.Defaults(cfg.HTTP, httpx.Languages(i18n.MustNew("zh-CN", locales.FS)))...)
```

返回 `article.not_found` 业务错误的接口，响应的 `detail` 是“文章不存在”。请求带着 `Accept-Language: en` 而 `en.yaml` 中也有这条消息时，返回英文。

<a id="enabling-localization"></a>
## 启用本地化：`httpx.Languages`、`command.Languages`

单语言项目默认使用简体中文：不传 `httpx.Languages` 时，`httpx.Defaults` 只启用框架内置的简体中文消息（`zh-CN`），即使客户端发送 `Accept-Language: en` 也保持简体中文；业务错误在 `errorx.Define` 中附带的默认提示直接生效，无需创建 `locales` 目录。

切换源语言或支持多语言时，把语言目录交给每个入口：

```go
languages := i18n.MustNew("zh-CN", locales.FS)

router.Use(httpx.Defaults(cfg.HTTP, httpx.Languages(languages))...)  // HTTP 接口
command.Execute(root, command.Languages(languages))                  // 命令失败时的提示
root.AddCommand(httpcmd.Commands(routes, httpcmd.Translations(locales.FS))...) // 生成的客户端中的校验提示
```

- `i18n.New` 只启用默认语言和 `sources` 中实际出现的语言：只有 `zh-CN` 时保持单语言；`locales.FS` 中加入 `en.yaml` 后自动同时启用英文及框架内置英文消息。
- `root` 是 `command.Root` 创建的根命令，`routes` 构造应用的全部路由，见[装配命令行](commands.md#declaring-the-cli)。
- 后台任务等其他入口自己设置语言，见[在 HTTP 之外使用](#outside-http)。

<a id="built-in-messages"></a>
### 框架内置的消息

框架内置简体中文（`zh-CN`）和英文（`en`）消息，框架用 `errorx.Define` 定义的每个错误码都有这两种语言的译文，例如 `auth.unauthenticated`（“请先认证”）、`page.invalid_cursor`（“分页游标无效”）。只有应用启用了对应语言时才会加载该语言的内置消息；应用的翻译文件可以覆盖它们，见[覆盖内置消息](#overriding-built-in-messages)。

<a id="determining-the-locale"></a>
## 确定请求语言：`Accept-Language`

`httpx.Defaults` 中的 `httpx.Locale` 中间件按请求的 `Accept-Language` 头，从语言目录已有的语言中选出最匹配的一种，写入 context，并在 `Content-Language` 响应头中返回。不支持或无法解析的语言回退到默认语言。

```bash
curl -i http://localhost:8080/articles -H 'Accept-Language: en'
```

- 发送了 `Accept-Language: zh-CN` 却得到英文时，看 `Content-Language`：应用没有这种语言的翻译文件，协商回退到了默认语言。
- 代码中用 `i18n.Locale(ctx)` 读取当前请求选定的语言。

<a id="translation-files"></a>
## 翻译文件

每种语言一个 YAML 文件，文件名是语言标签，放在 `locales` 中，例如 `locales/en.yaml`：

```yaml
article.not_found: Article not found
article.slug_locked: The slug of a published article cannot be changed
article.slug_taken: "The slug \"{{.slug}}\" is already taken"
```

`locales/zh-CN.yaml`：

```yaml
article.not_found: 文章不存在
article.slug_locked: 已发布文章的 slug 不能修改
article.slug_taken: "slug“{{.slug}}”已被使用"
```

- 消息 ID 就是[业务错误码](errors.md#defining-errors)，错误响应按错误码找到译文。
- `{{.slug}}` 取自错误的[公开参数](errors.md#public-parameters)。
- 值中有冒号、引号或以 `{` 开头时，用双引号把整个值包起来，否则加载时报 YAML 格式错误。

<a id="loading-translations"></a>
## 用 `i18n.New` 加载翻译

`i18n.New` 的第一个参数是默认语言，之后是任意多个存放翻译文件的文件系统，通常是嵌入的 `locales.FS`：

```go
languages, err := i18n.New("en", locales.FS)
if err != nil {
    return nil, err
}
```

- 框架内置的消息最先加载，再按顺序加载各个文件系统中的全部 YAML 文件。
- 翻译文件嵌入在程序中时，加载失败只可能是文件写错了，用 `i18n.MustNew`，出错时 panic。
- 返回的 `*goi18n.Bundle` 可以修改。在启动时完成所有修改，开始处理请求后只读取它。

<a id="overriding-built-in-messages"></a>
### 覆盖内置消息

后加载的同名消息覆盖先加载的。内置消息最先加载，所以在应用的 `zh-CN.yaml` 中重新定义同一个消息 ID 就覆盖了内置的译文：

```yaml
auth.unauthenticated: 请先登录
```

传入多个文件系统时，排在后面的覆盖排在前面的。

<a id="retrieving-translations"></a>
## 用 `i18n.T` 翻译文案

`i18n.T` 按 context 中的语言翻译，第三个参数是模板数据，没有时传 `nil`：

```go
message := i18n.T(ctx, "article.slug_locked", nil)
message = i18n.T(ctx, "article.slug_taken", map[string]any{"slug": slug})
```

- 请求语言中缺少这条消息时，返回默认语言的译文。
- 默认语言也没有，或者 context 中没有语言（例如没有设置语言的后台任务）时，返回消息 ID 本身。

复数等功能用 `i18n.Localizer(ctx)` 取得 go-i18n 的 `*goi18n.Localizer` 直接调用，用法见 [go-i18n 的文档](https://github.com/nicksnyder/go-i18n)。context 中没有语言时它返回 `nil`：

```go
import goi18n "github.com/nicksnyder/go-i18n/v2/i18n"

message, err := i18n.Localizer(ctx).Localize(&goi18n.LocalizeConfig{
    MessageID:   "article.comments",
    PluralCount: count,
})
```

<a id="translating-errors"></a>
## 翻译错误消息

业务错误的响应以错误码为消息 ID 翻译 `detail`，[公开参数](errors.md#public-parameters)是模板数据。查找顺序为：当前语言的译文 → 默认语言的译文 → `errorx.Define` 定义时附带的默认提示模板 → HTTP 状态描述。

- 单语言项目直接在 `errorx.Define` 中写默认提示，请求语言与默认语言一致时不记为漏翻。
- 多语言项目中，附加语言缺少译文时回退到默认语言译文或内联提示，并在[访问日志](../http/middleware.md#access-log)中带上 `"untranslated":true`；`go tool tenon check --only errors` 在提交前检查所有附加语言是否补齐译文。
- 业务错误和[校验错误](../http/requests.md#validation-messages)会被翻译。路由不存在、服务端内部错误等其他错误保持英文描述，客户端按状态码显示自己的提示。

<a id="outside-http"></a>
## 在 HTTP 之外使用：`i18n.WithLocale`、`i18n.ErrorMessage`

`i18n.T` 只读取 context 中的语言，HTTP 请求的语言由 `httpx.Locale` 写入。后台任务等其他入口用 `i18n.WithLocale` 自己写入，否则 `i18n.T` 原样返回消息 ID。`n.languages` 是 `i18n.New` 返回的语言目录：

```go
func (n *Notifier) ArticlePublished(ctx context.Context, a *Article, author *User) error {
    ctx = i18n.WithLocale(ctx, n.languages, i18n.Match(n.languages, author.Language))
    title := i18n.T(ctx, "article.published", map[string]any{"title": a.Title})
    return n.send(ctx, author, title)
}
```

- `i18n.Match` 接受 `Accept-Language` 格式的偏好，可以传多个值，匹配不到时返回默认语言。
- 命令的 context 已经带有 `command.Languages` 的默认语言，见[错误提示](commands.md#error-messages)。

把业务错误变成给人看的提示时用 `i18n.ErrorMessage`，它与 HTTP 响应的 `detail` 使用同一条译文：

```go
msg, _ := i18n.ErrorMessage(ctx, err)
if msg == "" {
    msg = "操作失败" // 没有错误码或没有译文时，用这个入口自己的通用提示
}
```

第二个返回值表示当前语言缺少这条译文，用来记录需要补的翻译。
