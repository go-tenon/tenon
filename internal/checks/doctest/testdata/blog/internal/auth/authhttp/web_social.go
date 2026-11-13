package authhttp

import (
	"context"
	"net/http"
	"net/url"

	"github.com/danielgtaylor/huma/v2"

	"example.com/blog/internal/auth/social"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/login/oauth"
)

type StartInput struct {
	Provider string `path:"provider"`
}

type StartOutput struct {
	Body struct {
		URL  string `json:"url" doc:"跳转到第三方的地址"`
		Flow string `json:"flow" doc:"保存到回调之后，交给 finish"`
	}
}

type FinishInput struct {
	Provider  string `path:"provider"`
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Flow     string `json:"flow" minLength:"1"`
		Callback string `json:"callback" doc:"回调页地址中的查询参数，原样交回，例如 code=…&state=…"`
	}
}

// FinishOutput 与密码登录的结果相同，三者只有一个不为空；登录成功时 refresh token 写入 cookie。
type FinishOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
	Body      struct {
		AccessToken string `json:"access_token,omitempty"`
		Pending     string `json:"pending,omitempty" doc:"带着它和验证器上的数字调用 /auth/two-factor"`
		Enroll      string `json:"enroll,omitempty" doc:"还没有启用二次验证：带着它调用 /auth/two-factor/enroll"`
	}
}

// RegisterWebSocial 注册编辑后台单页应用的第三方登录接口。
func RegisterWebSocial(api *rest.API, providers map[string]*oauth.Provider, editors *login.Realm, jwt *apitoken.JWT, cookie apitoken.RefreshCookie) {
	start := huma.Operation{OperationID: "editor-social-start", Method: http.MethodPost, Path: "/auth/{provider}/start"}
	rest.Public()(&start)
	rest.Register(api, start, func(_ context.Context, in *StartInput) (*StartOutput, error) {
		p, ok := providers[in.Provider]
		if !ok {
			return nil, social.ErrUnknownProvider.New()
		}
		out := &StartOutput{}
		var flow oauth.Flow
		out.Body.URL, flow = p.Start()
		out.Body.Flow = flow.String()
		return out, nil
	})

	finish := huma.Operation{OperationID: "editor-social-finish", Method: http.MethodPost, Path: "/auth/{provider}/finish"}
	rest.Public()(&finish)
	rest.Register(api, finish, func(ctx context.Context, in *FinishInput) (*FinishOutput, error) {
		p, ok := providers[in.Provider]
		if !ok {
			return nil, social.ErrUnknownProvider.New()
		}
		flow, err := oauth.ParseFlow(in.Body.Flow)
		if err != nil {
			return nil, err
		}
		params, err := url.ParseQuery(in.Body.Callback)
		if err != nil {
			return nil, oauth.ErrInvalid.Wrap(err)
		}
		ext, err := p.Finish(ctx, flow, params)
		if err != nil {
			return nil, err // 用户取消了授权：oauth.ErrDenied
		}
		res, err := editors.External(ctx, ext)
		if err != nil {
			return nil, err
		}
		out := &FinishOutput{}
		switch {
		case res.Proof != "":
			return nil, social.ErrNotLinked.New()
		case res.Pending != "" || res.Enroll != "":
			out.Body.Pending, out.Body.Enroll = res.Pending, res.Enroll
			return out, nil
		}
		pair, err := jwt.Login(ctx, res.Subject, apitoken.Device{UserAgent: in.UserAgent})
		if err != nil {
			return nil, err
		}
		out.SetCookie, out.Body.AccessToken = cookie.Set(pair), pair.Access
		return out, nil
	})
}
