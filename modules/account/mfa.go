package account

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"image/png"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/ryan053857/go-kit/support/random"
)

const (
	totpPeriod        = 30
	mfaChallengeTTL   = 5 * time.Minute
	recoveryCodeCount = 10
)

// MFARequiredError 由 Login 返回，表示密码正确但账号启用了两步验证：
// 客户端需要让用户输入验证码，再用 Challenge 调用 LoginMFA 完成登录。Challenge 5 分钟内有效。
//
//	var mfaErr *account.MFARequiredError
//	if errors.As(err, &mfaErr) { ... mfaErr.Challenge ... }
type MFARequiredError struct {
	UserID    uuid.UUID
	Challenge string
}

func (e *MFARequiredError) Error() string { return "account: mfa required" }

// MFASetup 是开始绑定两步验证时返回给用户的信息。
type MFASetup struct {
	Secret string // Base32 密钥，供无法扫码时手动输入
	URL    string // otpauth:// 地址，生成二维码给验证器 App 扫描（见 MFAQRCode）
}

// MFAStatus 是账号的两步验证状态。
type MFAStatus struct {
	Enabled       bool
	EnabledAt     time.Time
	RecoveryCodes int // 剩余可用的恢复码数量
}

// MFAStatus 返回账号的两步验证状态。
func (s *Service) MFAStatus(ctx context.Context, userID uuid.UUID) (MFAStatus, error) {
	m, err := s.loadMFA(ctx, s.db, userID, false)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && m.EnabledAt.IsZero()) {
		return MFAStatus{}, nil
	}
	if err != nil {
		return MFAStatus{}, errors.Wrap(err, "account: mfa status")
	}
	return MFAStatus{Enabled: true, EnabledAt: m.EnabledAt, RecoveryCodes: len(m.RecoveryCodes)}, nil
}

// SetupMFA 为账号生成新的 TOTP 密钥（尚未启用），用户用验证器 App 扫码后调用 EnableMFA 确认。
// 已启用时返回 ErrMFAEnabled。重复调用会生成新密钥，之前的密钥作废。
func (s *Service) SetupMFA(ctx context.Context, userID uuid.UUID) (*MFASetup, error) {
	u, err := s.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: s.cfg.Issuer, AccountName: u.Email, Period: totpPeriod})
	if err != nil {
		return nil, errors.Wrap(err, "account: setup mfa")
	}
	now := s.now()
	m := &mfa{UserID: userID, CreatedAt: now, UpdatedAt: now, Secret: s.cipher.EncryptString(key.Secret()), RecoveryCodes: []string{}}
	res, err := s.db.NewInsert().Model(m).
		On("CONFLICT (user_id) DO UPDATE").
		Set("secret = EXCLUDED.secret, updated_at = EXCLUDED.updated_at, last_step = 0").
		Where("m.enabled_at IS NULL").
		Exec(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "account: setup mfa")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrMFAEnabled
	}
	return &MFASetup{Secret: key.Secret(), URL: key.URL()}, nil
}

// MFAQRCode 把 MFASetup.URL 编码为 size×size 的 PNG 二维码。
func MFAQRCode(url string, size int) ([]byte, error) {
	key, err := otp.NewKeyFromURL(url)
	if err != nil {
		return nil, err
	}
	img, err := key.Image(size, size)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EnableMFA 用验证器 App 显示的验证码确认并启用两步验证，返回一次性的恢复码（只返回这一次，请提示用户保存）。
// 未调用 SetupMFA 时返回 ErrMFANotEnabled；已启用时返回 ErrMFAEnabled；验证码错误时返回 ErrInvalidMFACode。
func (s *Service) EnableMFA(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	var codes []string
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m, err := s.loadMFA(ctx, tx, userID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMFANotEnabled
		}
		if err != nil {
			return err
		}
		if !m.EnabledAt.IsZero() {
			return ErrMFAEnabled
		}
		if !s.checkTOTP(m, code) {
			return ErrInvalidMFACode
		}
		var hashes []string
		codes, hashes = newRecoveryCodes()
		m.RecoveryCodes = hashes
		m.EnabledAt = s.now()
		m.UpdatedAt = m.EnabledAt
		_, err = tx.NewUpdate().Model(m).Column("recovery_codes", "last_step", "enabled_at", "updated_at").WherePK().Exec(ctx)
		return err
	})
	if err != nil {
		return nil, mfaError("enable mfa", err)
	}
	s.emit(ctx, Event{Type: EventMFAEnabled, UserID: userID})
	return codes, nil
}

// DisableMFA 校验验证码（或恢复码）后关闭两步验证。
func (s *Service) DisableMFA(ctx context.Context, userID uuid.UUID, code string) error {
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := s.verifyMFA(ctx, tx, userID, code); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model((*mfa)(nil)).Where("user_id = ?", userID).Exec(ctx)
		return err
	})
	if err != nil {
		return mfaError("disable mfa", err)
	}
	s.emit(ctx, Event{Type: EventMFADisabled, UserID: userID})
	return nil
}

// RegenerateRecoveryCodes 校验验证码后生成一组新的恢复码，旧恢复码全部作废。
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	var codes []string
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := s.verifyMFA(ctx, tx, userID, code); err != nil {
			return err
		}
		var hashes []string
		codes, hashes = newRecoveryCodes()
		_, err := tx.NewUpdate().Model((*mfa)(nil)).Set("recovery_codes = ?", pgdialect.Array(hashes)).
			Set("updated_at = ?", s.now()).Where("user_id = ?", userID).Exec(ctx)
		return err
	})
	if err != nil {
		return nil, mfaError("regenerate recovery codes", err)
	}
	return codes, nil
}

// VerifyMFA 校验账号的两步验证码：6 位 TOTP 验证码或一个恢复码（用过即作废）。
// 同一个 TOTP 验证码只能使用一次。未启用时返回 ErrMFANotEnabled，错误时返回 ErrInvalidMFACode。
func (s *Service) VerifyMFA(ctx context.Context, userID uuid.UUID, code string) error {
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return s.verifyMFA(ctx, tx, userID, code)
	})
	return mfaError("verify mfa", err)
}

func (s *Service) verifyMFA(ctx context.Context, tx bun.Tx, userID uuid.UUID, code string) error {
	m, err := s.loadMFA(ctx, tx, userID, true)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && m.EnabledAt.IsZero()) {
		return ErrMFANotEnabled
	}
	if err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	if s.checkTOTP(m, code) {
		_, err = tx.NewUpdate().Model(m).Column("last_step").WherePK().Exec(ctx)
		return err
	}
	hash := random.HashToken(normalizeRecoveryCode(code))
	if i := slices.Index(m.RecoveryCodes, hash); i >= 0 && len(code) > 6 {
		m.RecoveryCodes = slices.Delete(m.RecoveryCodes, i, i+1)
		_, err = tx.NewUpdate().Model(m).Column("recovery_codes").WherePK().Exec(ctx)
		return err
	}
	return ErrInvalidMFACode
}

// checkTOTP 校验验证码，允许前后各一个时间步的误差；通过时把 m.LastStep 更新为所用的时间步。
func (s *Service) checkTOTP(m *mfa, code string) bool {
	if len(code) != 6 {
		return false
	}
	secret, err := s.cipher.DecryptString(m.Secret)
	if err != nil {
		return false
	}
	now := s.now()
	step := now.Unix() / totpPeriod
	for _, d := range []int64{0, -1, 1} {
		if step+d <= m.LastStep {
			continue
		}
		want, err := totp.GenerateCodeCustom(secret, time.Unix((step+d)*totpPeriod, 0),
			totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			m.LastStep = step + d
			return true
		}
	}
	return false
}

func (s *Service) loadMFA(ctx context.Context, db bun.IDB, userID uuid.UUID, lock bool) (*mfa, error) {
	m := new(mfa)
	q := db.NewSelect().Model(m).Where("user_id = ?", userID)
	if lock {
		q = q.For("UPDATE")
	}
	return m, q.Scan(ctx)
}

// newRecoveryCodes 生成恢复码（形如 abcde-12345）及其哈希。
func newRecoveryCodes() (codes, hashes []string) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	for range recoveryCodeCount {
		c := random.String(5, alphabet) + "-" + random.String(5, alphabet)
		codes = append(codes, c)
		hashes = append(hashes, random.HashToken(normalizeRecoveryCode(c)))
	}
	return codes, hashes
}

func normalizeRecoveryCode(c string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(c)), "-", "")
}

func mfaError(op string, err error) error {
	if err == nil || errors.Is(err, ErrInvalidMFACode) || errors.Is(err, ErrMFAEnabled) ||
		errors.Is(err, ErrMFANotEnabled) || errors.Is(err, ErrNotFound) {
		return err
	}
	return errors.Wrapf(err, "account: %s", op)
}

// mfaClaims 是两步验证 challenge 的内容，用单独派生的密钥签名，不能当作 access token 使用。
type mfaClaims struct {
	jwt.RegisteredClaims
	Remember bool `json:"rem,omitempty"`
}

func (s *Service) mfaChallenge(userID uuid.UUID, remember bool) (string, error) {
	now := s.now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, mfaClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(mfaChallengeTTL)),
		},
		Remember: remember,
	}).SignedString(deriveKey(s.cfg.JWTSecret, "mfa-challenge"))
}

// LoginMFA 完成两步验证登录：challenge 来自 Login 返回的 MFARequiredError，code 为验证码或恢复码。
// challenge 无效或过期时返回 ErrInvalidToken，验证码错误时返回 ErrInvalidMFACode。
func (s *Service) LoginMFA(ctx context.Context, challenge, code string, client ClientInfo) (*Tokens, error) {
	claims := new(mfaClaims)
	_, err := jwt.ParseWithClaims(challenge, claims,
		func(*jwt.Token) (any, error) { return deriveKey(s.cfg.JWTSecret, "mfa-challenge"), nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil {
		return nil, ErrInvalidToken
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, ErrInvalidToken
	}
	u, err := s.User(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if u.Disabled() {
		return nil, ErrUserDisabled
	}
	if err := s.VerifyMFA(ctx, userID, code); err != nil {
		if errors.Is(err, ErrInvalidMFACode) {
			s.emit(ctx, Event{Type: EventLoginFailed, UserID: userID, Email: u.Email})
		}
		return nil, err
	}
	return s.startAPISession(ctx, u, claims.Remember, client)
}
