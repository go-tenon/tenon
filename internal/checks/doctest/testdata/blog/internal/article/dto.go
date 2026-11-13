package article

import "time"

type ArticleDTO struct {
	ID          string     `json:"id" example:"0193a5c2-7d1e-7b3a-9f61-2c4e8d0a1b2c"`
	Title       string     `json:"title" example:"Hello World"`
	Slug        string     `json:"slug" example:"hello-world"`
	AuthorID    string     `json:"author_id" example:"alice"`
	Status      string     `json:"status" enum:"draft,published" example:"published"`
	PublishedAt *time.Time `json:"published_at,omitempty" doc:"发布时间，草稿没有这个字段" example:"2026-11-10T08:30:00Z"`
}

type ArticleOutput struct {
	Body ArticleDTO
}

func toDTO(a *Article) ArticleDTO {
	return ArticleDTO{
		ID: a.ID.String(), Title: a.Title, Slug: a.Slug,
		AuthorID: a.AuthorID, Status: a.Status, PublishedAt: a.PublishedAt,
	}
}
