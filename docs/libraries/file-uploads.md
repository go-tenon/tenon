# 文件的常见用法与上传接口

本页在[文件存储](filesystem.md)之上给出几种常见的写法：替换文件、多个有序文件、确认后异步处理、图片缩略图，以及在 REST 接口中提供服务端上传和客户端直传。文件服务本身（配置、磁盘、上传、确认、清理）见[文件存储](filesystem.md)。

- [替换文件](#replacing-files)
- [多个有序文件](#ordered-files)
- [确认后异步处理](#processing-files)
- [图片缩略图](#thumbnails)
- [在页面中上传](#pages)
- [在 REST 接口中使用](#rest)
    - [路由](#rest-routes)
    - [服务端上传接口](#rest-upload)
    - [直传接口](#rest-direct-upload)
    - [在业务接口中确认](#rest-confirm)
    - [请求体大小与超时](#rest-limits)
    - [错误响应](#rest-errors)

<a id="replacing-files"></a>
## 替换文件

更换封面时，在同一个事务中确认新文件、删除旧文件。新旧 ID 相同时什么也不用做：

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

<a id="ordered-files"></a>
## 多个有序文件

一篇文章有多张配图时，用一张关联表保存文件 ID 和顺序：

```sql
CREATE TABLE article_images (
    article_id uuid NOT NULL REFERENCES articles (id),
    file_id    uuid NOT NULL,
    position   integer NOT NULL,
    PRIMARY KEY (article_id, file_id)
);
```

保存时逐个 `Confirm` 并插入关联行；移除配图时，在同一个事务中删除关联行并 `Delete` 文件。读取时关联 `files` 表按顺序查询，地址由磁盘的 `URL` 生成：

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

<a id="processing-files"></a>
## 确认后异步处理

需要在上传后处理文件（例如提取元数据、生成视频封面）时，在确认文件的同一个事务中发布一个[队列](queue.md)任务。任务和文件状态一起提交，事务回滚时任务也不会执行：

```go
err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
    cover, err := fileService.WithDB(tx).Confirm(ctx, owner, input.CoverID, coverImage)
    if err != nil {
        return err
    }
    // ……保存文章
    return ProcessFile.Publish(ctx, tx, cover.ID) // queue.Topic[uuid.UUID]("files.process")
})
```

处理函数用 `Open` 读取正文；需要保存处理结果时，把它作为新文件 `Upload` 并 `Confirm`。

<a id="thumbnails"></a>
## 图片缩略图

框架不生成也不保存缩略图。文件都有公开地址，缩略图交给 [imgproxy](https://imgproxy.net)：`imgproxy` 包把文件地址和尺寸拼成一个签名的 URL，imgproxy 收到请求时下载原图，缩放后返回，结果由 CDN 缓存。

```yaml
# config/app.yaml
imgproxy:
  url: ${IMGPROXY_URL:-}
  key: ${IMGPROXY_KEY:-}
  salt: ${IMGPROXY_SALT:-}
```

把 `imgproxy.Config` 放进根配置结构体（`yaml:"imgproxy"`），在入口创建，`New` 不访问网络：

```go
images, err := imgproxy.New(cfg.Imgproxy)
```

尺寸在代码中声明为变量，返回数据时生成地址：

```go
var coverThumb = imgproxy.Options{Width: 600, Height: 400, Resize: imgproxy.Fill, Format: "webp"}

out.CoverURL = cover.URL
out.CoverThumbURL = images.URL(cover.URL, coverThumb)
```

- `Resize` 默认是 `imgproxy.Fit`，保持比例缩到宽高以内；`imgproxy.Fill` 裁掉多余部分，结果正好是给定尺寸。宽或高为 0 时按另一边等比计算。图片比目标尺寸小时不放大。
- `Format` 是输出格式，例如 `webp`、`avif`，为空时保持原格式。
- `url` 为空时 `URL` 原样返回原图地址，开发环境不需要启动 imgproxy。
- `key` 和 `salt` 是十六进制的签名密钥和盐，与 imgproxy 的 `IMGPROXY_KEY`、`IMGPROXY_SALT` 相同，可以用 `openssl rand -hex 32` 生成。不填时生成不签名的地址，任何人都能让你的 imgproxy 处理任意图片，生产环境必须填写。

imgproxy 按地址下载原图。S3 磁盘的公开地址是绝对 URL，可以直接使用；本地磁盘的 `public_url` 是 `/storage` 这样的路径时，用 `IMGPROXY_BASE_URL` 设置应用的地址，imgproxy 会把它拼在相对地址前面：

```yaml
# compose.yaml
services:
  imgproxy:
    image: ghcr.io/imgproxy/imgproxy:v3
    environment:
      IMGPROXY_KEY: ${IMGPROXY_KEY}
      IMGPROXY_SALT: ${IMGPROXY_SALT}
      IMGPROXY_BASE_URL: http://app:8080
```

需要把缩略图保存为文件时（例如视频封面），按[确认后异步处理](#processing-files)生成新文件。

<a id="pages"></a>
## 在页面中上传

单页应用的上传见[客户端代码的上传文件](../responses/clients.md#uploading-files)：用户选择文件后立即上传为临时文件，表单只提交文件 ID，保存时确认。

<a id="rest"></a>
## 在 REST 接口中使用

文件服务不注册路由，上传接口由你在 [REST](../http/routing.md) 模块中按需声明。下面是一个 `file` 模块的写法，目录组织与[组织路由](../http/routing.md#organizing-routes)一致：

```text
internal/file/
├── routes.go              注册文件模块的路由
├── upload.go              服务端上传
├── direct_upload.go       S3 直传：登记、签名、分片
└── dto.go                 共享的输入输出类型
```

这些业务动作的业务都由框架的文件服务完成，它们只有 HTTP 入口，不需要单独的 `Handle`。

<a id="rest-routes"></a>
### 路由

| 方法和路径 | 服务方法 | 用途 |
| --- | --- | --- |
| `POST /files` | `Upload` | 通过应用服务器上传（`multipart/form-data`） |
| `POST /files/prepare` | `Prepare` | 登记直传文件 |
| `POST /files/{id}/sign` | `SignPut` | 单次直传签名 |
| `POST /files/{id}/multipart` | `CreateMultipart` | 创建分片会话 |
| `POST /files/{id}/multipart/{uploadID}/parts/{number}/sign` | `SignPart` | 为一片签名 |
| `POST /files/{id}/multipart/{uploadID}/complete` | `CompleteMultipart` | 合并分片 |
| `DELETE /files/{id}/multipart/{uploadID}` | `AbortMultipart` | 放弃分片上传 |

只使用本地磁盘时，保留第一个路由即可。`Confirm` 不需要单独的接口，它在使用文件的业务接口中调用，见[在业务接口中确认](#rest-confirm)。

`routes.go` 声明这些路由。所有接口都需要登录，`requireUser` 由入口传入，例如 `rest.RequireBearer(api, validateToken)`：

```go
// Register 注册文件模块的全部路由。
func Register(api *rest.API, requireUser func(*huma.Operation), files *filesystem.Service) {
    upload := huma.Operation{
        OperationID:     "file-upload",
        Method:          http.MethodPost,
        Path:            "/files",
        Summary:         "上传文件",
        DefaultStatus: http.StatusCreated,
    }
    requireUser(&upload)
    rest.Register(api, upload, NewUpload(files).HTTP)

    prepare := huma.Operation{
        OperationID:   "file-prepare",
        Method:        http.MethodPost,
        Path:          "/files/prepare",
        Summary:       "登记直传文件",
        DefaultStatus: http.StatusCreated,
    }
    requireUser(&prepare)
    rest.Register(api, prepare, NewDirectUpload(files).Prepare)

    signPut := huma.Operation{
        OperationID: "file-sign-put",
        Method:      http.MethodPost,
        Path:        "/files/{id}/sign",
        Summary:     "单次直传签名",
    }
    requireUser(&signPut)
    rest.Register(api, signPut, NewDirectUpload(files).SignPut)

    createMultipart := huma.Operation{
        OperationID:   "file-multipart-create",
        Method:        http.MethodPost,
        Path:          "/files/{id}/multipart",
        Summary:       "创建分片会话",
        DefaultStatus: http.StatusCreated,
    }
    requireUser(&createMultipart)
    rest.Register(api, createMultipart, NewDirectUpload(files).CreateMultipart)

    signPart := huma.Operation{
        OperationID: "file-multipart-sign-part",
        Method:      http.MethodPost,
        Path:        "/files/{id}/multipart/{uploadID}/parts/{number}/sign",
        Summary:     "分片签名",
    }
    requireUser(&signPart)
    rest.Register(api, signPart, NewDirectUpload(files).SignPart)

    complete := huma.Operation{
        OperationID: "file-multipart-complete",
        Method:      http.MethodPost,
        Path:        "/files/{id}/multipart/{uploadID}/complete",
        Summary:     "合并分片",
    }
    requireUser(&complete)
    rest.Register(api, complete, NewDirectUpload(files).CompleteMultipart)

    abort := huma.Operation{
        OperationID:   "file-multipart-abort",
        Method:        http.MethodDelete,
        Path:          "/files/{id}/multipart/{uploadID}",
        Summary:       "放弃分片上传",
        DefaultStatus: http.StatusNoContent,
    }
    requireUser(&abort)
    rest.Register(api, abort, NewDirectUpload(files).AbortMultipart)
}
```

入口在组装应用时调用它：

```go
file.Register(api, rest.RequireBearer(api, validateToken), fileService)
```

`dto.go` 定义共享的输入和输出。响应体直接使用 `filesystem.File` 等类型，它们自带 JSON 标签，`File` 的 JSON 中包含公开地址 `url`，但不包含上传者信息：

```go
type FileIDInput struct {
    ID uuid.UUID `path:"id"`
}

type UploadIDInput struct {
    ID       uuid.UUID `path:"id"`
    UploadID string    `path:"uploadID"`
}

type FileOutput struct {
    Body filesystem.File
}

type SignedOutput struct {
    Body filesystem.SignedRequest
}

type MultipartOutput struct {
    Body filesystem.Multipart
}

type NoContentOutput struct{}
```

<a id="rest-upload"></a>
### 服务端上传接口

`upload.go` 用 Huma 的 `MultipartFormFiles` 接收 `multipart/form-data` 中名为 `file` 的字段，再交给 `Upload`：

```go
type UploadInput struct {
    RawBody huma.MultipartFormFiles[struct {
        File huma.FormFile `form:"file" required:"true"`
    }]
}

type Upload struct {
    files *filesystem.Service
}

func NewUpload(files *filesystem.Service) *Upload {
    return &Upload{files: files}
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
        return nil, err
    }
    return &FileOutput{Body: file}, nil
}
```

Huma 解析表单时，较小的文件留在内存中，较大的文件写入临时文件，`Upload` 再把它流式写入磁盘。错误直接返回即可，`filesystem` 的错误会转换为对应的 HTTP 响应，见[错误响应](#rest-errors)。

客户端上传：

```bash
curl -X POST https://example.com/files \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@cover.png"
```

```json
{
  "id": "0193a3c2-...",
  "disk": "local",
  "key": "2026/11/10/0193a3c2-....png",
  "filename": "cover.png",
  "content_type": "image/png",
  "size": 102400,
  "status": "temporary",
  "expires_at": "2026-11-11T08:00:00Z",
  "url": "https://example.com/storage/2026/11/10/0193a3c2-....png"
}
```

<a id="rest-direct-upload"></a>
### 直传接口

`direct_upload.go` 把直传相关的几个接口放在一个业务动作中，每个方法对应一个接口，都只是把参数转交给文件服务：

```go
type PrepareFileInput struct {
    Body struct {
        Filename    string `json:"filename" minLength:"1"`
        ContentType string `json:"content_type,omitempty"`
        Size        int64  `json:"size" minimum:"0"`
    }
}

type SignPartInput struct {
    ID       uuid.UUID `path:"id"`
    UploadID string    `path:"uploadID"`
    Number   int32     `path:"number" minimum:"1" maximum:"10000"`
}

type CompleteMultipartInput struct {
    ID       uuid.UUID `path:"id"`
    UploadID string    `path:"uploadID"`
    Body     struct {
        Parts []filesystem.Part `json:"parts" minItems:"1"`
    }
}

type DirectUpload struct {
    files *filesystem.Service
}

func NewDirectUpload(files *filesystem.Service) *DirectUpload {
    return &DirectUpload{files: files}
}

func (a *DirectUpload) Prepare(ctx context.Context, in *PrepareFileInput) (*FileOutput, error) {
    owner, _ := identity.FromContext(ctx)
    file, err := a.files.Prepare(ctx, owner, filesystem.FileInput{
        Disk:        "s3",
        Filename:    in.Body.Filename,
        ContentType: in.Body.ContentType,
        Size:        in.Body.Size,
    })
    if err != nil {
        return nil, err
    }
    return &FileOutput{Body: file}, nil
}

func (a *DirectUpload) SignPut(ctx context.Context, in *FileIDInput) (*SignedOutput, error) {
    owner, _ := identity.FromContext(ctx)
    signed, err := a.files.SignPut(ctx, owner, in.ID)
    if err != nil {
        return nil, err
    }
    return &SignedOutput{Body: signed}, nil
}

func (a *DirectUpload) CreateMultipart(ctx context.Context, in *FileIDInput) (*MultipartOutput, error) {
    owner, _ := identity.FromContext(ctx)
    upload, err := a.files.CreateMultipart(ctx, owner, in.ID)
    if err != nil {
        return nil, err
    }
    return &MultipartOutput{Body: upload}, nil
}

func (a *DirectUpload) SignPart(ctx context.Context, in *SignPartInput) (*SignedOutput, error) {
    owner, _ := identity.FromContext(ctx)
    signed, err := a.files.SignPart(ctx, owner, in.ID, in.UploadID, in.Number)
    if err != nil {
        return nil, err
    }
    return &SignedOutput{Body: signed}, nil
}

func (a *DirectUpload) CompleteMultipart(ctx context.Context, in *CompleteMultipartInput) (*FileOutput, error) {
    owner, _ := identity.FromContext(ctx)
    file, err := a.files.CompleteMultipart(ctx, owner, in.ID, in.UploadID, in.Body.Parts)
    if err != nil {
        return nil, err
    }
    return &FileOutput{Body: file}, nil
}

func (a *DirectUpload) AbortMultipart(ctx context.Context, in *UploadIDInput) (*NoContentOutput, error) {
    owner, _ := identity.FromContext(ctx)
    if err := a.files.AbortMultipart(ctx, owner, in.ID, in.UploadID); err != nil {
        return nil, err
    }
    return &NoContentOutput{}, nil
}
```

浏览器端的完整流程如下。小于 5GiB 的文件可以走单次上传，这里统一用分片演示；`api` 是带上登录 token 调用应用接口、返回 JSON 的函数：

```js
const file = input.files[0]
const record = await api('POST', '/files/prepare', { filename: file.name, content_type: file.type, size: file.size })
const upload = await api('POST', `/files/${record.id}/multipart`)

const parts = []
for (let n = 1; n <= upload.part_count; n++) {
  const chunk = file.slice((n - 1) * upload.part_size, n * upload.part_size)
  const signed = await api('POST', `/files/${record.id}/multipart/${upload.upload_id}/parts/${n}/sign`)
  const res = await fetch(signed.url, { method: signed.method, headers: signed.headers, body: chunk })
  parts.push({ number: n, etag: res.headers.get('ETag') })
}

await api('POST', `/files/${record.id}/multipart/${upload.upload_id}/complete`, { parts })
// 把 record.id 提交给业务接口，例如作为文章封面
```

每一片在上传前才签名，可以并发上传多片；某一片失败时，重新签名并重传这一片即可。

<a id="rest-confirm"></a>
### 在业务接口中确认

客户端上传完成后，把文件 ID 提交给使用它的业务接口，由业务接口确认。例如创建文章时设置封面，`Create` 通过构造函数接收文件服务，在插入文章的同一个事务中确认文件：

```go
type CreateInput struct {
    Body struct {
        Changes
        CoverID uuid.UUID `json:"cover_id"`
    }
}

func (a *Create) Handle(ctx context.Context, subject identity.Subject, c Changes, coverID uuid.UUID) (*Article, error) {
    item := &Article{ /* 与原来相同…… */ }
    err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
        cover, err := a.files.WithDB(tx).Confirm(ctx, subject, coverID, coverImage)
        if err != nil {
            return err
        }
        item.CoverID = cover.ID
        _, err = tx.NewInsert().Model(item).Exec(ctx)
        return err // 处理唯一约束冲突，与原来相同……
    })
    if err != nil {
        return nil, err
    }
    return item, nil
}
```

`HTTP` 把 `in.Body.Changes` 和 `in.Body.CoverID` 交给 `Handle`。用户提交了别人的文件 ID、已过期的 ID，文件还没上传完，或者不是允许的图片时，`Confirm` 会返回错误，接口直接返回它即可。事务回滚时文件保持 `temporary`，见[确认文件](filesystem.md#confirming-files)。

<a id="rest-limits"></a>
### 请求体大小与超时

服务端上传的请求体通常远大于普通接口。[默认中间件](../http/middleware.md#default-middleware)中的 `BodyLimit` 把请求体限制在 1 MiB，[请求超时](../http/middleware.md#request-timeout)也会限制上传时长。需要为 `/files` 单独放宽，做法见[自行组装中间件](../http/middleware.md#custom-stack)。上限至少应为 `max_upload_size` 加上表单的少量额外开销。Huma 不限制 `multipart/form-data` 请求体的大小，不需要再设置 `Operation.MaxBodyBytes`。

直传接口只收发少量 JSON，使用默认限制即可，文件正文不经过应用服务器。

<a id="rest-errors"></a>
### 错误响应

`filesystem` 的错误是 [errorx](../general/errors.md) 业务错误，处理器原样返回时，会转换为下面的状态码，响应体中的 `code` 字段是稳定的错误码：

| 错误 | 状态码 | `code` |
| --- | --- | --- |
| `filesystem.ErrInvalid` | 422 | `filesystem.invalid` |
| `filesystem.ErrTooLarge` | 422 | `filesystem.too_large` |
| `filesystem.ErrNotFound` | 404 | `filesystem.not_found` |
| `filesystem.ErrNotUploaded` | 409 | `filesystem.not_uploaded` |
| `filesystem.ErrUnsupported` | 422 | `filesystem.direct_upload_unsupported` |
| `filesystem.ErrNotAccepted` | 422 | `filesystem.not_accepted` |

```json
{
  "status": 404,
  "title": "Not Found",
  "detail": "Not Found",
  "code": "filesystem.not_found"
}
```

需要本地化提示时，在翻译文件中以错误码为消息 ID 添加译文，见[本地化](../general/localization.md)。
