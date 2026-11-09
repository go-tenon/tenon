package account

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/httpx/auth"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/storage"
)

func pngImage(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			if x < w/2 {
				img.Set(x, y, color.NRGBA{R: 255, A: 255})
			} // 右半边透明
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAvatars(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u := e.register(t, "a@example.com", "password1")
	dir := t.TempDir()
	avatars := NewAvatars(e.svc, storage.NewLocal(dir))

	if _, err := avatars.Set(ctx, u.ID, strings.NewReader("not an image")); !errors.Is(err, ErrInvalidImage) {
		t.Errorf("invalid image: %v", err)
	}

	got, err := avatars.Set(ctx, u.ID, bytes.NewReader(pngImage(t, 400, 200)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.AvatarKey, "avatars/"+u.ID.String()+"/") || e.svc.AvatarURL(got.AvatarKey) != "/"+got.AvatarKey {
		t.Fatalf("avatar key = %q, url = %q", got.AvatarKey, e.svc.AvatarURL(got.AvatarKey))
	}
	data, err := os.ReadFile(filepath.Join(dir, got.AvatarKey))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 256 || b.Dy() != 256 {
		t.Errorf("size = %v", b)
	}
	// 居中裁剪 400×200：左边是红色，右边透明部分填充为白色。
	if r, g, b, _ := img.At(10, 128).RGBA(); r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
		t.Errorf("left pixel = %d %d %d", r>>8, g>>8, b>>8)
	}
	if r, g, b, _ := img.At(245, 128).RGBA(); r>>8 < 230 || g>>8 < 230 || b>>8 < 230 {
		t.Errorf("right pixel = %d %d %d, want white", r>>8, g>>8, b>>8)
	}

	// Handler 提供头像。
	h := avatars.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+got.AvatarKey, nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(rec.Body.Bytes(), data) {
		t.Errorf("handler = %d %v", rec.Code, rec.Header())
	}
	for _, path := range []string{"/avatars/missing.jpg", "/files/x", "/avatars/../secret"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 404 {
			t.Errorf("GET %s = %d", path, rec.Code)
		}
	}

	// 替换头像后删除旧文件；删除头像后文件也被删除。
	old := got.AvatarKey
	got, err = avatars.Set(ctx, u.ID, bytes.NewReader(pngImage(t, 50, 80)))
	if err != nil || got.AvatarKey == old {
		t.Fatalf("replace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, old)); !os.IsNotExist(err) {
		t.Errorf("old avatar not deleted: %v", err)
	}
	got, err = avatars.Remove(ctx, u.ID)
	if err != nil || got.AvatarKey != "" || e.svc.AvatarURL(got.AvatarKey) != "" {
		t.Fatalf("remove = %+v, %v", got, err)
	}
	if reloaded, _ := e.svc.User(ctx, u.ID); reloaded.AvatarKey != "" {
		t.Errorf("avatar key not cleared in db")
	}
}

func TestAvatarEndpointAndAccessToken(t *testing.T) {
	e := newEnv(t)
	e.register(t, "a@example.com", "password1")
	avatars := NewAvatars(e.svc, storage.NewLocal(t.TempDir()))
	tok, _ := e.svc.Login(t.Context(), "a@example.com", "password1", false, ClientInfo{})

	r := chi.NewRouter()
	r.Use(e.svc.LoadAccessToken)
	api := humachi.New(r, huma.DefaultConfig("test", "1"))
	api.UseMiddleware(rest.Authorize(api, e.svc.VerifyAccessToken, nil))
	rest.Put(api, "/me/avatar", SetAvatarEndpoint(avatars))
	rest.Delete(api, "/me/avatar", RemoveAvatarEndpoint(avatars))
	r.Get("/whoami", func(w http.ResponseWriter, r *http.Request) {
		if id, ok := auth.UserID(r.Context()); ok {
			_, _ = w.Write([]byte(id.String()))
		}
	})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "me.png")
	_, _ = fw.Write(pngImage(t, 100, 100))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPut, "/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"avatar_url":"/avatars/`) {
		t.Fatalf("set avatar = %d %s", rec.Code, rec.Body)
	}

	// LoadAccessToken 让普通路由也能识别 Bearer token；无效 token 按未登录处理。
	for token, want := range map[string]string{tok.AccessToken: tok.UserID.String(), "bogus": ""} {
		req = httptest.NewRequest(http.MethodGet, "/whoami", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Body.String() != want {
			t.Errorf("whoami(%s) = %q, want %q", token[:5], rec.Body, want)
		}
	}
}
