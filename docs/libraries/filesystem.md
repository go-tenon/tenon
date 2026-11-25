# 文件存储与上传

`filesystem` 保存用户上传的文件（封面、头像、附件、导出的表格）：`files` 表记录每个文件，正文保存在磁盘上，磁盘是本地目录或 S3 兼容的对象存储。文件通过应用服务器上传，或由客户端直传到 S3；每个文件有公开地址，图片缩略图由 imgproxy 按地址生成。

包：`filesystem`、`filesystem/local`、`filesystem/s3`、`imgproxy`。

- 上传得到的文件是 `temporary`，业务使用它时调用 `Confirm` 变为 `ready`；超过 `temp_ttl` 没有确认的文件由 `Cleanup` 删除。用户放弃编辑时，存储中不留下没人引用的文件。
- 文件都是公开的：知道地址就能下载。`Get`、`Open` 只按 ID 查询，权限由业务在调用前检查。
- 客户端上传的线路格式见[客户端协议：上传](../http/protocol.md#uploads)。

<a id="example-usage"></a>
## 示例

```go
disk, err := local.Open(cfg.Filesystem.Disks.Local)
if err != nil {
    return err
}
files, err := filesystem.New(db, cfg.Filesystem, map[string]filesystem.Disk{"local": disk})
if err != nil {
    return err
}

// 上传：保存为临时文件，f.URL 是公开地址
f, err := files.Upload(ctx, owner, filesystem.FileInput{Filename: "cover.jpg", ContentType: "image/jpeg", Size: size}, body)

// 业务使用时确认，例如保存文章
f, err = files.Confirm(ctx, owner, f.ID, filesystem.Accept{Types: []string{"image/jpeg", "image/png"}})
```

`cfg.Filesystem` 是配置中的 `filesystem` 段，`db` 是入口打开的 `*bun.DB`，`owner` 是当前请求的 [`identity.Subject`](../security/authentication.md)，`body` 是长度为 `size` 的 `io.Reader`。

<a id="assembling-files"></a>
## 装配

1. 把 `filesystem.Migrations()` 加入[发布命令](../database/migrations.md#publishing-framework-migrations)，发布后执行迁移，建出 `files` 表。
2. 把 `filesystem.Config` 放进根配置（`yaml:"filesystem"`），见[配置](#configuration)。
3. 在入口打开用到的磁盘（[`local.Open`](#local-disk)、[`s3.Open`](#s3-disk)），用 `filesystem.New(db, cfg.Filesystem, map[string]filesystem.Disk{"local": localDisk, "s3": s3Disk})` 创建一次 `*filesystem.Service`，交给需要文件的[业务动作](../concepts/actions.md)。map 的键是磁盘名，写入 `files.disk` 列，读取文件时按它找到磁盘；只用一种磁盘时只传那一个。
4. 用[定时任务](#cleanup)执行 `Cleanup`。
5. 文件服务不注册路由，上传接口由应用声明，见[在 REST 接口中使用](#rest)。

<a id="configuration"></a>
## 配置

```yaml
# config/app.yaml
filesystem:
  default_disk: local      # FileInput.Disk 为空时用的磁盘，local 或 s3；必须在 disks 下配置。默认 local
  max_upload_size: 1GiB    # 单个文件的上限，可写 512MiB、2GiB 或字节数。默认 1GiB
  temp_ttl: 24h            # 上传后多久没有确认就回收，至少 1m。默认 24h
  disks:                   # 只填用到的磁盘，没填的磁盘不使用，也不校验
    local:
      root: storage/files                # 存储目录，由应用独占，不存在时创建
      public_url: ${APP_URL}/storage     # 公开地址的前缀：绝对 URL，或以 / 开头的路径（前后端同域时）
    s3:
      bucket: app-files                  # 桶名，预先创建
      region: ap-southeast-1             # 留空时读取 AWS 环境配置
      endpoint: ${S3_ENDPOINT:-}         # S3 兼容服务（MinIO、R2、OSS 等）的 API 地址，AWS 留空
      path_style: false                  # path-style 访问桶，MinIO 通常需要
      access_key: ${S3_ACCESS_KEY:-}     # 静态密钥，和 secret_key 同时填写；留空时用 AWS 默认凭证链
      secret_key: ${S3_SECRET_KEY:-}
      public_url: https://cdn.example.com  # 公开地址的前缀，CDN 或桶的公开域名，必填
```

[`config.Load`](../general/configuration.md#loading-configuration) 校验每个磁盘的配置，错误指出位置，例如 `filesystem.disks.s3: bucket is required`。

<a id="local-disk"></a>
## 本地磁盘：`local.Open`

`local.Open(cfg.Filesystem.Disks.Local)` 返回的 `*local.Disk` 是一个 `http.Handler`，挂在 `public_url` 的路径上提供下载。`router` 是入口的 chi Router：

```go
disk, err := local.Open(cfg.Filesystem.Disks.Local)
if err != nil {
    return err
}
defer disk.Close()

router.Handle("/storage/*", disk)
```

- HTML、SVG、XML 这类能执行脚本的类型作为附件下载并加上 `Content-Security-Policy: sandbox`：文件与应用同源，上传的网页不能在应用的源下运行脚本。`<img>` 中的 SVG 照常显示。
- 多个进程共用本地磁盘时挂载同一个目录。
- 本地磁盘不支持[客户端直传](#direct-upload)。

<a id="s3-disk"></a>
## S3 磁盘：`s3.Open`

```go
disk, err := s3.Open(ctx, cfg.Filesystem.Disks.S3)
```

- `Open` 检查桶可以访问。桶（或 CDN 回源）要配置公开读，`public_url` 加对象键能直接下载。
- S3 按上传时声明的 `Content-Type` 返回文件。`public_url` 用与应用不同的域名，上传的网页读不到应用的 cookie；要展示给用户的文件在确认时用 [`Accept`](#confirming-files) 限定类型。
- 已有 AWS SDK 的客户端时用 `s3.New(client, bucket, publicURL)` 创建磁盘；`disk.Client()` 返回原生客户端，用于未封装的 S3 功能。
- 客户端直传要为桶配置 CORS，存储要支持条件写入，见[客户端直传](#direct-upload)。`s3.Doctor(disk, uploadOrigins...)` 检查公开地址、CORS 和条件写入，见[自检](../installation/doctor.md#framework-checks)。

<a id="server-upload"></a>
## 通过应用服务器上传：`Upload`

`Upload` 把正文流式写入磁盘，内存占用与文件大小无关，所有磁盘都可以用。`fileService` 是 `*filesystem.Service`，`owner` 是当前请求的 `identity.Subject`，`body` 是长度为 `size` 的 `io.Reader`：

```go
record, err := fileService.Upload(ctx, owner, filesystem.FileInput{
    Filename: "report.pdf", ContentType: "application/pdf", Size: size,
}, body)
// record.Status 是 temporary，record.URL 可以直接给前端预览
```

- 正文长度必须正好等于 `Size`，否则返回 `filesystem.ErrInvalid`，写入的内容随之删除。超过 `max_upload_size` 返回 `filesystem.ErrTooLarge`。
- 服务端拉取未知长度流（如外部渠道下载或后台流式生成）时，`Size` 传 `filesystem.UnknownSize`（`-1`），流式写入磁盘后自动回填真实大小，同样受 `max_upload_size` 限制。
- `Disk` 为空时用 `default_disk`，也可以指定 `Disk: "s3"`。
- 对象键是上传日期、文件 ID 加小写扩展名，例如 `2026/11/10/0193a3c2-….jpg`；原始文件名只保存在 `filename` 列，公开地址中不出现。
- 后台任务生成的文件（导出的表格、PDF）同样用 `Upload` 保存。REST 接口见[服务端上传接口](#rest-upload)。

<a id="confirming-files"></a>
## 确认文件：`Confirm` 与 `Accept`

业务使用文件时调用 `Confirm`，用 `filesystem.Accept` 说明这个用途接受的文件。`fileService` 是 `*filesystem.Service`，`input.CoverID` 是客户端提交的文件 ID：

```go
var coverImage = filesystem.Accept{
    Types:        []string{"image/png", "image/jpeg", "image/webp"}, // "image/*" 匹配一类
    MaxSize:      5 * filesystem.MiB,                                // 0 时只受 max_upload_size 限制
    MaxDimension: 4096,                                              // 图片单边最大像素，0 表示不限
}

cover, err := fileService.Confirm(ctx, owner, input.CoverID, coverImage)
if err != nil {
    return err // 客户端提交了不合格的文件时原样返回
}
article.CoverID = cover.ID
// cover.Meta.Width / cover.Meta.Height 已自动提取并保存在 files.meta
```

- 对象没有完整上传时返回 `filesystem.ErrNotUploaded`。
- `Types` 非空时按文件内容识别真实类型（图片、常见音视频 `mp4/mov/webm/ogg/mp3/m4a/wav/aac/flac`、PDF、压缩包、Office 文档 `docx/xlsx/pptx/doc/xls/ppt` 及 `txt/log/csv/md/json`），它要在 `Types` 中，且与扩展名和声明的类型兼容；客户端上传时只给了兜底类型 `application/octet-stream` 时，确认后写回探测出的规范类型。`Accept{}` 只要求文件已经完整上传。
- 图片（PNG、JPEG、GIF、WebP）在确认时会自动解析像素宽高写入 `files.meta`（`File.Meta.Width` / `File.Meta.Height`）；设置 `MaxDimension` 后，无法解析出合法尺寸或任一边超过上限的图片会被拒绝。
- 不符合时返回 `filesystem.ErrNotAccepted`，文件保持 `temporary`，到期后被回收。
- 只有上传者本人能确认，其他主体和已过期的文件得到 `filesystem.ErrNotFound`：用户不能引用别人上传的文件。重复确认返回同一条记录。
- 要和业务数据一起提交时在事务中确认，见[在业务接口中确认](#rest-confirm)。

<a id="replacing-files"></a>
## 替换文件

更换封面时，在同一个事务中确认新文件、删除旧文件。`db` 是 `*bun.DB`，`fileService` 是 `*filesystem.Service`，`coverImage` 是这个用途的 [`Accept`](#confirming-files)：

```go
if newCoverID == article.CoverID {
    return nil
}
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    files := fileService.WithDB(tx)
    cover, err := files.Confirm(ctx, owner, newCoverID, coverImage)
    if err != nil {
        return err
    }
    if err := files.Delete(ctx, article.CoverID); err != nil {
        return err
    }
    _, err = tx.NewUpdate().Model(&article).Set("cover_id = ?", cover.ID).WherePK().Exec(ctx)
    return err
})
```

事务回滚时新文件保持 `temporary`，旧文件原样保留。

<a id="ordered-files"></a>
## 多个有序文件

一篇文章有多张配图时，用关联表 `article_images(article_id, file_id, position)` 保存文件 ID 和顺序。保存时逐个 `Confirm` 并插入关联行；移除配图时在同一个事务中删除关联行并 `Delete` 文件。读取时关联 `files` 表按顺序查询，地址由磁盘的 `URL` 生成，`disk` 是入口打开的磁盘：

```go
var images []filesystem.File
err := db.NewSelect().Model(&images).
    Join("JOIN article_images AS ai ON ai.file_id = f.id").
    Where("ai.article_id = ?", article.ID).Where("f.status = ?", filesystem.StatusReady).
    OrderExpr("ai.position").Scan(ctx)
for i := range images {
    images[i].URL = disk.URL(images[i].Key)
}
```

<a id="accessing-files"></a>
## 读取文件：`Get`、`Open`

业务表保存文件 ID，返回数据时用 `Get` 查出记录，`record.URL` 是公开地址。`fileService` 是 `*filesystem.Service`：

```go
record, err := fileService.Get(ctx, article.CoverID)
```

- `Get` 只按 ID 查询，不检查权限。已删除的文件返回 `filesystem.ErrNotFound`。批量读取多个文件时用 `GetMany(ctx, ids)`，返回按 ID 索引的 map，每条记录按所在磁盘填好 `URL`。
- 只需要拼地址时，业务表保存 `key`，用磁盘的 `URL(key)` 生成。

在服务端处理文件内容时用 `Open` 读取正文，调用方关闭它：

```go
body, record, err := fileService.Open(ctx, id)
if err != nil {
    return err
}
defer body.Close()
```

<a id="deleting-files"></a>
## 删除文件：`Delete`

`Delete` 把记录标为 `deleted`，之后 `Get`、`Open`、`Confirm` 都视为不存在；文件不存在时也返回成功。它只是一条 `UPDATE`，不访问存储，可以放进业务事务。`db` 是 `*bun.DB`，`fileService` 是 `*filesystem.Service`：

```go
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    if _, err := tx.NewDelete().Model(&article).WherePK().Exec(ctx); err != nil {
        return err
    }
    return fileService.WithDB(tx).Delete(ctx, article.CoverID)
})
```

- 事务回滚时文件原样保留。
- 对象由[清理任务](#cleanup)删除，在这之前它仍然可以通过公开地址下载。

<a id="cleanup"></a>
## 过期清理：`Cleanup`

`Cleanup` 是从存储中删除对象的唯一入口，每次最多处理 `limit` 条到期记录，返回清理的数量：

- 超过 `temp_ttl` 没有确认的 `temporary` 文件：中止未完成的分片会话，删除对象和记录；
- 已 `Delete` 的文件：删除对象和记录。

用[定时任务](../background/scheduling.md#framework-cleanups)每 5 分钟执行一次，`sched` 是 `*schedule.Scheduler`，`fileService` 是 `*filesystem.Service`：

```go
sched.Add("files:cleanup", "*/5 * * * *", schedule.Cleanup(fileService.Cleanup, 500))
```

- 多个实例同时执行是安全的；正在被确认的文件不会被删除。一条记录清理失败时推迟 5 分钟重试，其他记录照常处理。
- 直传的签名地址无法撤销，清理之后才开始的上传会让对象重新出现在 S3 中（签名只有 15 分钟，很少发生）。需要兜底时为桶配置[未完成分片的生命周期规则](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpu-abort-incomplete-mpu-lifecycle-config.html)。

<a id="processing-files"></a>
## 确认后异步处理

上传后要处理文件时（提取元数据、生成视频封面、为文档建索引），在确认文件的同一个事务中发布一个[队列](../background/queue.md)任务，事务回滚时任务随之撤销。`ProcessFile` 是 `queue.Topic[uuid.UUID]`，`db` 是 `*bun.DB`，`fileService` 是 `*filesystem.Service`：

```go
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    cover, err := fileService.WithDB(tx).Confirm(ctx, owner, input.CoverID, coverImage)
    if err != nil {
        return err
    }
    // ……保存文章
    return ProcessFile.Publish(ctx, tx, cover.ID)
})
```

处理函数用 [`Open`](#accessing-files) 读取正文；要保存处理结果时把它作为新文件 `Upload` 再 `Confirm`。

<a id="thumbnails"></a>
## 图片缩略图：`imgproxy`

缩略图由 [imgproxy](https://imgproxy.net) 生成：`imgproxy` 包把文件的公开地址和尺寸拼成签名的 URL，imgproxy 收到请求时下载原图、缩放后返回，结果由 CDN 缓存。存储中只有原图。

```yaml
# config/app.yaml
imgproxy:
  url: ${IMGPROXY_URL:-}    # imgproxy 的公开地址；为空时 URL 原样返回原图地址（开发环境）
  key: ${IMGPROXY_KEY:-}    # 十六进制的签名密钥，与 imgproxy 的 IMGPROXY_KEY 相同
  salt: ${IMGPROXY_SALT:-}  # 十六进制的盐，与 IMGPROXY_SALT 相同；和 key 同时填写
```

把 `imgproxy.Config` 放进根配置（`yaml:"imgproxy"`），在入口用 `imgproxy.New(cfg.Imgproxy)` 创建一次，它不访问网络。尺寸声明为包级变量，返回数据时生成地址，`images` 是 `*imgproxy.Proxy`，`cover` 是 `filesystem.File`：

```go
var coverThumb = imgproxy.Options{Width: 600, Height: 400, Resize: imgproxy.Fill, Format: "webp"}

out.CoverURL = cover.URL
out.CoverThumbURL = images.URL(cover.URL, coverThumb)
```

`Resize` 默认 `imgproxy.Fit`，保持比例缩到宽高以内；`imgproxy.Fill` 裁掉多余部分，结果正好是给定尺寸。宽或高为 0 时按另一边等比计算，不放大小图。`Format` 为空时保持原格式。

> [!WARNING]
> 生产环境必须填写 `key` 和 `salt`（用 `openssl rand -hex 32` 生成）。不填时生成不签名的地址，任何人都能让你的 imgproxy 处理任意图片。两边的密钥不一致时缩略图返回 403。

imgproxy 按地址下载原图。本地磁盘的 `public_url` 是 `/storage` 这样的路径时，在 imgproxy 的环境变量 `IMGPROXY_BASE_URL` 中写应用的地址，例如 `http://app:8080`。

<a id="direct-upload"></a>
## 客户端直传到 S3：`Prepare`

大文件（例如视频）由浏览器或 App 直接传到 S3，不经过应用服务器：应用登记文件、签发签名地址，客户端按地址上传，最后由业务确认。只有 S3 磁盘支持直传；本地磁盘的签名方法返回 `filesystem.ErrUnsupported`，用 `fileService.SupportsDirect(disk)` 判断。

先用 `Prepare` 登记文件，得到 `temporary` 的记录，再按大小选择[单次上传](#direct-put)或[分片上传](#multipart-upload)。`fileService` 是 `*filesystem.Service`，`owner` 是当前请求的 `identity.Subject`：

```go
record, err := fileService.Prepare(ctx, owner, filesystem.FileInput{
    Disk:        "s3",
    Filename:    "video.mp4",
    ContentType: "video/mp4",
    Size:        size,
})
```

- 签名方法返回 `filesystem.SignedRequest`（`url`、`method`、`headers`），客户端原样带上 `headers`。签名绑定声明的大小和类型，不符时 S3 返回 403。有效期 15 分钟，不超过临时文件剩余的时间。
- 上传完成后把文件 ID 交给业务接口，由它 [`Confirm`](#confirming-files)。
- 浏览器直传时，桶的 CORS 规则要允许站点来源、`PUT` 方法、请求头 `Content-Type` 和 `If-None-Match`，并在 `ExposeHeaders` 中列出 `ETag`（分片上传要读取它）。

<a id="direct-put"></a>
### 单次上传：`SignPut`

不超过 5 GiB 的文件一次上传，`record` 是 `Prepare` 返回的文件：

```go
signed, err := fileService.SignPut(ctx, owner, record.ID)
```

客户端用 `signed.method`、`signed.url` 和 `signed.headers` 把文件正文发给 S3。超过 5 GiB 时返回 `filesystem.ErrInvalid`，改用分片上传。

<a id="no-overwrite"></a>
### 不能覆盖已上传的对象

签名带着 `If-None-Match: *`，地址只能创建对象，对象已经存在时存储返回 `412`；分片上传的合并同样带着这个条件。签名在有效期内可以重复使用，没有这个条件时，文件确认之后客户端还能用同一个地址替换已经校验过的内容。遇到 `412` 时重新登记一个文件再上传。

> [!IMPORTANT]
> 直传要求存储支持条件写入。AWS S3、Cloudflare R2 和较新的 MinIO 都支持；不支持的存储会忽略这个请求头。[`s3.Doctor`](../installation/doctor.md#framework-checks) 传入 `uploadOrigins` 时检查它（`conditional_write`）。

<a id="multipart-upload"></a>
### 分片上传：`CreateMultipart`、`SignPart`、`CompleteMultipart`

大文件分片上传，一片失败只重传这一片。`record` 是 `Prepare` 返回的文件，`uploadID`、`number` 由客户端带回：

```go
upload, err := fileService.CreateMultipart(ctx, owner, record.ID)
// upload.UploadID、upload.PartSize、upload.PartCount 返回给客户端

// 每一片上传前单独签名，number 从 1 开始
signed, err := fileService.SignPart(ctx, owner, record.ID, uploadID, number)

// 全部上传后提交编号和 S3 返回的 ETag 响应头
record, err = fileService.CompleteMultipart(ctx, owner, record.ID, uploadID, []filesystem.Part{
    {Number: 1, ETag: `"9b2cf535f27731c974343645a3985328"`},
    {Number: 2, ETag: `"6f5902ac237024bdd0c176cb93063dc4"`},
})
```

- 分片大小默认 16 MiB，超大文件自动放大，不超过 10000 片；除最后一片外每片正好 `PartSize` 字节。空文件用 `SignPut`。
- 会话 ID 由客户端保存。每片上传前才签名，网络再慢也不会过期；分片可以并发上传。
- `parts` 必须正好覆盖 `1..PartCount`；合并成功后重复调用同样返回成功。
- 放弃时调用 `AbortMultipart(ctx, owner, id, uploadID)`，文件保持 `temporary`，可以重新创建会话。

<a id="rest"></a>
## 在 REST 接口中使用

上传接口放在应用的 `internal/file` 模块中（[组织路由](../http/routing.md#organizing-routes)），每个接口转交给一个同名的服务方法，响应体直接用服务返回的类型（自带 JSON 标签）：

| 方法和路径 | `OperationID` | 服务方法 | 响应体 |
| --- | --- | --- | --- |
| `POST /files` | `file-upload` | `Upload` | `File`（201） |
| `POST /files/prepare` | `file-prepare` | `Prepare` | `File`（201） |
| `POST /files/{id}/sign` | `file-sign-put` | `SignPut` | `SignedRequest` |
| `POST /files/{id}/multipart` | `file-multipart-create` | `CreateMultipart` | `Multipart`（201） |
| `POST /files/{id}/multipart/{uploadID}/parts/{number}/sign` | `file-multipart-sign-part` | `SignPart` | `SignedRequest` |
| `POST /files/{id}/multipart/{uploadID}/complete` | `file-multipart-complete` | `CompleteMultipart` | `File` |
| `DELETE /files/{id}/multipart/{uploadID}` | `file-multipart-abort` | `AbortMultipart` | 无（204） |

- 只用本地磁盘时只保留第一个接口。
- `Confirm` 不单独开接口，在使用文件的业务接口中调用，见[在业务接口中确认](#rest-confirm)。
- `OperationID` 决定[生成客户端](../http/clients.md)的方法名，例如 `api.file.signPut`。

<a id="rest-routes"></a>
### 注册路由

所有接口都需要登录。`requireUser` 是入口传入的 `rest.RequireToken(api, tokens.Validate)`，`tokens` 是 [`*apitoken.Store`](../security/api-tokens.md)，`files` 是 `*filesystem.Service`：

```go
upload := huma.Operation{
    OperationID:   "file-upload",
    Method:        http.MethodPost,
    Path:          "/files",
    DefaultStatus: http.StatusCreated,
}
requireUser(&upload)
rest.Register(api, upload, NewUpload(files).HTTP)
```

直传接口按[上表](#rest)同样注册，处理器是 `NewDirectUpload(files)` 的同名方法。

<a id="rest-upload"></a>
### 服务端上传接口

用 Huma 的 `MultipartFormFiles` 接收 `multipart/form-data` 中名为 `file` 的字段，交给 `Upload`：

```go
type UploadInput struct {
    RawBody huma.MultipartFormFiles[struct {
        File huma.FormFile `form:"file" required:"true"`
    }]
}

type FileOutput struct {
    Body filesystem.File
}

type Upload struct {
    files *filesystem.Service // NewUpload(files) 传入
}

func (a *Upload) HTTP(ctx context.Context, in *UploadInput) (*FileOutput, error) {
    owner, _ := identity.FromContext(ctx)
    f := in.RawBody.Data().File
    file, err := a.files.Upload(ctx, owner, filesystem.FileInput{
        Filename:    f.Filename,
        ContentType: f.ContentType,
        Size:        f.Size,
    }, f.File)
    if err != nil {
        return nil, err // 转换为对应的状态码，见错误码一节
    }
    return &FileOutput{Body: file}, nil
}
```

- 请求体上限要单独放宽，见[请求体大小与超时](#rest-limits)。
- 客户端的写法见[客户端代码：上传文件](../http/clients.md#uploading-files)。

<a id="rest-direct-upload"></a>
### 直传接口

`DirectUpload` 动作的每个方法对应[上表](#rest)中的一个直传接口：用 `identity.FromContext(ctx)` 取出当前主体，把参数转交给同名的服务方法，结果放进输出的 `Body`。输入：

- `Prepare`：请求体 `filename`（`minLength:"1"`）、`content_type`、`size`（`minimum:"0"`），`FileInput.Disk` 写 `"s3"`；
- `SignPut`、`CreateMultipart`：路径参数 `id`（`uuid.UUID`）；
- `SignPart`：`id`、`uploadID`（`string`）、`number`（`int32`，`minimum:"1" maximum:"10000"`）；
- `CompleteMultipart`：`id`、`uploadID`，请求体 `parts`（`[]filesystem.Part`，`minItems:"1"`）；
- `AbortMultipart`：`id`、`uploadID`。

<a id="pages"></a>
### 在页面中上传：`uploadFile`

用户选择文件后立即上传为临时文件，表单只提交文件 ID，保存时由业务接口确认。直传用 `@go-tenon/client` 的 `uploadFile`，各个步骤是[生成客户端](../http/clients.md)的方法；服务端上传见[客户端代码：上传文件](../http/clients.md#uploading-files)。

```ts
import { uploadFile } from "@go-tenon/client"

const record = await uploadFile(input.files[0], {
  prepare: (body) => api.file.prepare(body),
  sign: (id) => api.file.signPut(id),
  multipart: {
    create: (id) => api.file.multipartCreate(id),
    signPart: (id, uploadId, n) => api.file.multipartSignPart(id, uploadId, n),
    complete: (id, uploadId, parts) => api.file.multipartComplete(id, uploadId, { parts }),
    abort: (id, uploadId) => api.file.multipartAbort(id, uploadId),
  },
}, { onProgress: ({ loaded, total }) => setProgress(loaded / total), signal })
// 把 record.id 提交给业务接口，例如作为文章封面
```

- 不超过 `multipartThreshold`（默认 64 MiB）的文件一个请求上传，更大的分片上传，默认 4 片并发；一片失败时重新签名重传，仍然失败或被 `signal` 取消时放弃分片会话。
- 读不到 `ETag` 响应头时报错：桶的 CORS 规则要暴露它，见[客户端直传](#direct-upload)。

<a id="rest-confirm"></a>
### 在业务接口中确认：`WithDB`

客户端把文件 ID（例如请求体中的 `cover_id`）提交给使用它的业务接口，业务接口在写业务数据的同一个事务中确认。`a.db` 是 `*bun.DB`，`a.files` 是 `*filesystem.Service`，`coverImage` 是封面的 [`Accept`](#confirming-files)：

```go
func (a *Create) Handle(ctx context.Context, subject identity.Subject, item *Article, coverID uuid.UUID) error {
    return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        cover, err := a.files.WithDB(tx).Confirm(ctx, subject, coverID, coverImage)
        if err != nil {
            return err // 别人的、已过期的、没上传完或类型不符的文件
        }
        item.CoverID = cover.ID
        _, err = tx.NewInsert().Model(item).Exec(ctx)
        return err
    })
}
```

接口原样返回 `Confirm` 的错误，见[错误码](#rest-errors)。事务回滚时文件保持 `temporary`，到期后被回收。

<a id="rest-limits"></a>
### 请求体大小与超时

[默认中间件](../http/middleware.md#default-middleware)的 `BodyLimit` 把请求体限制在 1 MiB，超出时返回 `413`；[请求超时](../http/middleware.md#request-timeout)也限制上传时长。服务端上传接口要单独放宽这两项，见[自行组装中间件](../http/middleware.md#custom-stack)：`BodyLimit` 至少是 `max_upload_size` 加上表单的少量开销，上传接口放在超时更长的路由组中。`multipart/form-data` 的请求体不受 Huma 默认 1 MiB 的 `MaxBodyBytes` 限制。

直传接口只收发少量 JSON，用默认限制。

<a id="rest-errors"></a>
### 错误码

`filesystem` 的错误是 [errorx](../general/errors.md) 业务错误，处理器原样返回时转换为下面的状态码和 `code`；也可以用 `errors.Is` 匹配：

| 错误 | 状态码 | `code` | 原因 |
| --- | --- | --- | --- |
| `ErrInvalid` | 422 | `filesystem.invalid` | 输入不合法，例如正文长度与 `Size` 不符 |
| `ErrTooLarge` | 422 | `filesystem.too_large` | 超过 `max_upload_size` |
| `ErrNotFound` | 404 | `filesystem.not_found` | 不存在、已删除、已过期，或不属于当前主体 |
| `ErrNotUploaded` | 409 | `filesystem.not_uploaded` | 确认时对象还没有完整上传 |
| `ErrUnsupported` | 422 | `filesystem.direct_upload_unsupported` | 磁盘不支持客户端直传 |
| `ErrNotAccepted` | 422 | `filesystem.not_accepted` | 不符合 `Confirm` 的 `Accept` |

<a id="schema"></a>
## `files` 表

列：`id`（UUIDv7）、`disk` 和 `key`（联合唯一）、`filename`、`content_type`、`size`、`owner_type` 和 `owner_id`（上传者）、`status`、`expires_at`（`temporary` 的回收时间、`deleted` 的清理时间）、`created_at`、`updated_at`。应用可以为它加列，框架只读写这些列。状态变更都是带条件的单条 `UPDATE`，多个进程同时操作同一个文件是安全的。
