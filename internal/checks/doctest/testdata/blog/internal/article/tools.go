package article

import (
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/mcpx"
)

// Tools 登记文章模块的 MCP 工具。
func Tools(srv *mcpx.Server, db *bun.DB) {
	mcpx.Register(srv, mcpx.Tool{
		Name:        "article_search",
		Title:       "搜索文章",
		Description: "按关键词搜索已发布的文章，返回标题、作者和命中的片段。不传关键词时按发布时间从新到旧列出。",
		Permissions: []string{"articles:read"},
		ReadOnly:    true,
	}, NewSearch(db).Tool)

	mcpx.Register(srv, mcpx.Tool{
		Name:        "article_publish",
		Title:       "发布文章",
		Description: "发布一篇草稿，发布后读者立即可见。只能发布自己的文章，已经发布的文章不能再发布。",
		Permissions: []string{"articles:write"},
	}, NewPublish(db).Tool)
}
