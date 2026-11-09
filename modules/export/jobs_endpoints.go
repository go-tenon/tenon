package export

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/httpx/web"
)

// 异步导出的接口，user 返回当前用户 ID（如 account.CurrentUserID）：
//
//	rest.Post(v1, "/exports", export.StartEndpoint(jobs, account.CurrentUserID))
//	rest.Get(v1, "/exports", export.JobsEndpoint(jobs, account.CurrentUserID))
//	rest.Get(v1, "/exports/{id}", export.JobEndpoint(jobs, account.CurrentUserID))
//	a.Router.Get("/exports/{id}/download", jobs.DownloadHandler(account.CurrentUserID))
//
// 下载地址为 Jobs.URLPrefix + 任务 ID + "/download"，URLPrefix 默认 "/exports/"，与上例的挂载路径一致。

// CurrentUser 返回当前登录用户的 ID，例如 account.CurrentUserID。
type CurrentUser func(ctx context.Context) (uuid.UUID, bool)

var jobTags = []string{"导出"}

// JobDTO 是返回给客户端的导出任务。
type JobDTO struct {
	ID          string    `json:"id" format:"uuid"`
	Kind        string    `json:"kind"`
	Format      string    `json:"format" enum:"csv,xlsx"`
	Status      string    `json:"status" enum:"pending,running,done,failed"`
	Filename    string    `json:"filename"`
	Size        int64     `json:"size,omitempty"`
	Error       string    `json:"error,omitempty"`
	DownloadURL string    `json:"download_url,omitempty" doc:"完成后的下载地址"`
	CreatedAt   time.Time `json:"created_at"`
	FinishedAt  time.Time `json:"finished_at,omitzero"`
	ExpiresAt   time.Time `json:"expires_at" doc:"文件保留到此时间"`
}

// DTO 返回 job 的 JobDTO。
func (j *Jobs) DTO(job *Job) JobDTO {
	return JobDTO{ID: job.ID.String(), Kind: job.Kind, Format: string(job.Format), Status: job.Status,
		Filename: job.Filename, Size: job.Size, Error: job.Error, DownloadURL: j.URL(job),
		CreatedAt: job.CreatedAt, FinishedAt: job.FinishedAt, ExpiresAt: job.ExpiresAt}
}

func currentUser(ctx context.Context, user CurrentUser) (uuid.UUID, error) {
	id, ok := user(ctx)
	if !ok {
		return id, huma.Error401Unauthorized("未登录")
	}
	return id, nil
}

func mapJobError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound("导出任务不存在")
	case errors.Is(err, ErrUnknownKind):
		return huma.Error422UnprocessableEntity("不支持的导出类型")
	}
	return err
}

type startInput struct {
	Body struct {
		Kind   string          `json:"kind" maxLength:"64" doc:"导出类型"`
		Format string          `json:"format,omitempty" enum:"csv,xlsx" default:"xlsx"`
		Params json.RawMessage `json:"params,omitempty" doc:"导出参数（如筛选条件），由导出类型决定"`
	}
}

type jobOutput struct{ Body JobDTO }

// StartEndpoint 创建异步导出任务，返回 202 与任务信息；客户端轮询 JobEndpoint 直到 status 为 done。
func StartEndpoint(j *Jobs, user CurrentUser) rest.Endpoint[startInput, jobOutput] {
	return rest.Endpoint[startInput, jobOutput]{
		Operation: huma.Operation{OperationID: "export-start", Summary: "创建导出任务", Tags: jobTags,
			DefaultStatus: http.StatusAccepted, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *startInput) (*jobOutput, error) {
			uid, err := currentUser(ctx, user)
			if err != nil {
				return nil, err
			}
			format, err := ParseFormat(in.Body.Format)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("不支持的格式")
			}
			var params any
			if len(in.Body.Params) > 0 {
				params = in.Body.Params
			}
			job, err := j.Start(ctx, uid, in.Body.Kind, format, params)
			if err != nil {
				return nil, mapJobError(err)
			}
			return &jobOutput{Body: j.DTO(job)}, nil
		},
	}
}

type jobInput struct {
	ID string `path:"id" format:"uuid"`
}

// JobEndpoint 返回导出任务的状态。
func JobEndpoint(j *Jobs, user CurrentUser) rest.Endpoint[jobInput, jobOutput] {
	return rest.Endpoint[jobInput, jobOutput]{
		Operation: huma.Operation{OperationID: "export-get", Summary: "导出任务", Tags: jobTags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *jobInput) (*jobOutput, error) {
			uid, err := currentUser(ctx, user)
			if err != nil {
				return nil, err
			}
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, mapJobError(ErrNotFound)
			}
			job, err := j.Get(ctx, uid, id)
			if err != nil {
				return nil, mapJobError(err)
			}
			return &jobOutput{Body: j.DTO(job)}, nil
		},
	}
}

type jobsOutput struct{ Body []JobDTO }

// JobsEndpoint 列出当前用户最近 20 个导出任务。
func JobsEndpoint(j *Jobs, user CurrentUser) rest.Endpoint[struct{}, jobsOutput] {
	return rest.Endpoint[struct{}, jobsOutput]{
		Operation: huma.Operation{OperationID: "export-list", Summary: "导出任务列表", Tags: jobTags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*jobsOutput, error) {
			uid, err := currentUser(ctx, user)
			if err != nil {
				return nil, err
			}
			list, err := j.List(ctx, uid, 20)
			if err != nil {
				return nil, err
			}
			out := &jobsOutput{Body: make([]JobDTO, 0, len(list))}
			for i := range list {
				out.Body = append(out.Body, j.DTO(&list[i]))
			}
			return out, nil
		},
	}
}

// DownloadHandler 返回下载导出文件的 handler，路由中必须包含 {id}；只有任务的创建者可以下载。
func (j *Jobs) DownloadHandler(user CurrentUser) http.Handler {
	return web.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		uid, ok := user(r.Context())
		if !ok {
			return huma.Error404NotFound("导出任务不存在")
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			return mapJobError(ErrNotFound)
		}
		job, err := j.Get(r.Context(), uid, id)
		if err == nil {
			err = j.Serve(w, r, job)
		}
		return mapJobError(err)
	})
}
