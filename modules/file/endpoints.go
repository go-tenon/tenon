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

type createUploadInput struct {
	Body struct {
		Filename    string `json:"filename" minLength:"1" maxLength:"255"`
		ContentType string `json:"content_type,omitempty" maxLength:"255" doc:"文件的 MIME 类型；上传时的 Content-Type 头必须与它相同"`
		Size        int64  `json:"size" minimum:"1" doc:"文件字节数；上传的内容长度必须与它相同"`
	}
}

// UploadDTO 是客户端直传的上传请求。
type UploadDTO struct {
	URL       string            `json:"url"`
	Method    string            `json:"method" example:"PUT"`
	Headers   map[string]string `json:"headers" doc:"上传时必须带上的请求头"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type createUploadOutput struct {
	Body struct {
		File   FileDTO   `json:"file"`
		Upload UploadDTO `json:"upload"`
	}
}

// CreateUploadEndpoint 创建客户端直传：返回文件 ID 与预签名的上传请求。客户端按 upload 上传后，
// 调用 CompleteUploadEndpoint 完成。只在使用 S3 存储时可用，否则返回 501。
func CreateUploadEndpoint(s *Service, user CurrentUser) rest.Endpoint[createUploadInput, createUploadOutput] {
	return rest.Endpoint[createUploadInput, createUploadOutput]{
		Operation: huma.Operation{OperationID: "file-create-upload", Summary: "创建直传", Tags: []string{"文件"},
			DefaultStatus: http.StatusCreated, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *createUploadInput) (*createUploadOutput, error) {
			uid, ok := user(ctx)
			if !ok {
				return nil, huma.Error401Unauthorized("未登录")
			}
			f, up, err := s.CreateUpload(ctx, uid, in.Body.Filename, in.Body.ContentType, in.Body.Size)
			if err != nil {
				return nil, mapError(err)
			}
			out := &createUploadOutput{}
			out.Body.File = s.DTO(f)
			out.Body.Upload = UploadDTO{URL: up.URL, Method: up.Method, Headers: up.Header, ExpiresAt: up.ExpiresAt}
			return out, nil
		},
	}
}

type completeUploadInput struct {
	ID string `path:"id" format:"uuid"`
}

// CompleteUploadEndpoint 完成客户端直传，返回文件信息。
func CompleteUploadEndpoint(s *Service, user CurrentUser) rest.Endpoint[completeUploadInput, uploadOutput] {
	return rest.Endpoint[completeUploadInput, uploadOutput]{
		Operation: huma.Operation{OperationID: "file-complete-upload", Summary: "完成直传", Tags: []string{"文件"},
			Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *completeUploadInput) (*uploadOutput, error) {
			uid, ok := user(ctx)
			if !ok {
				return nil, huma.Error401Unauthorized("未登录")
			}
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, mapError(ErrNotFound)
			}
			f, err := s.CompleteUpload(ctx, uid, id)
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
	case errors.Is(err, ErrNoDirectUpload):
		return huma.NewError(http.StatusNotImplemented, "当前存储不支持直传")
	case errors.Is(err, ErrUploadIncomplete):
		return huma.Error422UnprocessableEntity("文件未上传或与声明的大小不符")
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
