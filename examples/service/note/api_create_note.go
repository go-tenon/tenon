package note

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/shellphy-kit/tools/examples/service/user"
	"github.com/shellphy-kit/tools/httpx/auth"
	"github.com/shellphy-kit/tools/httpx/rest"
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
		Tags:          []string{"note"},
		DefaultStatus: http.StatusCreated,
		Security:      rest.RequireAuth(),
	}, a.handle)
}

func (a *CreateNote) handle(ctx context.Context, in *CreateNoteInput) (*CreateNoteOutput, error) {
	u, _ := auth.From[*user.User](ctx) // rest.Authorize 已保证已登录
	n, err := a.Notes.Create(ctx, u.Name, in.Body.Text)
	switch {
	case errors.Is(err, ErrEmpty):
		return nil, huma.Error422UnprocessableEntity("笔记内容不能为空")
	case err != nil:
		return nil, err
	}
	return &CreateNoteOutput{Body: toDTO(n)}, nil
}
