package account

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/alexedwards/scs/v2"
	"github.com/uptrace/bun"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/ryan053857/go-kit/config"
	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/support/random"
)

// OAuthUser 是从第三方平台取得的账号信息。
type OAuthUser struct {
	Subject       string // 平台内的唯一 ID
	Email         string
	EmailVerified bool // 平台是否确认过该邮箱属于此用户
	Name          string
}

// OAuthProvider 描述一个第三方登录平台（OAuth 2.0 授权码模式）。内置 GitHub 与 Google，
// 其他标准 OAuth 2.0 平台可以自己填写 Config 与 User。
type OAuthProvider struct {
	Name   string // 用于路径与数据库，如 "github"
	Label  string // 显示名，如 "GitHub"
	Config oauth2.Config
	// User 用已授权的 client 获取第三方账号信息。
	User func(ctx context.Context, client *http.Client) (*OAuthUser, error)
}

// OAuthClient 是在第三方平台注册的应用凭证，放在产品配置中：
//
//	github:
//	  client_id: xxx
//	  client_secret: ${GITHUB_CLIENT_SECRET}
type OAuthClient struct {
	ClientID     string        `yaml:"client_id"`
	ClientSecret config.Secret `yaml:"client_secret"`
}

// GitHub 返回 GitHub 登录。redirectURL 为回调地址（如 https://example.com/auth/github/callback），
// 需与 GitHub OAuth App 中填写的一致。
func GitHub(c OAuthClient, redirectURL string) OAuthProvider {
	return OAuthProvider{
		Name: "github", Label: "GitHub",
		Config: oauth2.Config{ClientID: c.ClientID, ClientSecret: string(c.ClientSecret), Endpoint: endpoints.GitHub,
			RedirectURL: redirectURL, Scopes: []string{"read:user", "user:email"}},
		User: githubUser,
	}
}

func githubUser(ctx context.Context, client *http.Client) (*OAuthUser, error) {
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user", &u); err != nil {
		return nil, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, client, "https://api.github.com/user/emails", &emails); err != nil {
		return nil, err
	}
	out := &OAuthUser{Subject: strconv.FormatInt(u.ID, 10), Name: cmp.Or(u.Name, u.Login)}
	for _, e := range emails {
		if e.Primary {
			out.Email, out.EmailVerified = e.Email, e.Verified
		}
	}
	return out, nil
}

// Google 返回 Google 登录。redirectURL 为回调地址，需与 Google Cloud 控制台中填写的一致。
func Google(c OAuthClient, redirectURL string) OAuthProvider {
	return OAuthProvider{
		Name: "google", Label: "Google",
		Config: oauth2.Config{ClientID: c.ClientID, ClientSecret: string(c.ClientSecret), Endpoint: endpoints.Google,
			RedirectURL: redirectURL, Scopes: []string{"openid", "email", "profile"}},
		User: func(ctx context.Context, client *http.Client) (*OAuthUser, error) {
			var u struct {
				Sub           string `json:"sub"`
				Email         string `json:"email"`
				EmailVerified bool   `json:"email_verified"`
				Name          string `json:"name"`
			}
			if err := getJSON(ctx, client, "https://openidconnect.googleapis.com/v1/userinfo", &u); err != nil {
				return nil, err
			}
			return &OAuthUser{Subject: u.Sub, Email: u.Email, EmailVerified: u.EmailVerified, Name: u.Name}, nil
		},
	}
}

func getJSON(ctx context.Context, client *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return errors.Newf("GET %s: %s: %s", url, resp.Status, body)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// OAuthLogin 返回第三方账号对应的用户：
//
//   - 已绑定过：返回绑定的用户。
//   - 未绑定、平台确认过的邮箱与已验证邮箱的用户相同：自动绑定到该用户。
//   - 未绑定、邮箱未被注册：创建新用户（没有密码，邮箱视为已验证当且仅当平台确认过）。
//
// 邮箱已被一个未验证邮箱的用户注册时返回 ErrEmailTaken（防止有人抢先用别人的邮箱注册，
// 等邮箱主人用第三方登录时接管账号），用户需先用密码登录，再在设置中绑定。
// 平台没有返回邮箱时返回 ErrEmailRequired；用户已停用时返回 ErrUserDisabled；
// 没有配置 Actions.CreateOAuthUser 时不自动注册，返回 ErrRegistrationClosed。
func (s *Service) OAuthLogin(ctx context.Context, provider string, ou *OAuthUser) (*User, error) {
	u := new(User)
	err := s.db.NewSelect().Model(u).
		Where("id = (SELECT user_id FROM user_identities WHERE provider = ? AND subject = ?)", provider, ou.Subject).
		Scan(ctx)
	switch {
	case err == nil:
		if u.Disabled() {
			return nil, ErrUserDisabled
		}
		return u, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, errors.Wrap(err, "account: oauth login")
	}

	if ou.Email == "" {
		return nil, ErrEmailRequired
	}
	email := NormalizeEmail(ou.Email)
	err = s.db.NewSelect().Model(u).Where("email = ?", email).Scan(ctx)
	switch {
	case err == nil:
		if !ou.EmailVerified || !u.EmailVerified() {
			return nil, ErrEmailTaken
		}
		if u.Disabled() {
			return nil, ErrUserDisabled
		}
		if err := s.LinkIdentity(ctx, u.ID, provider, ou); err != nil {
			return nil, err
		}
		return u, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, errors.Wrap(err, "account: oauth login")
	}

	if s.actions.CreateOAuthUser == nil {
		return nil, ErrRegistrationClosed
	}
	fields := &UserFields{Email: email, Locale: i18n.Locale(ctx)}
	if ou.EmailVerified {
		fields.EmailVerifiedAt = s.now()
	}
	id, err := s.Register(ctx, func(ctx context.Context, tx bun.Tx) (uuid.UUID, error) {
		id, err := s.actions.CreateOAuthUser(ctx, tx, fields, ou)
		if err != nil {
			return id, err
		}
		return id, s.insertIdentity(ctx, tx, id, provider, ou)
	})
	if err != nil {
		return nil, err
	}
	s.emit(ctx, Event{Type: EventIdentityLinked, UserID: id, Provider: provider})
	return s.User(ctx, id)
}

// LinkIdentity 把第三方账号绑定到用户。该第三方账号已绑定其他用户时返回 ErrIdentityTaken，
// 已绑定当前用户时返回 nil。
func (s *Service) LinkIdentity(ctx context.Context, userID uuid.UUID, provider string, ou *OAuthUser) error {
	err := s.insertIdentity(ctx, s.db, userID, provider, ou)
	if database.IsUniqueViolation(err, "user_identities_provider_subject_key") {
		owner, err := s.db.NewSelect().Model((*Identity)(nil)).
			Where("provider = ? AND subject = ? AND user_id = ?", provider, ou.Subject, userID).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "account: link identity")
		}
		if owner {
			return nil
		}
		return ErrIdentityTaken
	}
	if err != nil {
		return errors.Wrap(err, "account: link identity")
	}
	s.emit(ctx, Event{Type: EventIdentityLinked, UserID: userID, Provider: provider})
	return nil
}

func (s *Service) insertIdentity(ctx context.Context, db bun.IDB, userID uuid.UUID, provider string, ou *OAuthUser) error {
	_, err := db.NewInsert().Model(&Identity{UserID: userID, Provider: provider, Subject: ou.Subject,
		Email: ou.Email, Name: ou.Name}).Exec(ctx)
	return err
}

// Identities 返回用户绑定的第三方账号。
func (s *Service) Identities(ctx context.Context, userID uuid.UUID) ([]Identity, error) {
	list := []Identity{}
	if err := s.db.NewSelect().Model(&list).Where("user_id = ?", userID).Order("id").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "account: list identities")
	}
	return list, nil
}

// UnlinkIdentity 解绑第三方账号。用户没有密码且这是最后一个绑定时返回 ErrLastLoginMethod
// （否则将无法再登录）；不存在时返回 ErrNotFound。
func (s *Service) UnlinkIdentity(ctx context.Context, userID, identityID uuid.UUID) error {
	var provider string
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		u := new(User)
		if err := tx.NewSelect().Model(u).Where("id = ?", userID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		id := new(Identity)
		err := tx.NewDelete().Model(id).Where("id = ? AND user_id = ?", identityID, userID).Returning("provider").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		provider = id.Provider
		if !u.HasPassword() {
			left, err := tx.NewSelect().Model((*Identity)(nil)).Where("user_id = ?", userID).Count(ctx)
			if err != nil {
				return err
			}
			if left == 0 {
				return ErrLastLoginMethod
			}
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrLastLoginMethod) {
		return err
	}
	if err != nil {
		return errors.Wrap(err, "account: unlink identity")
	}
	s.emit(ctx, Event{Type: EventIdentityRemoved, UserID: userID, Provider: provider})
	return nil
}

// OAuthWeb 提供网页第三方登录的两个处理函数，产品挂载在含 {provider} 参数的路径上：
//
//	oauth := &account.OAuthWeb{Users: users, Sessions: a.Sessions, Providers: []account.OAuthProvider{
//		account.GitHub(cfg.GitHub, cfg.BaseURL+"/auth/github/callback"),
//	}}
//	a.Router.Get("/auth/{provider}", oauth.Start)
//	a.Router.Get("/auth/{provider}/callback", oauth.Callback)
//
// 登录页放一个指向 /auth/github 的链接即可；已登录时访问则把第三方账号绑定到当前用户
// （设置页的“绑定 GitHub”）。可带 ?next=/settings 指定完成后跳转的站内地址。
// 失败时跳转到 ErrorURL 并带上 error 参数：denied（用户取消授权）、email_taken、email_required、
// identity_taken、disabled、registration_closed、failed（其他错误，详情见日志）。
type OAuthWeb struct {
	Users     *Service
	Sessions  *scs.SessionManager
	Providers []OAuthProvider

	Redirect string // 完成后的默认跳转地址，默认 "/"
	MFAURL   string // 账号启用了两步验证时跳转到的验证码页面，默认 "/login/mfa"
	ErrorURL string // 失败时跳转的页面，默认 "/login"
}

const oauthStateKey = "account.oauth"

type oauthState struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
	Next     string `json:"n"`
	Link     bool   `json:"l"`
}

func (o *OAuthWeb) provider(r *http.Request) (*OAuthProvider, bool) {
	name := r.PathValue("provider")
	for i := range o.Providers {
		if o.Providers[i].Name == name {
			return &o.Providers[i], true
		}
	}
	return nil, false
}

// Start 跳转到第三方平台的授权页面。
func (o *OAuthWeb) Start(w http.ResponseWriter, r *http.Request) {
	p, ok := o.provider(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, loggedIn := auth.From[*Principal](r.Context())
	st := oauthState{Provider: p.Name, State: random.Token(16), Verifier: oauth2.GenerateVerifier(),
		Next: auth.SafeRedirect(r.URL.Query().Get("next"), ""), Link: loggedIn}
	raw, _ := json.Marshal(st)
	o.Sessions.Put(r.Context(), oauthStateKey, string(raw))
	http.Redirect(w, r, p.Config.AuthCodeURL(st.State, oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

// Callback 处理第三方平台的回调：登录（或注册、绑定）后跳转。
func (o *OAuthWeb) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := o.provider(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var st oauthState
	raw := o.Sessions.PopString(ctx, oauthStateKey)
	q := r.URL.Query()
	if json.Unmarshal([]byte(raw), &st) != nil || st.Provider != p.Name || st.State == "" || q.Get("state") != st.State {
		o.fail(w, r, "failed", errors.New("oauth state mismatch"))
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		o.fail(w, r, "denied", nil)
		return
	}
	tok, err := p.Config.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		o.fail(w, r, "failed", errors.Wrap(err, "oauth exchange"))
		return
	}
	ou, err := p.User(ctx, p.Config.Client(ctx, tok))
	if err != nil || ou.Subject == "" {
		o.fail(w, r, "failed", errors.Wrap(err, "oauth user"))
		return
	}

	next := cmp.Or(st.Next, o.Redirect, "/")
	if cur, ok := auth.From[*Principal](ctx); ok && st.Link {
		if err := o.Users.LinkIdentity(ctx, cur.ID, p.Name, ou); err != nil {
			o.fail(w, r, "", err)
			return
		}
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	u, err := o.Users.OAuthLogin(ctx, p.Name, ou)
	if err != nil {
		o.fail(w, r, "", err)
		return
	}
	mfa, err := o.Users.WebBeginLogin(ctx, o.Sessions, u.ID, r)
	if err != nil {
		o.fail(w, r, "failed", err)
		return
	}
	if mfa {
		next = cmp.Or(o.MFAURL, "/login/mfa")
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (o *OAuthWeb) fail(w http.ResponseWriter, r *http.Request, code string, err error) {
	if code == "" {
		switch {
		case errors.Is(err, ErrEmailTaken):
			code = "email_taken"
		case errors.Is(err, ErrEmailRequired):
			code = "email_required"
		case errors.Is(err, ErrIdentityTaken):
			code = "identity_taken"
		case errors.Is(err, ErrUserDisabled):
			code = "disabled"
		case errors.Is(err, ErrRegistrationClosed):
			code = "registration_closed"
		default:
			code = "failed"
		}
	}
	if code == "failed" {
		slog.ErrorContext(r.Context(), "oauth login failed", "provider", r.PathValue("provider"), "err", err)
	}
	target := cmp.Or(o.ErrorURL, "/login")
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	http.Redirect(w, r, target+sep+"error="+url.QueryEscape(code), http.StatusFound)
}
