package note

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/modules/account"
)

type NoteDTO struct {
	ID        string    `json:"id" format:"uuid"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func toDTO(n *Note) NoteDTO {
	return NoteDTO{ID: n.ID.String(), Text: n.Text, CreatedAt: n.CreatedAt}
}

type ListNotesOutput struct {
	Body []NoteDTO
}

type ListNotes struct {
	Notes *Store
}

func (a *ListNotes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-notes",
		Method:      http.MethodGet,
		Path:        "/notes",
		Summary:     "我的笔记",
		Tags:        []string{"笔记"},
		Security:    rest.RequireAuth(),
	}, a.handle)
}

func (a *ListNotes) handle(ctx context.Context, _ *struct{}) (*ListNotesOutput, error) {
	p, _ := auth.From[*account.Principal](ctx) // rest.Authorize 已保证已登录
	notes, err := a.Notes.List(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out := &ListNotesOutput{Body: make([]NoteDTO, 0, len(notes))}
	for i := range notes {
		out.Body = append(out.Body, toDTO(&notes[i]))
	}
	return out, nil
}
