package account

import (
	"bytes"
	"context"
	"image"
	"image/color"
	_ "image/gif" // 注册解码器
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"uuid"

	"github.com/cockroachdb/errors"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/ryan053857/go-kit/httpx/web"
	"github.com/ryan053857/go-kit/storage"
)

const (
	avatarSize      = 256
	maxAvatarPixels = 40_000_000 // 解码前检查，避免超大图片耗尽内存
	// MaxAvatarBytes 是头像文件的大小上限。
	MaxAvatarBytes = 10 << 20
)

// Avatars 管理用户头像：上传的图片居中裁剪为 256×256 的 JPEG 保存到 storage，
// users.avatar_key 记录对象键。每次上传生成新的对象键，因此头像地址可以永久缓存。
type Avatars struct {
	users *Service
	store storage.Storage
}

// NewAvatars 返回把头像保存在 store 中的 Avatars。
func NewAvatars(users *Service, store storage.Storage) *Avatars {
	return &Avatars{users: users, store: store}
}

// AvatarURL 返回头像对象键（UserFields.AvatarKey）对应的地址，没有头像时返回空串。
func (s *Service) AvatarURL(key string) string {
	if key == "" {
		return ""
	}
	return s.cfg.AvatarBaseURL + key
}

// Set 处理并保存新头像，替换旧头像。不是支持的图片时返回 ErrInvalidImage。
func (a *Avatars) Set(ctx context.Context, userID uuid.UUID, r io.Reader) (*User, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxAvatarBytes+1))
	if err != nil {
		return nil, errors.Wrap(err, "account: read avatar")
	}
	if len(data) > MaxAvatarBytes {
		return nil, ErrInvalidImage
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 || cfg.Width*cfg.Height > maxAvatarPixels {
		return nil, ErrInvalidImage
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalidImage
	}
	out, err := encodeAvatar(src)
	if err != nil {
		return nil, err
	}

	u, err := a.users.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	old := u.AvatarKey
	u.AvatarKey = "avatars/" + userID.String() + "/" + uuid.NewV7().String() + ".jpg"
	if err := a.store.Put(ctx, u.AvatarKey, bytes.NewReader(out), "image/jpeg"); err != nil {
		return nil, err
	}
	if _, err := a.users.db.NewUpdate().Model(u).Column("avatar_key", "updated_at").WherePK().Exec(ctx); err != nil {
		_ = a.store.Delete(context.WithoutCancel(ctx), u.AvatarKey)
		return nil, errors.Wrap(err, "account: set avatar")
	}
	a.deleteObject(ctx, old)
	return u, nil
}

// Remove 删除用户头像。
func (a *Avatars) Remove(ctx context.Context, userID uuid.UUID) (*User, error) {
	u, err := a.users.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.AvatarKey == "" {
		return u, nil
	}
	old := u.AvatarKey
	u.AvatarKey = ""
	if _, err := a.users.db.NewUpdate().Model(u).Column("avatar_key", "updated_at").WherePK().Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "account: remove avatar")
	}
	a.deleteObject(ctx, old)
	return u, nil
}

// Handler 提供头像图片，挂在 /avatars/*（与 Config.AvatarBaseURL 的默认值 "/" 对应）：
//
//	a.Router.Handle("/avatars/*", avatars.Handler())
//
// 头像对所有人可见；对象键包含随机部分，地址无法猜测，内容不会改变，因此设置一年的公共缓存。
func (a *Avatars) Handler() http.Handler {
	return web.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		key := strings.TrimPrefix(r.URL.Path, "/")
		if !strings.HasPrefix(key, "avatars/") || !storage.ValidKey(key) {
			http.NotFound(w, r)
			return nil
		}
		rc, err := a.store.Get(r.Context(), key)
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return nil
		}
		if err != nil {
			return err
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, err = io.Copy(w, rc)
		return err
	})
}

func (a *Avatars) deleteObject(ctx context.Context, key string) {
	if key == "" {
		return
	}
	if err := a.store.Delete(context.WithoutCancel(ctx), key); err != nil {
		slog.WarnContext(ctx, "delete old avatar", "key", key, "err", err)
	}
}

// encodeAvatar 居中裁剪为正方形、缩放到 avatarSize，透明部分填充白色后编码为 JPEG。
func encodeAvatar(src image.Image) ([]byte, error) {
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2))

	dst := image.NewRGBA(image.Rect(0, 0, avatarSize, avatarSize))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, errors.Wrap(err, "account: encode avatar")
	}
	return buf.Bytes(), nil
}
