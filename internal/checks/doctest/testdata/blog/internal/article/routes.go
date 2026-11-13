package article

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"
)

// Register 注册文章模块的全部路由。
func Register(api *rest.API, requireArticles func(*huma.Operation), db *bun.DB) {
	create := huma.Operation{
		OperationID:   "article-create",
		Method:        http.MethodPost,
		Path:          "/articles",
		Summary:       "创建文章",
		DefaultStatus: http.StatusCreated,
	}
	requireArticles(&create)
	rest.Register(api, create, NewCreate(db).HTTP)

	get := huma.Operation{
		OperationID: "article-get",
		Method:      http.MethodGet,
		Path:        "/articles/{id}",
		Summary:     "获取文章",
	}
	requireArticles(&get)
	rest.Register(api, get, NewGet(db).HTTP)

	publish := huma.Operation{
		OperationID: "article-publish",
		Method:      http.MethodPost,
		Path:        "/articles/{id}/publish",
		Summary:     "发布文章",
	}
	requireArticles(&publish)
	rest.Register(api, publish, NewPublish(db).HTTP)
}
