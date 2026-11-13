package accounts

import (
	"context"
	"net/url"
	"time"

	"github.com/redis/rueidis"
	"github.com/uptrace/bun"

	"example.com/blog/internal/editor"
	"example.com/blog/internal/reader"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/hashing"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/login/oauth"
	"github.com/go-tenon/tenon/mail"
	"github.com/go-tenon/tenon/ratelimit"
	"github.com/go-tenon/tenon/ratelimit/redislimit"
	"github.com/go-tenon/tenon/sms"
)

type Config struct {
	// PasswordURL 是编辑后台单页应用中设置密码页面的地址，邀请邮件中的链接指向它。
	PasswordURL string          `yaml:"password_url"`
	EditorToken apitoken.Config `yaml:"editor_token"`
	ReaderToken apitoken.Config `yaml:"reader_token"`
	Apple       Apple           `yaml:"apple"`
}

// Apple 是在 Apple 开发者后台登记的 Sign in with Apple。
type Apple struct {
	ServiceID    string `yaml:"service_id"`
	ClientSecret string `yaml:"client_secret"`
	BundleID     string `yaml:"bundle_id"` // App 原生登录时 ID token 的受众
}

type Accounts struct {
	Editors      *login.Realm
	EditorTokens *apitoken.Store
	EditorJWT    *apitoken.JWT
	// EditorCookie 保存编辑后台单页应用的 refresh token，只发给 /admin/api/auth 下的接口。
	EditorCookie apitoken.RefreshCookie
	Readers      *login.Realm
	ReaderTokens *apitoken.Store
	ReaderJWT    *apitoken.JWT
	Apple        *oauth.Provider
}

func New(ctx context.Context, db *bun.DB, rdb rueidis.Client, cfg Config, hasher *hashing.Hasher, c *crypt.Crypt, mailer mail.Mailer, texts sms.Sender) (*Accounts, error) {
	every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
	editors := login.New(db, login.Options{
		Type:    editor.Type,
		Hasher:  hasher,
		Crypt:   c,
		Issuer:  "Blog 编辑后台",
		Resolve: editor.Resolve(db),
		// 编辑只用邮箱和密码登录，必须使用二次验证：还没有启用的，登录时先启用。
		Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}, RequireTwoFactor: true},
		Attempts: redislimit.New(rdb, "blog:login:editor:attempts", every15),
		Sends:    redislimit.New(rdb, "blog:login:editor:sends", every15),
		Send: func(ctx context.Context, code login.Code) error {
			link := cfg.PasswordURL + "?" + url.Values{"request": {code.RequestID}, "code": {code.Code}}.Encode()
			return mailer.Send(ctx, mail.Message{
				To:      []mail.Address{{Email: code.To.Value}},
				Subject: "设置 Blog 编辑后台的密码",
				Text:    "打开下面的链接设置密码，15 分钟内有效：\n" + link,
			})
		},
	})
	readers := login.New(db, login.Options{
		Type:     reader.Type,
		Policy:   login.Policy{Register: true},
		Hasher:   hasher,
		Crypt:    c,
		Resolve:  reader.Resolve(db),
		Attempts: redislimit.New(rdb, "blog:login:reader:attempts", every15),
		Sends:    redislimit.New(rdb, "blog:login:reader:sends", every15),
		Send: func(ctx context.Context, code login.Code) error {
			return texts.Send(ctx, sms.Message{
				To:       code.To.Value,
				Template: "login_code",
				Params:   map[string]string{"code": code.Code},
			})
		},
	})
	apple, err := oauth.Discover(ctx, oauth.Config{
		Provider:     "apple",
		Issuer:       "https://appleid.apple.com",
		ClientID:     cfg.Apple.ServiceID,
		ClientSecret: cfg.Apple.ClientSecret,
		Audiences:    []string{cfg.Apple.ServiceID, cfg.Apple.BundleID},
		NoPKCE:       true,
		FormPost:     true,
	})
	if err != nil {
		return nil, err
	}
	editorTokens := apitoken.NewStore(db, cfg.EditorToken)
	tokens := apitoken.NewStore(db, cfg.ReaderToken)
	return &Accounts{
		Editors:      editors,
		EditorTokens: editorTokens,
		EditorJWT:    apitoken.NewJWT(editorTokens, apitoken.JWTConfig{Keys: apitoken.HMACKeys(c)}),
		EditorCookie: apitoken.RefreshCookie{Name: "editor_refresh", Path: "/admin/api/auth"},
		Readers:      readers,
		ReaderTokens: tokens,
		ReaderJWT:    apitoken.NewJWT(tokens, apitoken.JWTConfig{Keys: apitoken.HMACKeys(c)}),
		Apple:        apple,
	}, nil
}
