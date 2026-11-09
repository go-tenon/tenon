package file

import (
	"context"
	"errors"
	"net/http"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/httpx/web"
)

// CurrentUser 返回当前登录用户的 ID，例如 account.CurrentUserID。
type CurrentUser func(ctx context.Context) (uuid.UUID, bool)

// FileDTO 是返回给客户端的文件信息。
type FileDTO struct {
	ID          string    `json:"id" format:"uuid"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	URL         string    `json:"url" doc:"下载地址"`
	CreatedAt   time.Time `json:"created_at"`
}

// DTO 返回 f 的 FileDTO。
func (s *Service) DTO(f *File) FileDTO {
	return FileDTO{ID: f.ID.String(), Filename: f.Filename, ContentType: f.ContentType, Size: f.Size, URL: s.URL(f), CreatedAt: f.CreatedAt}
}

type uploadInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true" doc:"要上传的文件"`
	}]
}

type uploadOutput struct{ Body FileDTO }

// UploadEndpoint 上传一个文件（multipart/form-data，字段名 file），上传者为当前用户。
func UploadEndpoint(s *Service, user CurrentUser) rest.Endpoint[uploadInput, uploadOutput] {
	return rest.Endpoint[uploadInput, uploadOutput]{
		Operation: huma.Operation{
			OperationID:   "file-upload",
			Summary:       "上传文件",
			Tags:          []string{"文件"},
			DefaultStatus: http.StatusCreated,
			Security:      rest.RequireAuth(),
			MaxBodyBytes:  s.cfg.MaxSize + 1<<20, // 留出 multipart 头部的余量，大小以 Upload 的判断为准
		},
		Handler: func(ctx context.Context, in *uploadInput) (*uploadOutput, error) {
			uid, ok := user(ctx)
			if !ok {
				return nil, huma.Error401Unauthorized("未登录")
			}
			ff := in.RawBody.Data().File
			defer ff.Close()
			f, err := s.Upload(ctx, uid, ff.Filename, ff)
			if err != nil {
				return nil, mapError(err)
			}
			return &uploadOutput{Body: s.DTO(f)}, nil
		},
	}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, ErrTooLarge):
		return huma.Error413RequestEntityTooLarge("文件太大")
	case errors.Is(err, ErrTypeNotAllowed):
		return huma.Error415UnsupportedMediaType("不支持的文件类型")
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound("文件不存在")
	}
	return err
}

// OwnerOnly 只允许上传者访问文件。
func OwnerOnly(user CurrentUser) func(r *http.Request, f *File) bool {
	return func(r *http.Request, f *File) bool {
		uid, ok := user(r.Context())
		return ok && uid == f.UserID
	}
}

// DownloadHandler 返回下载文件的 handler，路由中必须包含 {id}：
//
//	a.Router.Get("/files/{id}", files.DownloadHandler(file.OwnerOnly(account.CurrentUserID)))
//
// canRead 返回 false 时响应 404（不暴露文件是否存在）。
func (s *Service) DownloadHandler(canRead func(r *http.Request, f *File) bool) http.Handler {
	return web.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			return huma.Error404NotFound("文件不存在")
		}
		f, err := s.Get(r.Context(), id)
		if err == nil && !canRead(r, f) {
			err = ErrNotFound
		}
		if err == nil {
			err = s.Serve(w, r, f)
		}
		return mapError(err)
	})
}
