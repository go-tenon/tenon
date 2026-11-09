package audit

import (
	"context"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
)

var tags = []string{"audit"}

// LogDTO 是一条审计记录。
type LogDTO struct {
	ID           string         `json:"id" format:"uuid"`
	CreatedAt    time.Time      `json:"created_at"`
	ActorID      string         `json:"actor_id,omitempty" format:"uuid" doc:"操作者，系统操作时省略"`
	Action       string         `json:"action" example:"account.login"`
	Summary      string         `json:"summary" doc:"给人看的摘要（按请求的语言）"`
	ResourceType string         `json:"resource_type,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	Data         map[string]any `json:"data"`
	Failed       bool           `json:"failed"`
	IP           string         `json:"ip,omitempty"`
	UserAgent    string         `json:"user_agent,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
}

// LogDTO 返回 l 的 LogDTO，摘要按 ctx 的语言翻译。
func (s *Service) LogDTO(ctx context.Context, l *Log) LogDTO {
	d := LogDTO{ID: l.ID.String(), CreatedAt: l.CreatedAt, Action: l.Action, Summary: Summary(ctx, l),
		ResourceType: l.ResourceType, ResourceID: l.ResourceID, Data: l.Data, Failed: l.Failed, IP: l.IP,
		UserAgent: l.UserAgent, RequestID: l.RequestID}
	if l.ActorID != uuid.Nil() {
		d.ActorID = l.ActorID.String()
	}
	return d
}

type listOutput struct{ Body rest.Page[LogDTO] }

func (s *Service) page(ctx context.Context, f Filter) (*listOutput, error) {
	logs, next, err := s.List(ctx, f)
	if err != nil {
		return nil, err
	}
	out := &listOutput{}
	out.Body.Items = make([]LogDTO, 0, len(logs))
	for i := range logs {
		out.Body.Items = append(out.Body.Items, s.LogDTO(ctx, &logs[i]))
	}
	if next != uuid.Nil() {
		out.Body.Next = next.String()
	}
	return out, nil
}

type listInput struct {
	rest.PageInput
	ActorID      string    `query:"actor_id" format:"uuid"`
	Action       string    `query:"action"`
	ResourceType string    `query:"resource_type"`
	ResourceID   string    `query:"resource_id"`
	From         time.Time `query:"from" doc:"起始时间（含）"`
	To           time.Time `query:"to" doc:"结束时间（不含）"`
}

// ListEndpoint 按条件查询全部审计记录，供管理后台使用，需要 audit.view 能力。
func ListEndpoint(s *Service) rest.Endpoint[listInput, listOutput] {
	return rest.Endpoint[listInput, listOutput]{
		Operation: huma.Operation{OperationID: "audit-list", Summary: "审计日志", Tags: tags, Security: rest.RequireAuth(View)},
		Handler: func(ctx context.Context, in *listInput) (*listOutput, error) {
			f := Filter{Action: in.Action, ResourceType: in.ResourceType, ResourceID: in.ResourceID, From: in.From, To: in.To,
				Cursor: database.ParseCursor(in.Before, in.Limit)}
			f.ActorID, _ = uuid.Parse(in.ActorID)
			return s.page(ctx, f)
		},
	}
}

// MineEndpoint 列出当前用户自己作为操作者的记录（设置页的“安全日志”）。
func MineEndpoint(s *Service) rest.Endpoint[rest.PageInput, listOutput] {
	return rest.Endpoint[rest.PageInput, listOutput]{
		Operation: huma.Operation{OperationID: "audit-mine", Summary: "我的操作记录", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *rest.PageInput) (*listOutput, error) {
			uid, ok := auth.UserID(ctx)
			if !ok {
				return nil, auth.ErrUnauthenticated
			}
			return s.page(ctx, Filter{ActorID: uid, Cursor: database.ParseCursor(in.Before, in.Limit)})
		},
	}
}
