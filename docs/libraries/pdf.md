# 生成 PDF

用前端的 React 打印模板渲染 HTML，交给 [Gotenberg](https://gotenberg.dev) 中的 Chromium 生成 PDF。发票、合同、报表与页面共用组件和样式。

包：`pdf`、`pdf/gotenberg`、`pdf/pdftest`。

<a id="example-usage"></a>
## 示例：把发票生成为 PDF

```go
html, err := pages.Render(ctx, "web", "invoice", invoice) // 前端 web 中的打印模板 invoice
if err != nil {
    return err
}
data, err := renderer.Render(ctx, pdf.Document{HTML: html}) // PDF 的字节
```

`pages` 是入口创建的 `*frontend.Host`，见[渲染打印模板](../frontend/hosting.md#rendering)；`renderer` 是 `gotenberg.New` 创建的生成器，见[启动 Gotenberg](#running-gotenberg)。打印模板在服务端渲染，这个前端的 `package.json` 中要有 `"ssr": true`。

<a id="running-gotenberg"></a>
## 启动 Gotenberg，创建生成器

本地用 Docker 运行，`9-chromium` 只带 Chromium，完整镜像 `9` 另有 LibreOffice。镜像带有 Noto CJK 字体，中文直接可用：

```shell
docker run --rm -p 3000:3000 gotenberg/gotenberg:9-chromium
```

根配置中加上字段 ``Gotenberg gotenberg.Config `yaml:"gotenberg"` ``，[`config.Load`](../general/configuration.md#loading-configuration) 在启动时校验它：

```yaml
gotenberg:
  url: ${GOTENBERG_URL:-http://localhost:3000} # http 或 https 地址
  username: ""  # Gotenberg 开启 --api-enable-basic-auth 时填写
  password: ""
```

在[入口](../concepts/dependencies.md#assembling-the-application)的 `Open` 中用 `gotenberg.New(a.cfg.Gotenberg)` 创建一次生成器，保存在 `App` 中复用。`gotenberg.New` 不访问网络；生成器可以并发使用。

<a id="writing-templates"></a>
## 编写打印模板：`@go-tenon/start/print`

打印模板写在前端，只用 Go 传来的数据渲染，不调用接口，所以队列任务中没有用户的 cookie 也能生成：

```tsx title="apps/web/src/print.tsx"
import { definePrint, PrintDocument } from "@go-tenon/start/print"
import css from "./print/article.css?inline"

type Article = { title: string; content: string }

export const templates = {
  article: definePrint<Article>(({ data }) => (
    <PrintDocument title={data.title} styles={[css]}>
      <h1>{data.title}</h1>
      <div className="content">{data.content}</div>
    </PrintDocument>
  )),
}
```

- `PrintDocument` 是整个页面：`title` 成为 PDF 的标题；默认 A4、Noto Sans CJK 字体、打印背景色，表格跨页时重复表头。
- Chromium 读不到外部文件，样式表用 `?inline` 导入成字符串交给 `styles`。模板中用到的前端组件，样式同样要内联。

服务端入口加上 `printHandler`，否则 `Render` 返回 `frontend.ErrUnknownTemplate`：

```ts title="apps/web/src/server.ts"
import { createServerEntry, createStartHandler } from "@tanstack/react-start/server"
import { printHandler } from "@go-tenon/start/print"
import { streamHandler } from "@go-tenon/start/server"
import { templates } from "./print"

const print = printHandler(templates)
const start = createStartHandler(streamHandler)

export default createServerEntry({ fetch: (request) => print(request) ?? start(request) })
```

Go 用 `frontend.Host.Render` 调用模板：数据编码为 JSON，返回完整的 HTML。打印模板的地址只供 Go 调用，对外的请求返回 `404`。

<a id="page-setup"></a>
### 纸张、边距、页眉页脚与页码：CSS `@page`

纸张、方向、边距和页码只在 CSS 中设置，Go 中没有对应的选项。`print/article.css`：

```css
@page {
  size: A4;            /* 也可以写 A5 landscape、210mm 297mm */
  margin: 25mm 20mm;
  @bottom-center {     /* 页边距中的区域，另有 @top-right 等 */
    content: "第 " counter(page) " 页，共 " counter(pages) " 页";
  }
}
.running { position: fixed; top: -15mm; } /* 固定定位的元素在每一页重复，作为页眉 */
h1, h2 { break-after: avoid; }
```

<a id="assets"></a>
### 图片和字体：`Document.Assets`

- 每份文档不同的图片（例如二维码）写成 data URL。
- [文件存储](filesystem.md)中有公开地址的图片直接写在 `src` 中。
- 固定的文件（徽标、字体）放进 `pdf.Document` 的 `Assets`，与页面一起上传。HTML 中只写文件名，例如 `src="logo.svg"`；`Assets` 只用顶层文件，`index.html` 留给页面本身。其他字体用 `@font-face` 引用 `Assets` 中的文件。

```go
//go:embed assets
var files embed.FS

assets, err := fs.Sub(files, "assets")
if err != nil {
    return err
}
data, err := renderer.Render(ctx, pdf.Document{HTML: html, Assets: assets})
```

引用的文件加载失败（文件名写错、不在 `Assets` 中）时，`Render` 返回错误。

<a id="downloading"></a>
## 下载 PDF 的接口

`internal/article/articlepdf/download.go` 中的业务动作由 `NewDownload(db, print, renderer)` 创建：`print` 是 `func(ctx, template string, data any) ([]byte, error)`，通常是绑定前端 `web` 的 `pages.Render`；`renderer` 是 `pdf.Renderer`。HTTP 方法用响应头返回文件：

```go
func (a *Download) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) ([]byte, string, error) {
    item, err := a.get.Handle(ctx, subject, id) // article.Get：只允许作者看到草稿
    if err != nil {
        return nil, "", err
    }
    html, err := a.print(ctx, "article", item)
    if err != nil {
        return nil, "", err
    }
    data, err := a.pdf.Render(ctx, pdf.Document{HTML: html})
    if err != nil {
        return nil, "", errors.Wrapf(err, "render article %s", item.ID)
    }
    return data, item.Slug + ".pdf", nil
}

type DownloadOutput struct {
    ContentType        string `header:"Content-Type"`        // application/pdf
    ContentDisposition string `header:"Content-Disposition"` // mime.FormatMediaType("attachment", map[string]string{"filename": filename})
    Body               []byte
}
```

让浏览器直接打开时用 `inline`。

| 失败的情况 | 错误 | 响应 |
| --- | --- | --- |
| 连不上 Gotenberg、排队已满或超时 | `pdf.ErrUnavailable` | 503，“PDF 生成服务暂时不可用，请稍后再试” |
| 没有这个打印模板 | `frontend.ErrUnknownTemplate` | 500，记录日志 |
| 引用的文件加载失败、模板渲染出错 | 带原因的普通错误 | 500，记录日志 |

<a id="mail-attachments"></a>
## 作为邮件附件

把 `Render` 返回的 `data` 放进 `msg.Attachments`：`mail.Attachment{Filename: "invoice.pdf", ContentType: "application/pdf", Content: data}`。在事务提交之后或在队列任务中发送，见[在事务提交之后发送邮件](mail.md#sending-mail)。

<a id="background-generation"></a>
## 在队列任务中生成

几页的 PDF 通常一秒内生成，在请求中同步生成即可。页数很多或成批生成时交给[队列](../background/queue.md)，任务中调用 `Render`；Gotenberg 不可用时任务返回错误，按队列的规则重试。

worker 进程要托管 SSR 进程才能渲染打印模板：用 `frontend.Host.Run` 包住 worker 的运行函数，`frontend.Options.API` 留空。`apps.Dist` 是嵌入前端构建结果的 `fs.FS`：

```go
pages, err := frontend.New(apps.Dist, frontend.Options{})
if err != nil {
    return err
}
return pages.Run(ctx, worker.Run)
```

生成后用[文件存储](filesystem.md#server-upload)的 `Upload` 保存，再用[通知](../features/notifications.md)告诉用户下载地址。

<a id="testing"></a>
## 测试：`pdftest`

`pdftest.New()` 记录每份文档的 HTML，返回一页空白的 PDF。测试[下载 PDF 的接口](#downloading)中的业务动作时，用返回固定 HTML 的函数代替 `pages.Render`。`db` 是 [`dbtest.Open`](../testing/testing.md#creating-test-databases) 创建的临时库，`author`、`item` 是测试中创建的作者和文章：

```go
renderer := pdftest.New()
print := func(_ context.Context, template string, data any) ([]byte, error) {
    return []byte("<h1>" + data.(*article.Article).Title + "</h1>"), nil
}
download := articlepdf.NewDownload(db, print, renderer)

_, _, err := download.Handle(ctx, author, item.ID)
require.NoError(t, err)
html, _ := renderer.Last()
assert.Contains(t, html, "<h1>Hello World</h1>")
```

`FailWith(pdf.ErrUnavailable.New())` 让之后的 `Render` 都失败。排版在前端用一个开发时的页面渲染同一个模板，用浏览器的打印预览检查。

<a id="deployment"></a>
## 部署 Gotenberg

Gotenberg 和应用部署在同一台机器上，仅监听本机回环地址供应用访问（开发测试与单机部署都可直接用容器运行 Gotenberg）：

```bash
docker run -d --name gotenberg --restart unless-stopped \
  -p 127.0.0.1:3000:3000 gotenberg/gotenberg:9-chromium \
  gotenberg --chromium-deny-private-ips # 禁止访问内网地址
```

应用中将 `GOTENBERG_URL` 指向 `http://127.0.0.1:3000`。

- `gotenberg.Doctor(renderer)` 检查 Gotenberg 在运行、能生成 PDF、用户名和密码正确，见[自检](../installation/doctor.md#framework-checks)。
- 默认最多同时生成 6 份，其余排队；单次请求的时限是 30 秒。用 `--chromium-max-concurrency`、`--api-timeout` 调整，见 [Gotenberg 文档](https://gotenberg.dev)。
- 模板中有用户填写的图片地址时开启 `--chromium-deny-private-ips`：内网地址的图片留空，PDF 照常生成；文件存储的公开地址也要是公网地址。
