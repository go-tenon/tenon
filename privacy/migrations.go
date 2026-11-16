package privacy

import (
	"embed"
	"io/fs"
)

//go:embed migrations
var migrationFiles embed.FS

// Migrations 返回注销记录表 privacy_erasures 的迁移，供应用发布。
func Migrations() fs.FS {
	f, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return f
}
