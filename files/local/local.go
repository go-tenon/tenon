// Package local 为 files.Service 提供本地文件和持久化分片存储。
package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/ryan053857/mygoravel/files"
)

const partHeaderSize = sha256.Size*2 + 1

// Config 是本地存储配置。Root 目录必须由应用独占管理。
type Config struct {
	Root string `yaml:"root"`
}

// Validate 检查存储根目录已配置。
func (c Config) Validate() error {
	if strings.TrimSpace(c.Root) == "" {
		return errors.New("root is required")
	}
	return nil
}

// Store 将对象和分片隔离在不同根目录下。
// 同一实例支持并发操作；多个实例或进程操作同一会话时，调用方须通过数据库锁协调。
// Close 应在所有操作和读流结束后调用。
type Store struct {
	objects *os.Root
	uploads *os.Root
	mu      sync.Mutex
	locks   map[string]*uploadLock
}

type uploadLock struct {
	sync.RWMutex
	refs int
}

type metadata struct {
	Key       string `json:"key"`
	Completed bool   `json:"completed,omitempty"`
}

var _ files.Backend = (*Store)(nil)

// Open 创建并打开存储根目录；不读取应用配置，也不创建数据库表。
func Open(cfg Config) (*Store, error) {
	if err := cfg.Validate(); err != nil {
		return nil, errors.Wrap(err, "local")
	}
	if err := os.MkdirAll(cfg.Root, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(cfg.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, dir := range []string{"objects", "multipart"} {
		if err := root.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		info, err := root.Lstat(dir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.Newf("local: %s must be a directory, not a symlink", dir)
		}
	}
	objects, err := root.OpenRoot("objects")
	if err != nil {
		return nil, err
	}
	uploads, err := root.OpenRoot("multipart")
	if err != nil {
		objects.Close()
		return nil, err
	}
	return &Store{objects: objects, uploads: uploads, locks: make(map[string]*uploadLock)}, nil
}

// Close 释放目录句柄，保留对象和未完成分片。
func (s *Store) Close() error { return errors.Join(s.objects.Close(), s.uploads.Close()) }

// Put 流式写入对象；大小不符或读取失败时保留原对象。
func (s *Store) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if body == nil || size < 0 {
		return files.ErrInvalid
	}
	return atomicWrite(ctx, s.objects, key, func(f *os.File) error {
		return copyExact(ctx, f, body, size)
	})
}

// Open 返回对象读取流；调用方负责 Close。
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := s.objects.Open(key)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = files.ErrInvalid
		}
		return nil, err
	}
	return &contextReadCloser{ctx: ctx, ReadCloser: f}, nil
}

// Stat 返回对象大小；内容类型由 files 表保存。
func (s *Store) Stat(ctx context.Context, key string) (files.Object, error) {
	if err := validateKey(key); err != nil {
		return files.Object{}, err
	}
	if err := ctx.Err(); err != nil {
		return files.Object{}, err
	}
	info, err := s.objects.Stat(key)
	if err != nil {
		return files.Object{}, err
	}
	if !info.Mode().IsRegular() {
		return files.Object{}, files.ErrInvalid
	}
	return files.Object{Size: info.Size()}, nil
}

// Delete 删除单个对象；不存在视为成功。
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := s.objects.Lstat(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return files.ErrInvalid
	}
	err = s.objects.Remove(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Copy 复制对象到独立目标键，发布前不会暴露部分内容。
func (s *Store) Copy(ctx context.Context, source, destination string) error {
	if err := validateKey(destination); err != nil {
		return err
	}
	body, err := s.Open(ctx, source)
	if err != nil {
		return err
	}
	defer body.Close()
	return atomicWrite(ctx, s.objects, destination, func(f *os.File) error {
		_, err := io.Copy(f, body)
		return err
	})
}

// CreateMultipart 创建绑定 key 的分片会话，元数据落盘后返回会话 ID。
func (s *Store) CreateMultipart(ctx context.Context, key, contentType string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id := uuid.NewString()
	if err := s.uploads.Mkdir(id, 0o700); err != nil {
		return "", err
	}
	if err := s.writeMetadata(ctx, id, metadata{Key: key}); err != nil {
		return "", errors.Join(err, s.uploads.RemoveAll(id))
	}
	return id, nil
}

// UploadPart 原子替换指定分片，允许乱序上传及同一片号重传。
func (s *Store) UploadPart(ctx context.Context, key, uploadID string, number int32, body io.Reader, size int64) (files.Part, error) {
	if number < 1 || number > files.MaxParts || size < 0 || size > files.MaxPartSize || body == nil {
		return files.Part{}, files.ErrInvalid
	}
	unlock := s.lock(uploadID, false)
	defer unlock()
	if err := s.session(ctx, key, uploadID); err != nil {
		return files.Part{}, err
	}
	hash := sha256.New()
	var etag string
	err := atomicWrite(ctx, s.uploads, partPath(uploadID, number), func(f *os.File) error {
		if _, err := f.Write(make([]byte, partHeaderSize)); err != nil {
			return err
		}
		if err := copyExact(ctx, io.MultiWriter(f, hash), body, size); err != nil {
			return err
		}
		etag = hex.EncodeToString(hash.Sum(nil))
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		_, err := io.WriteString(f, etag+"\n")
		return err
	})
	return files.Part{Number: number, ETag: etag, Size: size}, err
}

// ListParts 返回所有已完整写入的分片，按编号排序。
func (s *Store) ListParts(ctx context.Context, key, uploadID string) ([]files.Part, error) {
	unlock := s.lock(uploadID, false)
	defer unlock()
	if err := s.session(ctx, key, uploadID); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(s.uploads.FS(), uploadID)
	if err != nil {
		return nil, err
	}
	parts := make([]files.Part, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(entry.Name(), ".part"), 10, 32)
		if err != nil || n < 1 || n > files.MaxParts {
			return nil, errors.New("local: invalid stored part number")
		}
		f, part, err := s.openPart(uploadID, int32(n))
		if err != nil {
			return nil, err
		}
		f.Close()
		parts = append(parts, part)
	}
	slices.SortFunc(parts, func(a, b files.Part) int { return int(a.Number - b.Number) })
	return parts, nil
}

// CompleteMultipart 校验分片并流式合并，成功发布后移除会话。
// 发布后先记录完成标记再清理。清理中断后，ListParts 返回 ErrUploadNotFound，
// 文件服务通过正式上传对象恢复完成状态，再由 AbortMultipart 回收残留目录。
func (s *Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []files.Part) error {
	if len(parts) == 0 || len(parts) > files.MaxParts {
		return files.ErrInvalid
	}
	parts = slices.Clone(parts)
	slices.SortFunc(parts, func(a, b files.Part) int { return int(a.Number - b.Number) })
	for i, part := range parts {
		if part.Number < 1 || part.Number > files.MaxParts || part.ETag == "" || (i > 0 && part.Number == parts[i-1].Number) {
			return files.ErrInvalid
		}
	}
	unlock := s.lock(uploadID, true)
	defer unlock()
	if err := s.session(ctx, key, uploadID); err != nil {
		return err
	}
	err := atomicWrite(ctx, s.objects, key, func(out *os.File) error {
		for i, want := range parts {
			f, got, err := s.openPart(uploadID, want.Number)
			if err != nil {
				return err
			}
			if want.ETag != got.ETag || got.Size > files.MaxPartSize || (i < len(parts)-1 && got.Size < files.MinPartSize) {
				f.Close()
				return errors.Wrap(files.ErrInvalid, "part ETag or size mismatch")
			}
			hash := sha256.New()
			err = copyExact(ctx, io.MultiWriter(out, hash), f, got.Size)
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				return errors.Join(err, closeErr)
			}
			if hex.EncodeToString(hash.Sum(nil)) != got.ETag {
				return errors.Wrap(files.ErrInvalid, "stored part checksum mismatch")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 必须先标记完成，再删除任何分片；否则部分删除后无法区分已完成与缺片。
	if err := s.writeMetadata(ctx, uploadID, metadata{Key: key, Completed: true}); err != nil {
		return err
	}
	return s.uploads.RemoveAll(uploadID)
}

// AbortMultipart 删除会话及其分片；会话不存在视为成功。
func (s *Store) AbortMultipart(ctx context.Context, key, uploadID string) error {
	unlock := s.lock(uploadID, true)
	defer unlock()
	if _, err := s.readMetadata(ctx, key, uploadID); err != nil && !errors.Is(err, files.ErrUploadNotFound) {
		return err
	}
	// 元数据可能在上次 RemoveAll 中先被删除，仍须继续回收剩余分片。
	return s.uploads.RemoveAll(uploadID)
}

func validateKey(key string) error {
	if key == "." || !fs.ValidPath(key) || strings.ContainsAny(key, "\\\x00") {
		return files.ErrInvalid
	}
	return nil
}

func (s *Store) session(ctx context.Context, key, id string) error {
	meta, err := s.readMetadata(ctx, key, id)
	if err != nil {
		return err
	}
	if meta.Completed {
		return files.ErrUploadNotFound
	}
	return nil
}

func (s *Store) readMetadata(ctx context.Context, key, id string) (metadata, error) {
	var meta metadata
	if err := ctx.Err(); err != nil {
		return meta, err
	}
	if err := validateKey(key); err != nil {
		return meta, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return meta, files.ErrInvalid
	}
	data, err := s.uploads.ReadFile(id + "/meta.json")
	if errors.Is(err, fs.ErrNotExist) {
		return meta, files.ErrUploadNotFound
	}
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	if meta.Key != key {
		return meta, files.ErrInvalid
	}
	return meta, nil
}

func (s *Store) writeMetadata(ctx context.Context, id string, meta metadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return atomicWrite(ctx, s.uploads, id+"/meta.json", func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

func partPath(id string, number int32) string { return fmt.Sprintf("%s/%05d.part", id, number) }

func (s *Store) openPart(id string, number int32) (*os.File, files.Part, error) {
	f, err := s.uploads.Open(partPath(id, number))
	if err != nil {
		return nil, files.Part{}, err
	}
	info, err := f.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Size() < partHeaderSize) {
		err = errors.New("local: invalid part file")
	}
	var header [partHeaderSize]byte
	if err == nil {
		_, err = io.ReadFull(f, header[:])
	}
	if err == nil {
		_, err = hex.DecodeString(string(header[:partHeaderSize-1]))
	}
	if err != nil {
		f.Close()
		return nil, files.Part{}, err
	}
	return f, files.Part{Number: number, ETag: string(header[:partHeaderSize-1]), Size: info.Size() - partHeaderSize}, nil
}

// lock 在使用期间保留锁条目，避免同一会话出现两个不同锁。
func (s *Store) lock(id string, exclusive bool) func() {
	s.mu.Lock()
	l := s.locks[id]
	if l == nil {
		l = &uploadLock{}
		s.locks[id] = l
	}
	l.refs++
	s.mu.Unlock()
	if exclusive {
		l.Lock()
	} else {
		l.RLock()
	}
	return func() {
		if exclusive {
			l.Unlock()
		} else {
			l.RUnlock()
		}
		s.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(s.locks, id)
		}
		s.mu.Unlock()
	}
}

func atomicWrite(ctx context.Context, root *os.Root, name string, write func(*os.File) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := path.Dir(name)
	if err := root.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path.Join(dir, ".tmp-"+uuid.NewString())
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		if removeErr := root.Remove(tmp); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if err := write(f); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}

func copyExact(ctx context.Context, dst io.Writer, src io.Reader, size int64) error {
	n, err := io.Copy(dst, io.LimitReader(&contextReader{ctx: ctx, Reader: src}, size))
	if err != nil {
		return err
	}
	if n != size {
		return errors.Wrap(files.ErrInvalid, "body shorter than declared size")
	}
	var extra [1]byte
	nExtra, err := io.ReadFull(&contextReader{ctx: ctx, Reader: src}, extra[:])
	if nExtra != 0 {
		return errors.Wrap(files.ErrInvalid, "body longer than declared size")
	}
	if !errors.Is(err, io.EOF) {
		return err
	}
	return ctx.Err()
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

type contextReadCloser struct {
	ctx context.Context
	io.ReadCloser
}

func (r *contextReadCloser) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.ReadCloser.Read(p)
}
