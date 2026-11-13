package auth

import "github.com/go-tenon/tenon/authz"

const (
	ArticlesRead       = "articles:read"
	ArticlesReadDrafts = "articles:read-drafts"
	ArticlesWrite      = "articles:write"
	ArticlesAdmin      = "articles:admin"
	CommentsDelete     = "comments:delete"
	MembersManage      = "members:manage"
)

var Permissions = authz.Catalog{
	{Name: ArticlesRead, Description: "查看文章"},
	{Name: ArticlesReadDrafts, Description: "查看所有人的草稿"},
	{Name: ArticlesWrite, Description: "撰写和修改自己的文章"},
	{Name: ArticlesAdmin, Description: "修改和发布任何人的文章"},
	{Name: CommentsDelete, Description: "删除评论"},
	{Name: MembersManage, Description: "管理成员"},
}
