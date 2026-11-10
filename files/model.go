package files

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const (
	StatusTemporary     = "temporary"
	StatusReady         = "ready"
	StatusDeleted       = "deleted"
	UploadModeSingle    = "single"
	UploadModeMultipart = "multipart"
)

// Record 是 files 表模型。应用可以加列，框架查询仅使用此模型声明的列。
type Record struct {
	bun.BaseModel `bun:"table:files,alias:f"`
	ID            uuid.UUID  `bun:"id,pk,type:uuid"`
	Disk          string     `bun:"disk,notnull"`
	Key           string     `bun:"key,notnull"`
	Filename      string     `bun:"filename,notnull"`
	ContentType   string     `bun:"content_type,notnull"`
	Size          int64      `bun:"size,notnull"` // 预期大小；上传完成时必须与实际大小一致
	OwnerType     string     `bun:"owner_type,notnull"`
	OwnerID       string     `bun:"owner_id,notnull"`
	Status        string     `bun:"status,notnull"`
	UploadMode    string     `bun:"upload_mode,notnull"` // 首次上传后冻结，single 或 multipart
	UploadID      string     `bun:"upload_id,nullzero"`  // 当前或已完成的分片会话，清理后为空
	PartSize      int64      `bun:"part_size,notnull"`   // 创建文件时冻结，配置变化不影响续传
	UploadedAt    *time.Time `bun:"uploaded_at"`         // 已完成上传并核验，仍需 Confirm 才能使用
	ExpiresAt     *time.Time `bun:"expires_at"`          // 会话到期和临时对象回收时间，ready 文件本身不会过期
	CreatedAt     time.Time  `bun:"created_at,notnull"`
	UpdatedAt     time.Time  `bun:"updated_at,notnull"`
}

// PrepareInput 是新上传的文件信息。Disk 为空时使用默认存储。
type PrepareInput struct {
	Disk        string
	Filename    string
	ContentType string
	Size        int64
}
