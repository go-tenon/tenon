// Package article 是 clientgen 测试用的示例模块：一个保存在内存中的文章接口。
package article

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/page"
)

// Status 是文章的状态。
type Status string

const (
	// StatusDraft 是还没有发布的草稿。
	StatusDraft Status = "draft"
	// StatusPublished 是已经发布的文章。
	StatusPublished Status = "published"
)

// Statuses 是文章的全部状态，显示文字是 article.status.<状态> 的消息。
var Statuses = enum.New("article.status", StatusDraft, StatusPublished)

// Schema 让 OpenAPI 中的 Status 引用命名类型 ArticleStatus。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }

var (
	// ErrNotFound 表示文章不存在。
	ErrNotFound = errorx.Define(errorx.NotFound, "article.not_found")
	// ErrSlugTaken 表示 slug 已被其他文章使用。
	ErrSlugTaken = errorx.Define(errorx.Conflict, "article.slug_taken")
	errInternal  = errorx.Define(errorx.Internal, "article.internal")
)

// Changes 是创建和修改文章时提交的内容。
type Changes struct {
	Title string   `json:"title" minLength:"1" maxLength:"200"` // 标题。
	Slug  string   `json:"slug" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$"`
	Tags  []string `json:"tags,omitempty" required:"false"` // 标签，可以为空。

	Draft bool   `json:"-"`
	notes string // 未导出的字段不复制
}

// Notes 返回备注，让未导出的字段有用处。
func (c Changes) Notes() string { return c.notes }

// ArticleDTO 是返回给客户端的文章。
type ArticleDTO struct {
	ID          uuid.UUID      `json:"id"`
	Title       string         `json:"title"`
	Slug        string         `json:"slug"`
	Status      Status         `json:"status"`
	Tags        []string       `json:"tags"`
	Author      Author         `json:"author"`
	PublishedAt *time.Time     `json:"published_at,omitempty" doc:"发布时间，草稿没有这个字段"`
	Meta        map[string]any `json:"meta,omitempty"`
	Featured    bool           `json:"featured"`
}

// Author 是文章的作者。
type Author struct {
	Name string `json:"name"`
}

// ArticleOutput 是返回一篇文章的响应。
type ArticleOutput struct {
	Body ArticleDTO
}

// CreatedOutput 是创建文章的响应。
type CreatedOutput struct {
	Location string `header:"Location"`
	Body     ArticleDTO
}

// CreateInput 是创建文章的请求。
type CreateInput struct {
	Body Changes
}

// IDInput 是按 ID 操作文章的请求。
type IDInput struct {
	ID uuid.UUID `path:"id"`
}

// UpdateInput 是修改文章的请求。
type UpdateInput struct {
	ID   uuid.UUID `path:"id"`
	Body Changes
}

// PublishInput 是发布文章的参数。
type PublishInput struct {
	ID             uuid.UUID `path:"id"`
	IdempotencyKey string    `header:"Idempotency-Key"` // 让重试只发布一次。
}

// ListInput 是文章列表的筛选条件。
type ListInput struct {
	page.OffsetParams
	Status Status   `query:"status"`
	Tags   []string `query:"tag,explode"`
	Title  string   `query:"title" doc:"标题包含的文字"`
}

// ListOutput 是文章列表的响应。
type ListOutput struct {
	Body page.Offset[ArticleDTO]
}

// FeatureInput 设置文章是否精选，请求体是匿名结构体。
type FeatureInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Featured bool `json:"featured"`
	}
}

// EventsInput 订阅文章的变化。
type EventsInput struct {
	Tag string `query:"tag"`
}

// CoverInput 上传文章的封面和附图。
type CoverInput struct {
	ID      uuid.UUID `path:"id"`
	RawBody huma.MultipartFormFiles[struct {
		File   huma.FormFile   `form:"file" contentType:"image/png,image/jpeg" required:"true"`
		Extras []huma.FormFile `form:"extras"`
		Alt    string          `form:"alt"`
	}]
}

// Cover 是上传的封面。
type Cover struct {
	Filename    string   `json:"filename"`
	ContentType string   `json:"content_type"`
	Size        int64    `json:"size"`
	Alt         string   `json:"alt"`
	Extras      []string `json:"extras"`
}

// CoverOutput 是上传封面的响应。
type CoverOutput struct {
	Body Cover
}

// PingOutput 是 ping 的响应。
type PingOutput struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

// BlogSettings 是博客的设置，保存在 Store 中。
type BlogSettings struct {
	Name          string `json:"name" minLength:"1" maxLength:"60"`
	Comments      bool   `json:"comments"`
	DefaultStatus Status `json:"default_status"`
}

// BlogSettingsInput 是保存设置的请求。
type BlogSettingsInput struct {
	Body BlogSettings
}

// BlogSettingsOutput 是设置。
type BlogSettingsOutput struct {
	Body BlogSettings
}

// Store 把文章和设置保存在内存中。
type Store struct {
	mu       sync.Mutex
	articles []ArticleDTO
	keys     map[string]bool
	settings BlogSettings
}

// NewStore 创建空的 Store。
func NewStore() *Store {
	return &Store{keys: map[string]bool{}, settings: BlogSettings{Name: "博客", DefaultStatus: StatusDraft}}
}

// getSettings 返回博客设置。
func (s *Store) getSettings(context.Context, *struct{}) (*BlogSettingsOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &BlogSettingsOutput{Body: s.settings}, nil
}

// updateSettings 保存博客设置。
func (s *Store) updateSettings(_ context.Context, in *BlogSettingsInput) (*BlogSettingsOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = in.Body
	return &BlogSettingsOutput{Body: s.settings}, nil
}

// Register 登记示例接口；auth 保护需要登录的接口。
func Register(api *rest.API, s *Store, auth func(*huma.Operation)) {
	op := func(id, method, path, summary string, protected bool) huma.Operation {
		o := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		if protected {
			auth(&o)
		} else {
			rest.Public()(&o)
		}
		return o
	}
	create := op("article-create", http.MethodPost, "/articles", "创建文章", true)
	create.DefaultStatus = http.StatusCreated
	rest.Register(api, create, s.create)
	rest.Register(api, op("article-get", http.MethodGet, "/articles/{id}", "获取文章", false), s.get)
	rest.Register(api, op("article-update", http.MethodPut, "/articles/{id}", "修改文章", true), s.update)
	rest.Register(api, op("article-delete", http.MethodDelete, "/articles/{id}", "删除文章", true), s.delete)
	rest.Register(api, op("article-publish", http.MethodPost, "/articles/{id}/publish", "发布文章", true), s.publish)
	rest.Register(api, op("article-feature", http.MethodPut, "/articles/{id}/feature", "设置精选", true), s.feature)
	rest.Register(api, op("article-list", http.MethodGet, "/articles", "文章列表", false), s.list)
	rest.RegisterSSE(api, op("article-events", http.MethodGet, "/articles/events", "文章变化", true),
		map[string]any{"changed": ArticleDTO{}, "deleted": uuid.UUID{}, "message": ""}, s.events)
	rest.Register(api, op("article-cover", http.MethodPost, "/articles/{id}/cover", "上传封面", true), s.cover)
	rest.Register(api, op("ping", http.MethodGet, "/ping", "", false), ping)
	rest.Register(api, op("article-preview", http.MethodPost, "/articles/preview", "预览链接", false), preview)
	// 文章的后台页面：显示文字是 article.title、article.fields.<字段>、article.actions.<操作> 的消息。
	api.Resource(rest.Resource{
		Name: "article", List: "article-list", Get: "article-get", Create: "article-create", Update: "article-update", Delete: "article-delete",
		Columns: []string{"title", "status", "published_at", "featured"}, Filters: []string{"title", "status"},
		Actions: []rest.Action{{Name: "publish", Operation: "article-publish", Confirm: true, When: map[string][]string{"status": {string(StatusDraft)}}}},
	})
	// 博客的设置页面：显示文字是 blog.title、blog.fields.<字段> 的消息。
	rest.Register(api, op("blog-settings-get", http.MethodGet, "/settings", "博客设置", true), s.getSettings)
	rest.Register(api, op("blog-settings-update", http.MethodPut, "/settings", "保存博客设置", true), s.updateSettings)
	api.Settings(rest.Settings{Name: "blog", Get: "blog-settings-get", Update: "blog-settings-update"})
}

// PreviewInput 记录预览者的浏览器：User-Agent 由浏览器设置，TS 客户端不把它作为参数。
type PreviewInput struct {
	UserAgent string `header:"User-Agent"`
}

// PreviewOutput 把预览凭据写入 cookie：浏览器中的脚本读不到 Set-Cookie，TS 客户端只返回响应体。
type PreviewOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
	Body      struct {
		URL string `json:"url"`
	}
}

// preview 返回预览地址，设置预览 cookie。
func preview(_ context.Context, in *PreviewInput) (*PreviewOutput, error) {
	out := &PreviewOutput{SetCookie: &http.Cookie{Name: "preview", Value: in.UserAgent, Path: "/articles", HttpOnly: true}}
	out.Body.URL = "/articles/preview"
	return out, nil
}

// ping 返回 ok。
func ping(context.Context, *struct{}) (*PingOutput, error) {
	out := &PingOutput{}
	out.Body.OK = true
	return out, nil
}

// create 创建文章；slug 已存在时返回冲突。
func (s *Store) create(_ context.Context, in *CreateInput) (*CreatedOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.find(in.Body.Slug) >= 0 {
		return nil, ErrSlugTaken.New(errorx.Public("slug", in.Body.Slug))
	}
	a := ArticleDTO{ID: uuid.New(), Title: in.Body.Title, Slug: in.Body.Slug, Status: StatusDraft,
		Tags: append([]string{}, in.Body.Tags...), Author: Author{Name: "alice"}}
	s.articles = append(s.articles, a)
	return &CreatedOutput{Location: "/articles/" + a.ID.String(), Body: a}, nil
}

// get 返回一篇文章。
func (s *Store) get(_ context.Context, in *IDInput) (*ArticleOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.index(in.ID)
	if err != nil {
		return nil, err
	}
	return &ArticleOutput{Body: s.articles[i]}, nil
}

// update 修改一篇文章。
func (s *Store) update(_ context.Context, in *UpdateInput) (*ArticleOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.index(in.ID)
	if err != nil {
		return nil, err
	}
	if j := s.find(in.Body.Slug); j >= 0 && j != i {
		return nil, ErrSlugTaken.New()
	}
	a := &s.articles[i]
	a.Title, a.Slug, a.Tags = in.Body.Title, in.Body.Slug, append([]string{}, in.Body.Tags...)
	return &ArticleOutput{Body: *a}, nil
}

// delete 删除一篇文章。
func (s *Store) delete(_ context.Context, in *IDInput) (*struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.index(in.ID)
	if err != nil {
		return nil, err
	}
	s.articles = slices.Delete(s.articles, i, i+1)
	return &struct{}{}, nil
}

// publish 发布一篇文章。
func (s *Store) publish(_ context.Context, in *PublishInput) (*ArticleOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.index(in.ID)
	if err != nil {
		return nil, err
	}
	a := &s.articles[i]
	if in.IdempotencyKey != "" && s.keys[in.IdempotencyKey] {
		return &ArticleOutput{Body: *a}, nil
	}
	s.keys[in.IdempotencyKey] = true
	if a.Status == StatusPublished {
		return nil, errInternal.New()
	}
	now := time.Date(2026, 11, 13, 8, 30, 0, 0, time.UTC)
	a.Status, a.PublishedAt = StatusPublished, &now
	return &ArticleOutput{Body: *a}, nil
}

// feature 设置文章是否推荐。
func (s *Store) feature(_ context.Context, in *FeatureInput) (*ArticleOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.index(in.ID)
	if err != nil {
		return nil, err
	}
	s.articles[i].Featured = in.Body.Featured
	return &ArticleOutput{Body: s.articles[i]}, nil
}

// list 按条件列出文章，分页返回。
func (s *Store) list(_ context.Context, in *ListInput) (*ListOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var matched []ArticleDTO
	for _, a := range s.articles {
		if in.Status != "" && a.Status != in.Status {
			continue
		}
		if in.Title != "" && !strings.Contains(a.Title, in.Title) {
			continue
		}
		if !hasAll(a.Tags, in.Tags) {
			continue
		}
		matched = append(matched, a)
	}
	from := min(int(in.Offset()), len(matched))
	to := min(from+int(in.Limit()), len(matched))
	return &ListOutput{Body: page.NewOffset(matched[from:to], in.OffsetParams, int64(len(matched)))}, nil
}

// events 发出固定的几条事件：Last-Event-ID 之前的跳过，future 是客户端不认识的事件，最后以 [DONE] 结束。
func (s *Store) events(_ context.Context, in *EventsInput) (*huma.StreamResponse, error) {
	if in.Tag == "missing" {
		return nil, ErrNotFound.New()
	}
	id := uuid.MustParse("0193a5c2-7d1e-7b3a-9f61-2c4e8d0a1b2c")
	events := []struct {
		name string
		data any
	}{
		{"changed", ArticleDTO{ID: id, Title: "Hello", Status: StatusPublished, Tags: []string{}}},
		{"future", map[string]int{"n": 1}},
		{"deleted", id},
		{"", "multi\nline"},
	}
	return rest.SSE(func(st *httpx.Stream) error {
		after := 0
		if v := st.LastEventID(); v != "" {
			after, _ = strconv.Atoi(v)
		}
		for i, ev := range events {
			if i+1 <= after {
				continue
			}
			if err := st.SendJSON(httpx.Event{Name: ev.name, ID: strconv.Itoa(i + 1)}, ev.data); err != nil {
				return err
			}
		}
		return st.Send(httpx.Event{}, "[DONE]")
	}), nil
}

// cover 保存上传的封面。
func (s *Store) cover(_ context.Context, in *CoverInput) (*CoverOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.index(in.ID); err != nil {
		return nil, err
	}
	form := in.RawBody.Data()
	data, err := io.ReadAll(form.File)
	if err != nil {
		return nil, err
	}
	c := Cover{Filename: form.File.Filename, ContentType: form.File.ContentType, Size: int64(len(data)), Alt: form.Alt, Extras: []string{}}
	for _, f := range form.Extras {
		c.Extras = append(c.Extras, f.Filename)
	}
	return &CoverOutput{Body: c}, nil
}

// find 返回 slug 对应的文章的下标，没有时返回 -1。
func (s *Store) find(slug string) int {
	return slices.IndexFunc(s.articles, func(a ArticleDTO) bool { return a.Slug == slug })
}

// index 返回 ID 对应的文章的下标，没有时返回 ErrNotFound。
func (s *Store) index(id uuid.UUID) (int, error) {
	if i := slices.IndexFunc(s.articles, func(a ArticleDTO) bool { return a.ID == id }); i >= 0 {
		return i, nil
	}
	return -1, ErrNotFound.New()
}

// hasAll 报告 have 是否包含 want 中的每一项。
func hasAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}
