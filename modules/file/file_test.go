package file_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/database/dbtest"
	"github.com/ryan053857/go-kit/httpx/rest"
	"github.com/ryan053857/go-kit/modules/file"
	"github.com/ryan053857/go-kit/storage"
)

func newService(t *testing.T, cfg file.Config) (*file.Service, storage.Storage) {
	t.Helper()
	dcfg := database.DefaultConfig()
	dcfg.DSN = dbtest.DSN(t)
	db, err := database.Open(t.Context(), dcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	if _, err := database.PublishMigrations(dir, file.Migrations); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), db.DB, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	store := storage.NewLocal(t.TempDir())
	return file.NewService(db, store, cfg), store
}

func pngBytes() []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	return buf.Bytes()
}

func TestUploadAndTypes(t *testing.T) {
	cfg := file.DefaultConfig()
	cfg.MaxSize = 1000
	s, store := newService(t, cfg)
	ctx := t.Context()
	owner := uuid.NewV7()

	// 类型按内容识别：扩展名是 .txt 的 PNG 仍是 image/png。
	f, err := s.Upload(ctx, owner, "../../evil/photo.txt", bytes.NewReader(pngBytes()))
	if err != nil {
		t.Fatal(err)
	}
	if f.ContentType != "image/png" || f.Filename != "photo.txt" || f.UserID != owner || !strings.HasPrefix(f.Key, "files/") {
		t.Errorf("file = %+v", f)
	}
	// 内容只能识别为纯文本时参考扩展名。
	csv, err := s.Upload(ctx, owner, "data.csv", strings.NewReader("a,b\n1,2\n"))
	if err != nil || !strings.HasPrefix(csv.ContentType, "text/csv") {
		t.Errorf("csv = %+v, %v", csv, err)
	}
	// 伪装成 .png 的 HTML 仍识别为 text/html，下载时作为附件，不会在浏览器中执行。
	html, _ := s.Upload(ctx, owner, "x.png", strings.NewReader("<html><script>alert(1)</script></html>"))
	if html == nil || !strings.HasPrefix(html.ContentType, "text/html") {
		t.Errorf("html = %+v", html)
	}

	if _, err := s.Upload(ctx, owner, "big.bin", bytes.NewReader(make([]byte, 1001))); !errors.Is(err, file.ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}

	got, err := s.Get(ctx, f.ID)
	if err != nil || got.Key != f.Key || got.Size != f.Size {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if err := s.Delete(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, f.ID); !errors.Is(err, file.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if _, err := store.Get(ctx, f.Key); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("content not deleted: %v", err)
	}
	if err := s.Delete(ctx, f.ID); !errors.Is(err, file.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}

	// 限制类型。
	cfg.AllowedTypes = []string{"image/", "application/pdf"}
	s2, _ := newService(t, cfg)
	if _, err := s2.Upload(ctx, owner, "a.png", bytes.NewReader(pngBytes())); err != nil {
		t.Errorf("allowed image: %v", err)
	}
	if _, err := s2.Upload(ctx, owner, "a.csv", strings.NewReader("a,b")); !errors.Is(err, file.ErrTypeNotAllowed) {
		t.Errorf("csv should be rejected: %v", err)
	}
}

type ctxKey struct{}

func currentUser(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKey{}).(uuid.UUID)
	return id, ok
}

func TestEndpoints(t *testing.T) {
	s, _ := newService(t, file.DefaultConfig())
	owner, other := uuid.NewV7(), uuid.NewV7()

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler { // 用 X-User 头模拟登录
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id, err := uuid.Parse(r.Header.Get("X-User")); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, id))
			}
			next.ServeHTTP(w, r)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("test", "1"))
	rest.Post(api, "/files", file.UploadEndpoint(s, currentUser), func(o *huma.Operation) { o.Security = nil })
	r.Get("/files/{id}", s.DownloadHandler(file.OwnerOnly(currentUser)).ServeHTTP)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "报告 2026.pdf")
	_, _ = fw.Write([]byte("%PDF-1.4 fake"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/files", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-User", owner.String())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"content_type":"application/pdf"`) {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body)
	}
	id := rec.Body.String()[strings.Index(rec.Body.String(), `"id":"`)+6:][:36]

	get := func(user uuid.UUID, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/files/"+id, nil)
		req.Header.Set("X-User", user.String())
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec = get(owner)
	if rec.Code != 200 || rec.Body.String() != "%PDF-1.4 fake" || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("download = %d %v", rec.Code, rec.Header())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") || !strings.Contains(cd, "filename*=utf-8''") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if rec := get(owner, "If-None-Match", rec.Header().Get("ETag")); rec.Code != http.StatusNotModified {
		t.Errorf("conditional get = %d", rec.Code)
	}
	if rec := get(other); rec.Code != 404 {
		t.Errorf("other user = %d", rec.Code)
	}
}
