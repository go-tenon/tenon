# 本地化

[文档首页](README.md) / 进阶使用

本地化用于展示文案，不改变业务错误的 Code 或 Kind。目录组合后得到原生 go-i18n Bundle，应用可继续使用 go-i18n Localizer。

本章内容：

- [启用内置语言](#bundle)
- [请求语言](#negotiation)
- [添加应用消息](#messages)
- [在代码中翻译](#localizer)
- [错误文案的回退](#fallback)

<a id="bundle"></a>

## 启用内置语言

下面是构建语言目录并连接 API 的完整函数：

```go
package api

import (
    "github.com/ryan053857/mygoravel/httpx"
    "github.com/ryan053857/mygoravel/httpx/rest"
    "github.com/ryan053857/mygoravel/i18n"
)

func NewAPI() (*rest.API, error) {
    bundle, err := i18n.New(i18n.Options{
        Default: "zh-CN",
        Layers: []i18n.Layer{i18n.FrameworkLayer()},
    })
    if err != nil {
        return nil, err
    }
    return rest.New(rest.Options{
        Middleware: httpx.MiddlewareOptions{Languages: bundle},
    }), nil
}
```

内置目录提供 en 和 zh-CN。未填写 `i18n.Options.Default` 时，目录构建默认使用 zh-CN。应用可以通过自己的配置将 Default 显式设置为 en。

<a id="negotiation"></a>

## 请求语言

提供 Languages 后，中间件根据 `Accept-Language` 选择已加载语言，并写入 `Content-Language` 响应头。不支持或无效的语言请求回退到默认语言。

```bash
curl -i http://localhost:8080/notes -H 'Accept-Language: zh-CN'
```

对于应用已注册且要求 Bearer 的 `/notes` 路由，这个请求因缺少凭据返回 401；启用内置语言目录后，detail 为中文提示，错误 code 仍是 `auth.unauthenticated`。

<a id="messages"></a>

## 添加应用消息

创建 `locales/en.yaml` 和 `locales/zh-CN.yaml`，文件名表示语言。例如：

```yaml
note.closed: This note has been closed
```

中文文件：

```yaml
note.closed: 笔记已关闭
```

通过 embed.FS 或其他 fs.FS 作为一层目录传入。下面的片段假定 `localeFiles` 已由 `//go:embed locales/*.yaml` 声明：

```go
bundle, err := i18n.New(i18n.Options{
    Default: "en",
    Layers: []i18n.Layer{
        i18n.FrameworkLayer(),
        {Priority: i18n.PriorityProduct, FS: localeFiles, Name: "notes"},
    },
    RequiredIDs: []string{"note.closed"},
})
```

覆盖顺序是框架 → 模块 → 产品，对应 PriorityFramework、PriorityModule、PriorityProduct。同一优先级、同一语言重复消息 ID 会失败。RequiredIDs 检查默认语言中指定的消息是否存在。

Bundle 构建后按不可变目录使用，不在处理请求时添加或覆盖消息。

<a id="localizer"></a>

## 在代码中翻译

使用 `github.com/nicksnyder/go-i18n/v2/i18n`，通常别名为 `goi18n`，与框架辅助包区分：

```go
localizer := goi18n.NewLocalizer(bundle, i18n.Locale(ctx))
message, err := localizer.Localize(&goi18n.LocalizeConfig{
    MessageID: "note.closed",
})
```

模板数据、复数和其他翻译能力沿用 go-i18n 的原生 LocalizeConfig。请求 context 只保存语言标记，不保存目录或其他依赖。

<a id="fallback"></a>

## 错误文案的回退

REST 呈现层将错误 code 作为消息 ID。找不到消息时使用通用状态文案，并在有 Logger 时记录缺失诊断。只检查应用明确声明的 RequiredIDs，不能在启动时发现所有运行时拼接的 ID。

翻译不自动消费 `fault.Public` 中的参数。需要插值时，在应用展示逻辑中显式选择允许公开的数据。

---

[上一篇：缓存](cache.md) · [下一篇：应用命令](commands.md)
