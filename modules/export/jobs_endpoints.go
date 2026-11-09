package export

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/i18n"
)

// 异步导出的接口，当前用户取自 auth.UserID：
//
//	rest.Post(v1, "/exports", export.StartEndpoint(jobs))
//	rest.Get(v1, "/exports", export.JobsEndpoint(jobs))
//	rest.Get(v1, "/exports/{id}", export.JobEndpoint(jobs))
//	a.Router.Get("/exports/{id}/download", jobs.DownloadHandler().ServeHTTP)
//
// 下载地址为 JobsConfig.URLPrefix + 任务 ID + "/download"，URLPrefix 默认 "/exports/"，与上例的挂载路径一致。

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

// JobDTO 返回 job 的 JobDTO，错误信息按 ctx 的语言翻译。
func (j *Jobs) JobDTO(ctx context.Context, job *Job) JobDTO {
	return JobDTO{ID: job.ID.String(), Kind: job.Kind, Format: string(job.Format), Status: job.Status,
		Filename: job.Filename, Size: job.Size, Error: translate(ctx, job.Error), DownloadURL: j.URL(job),
		CreatedAt: job.CreatedAt, FinishedAt: job.FinishedAt, ExpiresAt: job.ExpiresAt}
}

func currentUser(ctx context.Context) (uuid.UUID, error) {
	id, ok := auth.UserID(ctx)
	if !ok {
		return id, auth.ErrUnauthenticated
	}
	return id, nil
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
func StartEndpoint(j *Jobs) rest.Endpoint[startInput, jobOutput] {
	return rest.Endpoint[startInput, jobOutput]{
		Operation: huma.Operation{OperationID: "export-start", Summary: "创建导出任务", Tags: jobTags,
			DefaultStatus: http.StatusAccepted, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *startInput) (*jobOutput, error) {
			uid, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			format, err := ParseFormat(in.Body.Format)
			if err != nil {
				return nil, err
			}
			var params any
			if len(in.Body.Params) > 0 {
				params = in.Body.Params
			}
			job, err := j.Start(ctx, uid, in.Body.Kind, format, params)
			if err != nil {
				return nil, err
			}
			return &jobOutput{Body: j.JobDTO(ctx, job)}, nil
		},
	}
}

type jobInput struct {
	ID string `path:"id" format:"uuid"`
}

// JobEndpoint 返回导出任务的状态。
func JobEndpoint(j *Jobs) rest.Endpoint[jobInput, jobOutput] {
	return rest.Endpoint[jobInput, jobOutput]{
		Operation: huma.Operation{OperationID: "export-get", Summary: "导出任务", Tags: jobTags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *jobInput) (*jobOutput, error) {
			uid, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, ErrNotFound
			}
			job, err := j.Get(ctx, uid, id)
			if err != nil {
				return nil, err
			}
			return &jobOutput{Body: j.JobDTO(ctx, job)}, nil
		},
	}
}

type jobsOutput struct{ Body []JobDTO }

// JobsEndpoint 列出当前用户最近 20 个导出任务。
func JobsEndpoint(j *Jobs) rest.Endpoint[struct{}, jobsOutput] {
	return rest.Endpoint[struct{}, jobsOutput]{
		Operation: huma.Operation{OperationID: "export-list", Summary: "导出任务列表", Tags: jobTags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*jobsOutput, error) {
			uid, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			list, err := j.List(ctx, uid, 20)
			if err != nil {
				return nil, err
			}
			out := &jobsOutput{Body: make([]JobDTO, 0, len(list))}
			for i := range list {
				out.Body = append(out.Body, j.JobDTO(ctx, &list[i]))
			}
			return out, nil
		},
	}
}

// DownloadHandler 返回下载导出文件的 handler，路由中必须包含 {id}；只有任务的创建者可以下载。
func (j *Jobs) DownloadHandler() http.Handler {
	return web.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		uid, ok := auth.UserID(r.Context())
		if !ok {
			return ErrNotFound
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			return ErrNotFound
		}
		job, err := j.Get(r.Context(), uid, id)
		if err == nil {
			err = j.Serve(w, r, job)
		}
		return err
	})
}

func translate(ctx context.Context, code string) string {
	if code == "" {
		return ""
	}
	return i18n.T(ctx, code)
}
