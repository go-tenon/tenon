# 生成 PDF

`pdf` 包把一个 HTML 页面生成为 PDF。你用写页面的方式编写 PDF：前端的一个 React 打印模板，由 Go 托管的 Bun 渲染成 HTML，交给 Chromium 打印。发票、合同、报表都可以这样生成，模板与页面共用组件和样式。

`pdf` 包定义了文档和生成器：

```go
type Document struct {
    HTML   []byte // 整个页面
    Assets fs.FS  // 页面引用的图片和字体，可以为 nil
}

type Renderer interface {
    Render(ctx context.Context, doc Document) ([]byte, error)
}
```

`pdf/gotenberg` 实现 `Renderer`，调用 [Gotenberg](https://gotenberg.dev) 服务中的 Chromium。浏览器运行在独立的容器中，崩溃、内存占用、排队和定期重启都由 Gotenberg 负责；它的镜像带有 Noto CJK 字体，中文直接可用。

阅读前需要了解：[服务端渲染](../frontend/ssr.md)；[TanStack Start 的接入](../frontend/start.md)；CSS 的基本写法。

- [示例用法](#example-usage)
- [启动 Gotenberg](#running-gotenberg)
- [编写模板](#writing-templates)
    - [纸张、边距与页码](#page-setup)
    - [图片和字体](#assets)
- [下载 PDF](#downloading)
- [作为邮件附件](#mail-attachments)
- [后台生成](#background-generation)
- [测试](#testing)
- [部署](#deployment)
- [常见问题](#troubleshooting)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

下面把一张发票生成为 PDF：

```go
renderer, err := gotenberg.New(cfg.PDF) // 连接 Gotenberg 服务
if err != nil {
    return err
}

html, err := pages.Render(ctx, "invoice", invoice) // 前端的打印模板 invoice，pages 是 *ssr.Host
if err != nil {
    return err
}
data, err := renderer.Render(ctx, pdf.Document{HTML: html})
```

`invoice` 是前端的一个打印模板，纸张大小、边距和页眉页脚都用 CSS 设置。`Render` 返回 PDF 的字节，你可以把它作为下载返回，或者保存到[文件存储](filesystem.md)。下面先启动 Gotenberg，再介绍怎样编写页面。

<a id="running-gotenberg"></a>
## 启动 Gotenberg

本地开发时用 Docker 运行：

```shell
docker run --rm -p 3000:3000 gotenberg/gotenberg:9-chromium
```

`9-chromium` 是只带 Chromium 的版本，体积更小。完整镜像 `9` 还包含用来转换 Office 文档的 LibreOffice。

把 `gotenberg.Config` 放进应用的根配置结构体：

```go
type Config struct {
    // ...
    Gotenberg gotenberg.Config `yaml:"gotenberg"`
}
```

```yaml
gotenberg:
  url: ${GOTENBERG_URL:-http://localhost:3000}
```

挂进去之后，[`config.Load`](../general/configuration.md#loading-configuration) 会在启动时校验配置。真正连 Gotenberg、渲染 PDF 的是下面在入口创建的生成器。

Gotenberg 用 `--api-enable-basic-auth` 开启了基本认证时，再填写 `username` 和 `password`。

在[入口](../concepts/dependencies.md#assembling-the-application)的 `open` 中创建一次生成器，保存在 `App` 中，之后渲染都复用它。`gotenberg.New` 不访问网络：

```go
renderer, err := gotenberg.New(cfg.Gotenberg)
if err != nil {
    return nil, err
}
```

<a id="writing-templates"></a>
## 编写模板

打印模板写在前端，用 `@go-tenon/start/print` 声明。模板只用 Go 传来的数据渲染，不调用接口：队列任务中没有用户的 cookie 也能生成。

```tsx title="apps/web/src/print.tsx"
import { definePrint, PrintDocument } from "@go-tenon/start/print"
import css from "./print/article.css?inline"

type Article = { title: string; publishedAt?: string; content: string }

export const templates = {
  article: definePrint<Article>(({ data }) => (
    <PrintDocument title={data.title} styles={[css]}>
      <header className="running">示例</header>
      <h1>{data.title}</h1>
      {data.publishedAt && <p className="meta">发布于 {data.publishedAt.slice(0, 10)}</p>}
      <div className="content">{data.content}</div>
    </PrintDocument>
  )),
}
```

- `PrintDocument` 是整个页面：`title` 成为 PDF 的标题；默认 A4、Noto Sans CJK 字体、打印背景色，表格跨页时重复表头。
- Chromium 读不到外部文件，样式表用 `?inline` 导入成字符串，交给 `styles`。
- 模板中可以使用前端的组件，例如表格和徽标；它们的样式同样要内联。

在服务端入口中加上打印请求的处理：

```ts title="apps/web/src/server.ts"
import { createServerEntry, createStartHandler } from "@tanstack/react-start/server"
import { printHandler } from "@go-tenon/start/print"
import { streamHandler } from "@go-tenon/start/server"
import { templates } from "./print"

const print = printHandler(templates)
const start = createStartHandler(streamHandler)

export default createServerEntry({ fetch: (request) => print(request) ?? start(request) })
```

Go 用 [`ssr.Host.Render`](../frontend/ssr.md#rendering) 调用它：数据编码为 JSON 交给模板，返回完整的 HTML。打印模板的地址只供 Go 调用，对外的请求返回 `404`。

<a id="page-setup"></a>
### 纸张、边距与页码

纸张、方向、边距和页码都在 CSS 的 `@page` 中设置，Go 代码中没有对应的选项。下面是 `print/article.css`：

```css
@page {
  size: A4;
  margin: 25mm 20mm;

  @bottom-center {
    content: "第 " counter(page) " 页，共 " counter(pages) " 页";
    font-size: 9pt;
    color: #666;
  }
}

body { font-family: "Noto Sans CJK SC", sans-serif; font-size: 11pt; line-height: 1.7; }
.content { white-space: pre-wrap; }

/* 固定定位的元素在每一页重复，放在上边距中作为页眉 */
.running { position: fixed; top: -15mm; left: 0; font-size: 9pt; color: #666; }

h1, h2 { break-after: avoid; }
img, pre, tr { break-inside: avoid; }
thead { display: table-header-group; } /* 表格跨页时重复表头 */
```

- `size` 可以写 `A4`、`A5 landscape`、`210mm 297mm` 等。没有写 `size` 时使用 A4。
- `@bottom-center` 是页边距中的区域，类似的还有 `@top-right`、`@bottom-left` 等，`counter(page)` 是当前页码，`counter(pages)` 是总页数。
- 背景色和背景图默认会打印出来。
- Gotenberg 镜像中的中文字体是 Noto Sans CJK（黑体）。需要其他字体时，把字体文件放进 `Assets`，用 `@font-face` 引用。

<a id="assets"></a>
### 图片和字体

样式内联在页面中。图片和字体有三种来源：

- 每份文档不同的图片，例如二维码，写成 data URL：`<img src={`data:image/png;base64,${data.qrcode}`} />`。
- [文件存储](filesystem.md)中的图片有公开地址，直接写在 `src` 中，Gotenberg 会去下载。
- 固定的文件（徽标、字体）放进 `Document.Assets`，和页面一起上传，**HTML 中只写文件名**，例如 `src="logo.svg"`。`Assets` 只包含顶层文件：

```go
//go:embed assets
var files embed.FS

var assets, _ = fs.Sub(files, "assets")
```

引用的文件加载失败（文件名写错、不在 `Assets` 中）时，`Render` 返回错误。

<a id="downloading"></a>
## 下载 PDF

`internal/article/articlepdf/download.go` 中的业务动作先用 `article.Get` 读取文章，同样只允许作者本人看到草稿，再用打印模板生成 PDF：

```go
// Print 用前端的打印模板渲染数据，通常是 ssr.Host 的 Render。
type Print func(ctx context.Context, template string, data any) ([]byte, error)

type Download struct {
    get   *article.Get
    print Print
    pdf   pdf.Renderer
}

func NewDownload(db *bun.DB, print Print, renderer pdf.Renderer) *Download {
    return &Download{get: article.NewGet(db), print: print, pdf: renderer}
}

// Handle 返回文章的 PDF 和文件名。
func (a *Download) Handle(ctx context.Context, subject identity.Subject, id uuid.UUID) ([]byte, string, error) {
    item, err := a.get.Handle(ctx, subject, id)
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

type DownloadInput struct {
    ID uuid.UUID `path:"id"`
}

type DownloadOutput struct {
    ContentType        string `header:"Content-Type"`
    ContentDisposition string `header:"Content-Disposition"`
    Body               []byte
}

// HTTP 处理 GET /articles/{id}/pdf。
func (a *Download) HTTP(ctx context.Context, in *DownloadInput) (*DownloadOutput, error) {
    subject, _ := identity.FromContext(ctx)
    data, filename, err := a.Handle(ctx, subject, in.ID)
    if err != nil {
        return nil, err
    }
    return &DownloadOutput{
        ContentType:        "application/pdf",
        ContentDisposition: mime.FormatMediaType("attachment", map[string]string{"filename": filename}),
        Body:               data,
    }, nil
}
```

想让浏览器直接打开，把 `attachment` 换成 `inline`。路由的注册方式见[组织路由](../http/routing.md#organizing-routes)。

生成失败时：

| 情况 | 错误 | 响应 |
| --- | --- | --- |
| 连不上 Gotenberg、排队已满或超时 | `pdf.ErrUnavailable` | 503，“PDF 生成服务暂时不可用” |
| 没有这个打印模板 | `ssr.ErrUnknownTemplate` | 500，记录日志 |
| 引用的文件加载失败、模板渲染出错等 | 带有原因的普通错误 | 500，记录日志 |

<a id="mail-attachments"></a>
## 作为邮件附件

把生成的内容放进 `mail.Attachment`：

```go
msg.Attachments = append(msg.Attachments, mail.Attachment{
    Filename:    filename,
    ContentType: "application/pdf",
    Content:     data,
})
```

和其他邮件一样，在事务提交之后或在队列任务中发送，见[邮件](mail.md#sending-mail)。

<a id="background-generation"></a>
## 后台生成

一份几页的 PDF 通常在一秒内生成，在请求中同步生成即可。页数很多，或者要一次生成一批时，交给[队列](../background/queue.md)：任务中调用 `Render`。

队列 worker 是另一个进程，同样要托管 SSR 进程才能渲染打印模板：用 `ssr.Host.Run` 包住 worker 的运行函数。worker 不提供接口，`ssr.Options.API` 留空：

```go
pages, err := ssr.New(ssr.Options{Executable: web.SSR})
if err != nil {
    return err
}
return pages.Run(ctx, worker.Run)
```

任务中生成之后，用[文件存储](filesystem.md#server-upload)的 `Upload` 保存结果，再用[通知](../features/notifications.md)告诉用户下载地址。需要长期保留的文件，上传后用 `Confirm` 确认。

Gotenberg 暂时不可用时，任务返回错误，按队列的规则稍后重试。

<a id="testing"></a>
## 测试

`pdftest.Renderer` 记录 HTML，返回一页空白的 PDF。打印模板由前端渲染，Go 的测试中传一个返回固定 HTML 的函数代替 `pages.Render`：

```go
func TestDownload(t *testing.T) {
    renderer := pdftest.New()
    print := func(_ context.Context, template string, data any) ([]byte, error) {
        return []byte("<h1>" + data.(*article.Article).Title + "</h1>"), nil
    }
    download := articlepdf.NewDownload(db, print, renderer)

    _, filename, err := download.Handle(ctx, author, item.ID)
    require.NoError(t, err)
    assert.Equal(t, "hello-world.pdf", filename)

    html, _ := renderer.Last()
    assert.Contains(t, html, "<h1>Hello World</h1>")
}
```

`FailWith(pdf.ErrUnavailable.New())` 让之后的 `Render` 都失败，用来测试出错时的处理。

模板的排版：在前端写一个只在开发时使用的页面渲染同一个模板，用浏览器的打印预览检查分页。

<a id="deployment"></a>
## 部署

Gotenberg 和应用一起部署。`gotenberg.Doctor(renderer)` 检查它在运行、能生成 PDF，用户名和密码正确，见[自检](../installation/doctor.md#framework-checks)。在[部署](../installation/deployment.md#docker)的 compose 文件中加一个服务，应用通过 `GOTENBERG_URL` 访问它：

```yaml
x-app: &app
  environment:
    GOTENBERG_URL: http://gotenberg:3000
    # ...

services:
  gotenberg:
    image: gotenberg/gotenberg:9-chromium
    restart: unless-stopped
  # ...
```

使用 systemd 部署时，同样用 Docker 在同一台机器上运行 Gotenberg。几点注意：

- **Gotenberg 只在内网提供服务**，只让应用访问它。
- 默认最多同时生成 6 份，其余排队；单次请求的时限是 30 秒。分别用 `--chromium-max-concurrency`、`--api-timeout` 调整，见 [Gotenberg 文档](https://gotenberg.dev)。
- 模板中有用户填写的图片地址时，Chromium 会去访问这些地址。在服务中加上 `command: [gotenberg, --chromium-deny-private-ips]` 可以禁止它访问内网地址，被拒绝的图片留空，PDF 照常生成。开启后，文件存储的公开地址也要是公网地址。

<a id="troubleshooting"></a>
## 常见问题

**`Render` 报错连不上 Gotenberg**

Gotenberg 没有运行，或者配置中的地址不对。本地用 Docker 启动它，见[启动 Gotenberg](#running-gotenberg)。

**PDF 中的样式没有生效**

样式表要用 `?inline` 导入成字符串交给 `PrintDocument` 的 `styles`，外部文件 Chromium 读不到，见[编写模板](#writing-templates)。

**PDF 中的图片没有显示**

固定的图片放进 `Assets`，HTML 中只写文件名；`Assets` 只包含顶层文件，见[图片和字体](#assets)。

**`Render` 报错 `unknown print template`**

服务端入口没有加上 `printHandler`，或者模板没有放进它的 `templates`，见[编写模板](#writing-templates)。

**纸张大小或页边距不对**

纸张和边距只能在 CSS 的 `@page` 中设置，Go 代码中没有对应的选项，见[纸张、边距与页码](#page-setup)。

**中文显示为方块**

Gotenberg 镜像中带有 Noto Sans CJK。使用了其他字体时，把字体文件放进 `Assets` 并用 `@font-face` 引用。

<a id="api-reference"></a>
## API 参考

### pdf.Renderer

```go
type Renderer interface {
    Render(ctx context.Context, doc Document) ([]byte, error)
}
```

- **参数**（`Render`）
    - `doc`：要生成的文档，见下面的 `Document`
- **返回**：PDF 的字节；生成服务不可用时返回 `ErrUnavailable`（分类 `Unavailable`，响应 `503`）

包 `github.com/go-tenon/tenon/pdf`。

### pdf.Document

| 字段 | 说明 |
| --- | --- |
| `HTML []byte` | 完整的 HTML 页面，`<title>` 成为 PDF 的标题；通常由 `ssr.Host.Render` 渲染 |
| `Assets fs.FS` | 页面引用的图片和字体，只能有顶层文件，见[图片和字体](#assets) |

### gotenberg.New

```go
func New(cfg Config) (*Renderer, error)
```

包 `github.com/go-tenon/tenon/pdf/gotenberg`。字段见[启动 Gotenberg](#running-gotenberg)。

### pdftest.New

```go
func New() *Renderer
```

包 `github.com/go-tenon/tenon/pdf/pdftest`。不生成 PDF，只记录 HTML，见[测试](#testing)。
