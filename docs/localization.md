# 本地化

[文档首页](README.md) / 进阶使用

本地化提供展示文案，Code 和 Kind 保持稳定的业务语义。目录组合后得到原生 go-i18n Bundle，应用可继续使用 go-i18n Localizer。

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

每个 Layer 需要提供 FS。Bundle 是原生可变对象，应用在启动阶段完成目录修改，请求阶段按只读目录使用。

<a id="localizer"></a>

## 在代码中翻译

使用 `github.com/nicksnyder/go-i18n/v2/i18n`，通常别名为 `goi18n`，与框架辅助包区分：

```go
localizer := goi18n.NewLocalizer(bundle, i18n.Locale(ctx))
message, err := localizer.Localize(&goi18n.LocalizeConfig{
    MessageID: "note.closed",
})
```

模板数据、复数和其他翻译能力沿用 go-i18n 的原生 LocalizeConfig。请求 context 保存语言标记，Bundle 由应用显式传入。

<a id="fallback"></a>

## 错误文案的回退

REST 呈现层将错误 code 作为消息 ID。找不到消息时使用通用状态文案，并在有 Logger 时记录缺失诊断。启动校验的范围由 RequiredIDs 明确指定，动态消息在翻译时按同样的回退策略处理。

模板插值使用 LocalizeConfig.TemplateData。应用可通过 fault.PublicOf 读取元数据，再选择允许展示的字段。

---

[上一篇：缓存](cache.md) · [下一篇：应用命令](commands.md)
