package article

import "github.com/go-tenon/tenon/page"

type ListQuery struct {
	Q      string `query:"q" json:"q,omitempty"`
	Status string `query:"status" json:"status,omitempty" enum:"draft,published"`
	Sort   string `query:"sort" json:"sort,omitempty" default:"-created" enum:"-created,created,title,-title"`
	page.OffsetParams
}
