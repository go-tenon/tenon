package settings_test

import (
	"strings"
	"testing"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/settings"
)

type secretNested struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key" settings:"secret"`
}

type secretSettings struct {
	Title   string       `json:"title"`
	Token   string       `json:"token" settings:"secret"`
	Gateway secretNested `json:"gateway"`
}

func TestSecretEncryption(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, settings.Migrations())
	c := crypt.New(crypt.Config{Key: crypt.GenerateKey()})
	ctx := t.Context()

	// 未传入 WithCrypt 时包含 secret 字段必须 panic。
	var panicked bool
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_ = settings.New(db, "secret_missing_crypt", secretSettings{})
	}()
	if !panicked {
		t.Fatal("expected panic when secret fields lack WithCrypt")
	}

	// 非 string 字段标 secret 必须 panic。
	type badSecret struct {
		Count int `json:"count" settings:"secret"`
	}
	panicked = false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_ = settings.New(db, "bad_secret_type", badSecret{}, settings.WithCrypt(c))
	}()
	if !panicked {
		t.Fatal("expected panic when settings:\"secret\" is used on non-string field")
	}

	g := settings.New(db, "model", secretSettings{Title: "default"}, settings.WithCrypt(c))
	want := secretSettings{
		Title:   "prod",
		Token:   "tok-top-secret",
		Gateway: secretNested{BaseURL: "https://api.example.com", APIKey: "sk-live-123456"},
	}
	if err := g.Save(ctx, want); err != nil {
		t.Fatal(err)
	}

	// 检查数据库中落库的是密文，绝不含明文。
	var raw string
	if err := db.NewSelect().Table("settings").ColumnExpr("payload::text").
		Where("name = ? AND tenant = ''", "model").Scan(ctx, &raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "tok-top-secret") || strings.Contains(raw, "sk-live-123456") {
		t.Fatalf("plaintext secret leaked in db payload: %s", raw)
	}
	if !strings.Contains(raw, "enc:v1:") {
		t.Fatalf("expected encrypted prefix in db payload: %s", raw)
	}

	got, err := g.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// 空字符串不加密，读写正常。
	empty := secretSettings{Title: "empty"}
	if err := g.Save(ctx, empty); err != nil {
		t.Fatal(err)
	}
	got, err = g.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != empty {
		t.Fatalf("got %+v, want %+v", got, empty)
	}
}
