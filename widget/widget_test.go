package widget_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/go-tenon/tenon/widget"
)

var key = []byte(strings.Repeat("k", 32))

func TestSignVerify(t *testing.T) {
	token, err := widget.Sign(key, widget.Identity{ID: "u1", Name: "小明", Attributes: map[string]string{"plan": "pro"}}, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	id, err := widget.Verify(key, token)
	if err != nil || id.ID != "u1" || id.Name != "小明" || id.Attributes["plan"] != "pro" {
		t.Fatalf("verify: %+v %v", id, err)
	}
	// 其他租户的密钥：签名不符。
	if _, err := widget.Verify([]byte(strings.Repeat("x", 32)), token); !errors.Is(err, widget.ErrInvalid) {
		t.Errorf("other key: %v", err)
	}
	// 改动内容：签名不符。
	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + parts[1] + "x." + parts[2]
	if _, err := widget.Verify(key, tampered); !errors.Is(err, widget.ErrInvalid) {
		t.Errorf("tampered: %v", err)
	}
	for _, bad := range []string{"", "not-a-jwt", "a.b.c"} {
		if _, err := widget.Verify(key, bad); !errors.Is(err, widget.ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// sign 直接签发任意的声明，模拟其他语言的宿主后端（或攻击者）。
func sign(t *testing.T, alg jose.SignatureAlgorithm, k any, c jwt.Claims) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: k}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jwt.Signed(signer).Claims(c).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifyRejects(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) *jwt.NumericDate { return jwt.NewNumericDate(now.Add(d)) }
	for name, c := range map[string]struct {
		alg    jose.SignatureAlgorithm
		claims jwt.Claims
	}{
		"expired":       {jose.HS256, jwt.Claims{Subject: "u1", IssuedAt: at(-time.Hour), Expiry: at(-2 * time.Minute)}},
		"no expiry":     {jose.HS256, jwt.Claims{Subject: "u1", IssuedAt: at(0)}},
		"no issued at":  {jose.HS256, jwt.Claims{Subject: "u1", Expiry: at(time.Minute)}},
		"no subject":    {jose.HS256, jwt.Claims{IssuedAt: at(0), Expiry: at(time.Minute)}},
		"too long":      {jose.HS256, jwt.Claims{Subject: "u1", IssuedAt: at(0), Expiry: at(25 * time.Hour)}},
		"not yet valid": {jose.HS256, jwt.Claims{Subject: "u1", IssuedAt: at(0), Expiry: at(time.Hour), NotBefore: at(10 * time.Minute)}},
		"HS512":         {jose.HS512, jwt.Claims{Subject: "u1", IssuedAt: at(0), Expiry: at(time.Minute)}},
	} {
		k := key
		if c.alg == jose.HS512 {
			k = []byte(strings.Repeat("k", 64))
		}
		if _, err := widget.Verify(k, sign(t, c.alg, k, c.claims)); !errors.Is(err, widget.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// 一分钟以内的时钟偏差可以接受。
	ok := sign(t, jose.HS256, key, jwt.Claims{Subject: "u1", IssuedAt: at(30 * time.Second), Expiry: at(10 * time.Minute)})
	if _, err := widget.Verify(key, ok); err != nil {
		t.Errorf("clock skew: %v", err)
	}
}

func TestSignRejects(t *testing.T) {
	for name, err := range map[string]error{
		"short key": func() error {
			_, err := widget.Sign([]byte("short"), widget.Identity{ID: "u1"}, time.Minute)
			return err
		}(),
		"no id": func() error { _, err := widget.Sign(key, widget.Identity{}, time.Minute); return err }(),
		"ttl":   func() error { _, err := widget.Sign(key, widget.Identity{ID: "u1"}, 48*time.Hour); return err }(),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := widget.Verify([]byte("short"), "x"); err == nil || errors.Is(err, widget.ErrInvalid) {
		t.Errorf("short key on verify: %v", err)
	}
}
