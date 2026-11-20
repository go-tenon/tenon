# 文档转换与切分

`document/convert` 把 PDF、Word、PowerPoint、Excel、CSV、HTML、纯文本和 JSON 转换为 Markdown，并记录每一页、每张幻灯片、每个工作表在 Markdown 中的字节区间；`document/split` 把 Markdown 切成长度有上限的分段，交给[向量检索](vector.md)建立索引。两步都在本地完成。

包：`document/convert`、`document/convert/all`、`document/convert/csv`、`document/convert/docx`、`document/convert/html`、`document/convert/pdf`、`document/convert/pptx`、`document/convert/text`、`document/convert/xlsx`、`document/split`。

<a id="example-usage"></a>
## 示例

```go
converters := all.New(all.Options{})
defer converters.Close()

format, _ := convert.FormatOf("manual.pdf") // 按扩展名，不区分大小写
doc, err := converters.Convert(ctx, format, convert.Input{Reader: file, Name: "manual.pdf"})
if err != nil {
    return err
}

splitter, err := split.New(split.Options{Size: 500, Overlap: 50})
if err != nil {
    return err
}
for _, c := range splitter.Split(doc.Markdown) {
    // c.Text 是 doc.Markdown[c.Start:c.End]，c.Headings 是它所在的各级标题
}
```

`file` 是打开的原件（`io.Reader`）。没有对应转换器的格式，`Convert` 返回 `convert.ErrUnsupported`。

<a id="converting"></a>
## 用 `convert/all` 转换文档

`all.New` 返回注册了全部转换器的 `Registry`。PDF 转换器持有 PDFium 实例，所以在[入口](../concepts/dependencies.md#assembling-the-application)中创建一次，登记关闭，之后并发共用：

```go
converters := all.New(all.Options{
    Fallback: simplifiedchinese.GB18030, // 没有 BOM、没有声明编码、又不是 UTF-8 的文本按 GBK 解码
    PDF:      pdf.Options{Workers: 4},   // 同时转换的 PDF 数，默认 2
})
stack.MustAdd("documents", func(context.Context) error { return converters.Close() })
```

`stack` 是入口的关闭栈。转换结果的 `Markdown` 是正文，`Sections` 是各页（幻灯片、工作表）在其中的区间。

<a id="choosing-converters"></a>
### 只注册需要的转换器

`all` 引入全部转换器的依赖，包括 PDFium 的 WebAssembly。只需要部分格式时自己创建 `convert.Registry`，程序更小：

```go
var converters convert.Registry // 零值可用
converters.Register(text.New(text.Options{}), convert.Text, convert.Markdown)
converters.Register(docx.New(docx.Options{}), convert.DOCX)
```

自己注册的转换器由调用者关闭，例如 `pdf.Converter`。其他格式实现 `convert.Converter` 后注册进来，输出 Markdown 后照样用 `split` 切分。

<a id="limits"></a>
### 上限

超出上限时转换立即停止，返回 `convert.ErrTooLarge`，防止超大文档和压缩炸弹：

| 上限 | 限制的对象 | 默认值 |
| --- | --- | --- |
| `Limits.MaxBytes` | 读取的原件字节数 | 32 MiB |
| `Limits.MaxExpandedBytes` | Word、PowerPoint、Excel 解压后的字节数，HTML 展开合并单元格的复制量 | 128 MiB |
| `Limits.MaxOutputBytes` | 生成的 Markdown 字节数 | 64 MiB |
| `pdf.Options.MaxPages` | PDF 的页数 | 10000 |
| `pdf.Options.MaxPageChars` | PDF 一页的字符数 | 1 Mi |

零值取默认值，负数表示不限制。`all.Options.Limits` 设置所有转换器的上限，转换器自己 `Options` 中的 `Limits` 不为零的字段优先，例如 `all.Options{PDF: pdf.Options{Limits: convert.Limits{MaxBytes: 100 << 20}}}`。

<a id="errors"></a>
### 错误

文档本身有问题时，返回的错误包装了下面四种之一。它们都属于 `errorx.InvalidArgument`：业务动作直接返回时得到 `422` 和翻译好的提示；队列任务返回时不再重试，直接转入[失败任务表](../background/queue.md#failed-jobs)。

| 错误 | 含义 |
| --- | --- |
| `convert.ErrUnsupported` | 没有这种格式的转换器，包括旧的 `.doc`、`.xls`、`.ppt`：让用户另存为 `.docx`、`.xlsx`、`.pptx` |
| `convert.ErrEncrypted` | 文档设置了打开密码；只设置了权限密码的 PDF 照常转换 |
| `convert.ErrCorrupt` | 文档已损坏或不是声明的格式；二进制格式的空文件也是这种错误 |
| `convert.ErrTooLarge` | 超出了某个[上限](#limits)，用 `errors.As` 取出 `*convert.LimitError` 看是哪一个 |

读取失败、PDFium 启动失败等其他错误照常重试。

<a id="splitting"></a>
## 用 `split` 切分 Markdown

`split.New(split.Options{Size: 500, Overlap: 50})` 创建 `Splitter`，选项不合法时返回错误；`splitter.Split(doc.Markdown)` 返回分段。`Splitter` 不可变，创建一次后在多个 goroutine 中共用。

| 选项 | 含义 |
| --- | --- |
| `Size` | 目标长度，必填 |
| `MaxSize` | 长度的硬上限，默认 `Size + Size/5`：略长于 `Size` 的小节保持完整 |
| `Overlap` | 与上一段重叠的上限，必须小于 `Size`；在标题处或找不到合适的边界时不重叠 |

- 长度按码点计算，一个汉字和一个英文字母都算 1，与模型的 token 数不同。中文资料的 `Size` 通常取 300 到 800。
- 中英文都在句末、分句标点处断开，标题处总会断开，表格和代码只在行之间断开，没有空格的中文段落也能切开。
- 分段正文与原文逐字节相同，`Start`、`End` 是它在 Markdown 中的字节区间，引用时可以直接高亮原文。

<a id="chunk-context"></a>
### 用 `TextWithContext` 计算向量

每个分段带着它所在的标题路径 `Headings`，从表格中间开始的分段还带着表头 `TableHeader`。`chunk.TextWithContext()` 把它们放在正文前面，用来计算向量，模型才知道这一段出自哪一章：

```text
采购合同 > 第三章 付款

付款应在验收后 30 日内完成……
```

`chunk` 是 `Split` 返回的一个分段。存储和展示仍用原样的 `chunk.Text`。上下文的长度不计入 `Size`，模型对输入长度有要求时用 `Headings` 自己拼接或截断。

<a id="locating-pages"></a>
### 用 `SectionsIn` 定位原文的页

`doc.SectionsIn(chunk.Start, chunk.End)` 返回分段涉及的页（幻灯片、工作表），回答问题时可以注明“见第 3–4 页”：

```go
var pages []int
for _, s := range doc.SectionsIn(chunk.Start, chunk.End) {
    pages = append(pages, s.Number)
}
```

`doc` 是转换结果，`chunk` 是它切分出的一个分段。`Number` 从 1 开始，与原件的页码一致，空白页和跳过的隐藏幻灯片也计数。Word、HTML 和文本类格式没有页，`Sections` 为空。

<a id="processing-uploads"></a>
## 在队列任务中处理上传的文档

附件通过[文件存储](../libraries/filesystem.md)上传，确认时在同一个事务中发布任务，见[确认后异步处理](../libraries/filesystem.md#processing-files)。任务读取文件，转换、切分，在一个事务中替换这个附件原有的分段，并发起向量索引的同步：

```go
func (a *Index) Job(ctx context.Context, job IndexJob) error {
    r, file, err := a.files.Open(ctx, job.FileID)
    if err != nil {
        return err
    }
    defer r.Close()
    format, _ := convert.FormatOf(file.Filename)
    doc, err := a.converters.Convert(ctx, format, convert.Input{Reader: r, Name: file.Filename})
    if err != nil {
        return err // 文档本身的问题不重试，读取失败等稍后重试
    }

    // 每个分段一行：Text、TextWithContext()、Start、End 和 SectionsIn 得到的页码
    rows := toRows(job, doc, a.splitter.Split(doc.Markdown))
    return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        if _, err := tx.NewDelete().Model((*AttachmentChunk)(nil)).Where("file_id = ?", job.FileID).Exec(ctx); err != nil {
            return err
        }
        if len(rows) > 0 { // 扫描件等没有文字的文档没有分段
            if _, err := tx.NewInsert().Model(&rows).Exec(ctx); err != nil {
                return err
            }
        }
        return Chunks.Sync(ctx, tx, job.FileID.String())
    })
}
```

- `IndexJob` 带 `ArticleID` 和 `FileID`；`a.db` 是 `*bun.DB`，`a.files` 是 `*filesystem.Service`，`a.converters`、`a.splitter` 由入口创建后传入；`toRows` 是应用自己写的转换，页码见[定位原文的页](#locating-pages)。
- 任务可能重复执行，先删后插让结果一致。
- `Chunks` 的声明和检索见[向量检索](vector.md#defining-indexes)；多租户应用中分段表和向量文档都带上租户，见[多租户](vector.md#tenancy)。

<a id="formats"></a>
## 各种格式的转换结果

- PowerPoint 每张幻灯片一个 Section，含标题、文本框、表格、图表数据和演讲者备注；Excel 每个工作表一个 Section，是一个二级标题加一个表格，单元格是格式化后的值。隐藏的幻灯片和工作表默认跳过。
- PDF 每页一个 Section，按字号识别标题和段落。只提取文字层：没有文字层的扫描件得到空的 Markdown，需要 OCR 时先用其他服务识别。
- Word 的标题来自标题样式和大纲级别；HTML 的表格转为 GFM 表格，相对链接按 `Input.BaseURL` 解析；CSV 转为一个表格，第一行是表头；JSON 放进 `json` 代码围栏；纯文本和 Markdown 原样输出。这些格式没有 Section。
