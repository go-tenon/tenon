package oauthserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// tokenError 是令牌端点的错误响应（RFC 6749 5.2）。
type tokenError struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

// token 处理授权码换令牌。授权码在校验全部通过后与签发令牌在同一个事务中完成：
// 客户端换一种认证方式重试时授权码仍然有效，签发失败时授权码也不会提前作废。
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if err := r.ParseForm(); err != nil {
		writeTokenError(w, http.StatusBadRequest, "invalid_request", "the request body must be a form")
		return
	}
	form := r.PostForm
	if gt := form.Get("grant_type"); gt != "authorization_code" {
		writeTokenError(w, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code is supported")
		return
	}
	clientID := form.Get("client_id")
	if id, _, ok := r.BasicAuth(); ok && clientID == "" {
		// Basic 认证中的 client_id 按表单编码（RFC 6749 2.3.1），https 地址形式的 client_id 需要解码。
		if decoded, err := url.QueryUnescape(id); err == nil {
			clientID = decoded
		}
	}
	plain, verifier := form.Get("code"), form.Get("code_verifier")
	if plain == "" || clientID == "" || verifier == "" {
		writeTokenError(w, http.StatusBadRequest, "invalid_request", "code, client_id and code_verifier are required")
		return
	}

	var c code
	var sub identity.Subject
	var access string
	var firstParty bool
	var reject *tokenError
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	err := database.RunInTx(r.Context(), s.db, nil, func(ctx context.Context, tx database.Tx) error {
		err := tx.NewSelect().Model(&c).Where("code_hash = ?", hashCode(plain)).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			reject = &tokenError{"invalid_grant", "the code is invalid, expired or already used"}
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "load code")
		}
		switch {
		case !time.Now().Before(c.ExpiresAt):
			reject = &tokenError{"invalid_grant", "the code has expired"}
		case c.ClientID != clientID:
			reject = &tokenError{"invalid_grant", "the code was issued to another client"}
		case form.Get("redirect_uri") != c.RedirectURI:
			reject = &tokenError{"invalid_grant", "redirect_uri does not match the authorization request"}
		case !pkce(verifier, c.CodeChallenge):
			reject = &tokenError{"invalid_grant", "code_verifier does not match the code challenge"}
		case form.Get("resource") != "" && form.Get("resource") != c.Resource:
			reject = &tokenError{"invalid_target", "resource does not match the authorization request"}
		}
		if reject != nil {
			return nil
		}
		if _, err := tx.NewDelete().Model(&c).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "consume code")
		}
		sub = identity.Subject{
			Type: identity.SubjectType(c.SubjectType), ID: c.SubjectID, Tenant: c.Tenant,
			Impersonator: identity.Ref{Type: identity.SubjectType(c.ImpersonatorType), ID: c.ImpersonatorID},
		}
		tokens := s.tokens.WithDB(tx)
		if cl, ok := s.clients[c.ClientID]; ok && cl.FirstParty {
			firstParty = true
			access, err = tokens.Login(ctx, sub, apitoken.Device{Remember: true, IP: ip, UserAgent: r.UserAgent()})
			return errors.Wrap(err, "issue login token")
		}
		access, err = tokens.Authorize(ctx, sub, apitoken.Grant{
			ClientID: c.ClientID, ClientName: c.ClientName, Resource: c.Resource,
			Scopes:   strings.Fields(c.Scopes),
			Lifetime: s.opts.TokenLifetime, IdleTimeout: s.opts.TokenIdleTimeout,
		})
		return errors.Wrap(err, "issue oauth token")
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), "oauth token failed", "error", err)
		writeTokenError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if reject != nil {
		writeTokenError(w, http.StatusBadRequest, reject.Error, reject.Description)
		return
	}
	if firstParty {
		s.logger.InfoContext(r.Context(), "oauth sign-in", "client_id", c.ClientID, "subject", sub.Ref().String())
		writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "token_type": "Bearer"})
		return
	}
	s.logger.InfoContext(r.Context(), "oauth token issued", "client_id", c.ClientID, "subject", sub.Ref().String(), "resource", c.Resource)
	expiresIn := s.opts.TokenIdleTimeout
	if expiresIn <= 0 || expiresIn > s.opts.TokenLifetime {
		expiresIn = s.opts.TokenLifetime
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access,
		"token_type":   "Bearer",
		"expires_in":   int64(expiresIn / time.Second),
		"scope":        c.Scopes,
	})
}

// revoke 撤销客户端交回的令牌（RFC 7009）。令牌无效时同样返回 200。
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeTokenError(w, http.StatusBadRequest, "invalid_request", "the request body must be a form")
		return
	}
	if err := s.tokens.RevokeToken(r.Context(), r.PostForm.Get("token")); err != nil {
		s.logger.ErrorContext(r.Context(), "oauth revoke failed", "error", err)
		writeTokenError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// pkce 报告 verifier 的 S256 是否等于 challenge（RFC 7636）。
func pkce(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

// writeTokenError 输出 OAuth 的错误响应。
func writeTokenError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, tokenError{Error: code, Description: description})
}

// writeJSON 以状态码 status 输出 JSON。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
