package pages

import (
	"encoding/base64"
	"net/http"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/modules/account"
	"github.com/ryan053857/go-kit/modules/audit"
)

// 安全设置页：两步验证、API Key、第三方账号、安全日志。各卡片的操作由 HTMX 提交，只替换所在的卡片。

func (p *Pages) mountSecurity(r chi.Router, limit func(http.Handler) http.Handler) {
	r.Method(http.MethodGet, "/settings/security", web.HandlerFunc(p.security))
	r.Method(http.MethodPost, "/settings/mfa/setup", web.HandlerFunc(p.mfaSetup))
	r.With(limit).Method(http.MethodPost, "/settings/mfa/enable", web.HandlerFunc(p.mfaEnable))
	r.With(limit).Method(http.MethodPost, "/settings/mfa/disable", web.HandlerFunc(p.mfaDisable))
	r.With(limit).Method(http.MethodPost, "/settings/mfa/recovery-codes", web.HandlerFunc(p.mfaRecoveryCodes))
	r.Method(http.MethodPost, "/settings/api-keys", web.HandlerFunc(p.createAPIKey))
	r.Method(http.MethodPost, "/settings/api-keys/{id}/revoke", web.HandlerFunc(p.revokeAPIKey))
	r.Method(http.MethodPost, "/settings/identities/{id}/unlink", web.HandlerFunc(p.unlinkIdentity))
}

type securityData struct {
	MFA        mfaData
	APIKeys    apiKeysData
	Identities []identityItem
	Logs       []audit.Log
}

// mfaData 是两步验证卡片的状态。
type mfaData struct {
	Status account.MFAStatus
	Setup  *account.MFASetup // 正在绑定：显示二维码
	QRCode string            // 二维码的 data URI
	Codes  []string          // 刚生成的恢复码，只显示这一次
	Error  string
}

type apiKeysData struct {
	Keys     []account.APIKey
	NewToken string // 刚创建的 Key，只显示这一次
	Error    string
}

type identityItem struct {
	Provider account.OAuthProvider
	Identity *account.Identity // 未绑定时为 nil
}

func (p *Pages) security(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	pr, _ := auth.From[*account.Principal](ctx)
	d := &securityData{}
	var err error
	if d.MFA.Status, err = p.Users.MFAStatus(ctx, pr.ID); err != nil {
		return err
	}
	if d.APIKeys.Keys, err = p.Users.APIKeys(ctx, pr.ID); err != nil {
		return err
	}
	if d.Identities, err = p.identities(r); err != nil {
		return err
	}
	if d.Logs, _, err = p.Audit.List(ctx, audit.Filter{ActorID: pr.ID, Limit: 20}); err != nil {
		return err
	}
	return p.View.Page(w, r, http.StatusOK, "安全设置", securityPage(d))
}

func (p *Pages) identities(r *http.Request) ([]identityItem, error) {
	pr, _ := auth.From[*account.Principal](r.Context())
	linked, err := p.Users.Identities(r.Context(), pr.ID)
	if err != nil {
		return nil, err
	}
	items := make([]identityItem, 0, len(p.OAuth))
	for _, prov := range p.OAuth {
		item := identityItem{Provider: prov}
		for i := range linked {
			if linked[i].Provider == prov.Name {
				item.Identity = &linked[i]
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// renderMFA 重新渲染两步验证卡片。
func (p *Pages) renderMFA(w http.ResponseWriter, r *http.Request, d mfaData) error {
	if d.Setup == nil {
		pr, _ := auth.From[*account.Principal](r.Context())
		st, err := p.Users.MFAStatus(r.Context(), pr.ID)
		if err != nil {
			return err
		}
		d.Status = st
	} else {
		png, err := account.MFAQRCode(d.Setup.URL, 200)
		if err != nil {
			return err
		}
		d.QRCode = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	return web.Render(w, r, http.StatusOK, mfaCard(d))
}

func (p *Pages) mfaSetup(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	setup, err := p.Users.SetupMFA(r.Context(), pr.ID)
	if errors.Is(err, account.ErrMFAEnabled) {
		return p.renderMFA(w, r, mfaData{})
	}
	if err != nil {
		return err
	}
	return p.renderMFA(w, r, mfaData{Setup: setup})
}

func (p *Pages) mfaEnable(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	codes, err := p.Users.EnableMFA(r.Context(), pr.ID, r.FormValue("code"))
	switch {
	case errors.Is(err, account.ErrInvalidMFACode):
		// 密钥仍是刚才生成的那个，从表单带回的 otpauth 地址重新显示二维码。
		setup := &account.MFASetup{Secret: r.FormValue("secret"), URL: r.FormValue("url")}
		return p.renderMFA(w, r, mfaData{Setup: setup, Error: "验证码错误，请输入验证器 App 中当前显示的 6 位数字"})
	case err != nil && !errors.Is(err, account.ErrMFAEnabled) && !errors.Is(err, account.ErrMFANotEnabled):
		return err
	}
	return p.renderMFA(w, r, mfaData{Codes: codes})
}

func (p *Pages) mfaDisable(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	err := p.Users.DisableMFA(r.Context(), pr.ID, r.FormValue("code"))
	switch {
	case errors.Is(err, account.ErrInvalidMFACode):
		return p.renderMFA(w, r, mfaData{Error: "验证码错误"})
	case err != nil && !errors.Is(err, account.ErrMFANotEnabled):
		return err
	}
	return p.renderMFA(w, r, mfaData{})
}

func (p *Pages) mfaRecoveryCodes(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	codes, err := p.Users.RegenerateRecoveryCodes(r.Context(), pr.ID, r.FormValue("code"))
	switch {
	case errors.Is(err, account.ErrInvalidMFACode):
		return p.renderMFA(w, r, mfaData{Error: "验证码错误"})
	case err != nil && !errors.Is(err, account.ErrMFANotEnabled):
		return err
	}
	return p.renderMFA(w, r, mfaData{Codes: codes})
}

func (p *Pages) renderAPIKeys(w http.ResponseWriter, r *http.Request, d apiKeysData) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	var err error
	if d.Keys, err = p.Users.APIKeys(r.Context(), pr.ID); err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, apiKeysCard(d))
}

func (p *Pages) createAPIKey(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	name := r.FormValue("name")
	if name == "" {
		return p.renderAPIKeys(w, r, apiKeysData{Error: "请填写名称"})
	}
	_, token, err := p.Users.CreateAPIKey(r.Context(), pr.ID, name, nil, time.Time{})
	if err != nil {
		return err
	}
	return p.renderAPIKeys(w, r, apiKeysData{NewToken: token})
}

func (p *Pages) revokeAPIKey(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err == nil {
		err = p.Users.RevokeAPIKey(r.Context(), pr.ID, id)
	}
	if err != nil && !errors.Is(err, account.ErrNotFound) {
		return err
	}
	return p.renderAPIKeys(w, r, apiKeysData{})
}

func (p *Pages) unlinkIdentity(w http.ResponseWriter, r *http.Request) error {
	pr, _ := auth.From[*account.Principal](r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return huma.Error404NotFound("不存在")
	}
	switch err := p.Users.UnlinkIdentity(r.Context(), pr.ID, id); {
	case errors.Is(err, account.ErrLastLoginMethod):
		return huma.Error409Conflict("这是唯一的登录方式，请先在账号设置中设置密码")
	case errors.Is(err, account.ErrNotFound):
		return huma.Error404NotFound("不存在")
	case err != nil:
		return err
	}
	items, err := p.identities(r)
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, identitiesCard(items))
}
