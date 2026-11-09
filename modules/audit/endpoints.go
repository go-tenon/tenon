package audit

import (
	"context"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/rest"
)

var tags = []string{"审计日志"}

// LogDTO 是一条审计记录。
type LogDTO struct {
	ID           string         `json:"id" format:"uuid"`
	CreatedAt    time.Time      `json:"created_at"`
	ActorID      string         `json:"actor_id,omitempty" format:"uuid" doc:"操作者，系统操作时省略"`
	Action       string         `json:"action" example:"account.login"`
	ResourceType string         `json:"resource_type,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	Summary      string         `json:"summary"`
	Data         map[string]any `json:"data"`
	Failed       bool           `json:"failed"`
	IP           string         `json:"ip,omitempty"`
	UserAgent    string         `json:"user_agent,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
}

// DTO 返回 l 的 LogDTO。
func DTO(l *Log) LogDTO {
	d := LogDTO{ID: l.ID.String(), CreatedAt: l.CreatedAt, Action: l.Action, ResourceType: l.ResourceType,
		ResourceID: l.ResourceID, Summary: l.Summary, Data: l.Data, Failed: l.Failed, IP: l.IP,
		UserAgent: l.UserAgent, RequestID: l.RequestID}
	if l.ActorID != uuid.Nil() {
		d.ActorID = l.ActorID.String()
	}
	return d
}

type pageInput struct {
	Before string `query:"before" format:"uuid" doc:"上一页返回的 next，省略表示第一页"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type listInput struct {
	pageInput
	ActorID      string    `query:"actor_id" format:"uuid"`
	Action       string    `query:"action"`
	ResourceType string    `query:"resource_type"`
	ResourceID   string    `query:"resource_id"`
	From         time.Time `query:"from" doc:"起始时间（含）"`
	To           time.Time `query:"to" doc:"结束时间（不含）"`
}

type listOutput struct {
	Body struct {
		Items []LogDTO `json:"items"`
		Next  string   `json:"next,omitempty" doc:"下一页的 before 参数，没有更多时省略"`
	}
}

func (s *Service) page(ctx context.Context, f Filter, p pageInput) (*listOutput, error) {
	f.Limit = p.Limit
	f.Before, _ = uuid.Parse(p.Before)
	logs, next, err := s.List(ctx, f)
	if err != nil {
		return nil, err
	}
	out := &listOutput{}
	out.Body.Items = make([]LogDTO, 0, len(logs))
	for i := range logs {
		out.Body.Items = append(out.Body.Items, DTO(&logs[i]))
	}
	if next != uuid.Nil() {
		out.Body.Next = next.String()
	}
	return out, nil
}

// ListEndpoint 按条件查询全部审计记录，供管理后台使用。
// 接口本身只要求登录，产品必须在挂载处限制只有管理员能访问（例如挂在带管理员检查中间件的 Group 上）。
func ListEndpoint(s *Service) rest.Endpoint[listInput, listOutput] {
	return rest.Endpoint[listInput, listOutput]{
		Operation: huma.Operation{OperationID: "audit-list", Summary: "审计日志", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *listInput) (*listOutput, error) {
			f := Filter{Action: in.Action, ResourceType: in.ResourceType, ResourceID: in.ResourceID, From: in.From, To: in.To}
			f.ActorID, _ = uuid.Parse(in.ActorID)
			return s.page(ctx, f, in.pageInput)
		},
	}
}

// MineEndpoint 列出当前用户自己作为操作者的记录（设置页的“安全日志”）。user 返回当前用户 ID，如 account.CurrentUserID。
func MineEndpoint(s *Service, user func(context.Context) (uuid.UUID, bool)) rest.Endpoint[pageInput, listOutput] {
	return rest.Endpoint[pageInput, listOutput]{
		Operation: huma.Operation{OperationID: "audit-mine", Summary: "我的操作记录", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *pageInput) (*listOutput, error) {
			uid, ok := user(ctx)
			if !ok {
				return nil, huma.Error401Unauthorized("未登录")
			}
			return s.page(ctx, Filter{ActorID: uid}, *in)
		},
	}
}
