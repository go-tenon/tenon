// Package migrations 打包本服务的全部迁移，包括从 go-kit 发布过来的迁移。
//
//	go run . migrate create NAME   新建迁移
//	go run . migrate publish       发布 go-kit 模块的新迁移
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
