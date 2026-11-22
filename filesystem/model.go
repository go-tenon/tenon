package filesystem

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// 文件记录的状态，即 files 表的 status 列。
const (
	StatusTemporary = "temporary" // 已登记，等待上传或业务确认；过期后由 Cleanup 回收
	StatusReady     = "ready"     // 业务已确认使用，长期保留
	StatusDeleted   = "deleted"   // 已删除，等待清理对象后删除记录
)

// Meta 是文件内容的固有元数据，在 Confirm 时按文件类型自动提取并保存到 files.meta。
type Meta struct {
	Width  int `json:"width,omitempty"`  // 图片宽度（像素）
	Height int `json:"height,omitempty"` // 图片高度（像素）
}

// File 是 files 表模型。应用可以加列，框架只读写这里声明的列。
// URL 不入库，服务返回记录时按磁盘公开基址填写。
type File struct {
	bun.BaseModel `bun:"table:files,alias:f"`
	ID            uuid.UUID `bun:"id,pk,type:uuid"      json:"id"`
	database.Timestamps
	Disk        string               `bun:"disk,notnull"         json:"disk"`
	Key         string               `bun:"key,notnull"          json:"key"`
	Filename    string               `bun:"filename,notnull"     json:"filename"`
	ContentType string               `bun:"content_type,notnull" json:"content_type"`
	Size        int64                `bun:"size,notnull"         json:"size"`
	Meta        Meta                 `bun:"meta,type:jsonb,notnull,default:'{}'" json:"meta"`
	OwnerType   identity.SubjectType `bun:"owner_type,notnull"   json:"-"`
	OwnerID     string               `bun:"owner_id,notnull"     json:"-"`
	Status      string               `bun:"status,notnull"       json:"status"`
	ExpiresAt   *time.Time           `bun:"expires_at"           json:"expires_at,omitempty"`
	URL         string               `bun:"-"                    json:"url"`
}

// FileInput 描述一个待上传文件。Disk 为空时使用默认磁盘；Size 是声明大小，上传时严格核对。
type FileInput struct {
	Disk        string
	Filename    string
	ContentType string
	Size        int64
}

// Accept 描述一种用途接受的文件，由 Confirm 检查。零值只要求文件已完整上传。
type Accept struct {
	Types        []string // 允许的 MIME 类型，例如 "image/png"，或用 "image/*" 匹配一类。非空时按文件内容识别真实类型（图片、PDF、压缩包、Office 文档及纯文本等），它必须在 Types 中，且与文件扩展名和上传时声明的 MIME 类型兼容。
	MaxSize      ByteSize // 允许的最大字节数，0 表示只受 max_upload_size 限制。
	MaxDimension int      // 图片允许的最大单边像素数，0 表示不限；大于 0 时图片必须能解析出合法宽高且不超过此值。
}

// Multipart 是分片直传会话。除最后一片外，每片正好 PartSize 字节。
type Multipart struct {
	UploadID  string `json:"upload_id"`
	PartSize  int64  `json:"part_size"`
	PartCount int    `json:"part_count"`
}
