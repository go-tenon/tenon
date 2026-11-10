# 文件存储与上传

- [简介](#introduction)
- [发布迁移](#publishing-migrations)
- [装配文件服务](#assembling-files)
    - [本地存储](#local-storage)
    - [S3 存储](#s3-storage)
- [单次上传与确认](#single-upload)
- [分片与断点续传](#multipart-upload)
- [S3 客户端直传](#direct-upload)
- [读取与删除](#reading-and-deleting)
- [过期清理](#cleanup)
- [表结构和一致性](#schema-and-consistency)

<a id="introduction"></a>
## 简介

`files` 为你提供依赖 PostgreSQL 的文件服务，使用一张 `files` 表管理文件信息、所有者、状态和当前上传会话。底层可使用本地目录或 S3 兼容存储，两种方式支持单次上传、分片重传、断点续传和取消上传。

应用显式创建数据库、存储和文件服务，并在自己的 HTTP 处理器中调用服务方法。上传接口的认证、路由、请求体限制和业务权限由应用设置。文件服务始终校验传入主体的类型和 ID；不使用全局存储注册表，也不会自动建表、挂载路由或启动清理任务。

<a id="publishing-migrations"></a>
## 发布迁移

在现有命令装配中加入下面的片段。`migrations.FS` 是应用自己的迁移文件系统，`dbConfig` 从应用配置中取得 `database.Config`，`root` 是 `command.Root` 返回的根命令。导入 `command/migratecmd`、`database/migrate` 和 `files`：

```go
migrationCmd := migratecmd.New(migrations.FS, dbConfig)
migrationCmd.AddCommand(migratecmd.Publish(
    migrate.Source{Name: "files", FS: files.Migrations()},
))
root.AddCommand(migrationCmd)
```

然后在项目根目录运行：

```bash
go run ./cmd/api migrate publish --dir migrations
```

发布仅拷贝 SQL，不需要配置文件、数据库或存储连接。审查并提交生成的文件，重新构建应用，再执行 `migrate up`。再次发布会跳过已有来源，框架升级时只追加新增迁移。空项目尚无 SQL 时的首次发布入口见[发布框架迁移](migrations.md#publishing-framework-migrations)。

<a id="assembling-files"></a>
## 装配文件服务

`files.New(db, disks, cfg)` 必须传入已完成迁移的 `*bun.DB`。`disks` 是应用创建的 `map[string]files.Backend`，键是稳定的存储配置名。每个文件保存创建时选中的名称，后续读取不会随默认存储切换而改变位置。

`files.Config` 可以嵌入应用配置，支持 `config.Load` 的默认值和校验：

| 配置 | 默认值 | 含义 |
| --- | --- | --- |
| `default_disk` | `local` | 默认存储配置名 |
| `max_upload_bytes` | 5 GiB | 单个文件的最大声明大小 |
| `part_size` | 8 MiB | 分片大小；创建文件时保存，配置变化不影响续传 |
| `temp_ttl` | 24h | 准备上传后允许完成和确认的时间 |
| `sign_ttl` | 15m | S3 签名有效期，同时受剩余会话时间约束 |

分片大小必须在 5 MiB 到 5 GiB 之间，文件最多包含 10,000 片，末片可以小于 5 MiB。文件大小可以为零。

<a id="local-storage"></a>
### 本地存储

下面是完整的装配函数。`db` 由应用入口打开，调用方在所有上传、下载结束后执行返回的关闭函数：

```go
package bootstrap

import (
    "github.com/uptrace/bun"
    "github.com/ryan053857/mygoravel/files"
    "github.com/ryan053857/mygoravel/files/local"
)

func LocalFiles(db *bun.DB, root string) (*files.Service, func() error, error) {
    disk, err := local.Open(local.Config{Root: root})
    if err != nil {
        return nil, nil, err
    }
    service, err := files.New(db, map[string]files.Backend{"local": disk}, files.DefaultConfig())
    if err != nil {
        _ = disk.Close()
        return nil, nil, err
    }
    return service, disk.Close, nil
}
```

本地对象和未完成分片存放在隔离目录中；写入成功后才原子替换目标文件，失败不会发布半个文件。分片元数据和正文落盘，进程重启后可以恢复。根目录应由应用管理，不能直接作为整个目录的公开静态资源挂载。

同一存储实例支持并发。多个进程使用文件服务时，数据库行锁协调同一文件的修改；它们必须连接同一数据库，并访问同一共享存储目录。各实例使用独立磁盘时，需要应用将会话路由到保存分片的实例。本地持久化支持正常进程重启恢复，不承诺断电情况下的完整持久性。

<a id="s3-storage"></a>
### S3 存储

下面是已有入口函数中的片段：`ctx` 是入口 context，`db` 是已打开的数据库；需要导入 `files`，并将 `files/s3` 别名导入为 `s3store`：

```go
disk, err := s3store.Open(ctx, s3store.Config{
    Bucket: "app-attachments",
    Region: "ap-southeast-1",
})
if err != nil {
    return err
}
cfg := files.DefaultConfig()
cfg.DefaultDisk = "attachments"
service, err := files.New(db, map[string]files.Backend{"attachments": disk}, cfg)
if err != nil {
    return err
}
// 把 service 传给应用的处理器。
_ = service
```

未设置静态凭证时使用 AWS 默认凭证链。S3 兼容服务可配置 `Endpoint`、`PathStyle`、`AccessKey`、`SecretKey` 和可选 `SessionToken`。`Open` 会检查桶可访问，桶由应用或运维预先创建；不要求公开访问 URL。已有原生 SDK 客户端时可用 `s3store.New(client, bucket)`，后续也可通过 `disk.Client()` 使用 SDK 的其他能力。

单次 S3 上传上限为 5 GiB，更大文件使用分片。通过应用服务器上传时，适配器先流式暂存到临时磁盘，验证长度后发送，以支持不可 seek 的 HTTP 请求体和 SDK 重试；应为并发请求预留临时磁盘空间。S3 直传不占用应用服务器的这部分磁盘。

<a id="single-upload"></a>
## 单次上传与确认

以下片段中的 `service` 来自上述装配，`ctx` 是请求 context，`owner` 是通过认证取得的 `identity.Subject`，`body` 是上传正文 `io.Reader`：

```go
record, err := service.Prepare(ctx, owner, files.PrepareInput{
    Filename: "photo.jpg",
    ContentType: "image/jpeg",
    Size: size, // 请求声明的正文长度
})
if err != nil {
    return err
}
record, err = service.Put(ctx, owner, record.ID, body)
if err != nil {
    return err
}
record, err = service.Confirm(ctx, owner, record.ID)
if err != nil {
    return err
}
```

`Put` 会检查实际长度并设置 `UploadedAt`。文件此时仍为 `temporary`；业务确定使用该文件时调用 `Confirm`，复制到独立正式键后成为 `ready`。这样用户取消表单时，未确认文件可以到期回收。`Confirm` 可以重复调用，已确认文件不能再上传覆盖。

如果上传正文成功但收到数据库或网络错误，可以读取记录并重试相应步骤。单次上传也可调用 `Complete` 核验存储中已有对象，恢复“正文已到达但完成状态尚未保存”的情况。

<a id="multipart-upload"></a>
## 分片与断点续传

先调用 `Prepare`，再创建分片会话。下面的片段沿用上面的 `service`、`ctx`、`owner` 和 `record`；`partBody` 是当前分片正文，`number` 从 1 开始：

```go
upload, err := service.CreateMultipart(ctx, owner, record.ID)
if err != nil {
    return err
}
part, err := service.UploadPart(ctx, owner, record.ID, upload.UploadID, number, partBody)
if err != nil {
    return err
}
// 保存 part.Number 和 part.ETag；不同分片可以并发上传。
_ = part
```

每个文件同时只有一个有效会话；重复创建返回同一个 `UploadID`。首次 `Put`、`SignPut` 或 `CreateMultipart` 会把上传方式固定到 `files.upload_mode`，同一个文件不能混用单次与分片上传。取消分片后可以重新创建分片会话，改用单次上传则需要重新 `Prepare`。除末片外，正文必须正好为记录中的 `PartSize` 字节，末片大小由文件总大小计算。同号重传替换旧片，请使用最后一次成功上传的 ETag。

客户端恢复时，调用 `Get` 取得记录中的 `UploadID` 和 `PartSize`，再调用 `ListParts(ctx, owner, id, uploadID)` 查询存储端已存在的分片。客户端根据返回的编号补传缺片，不依赖服务进程内存或另一张会话表。

上传完成后，传入保存的编号和 ETag 清单；`parts` 为 `[]files.Part`，不必提交可信的 `Size`，服务会查询存储端核验实际大小：

```go
record, err = service.CompleteMultipart(ctx, owner, record.ID, upload.UploadID, parts)
if err != nil {
    return err
}
record, err = service.Confirm(ctx, owner, record.ID)
if err != nil {
    return err
}
```

完成会检查缺片、重复编号、ETag 和每片实际大小，再合并并核验总大小。同一会话完成后可重复调用 `CompleteMultipart`，确认后只要会话 ID 尚未被清理也可重试。合并成功但状态提交失败时，重试可通过已生成对象恢复。取消使用 `AbortMultipart(ctx, owner, id, uploadID)`；之后可以重新创建会话。旧请求携带旧 ID，不能修改新会话。

<a id="direct-upload"></a>
## S3 客户端直传

小文件使用 `Prepare → SignPut → 客户端上传 → Complete → Confirm`。大文件使用 `Prepare → CreateMultipart → SignPart → 客户端上传各片 → CompleteMultipart → Confirm`。

`SignPut(ctx, owner, id)`、`SignPart(ctx, owner, id, uploadID, number)` 返回 `SignedRequest`。客户端使用返回的 `Method`、`URL` 和签名请求头，按指定字节数发送正文；浏览器自动处理 `Host` 和 `Content-Length`，应用不能手工设置这些受限制的头。分片上传响应的 ETag 必须原样保存，包含引号。浏览器直传需要在桶的 CORS 设置中允许实际使用的来源、方法和请求头，并暴露 `ETag` 响应头。

本地存储没有预签名能力，签名方法返回 `ErrUnsupported`。本地请求由应用 HTTP 处理器接收正文，调用 `Put` 或 `UploadPart`。

预签名不能立即撤销。签发单次上传 URL 后，服务会拒绝为该文件创建分片会话；切换方式需要重新 `Prepare` 创建文件。文件确认后使用独立正式键，因此旧上传地址不会直接写入正式对象。签名下载通过 `SignGet(ctx, owner, id)` 提供，仅接受已确认文件。

<a id="reading-and-deleting"></a>
## 读取与删除

`Open(ctx, owner, id)` 返回 `io.ReadCloser` 和文件记录，调用方负责关闭流。你可以使用记录中的 `ContentType`、`Filename` 设置响应头，再把正文流式写到 HTTP 响应；文件名应通过标准库进行响应头编码。需要额外的租户、文章归属等权限时，在调用前执行应用自己的授权检查。

`Delete(ctx, owner, id)` 先把记录标为 `deleted` 停止读取，再删除正文和未完成会话。存储删除失败会返回错误，记录保持已删除状态，调用方可重试 `Delete`，也可等待 `Cleanup` 在回收时间到达后重试。重复删除同一主体的记录是幂等的，记录保留在数据库。其他主体查询或操作同一 ID 得到 `ErrNotFound`，用户和服务即使 ID 相同也分别校验。

应用可用 `errors.Is` 匹配 `ErrInvalid`、`ErrTooLarge`、`ErrConflict`、`ErrNotFound` 和 `ErrUnsupported`，按自己的 HTTP 错误约定映射；底层存储错误保留原始错误链。

<a id="cleanup"></a>
## 过期清理

应用通过已有调度器或显式命令定期调用 `service.Cleanup(ctx, limit)`。它最多处理 `limit` 条到期记录，返回成功处理数及合并的错误；失败记录保留回收时间，后续可以重试。候选按更新时间排序，失败时登记本次尝试时间，使后续调用能够继续处理其他过期文件。

- `temporary`：终止会话、删除对象并标记 `deleted`。
- `ready`：只回收原上传对象及会话，不删除正式文件。
- `deleted`：再次回收上传对象，处理旧直传地址在删除后重建内容的情况。

`ExpiresAt` 在确认后暂时保留，用于等待原上传会话到期；它不意味着正式文件会过期。清理成功后置空。请保留已登记的存储配置，直到该存储上的记录都已处理。

> [!NOTE]
> S3 会在请求开始时判断签名是否过期，已经开始的请求仍可能在数据库清理之后才写完，参见 [AWS 预签名有效期说明](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html)。请为 `uploads/` 前缀配置对象过期规则，并配置[未完成 multipart 的生命周期回收](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpu-abort-incomplete-mpu-lifecycle-config.html)，期限应长于应用允许的上传会话。未完成会话的规则应覆盖 `uploads/` 和 `objects/`，因为大文件确认会使用服务端分片复制；正式 `objects/` 对象不应套用临时对象过期规则。生命周期规则还能回收进程突然退出、尚未来得及登记到数据库的会话。数据库清理无法与 S3 在途请求构成原子事务。

<a id="schema-and-consistency"></a>
## 表结构和一致性

只有一张 `files` 表，没有上传会话表和分片表：

| 字段 | 含义 |
| --- | --- |
| `id` | Go 生成的 UUIDv7 |
| `disk / key` | 稳定存储名称和对象键，联合唯一 |
| `filename / content_type / size` | 原始信息和预期大小；完成时核验实际大小 |
| `owner_type / owner_id` | 字符串主体类型和 ID |
| `status` | `temporary / ready / deleted` |
| `upload_mode` | 首次操作固定为 `single` 或 `multipart`，防止两种上传方式混用 |
| `upload_id / part_size` | 当前分片会话和冻结的片大小；完成 ID 保留用于重试，清理后清空 |
| `uploaded_at` | 完成上传和大小核验的时间 |
| `expires_at` | 临时会话到期及上传对象回收时间 |
| `created_at / updated_at` | 文件记录的创建和更新时间；失败的清理尝试也更新 `updated_at` |

框架使用 Bun 模型列查询，应用可以增加列，不使用 `SELECT *` 扫描模型。主体没有物理外键，不要求应用使用某种用户表。修改迁移时应保留服务需要的字段、类型和约束。

服务用 PostgreSQL 行锁协调上传、确认、取消和清理；不同分片使用共享锁，可以并行写入。网络和文件操作会占用数据库事务和连接，因此应用应限制并发、设置请求期限，避免一个文件无限占用连接。S3 大文件优先使用客户端直传。

数据库提交和外部存储不是一个事务。服务提供幂等重试、完成恢复和过期清理，但进程崩溃仍可能留下尚未登记的对象或会话。本地孤立目录需要运维根据数据库和保留期限回收，S3 使用上述生命周期规则兜底。`Service` 不提供 `WithDB(tx)`，业务表写入失败时应由应用决定保留或删除已经确认的文件。
