package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Local 把对象保存在本地目录中，key 对应目录下的相对路径。
type Local struct {
	dir string
}

// NewLocal 返回保存在 dir 中的 Local，目录在首次写入时创建。
func NewLocal(dir string) *Local { return &Local{dir: dir} }

func (l *Local) path(key string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	return filepath.Join(l.dir, filepath.FromSlash(key)), nil
}

// Put 先写入同目录下的临时文件再改名，读取方不会看到写了一半的文件。
func (l *Local) Put(_ context.Context, key string, r io.ReadSeeker, _ string) (err error) {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = io.Copy(tmp, r); err != nil {
		return fmt.Errorf("storage: write %s: %w", key, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("storage: write %s: %w", key, err)
	}
	if err = os.Rename(tmp.Name(), p); err != nil {
		return fmt.Errorf("storage: write %s: %w", key, err)
	}
	return nil
}

func (l *Local) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	return f, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: %w", err)
	}
	return nil
}
