package notification

import (
	"context"
	"errors"
	"net/http"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/rest"
)

// 以下函数返回通知相关的 REST 接口，user 返回当前用户 ID（如 account.CurrentUserID）：
//
//	rest.Get(v1, "/notifications", notification.ListEndpoint(notes, account.CurrentUserID))
//	rest.Get(v1, "/notifications/unread-count", notification.UnreadCountEndpoint(notes, account.CurrentUserID))
//	rest.Post(v1, "/notifications/{id}/read", notification.MarkReadEndpoint(notes, account.CurrentUserID))
//	rest.Post(v1, "/notifications/read-all", notification.MarkAllReadEndpoint(notes, account.CurrentUserID))
//	rest.Delete(v1, "/notifications/{id}", notification.DeleteEndpoint(notes, account.CurrentUserID))

// CurrentUser 返回当前登录用户的 ID，例如 account.CurrentUserID。
type CurrentUser func(ctx context.Context) (uuid.UUID, bool)

var tags = []string{"通知"}

// DTO 是返回给客户端的通知。
type DTO struct {
	ID        string         `json:"id" format:"uuid"`
	Type      string         `json:"type" example:"order.shipped"`
	Title     string         `json:"title"`
	Body      string         `json:"body,omitempty"`
	URL       string         `json:"url,omitempty"`
	Data      map[string]any `json:"data"`
	Read      bool           `json:"read"`
	CreatedAt time.Time      `json:"created_at"`
}

// ToDTO 返回 n 的 DTO。
func ToDTO(n *Notification) DTO {
	return DTO{ID: n.ID.String(), Type: n.Type, Title: n.Title, Body: n.Body, URL: n.URL, Data: n.Data,
		Read: n.Read(), CreatedAt: n.CreatedAt}
}

func uid(ctx context.Context, user CurrentUser) (uuid.UUID, error) {
	id, ok := user(ctx)
	if !ok {
		return id, huma.Error401Unauthorized("未登录")
	}
	return id, nil
}

func mapError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return huma.Error404NotFound("通知不存在")
	}
	return err
}

type listInput struct {
	Unread bool   `query:"unread" doc:"只返回未读"`
	Before string `query:"before" format:"uuid" doc:"上一页返回的 next，省略表示第一页"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"20"`
}

type listOutput struct {
	Body struct {
		Items []DTO  `json:"items"`
		Next  string `json:"next,omitempty" doc:"下一页的 before 参数，没有更多时省略"`
	}
}

// ListEndpoint 列出当前用户的通知，最新的在前。
func ListEndpoint(s *Service, user CurrentUser) rest.Endpoint[listInput, listOutput] {
	return rest.Endpoint[listInput, listOutput]{
		Operation: huma.Operation{OperationID: "notification-list", Summary: "通知列表", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *listInput) (*listOutput, error) {
			id, err := uid(ctx, user)
			if err != nil {
				return nil, err
			}
			before, _ := uuid.Parse(in.Before)
			list, next, err := s.List(ctx, id, ListOptions{UnreadOnly: in.Unread, Before: before, Limit: in.Limit})
			if err != nil {
				return nil, err
			}
			out := &listOutput{}
			out.Body.Items = make([]DTO, 0, len(list))
			for i := range list {
				out.Body.Items = append(out.Body.Items, ToDTO(&list[i]))
			}
			if next != uuid.Nil() {
				out.Body.Next = next.String()
			}
			return out, nil
		},
	}
}

type countOutput struct {
	Body struct {
		Count int `json:"count"`
	}
}

// UnreadCountEndpoint 返回当前用户的未读通知数。
func UnreadCountEndpoint(s *Service, user CurrentUser) rest.Endpoint[struct{}, countOutput] {
	return rest.Endpoint[struct{}, countOutput]{
		Operation: huma.Operation{OperationID: "notification-unread-count", Summary: "未读通知数", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*countOutput, error) {
			id, err := uid(ctx, user)
			if err != nil {
				return nil, err
			}
			out := &countOutput{}
			out.Body.Count, err = s.UnreadCount(ctx, id)
			return out, err
		},
	}
}

type idInput struct {
	ID string `path:"id" format:"uuid"`
}

// MarkReadEndpoint 把一条通知标记为已读。
func MarkReadEndpoint(s *Service, user CurrentUser) rest.Endpoint[idInput, struct{}] {
	return rest.Endpoint[idInput, struct{}]{
		Operation: huma.Operation{OperationID: "notification-mark-read", Summary: "标记已读", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *idInput) (*struct{}, error) {
			id, err := uid(ctx, user)
			if err != nil {
				return nil, err
			}
			nid, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, mapError(ErrNotFound)
			}
			return nil, mapError(s.MarkRead(ctx, id, nid))
		},
	}
}

// MarkAllReadEndpoint 把当前用户的全部通知标记为已读。
func MarkAllReadEndpoint(s *Service, user CurrentUser) rest.Endpoint[struct{}, struct{}] {
	return rest.Endpoint[struct{}, struct{}]{
		Operation: huma.Operation{OperationID: "notification-mark-all-read", Summary: "全部标记已读", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			id, err := uid(ctx, user)
			if err != nil {
				return nil, err
			}
			return nil, s.MarkAllRead(ctx, id)
		},
	}
}

// DeleteEndpoint 删除一条通知。
func DeleteEndpoint(s *Service, user CurrentUser) rest.Endpoint[idInput, struct{}] {
	return rest.Endpoint[idInput, struct{}]{
		Operation: huma.Operation{OperationID: "notification-delete", Summary: "删除通知", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *idInput) (*struct{}, error) {
			id, err := uid(ctx, user)
			if err != nil {
				return nil, err
			}
			nid, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, mapError(ErrNotFound)
			}
			return nil, mapError(s.Delete(ctx, id, nid))
		},
	}
}
