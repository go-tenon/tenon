package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"uuid"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/support/random"
)

// API Key 最近使用时间最多每隔这么久更新一次。
const apiKeyTouchInterval = time.Minute

// CreateAPIKey 为用户创建 API Key，返回记录与 token 原文（只返回这一次）。
// scopes 为空表示不限；expiresAt 为零值表示永不过期。
// 产品在接口的 Security 中声明 scope（rest.RequireAuth("order:read")），API Key 只能访问具备的 scope。
func (s *Service) CreateAPIKey(ctx context.Context, userID uuid.UUID, name string, scopes []string, expiresAt time.Time) (*APIKey, string, error) {
	token := s.cfg.APIKeyPrefix + "_" + random.Token(32)
	if scopes == nil {
		scopes = []string{}
	}
	k := &APIKey{
		UserID:    userID,
		Name:      strings.TrimSpace(name),
		TokenHash: random.HashToken(token),
		Hint:      token[len(token)-4:],
		Scopes:    scopes,
		ExpiresAt: expiresAt,
	}
	if _, err := s.db.NewInsert().Model(k).Exec(ctx); err != nil {
		return nil, "", fmt.Errorf("account: create api key: %w", err)
	}
	s.emit(ctx, Event{Type: EventAPIKeyCreated, UserID: userID, SubjectID: k.ID})
	return k, token, nil
}

// APIKeys 返回用户的 API Key，最新创建的在前。
func (s *Service) APIKeys(ctx context.Context, userID uuid.UUID) ([]APIKey, error) {
	list := []APIKey{}
	if err := s.db.NewSelect().Model(&list).Where("user_id = ?", userID).Order("id DESC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("account: list api keys: %w", err)
	}
	return list, nil
}

// RevokeAPIKey 删除用户的 API Key，立即失效。不存在时返回 ErrNotFound。
func (s *Service) RevokeAPIKey(ctx context.Context, userID, keyID uuid.UUID) error {
	res, err := s.db.NewDelete().Model((*APIKey)(nil)).Where("id = ? AND user_id = ?", keyID, userID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("account: revoke api key: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.emit(ctx, Event{Type: EventAPIKeyRevoked, UserID: userID, SubjectID: keyID})
	return nil
}

func (s *Service) verifyAPIKey(ctx context.Context, token string) (*Principal, error) {
	k := new(APIKey)
	now := s.now()
	err := s.db.NewSelect().Model(k).
		Where("k.token_hash = ?", random.HashToken(token)).
		Where("k.expires_at IS NULL OR k.expires_at > ?", now).
		Where("EXISTS (SELECT 1 FROM users WHERE users.id = k.user_id AND users.disabled_at IS NULL)").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("account: verify api key: %w", err)
	}
	if now.Sub(k.LastUsedAt) > apiKeyTouchInterval {
		if _, err := s.db.NewUpdate().Model(k).Set("last_used_at = ?", now).WherePK().Exec(ctx); err != nil {
			slog.WarnContext(ctx, "touch api key", "err", err)
		}
	}
	return &Principal{ID: k.UserID, APIKeyID: k.ID, Scopes: k.Scopes}, nil
}
