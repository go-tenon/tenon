// Package widget 是嵌入第三方网站的端（例如客服挂件）的服务端部分：宿主网站上已登录的用户身份。
//
// 宿主网站的用户已经登录时，宿主的后端用租户的密钥对用户身份签名（HS256 JWT），页面把签名交给挂件
// （@go-tenon/embed 的 createWidget 的 identity 或 login），挂件把它交给应用的接口换取访客令牌。
// 签名只能由持有密钥的宿主后端生成，访客无法冒充他人。宿主的后端可以用任何语言的 JWT 库签发，
// 用 Go 时直接调用 Sign。
//
//	// 宿主的后端
//	token, err := widget.Sign(tenantSecret, widget.Identity{ID: user.ID, Name: user.Name}, 10*time.Minute)
//
//	// 应用换取访客令牌的接口
//	id, err := widget.Verify(tenant.WidgetSecret, in.Body.Identity)
package widget

import (
	"time"

	"github.com/cockroachdb/errors"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/go-tenon/tenon/errorx"
)

// ErrInvalid 表示身份签名无效、已过期或有效期太长，响应 401。
var ErrInvalid = errorx.Define(errorx.Unauthenticated, "widget.invalid_identity")

// MinKeySize 是签名密钥的最小长度（字节）。
const MinKeySize = 32

// MaxLifetime 是签名的最长有效期：过期时间与签发时间的间隔超过它时 Verify 拒绝，
// 防止宿主签发长期有效的身份，被截获后长期冒用。宿主应当每次打开页面时签发。
const MaxLifetime = 24 * time.Hour

// leeway 是允许的时钟偏差。
const leeway = time.Minute

// Identity 是宿主网站上已登录的用户。ID 是宿主网站中的用户 ID，其余字段由宿主决定是否提供。
type Identity struct {
	ID    string `json:"sub"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	// Attributes 是宿主附带的其他信息，例如会员等级，显示给客服。
	Attributes map[string]string `json:"attributes,omitempty"`
}

type claims struct {
	jwt.Claims
	Name       string            `json:"name,omitempty"`
	Email      string            `json:"email,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Sign 用 key 对 id 签名，ttl 后过期（不超过 MaxLifetime）。
func Sign(key []byte, id Identity, ttl time.Duration) (string, error) {
	if len(key) < MinKeySize {
		return "", errors.Newf("widget: key must be at least %d bytes", MinKeySize)
	}
	if id.ID == "" {
		return "", errors.New("widget: identity has no ID")
	}
	if ttl <= 0 || ttl > MaxLifetime {
		return "", errors.Newf("widget: ttl must be in (0, %s]", MaxLifetime)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", errors.Wrap(err, "widget: signer")
	}
	now := time.Now()
	c := claims{
		Claims: jwt.Claims{Subject: id.ID, IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(ttl))},
		Name:   id.Name, Email: id.Email, Attributes: id.Attributes,
	}
	token, err := jwt.Signed(signer).Claims(c).Serialize()
	return token, errors.Wrap(err, "widget: sign")
}

// Verify 校验 token 的签名和有效期，返回身份。签名算法必须是 HS256，必须有 sub、iat 和 exp，
// exp 与 iat 的间隔不超过 MaxLifetime；允许一分钟的时钟偏差。不符合时返回 ErrInvalid。
func Verify(key []byte, token string) (Identity, error) {
	if len(key) < MinKeySize {
		return Identity{}, errors.Newf("widget: key must be at least %d bytes", MinKeySize)
	}
	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return Identity{}, ErrInvalid.New()
	}
	var c claims
	if err := tok.Claims(key, &c); err != nil {
		return Identity{}, ErrInvalid.New()
	}
	if c.Subject == "" || c.IssuedAt == nil || c.Expiry == nil || c.Expiry.Time().Sub(c.IssuedAt.Time()) > MaxLifetime {
		return Identity{}, ErrInvalid.New()
	}
	if err := c.ValidateWithLeeway(jwt.Expected{Time: time.Now()}, leeway); err != nil {
		return Identity{}, ErrInvalid.New()
	}
	return Identity{ID: c.Subject, Name: c.Name, Email: c.Email, Attributes: c.Attributes}, nil
}
