package note

import (
	"context"
	"net/http"

	"github.com/cockroachdb/errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/modules/account"
)

type CreateNoteInput struct {
	Body struct {
		Text string `json:"text" minLength:"1" maxLength:"500" doc:"笔记内容"`
	}
}

type CreateNoteOutput struct {
	Body NoteDTO
}

type CreateNote struct {
	Notes *Store
}

func (a *CreateNote) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-note",
		Method:        http.MethodPost,
		Path:          "/notes",
		Summary:       "创建笔记",
		Tags:          []string{"笔记"},
		DefaultStatus: http.StatusCreated,
		Security:      rest.RequireAuth(),
	}, a.handle)
}

func (a *CreateNote) handle(ctx context.Context, in *CreateNoteInput) (*CreateNoteOutput, error) {
	p, _ := auth.From[*account.Principal](ctx) // rest.Authorize 已保证已登录
	n, err := a.Notes.Create(ctx, p.ID, in.Body.Text)
	switch {
	case errors.Is(err, ErrEmpty):
		return nil, huma.Error422UnprocessableEntity("笔记内容不能为空")
	case err != nil:
		return nil, err
	}
	return &CreateNoteOutput{Body: toDTO(n)}, nil
}
