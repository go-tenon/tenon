package note

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/rest"
)

type NoteDTO struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Author    string `json:"author"`
	CreatedAt string `json:"created_at" format:"date-time"`
}

func toDTO(n Note) NoteDTO {
	return NoteDTO{ID: n.ID, Text: n.Text, Author: n.Author, CreatedAt: n.CreatedAt.Format("2006-01-02T15:04:05Z07:00")}
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
		Summary:     "笔记列表",
		Tags:        []string{"note"},
		Security:    rest.RequireAuth(),
	}, a.handle)
}

func (a *ListNotes) handle(ctx context.Context, _ *struct{}) (*ListNotesOutput, error) {
	out := &ListNotesOutput{Body: []NoteDTO{}}
	for _, n := range a.Notes.List(ctx) {
		out.Body = append(out.Body, toDTO(n))
	}
	return out, nil
}
