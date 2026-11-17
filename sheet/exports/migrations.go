package exports

import (
	"embed"
	"io/fs"
)

//go:embed migrations
var migrationFiles embed.FS

// Migrations 返回导出表 exports 的迁移，供应用发布。
// 它引用 operation 包的 operations 表，迁移时间晚于 operation 的迁移，需要一起发布。
func Migrations() fs.FS {
	f, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return f
}
