package authhttp

import (
	"context"
	"net"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"example.com/blog/internal/auth/social"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/login/oauth"
)

type SocialTokenInput struct {
	Provider  string `path:"provider"`
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Code         string `json:"code,omitempty"`
		CodeVerifier string `json:"code_verifier,omitempty"`
		RedirectURI  string `json:"redirect_uri,omitempty"`
		IDToken      string `json:"id_token,omitempty" doc:"App 原生登录时 SDK 返回的 ID token，代替 code"`
		Nonce        string `json:"nonce,omitempty"`
	}
	ip string
}

// Resolve 记录客户端地址，见 Huma 的 Resolver。
func (in *SocialTokenInput) Resolve(ctx huma.Context) []error {
	in.ip, _, _ = net.SplitHostPort(ctx.RemoteAddr())
	return nil
}

type SocialTokenOutput struct {
	Body struct {
		Token string `json:"token,omitempty"`
		Proof string `json:"proof,omitempty" doc:"还没有账号：带着它调用注册接口"`
		Name  string `json:"name,omitempty"`
	}
}

// RegisterSocial 注册用第三方账号换取 API 令牌的接口。
func RegisterSocial(api *rest.API, providers map[string]*oauth.Provider, users *login.Realm, tokens *apitoken.Store) {
	op := huma.Operation{OperationID: "auth-social-token", Method: http.MethodPost, Path: "/auth/{provider}/tokens"}
	rest.Public()(&op)
	rest.Register(api, op, func(ctx context.Context, in *SocialTokenInput) (*SocialTokenOutput, error) {
		p, ok := providers[in.Provider]
		if !ok {
			return nil, social.ErrUnknownProvider.New()
		}
		var ext login.External
		var err error
		if in.Body.IDToken != "" {
			ext, err = p.IDToken(ctx, in.Body.IDToken, in.Body.Nonce)
		} else {
			ext, err = p.Exchange(ctx, oauth.Grant{
				Code: in.Body.Code, Verifier: in.Body.CodeVerifier, RedirectURI: in.Body.RedirectURI, Nonce: in.Body.Nonce,
			})
		}
		if err != nil {
			return nil, err
		}
		out, err := users.External(ctx, ext)
		if err != nil {
			return nil, err
		}
		resp := &SocialTokenOutput{}
		if out.Proof != "" {
			resp.Body.Proof, resp.Body.Name = out.Proof, ext.Name
			return resp, nil
		}
		resp.Body.Token, err = tokens.Login(ctx, out.Subject, apitoken.Device{IP: in.ip, UserAgent: in.UserAgent})
		return resp, err
	})
}
