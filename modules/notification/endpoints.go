package notification

import (
	"context"
	"net/http"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
)

// 以下函数返回站内信的 REST 接口，当前用户取自 auth.UserID：
//
//	rest.Get(v1, "/notifications", notification.ListEndpoint(notes))
//	rest.Get(v1, "/notifications/unread-count", notification.UnreadCountEndpoint(notes))
//	rest.Post(v1, "/notifications/{id}/read", notification.MarkReadEndpoint(notes))
//	rest.Post(v1, "/notifications/read-all", notification.MarkAllReadEndpoint(notes))
//	rest.Delete(v1, "/notifications/{id}", notification.DeleteEndpoint(notes))

var tags = []string{"notification"}

// MessageDTO 是返回给客户端的站内信。
type MessageDTO struct {
	ID        string         `json:"id" format:"uuid"`
	Type      string         `json:"type" example:"order.shipped"`
	Title     string         `json:"title"`
	Body      string         `json:"body,omitempty"`
	URL       string         `json:"url,omitempty"`
	Data      map[string]any `json:"data"`
	Read      bool           `json:"read"`
	CreatedAt time.Time      `json:"created_at"`
}

// MessageDTO 返回 m 的 MessageDTO。
func (s *Service) MessageDTO(m *Message) MessageDTO {
	return MessageDTO{ID: m.ID.String(), Type: m.Type, Title: m.Title, Body: m.Body, URL: m.URL, Data: m.Data,
		Read: m.Read(), CreatedAt: m.CreatedAt}
}

func currentUser(ctx context.Context) (uuid.UUID, error) {
	id, ok := auth.UserID(ctx)
	if !ok {
		return id, auth.ErrUnauthenticated
	}
	return id, nil
}

type listInput struct {
	rest.PageInput
	Unread bool `query:"unread" doc:"只返回未读"`
}

type listOutput struct{ Body rest.Page[MessageDTO] }

// ListEndpoint 列出当前用户的站内信，最新的在前（游标分页）。
func ListEndpoint(s *Service) rest.Endpoint[listInput, listOutput] {
	return rest.Endpoint[listInput, listOutput]{
		Operation: huma.Operation{OperationID: "notification-list", Summary: "站内信列表", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *listInput) (*listOutput, error) {
			uid, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			list, next, err := s.List(ctx, uid, ListOptions{UnreadOnly: in.Unread,
				Cursor: database.ParseCursor(in.Before, in.Limit)})
			if err != nil {
				return nil, err
			}
			out := &listOutput{}
			out.Body.Items = make([]MessageDTO, 0, len(list))
			for i := range list {
				out.Body.Items = append(out.Body.Items, s.MessageDTO(&list[i]))
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

// UnreadCountEndpoint 返回当前用户的未读站内信数。
func UnreadCountEndpoint(s *Service) rest.Endpoint[struct{}, countOutput] {
	return rest.Endpoint[struct{}, countOutput]{
		Operation: huma.Operation{OperationID: "notification-unread-count", Summary: "未读数", Tags: tags, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*countOutput, error) {
			uid, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			out := &countOutput{}
			out.Body.Count, err = s.UnreadCount(ctx, uid)
			return out, err
		},
	}
}

type idInput struct {
	ID string `path:"id" format:"uuid"`
}

func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return id, ErrNotFound
	}
	return id, nil
}

// MarkReadEndpoint 把一条站内信标记为已读。
func MarkReadEndpoint(s *Service) rest.Endpoint[idInput, struct{}] {
	return rest.Endpoint[idInput, struct{}]{
		Operation: huma.Operation{OperationID: "notification-mark-read", Summary: "标记已读", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *idInput) (*struct{}, error) {
			uid, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			return nil, s.MarkRead(ctx, uid, id)
		},
	}
}

// MarkAllReadEndpoint 把当前用户的全部站内信标记为已读。
func MarkAllReadEndpoint(s *Service) rest.Endpoint[struct{}, struct{}] {
	return rest.Endpoint[struct{}, struct{}]{
		Operation: huma.Operation{OperationID: "notification-mark-all-read", Summary: "全部标记已读", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			uid, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			return nil, s.MarkAllRead(ctx, uid)
		},
	}
}

// DeleteEndpoint 删除一条站内信。
func DeleteEndpoint(s *Service) rest.Endpoint[idInput, struct{}] {
	return rest.Endpoint[idInput, struct{}]{
		Operation: huma.Operation{OperationID: "notification-delete", Summary: "删除站内信", Tags: tags,
			DefaultStatus: http.StatusNoContent, Security: rest.RequireAuth()},
		Handler: func(ctx context.Context, in *idInput) (*struct{}, error) {
			uid, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			return nil, s.Delete(ctx, uid, id)
		},
	}
}
