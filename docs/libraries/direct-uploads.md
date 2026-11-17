# 客户端直传

上传文件时，文件通常先传到应用服务器，再由应用保存到磁盘或 S3（见[通过应用服务器上传](filesystem.md#server-upload)）。大文件（例如视频）这样传会占用应用服务器的带宽和时间。**客户端直传**让浏览器或 App 拿着应用签发的**签名地址**，把文件直接传到 S3：签名地址是一个带有效期和签名的 URL，只允许在有效期内、按约定的大小和类型上传这一个文件。应用只负责登记文件、签发地址，最后确认上传完成。

这一页先介绍文件服务提供的直传方法，再介绍怎样把它们做成 REST 接口。

阅读前需要了解：[文件存储](filesystem.md)；[文件的常见用法与上传接口](file-uploads.md)中的 REST 路由。本地磁盘不支持直传。

- [直传的方法](#direct-upload)
    - [单次上传](#direct-put)
    - [不能覆盖已上传的对象](#no-overwrite)
    - [分片上传](#multipart-upload)
- [直传接口](#rest-direct-upload)
- [常见问题](#troubleshooting)

<a id="direct-upload"></a>
## 直传的方法

使用 S3 磁盘时，客户端可以拿着签名地址把文件直接传到 S3。先用 `Prepare` 登记文件，再根据大小选择单次上传或分片上传：

```go
record, err := fileService.Prepare(ctx, owner, filesystem.FileInput{
    Disk:        "s3",
    Filename:    "video.mp4",
    ContentType: "video/mp4",
    Size:        size,
})
```

签名返回 `filesystem.SignedRequest`，包含 `url`、`method` 和必须原样携带的 `headers`。签名绑定了文件的大小和类型，长度不符的请求会被 S3 拒绝。签名有效期是 15 分钟，并且不超过临时文件剩余的时间。

本地磁盘不支持直传，签名方法返回 `filesystem.ErrUnsupported`，请改用 `Upload`。

<a id="direct-put"></a>
### 单次上传

不超过 5GiB 的文件可以一次上传：

```go
signed, err := fileService.SignPut(ctx, owner, record.ID)
```

客户端用 `signed` 发起请求：

```js
await fetch(signed.url, { method: signed.method, headers: signed.headers, body: file })
```

上传成功后，业务使用文件时调用 [Confirm](filesystem.md#confirming-files)。

<a id="no-overwrite"></a>
### 不能覆盖已上传的对象

签名带着 `If-None-Match: *`：这个地址只能创建对象，对象已经存在时存储返回 `412`。签名在有效期（最长 15 分钟）内可以重复使用，没有这个条件的话，文件确认之后，客户端还能用同一个地址把通过校验的内容换掉，记录仍然是 `ready`。分片上传的合并同样带着这个条件。

因此直传要求存储支持条件写入：AWS S3、Cloudflare R2 和较新的 MinIO 都支持。不支持的存储可能直接忽略这个请求头，[`s3.Doctor`](../installation/doctor.md) 传入 `uploadOrigins` 时会检查（`conditional_write`）。

<a id="multipart-upload"></a>
### 分片上传

大文件使用分片上传，单片失败只需重传这一片。先创建分片会话：

```go
upload, err := fileService.CreateMultipart(ctx, owner, record.ID)
// upload.UploadID、upload.PartSize、upload.PartCount
```

分片大小默认 16MiB，超大文件会自动放大，保证在 S3 的 10000 片上限以内。除最后一片外，每片正好 `PartSize` 字节。会话 ID 由客户端保存并在后续请求中带回。

每一片上传前，单独为它签名，签名后立即使用，因此网络再慢签名也始终有效：

```go
signed, err := fileService.SignPart(ctx, owner, record.ID, uploadID, number) // number 从 1 开始
```

客户端上传每一片后，从响应头读取 `ETag` 并保存。不同分片可以并发上传；某一片失败时，重新签名并重传这一片即可。全部上传后，提交编号和 ETag 合并：

```go
record, err := fileService.CompleteMultipart(ctx, owner, record.ID, uploadID, []filesystem.Part{
    {Number: 1, ETag: `"9b2cf535f27731c974343645a3985328"`},
    {Number: 2, ETag: `"6f5902ac237024bdd0c176cb93063dc4"`},
})
```

`parts` 必须正好覆盖 `1..PartCount`。合并成功但响应丢失时，重复调用同样返回成功。放弃上传时调用 `AbortMultipart(ctx, owner, id, uploadID)`，之后可以重新创建会话。

<a id="rest-direct-upload"></a>
## 直传接口

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

客户端用 `@go-tenon/client` 的 `uploadFile` 执行完整的流程，各个步骤是[生成客户端](../http/clients.md)的方法：

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

- 文件不超过 `multipartThreshold`（默认 64 MiB）时签名一次，用一个请求上传；更大的文件分片上传，默认 4 片并发，每一片在上传前才签名。
- 某一片失败时重新签名并重传这一片（默认 2 次）；仍然失败或被 `signal` 取消时放弃分片会话。
- 合并时要提交每一片的 `ETag`，它是对象存储的响应头：bucket 的 CORS 规则要允许 `PUT` 和请求头 `Content-Type`、`If-None-Match`，并在 `ExposeHeaders` 中列出 `ETag`，否则浏览器中的脚本读不到它，`uploadFile` 报错。
- 只使用本地磁盘时不提供 `multipart`，或者用服务端上传接口。

步骤的格式见[客户端协议](../http/protocol.md#uploads)。

<a id="troubleshooting"></a>
## 常见问题

**签名方法返回 `filesystem.ErrUnsupported`**

本地磁盘不支持直传，改用 `Upload` 通过应用服务器上传。

**S3 拒绝了上传请求，返回 403**

签名地址绑定了文件的大小、类型和必须携带的请求头。检查客户端是否原样带上了签名返回的 `headers`，文件的实际大小是否与登记时一致，以及签名是否已经过期（有效期 15 分钟）。

**浏览器上传时报跨域错误**

浏览器直接请求 S3 的域名，需要在存储桶上配置 CORS，允许应用的域名用 `PUT` 上传，允许请求头 `Content-Type` 和 `If-None-Match`。`s3.Doctor(disk, "https://example.com")` 读取存储桶的 CORS 规则，检查它们是否允许这个来源直传，见[自检](../installation/doctor.md#framework-checks)。

**S3 返回 412 Precondition Failed**

这个地址已经上传过，对象已经存在，签名地址不能覆盖它，见[不能覆盖已上传的对象](#no-overwrite)。重新登记一个文件再上传。

**上传成功了，但文件过一段时间后不见了**

上传之后还要确认文件，没有确认的临时文件会被过期清理删除，见[确认文件](filesystem.md#confirming-files)。

