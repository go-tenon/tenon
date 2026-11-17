package migrate

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
)

// Publish 把 sources 中的迁移原样复制到 dir，返回本次写入的文件名。sources 是功能包的 Migrations()，
// 文件名与应用自己的迁移格式相同，例如 2026_11_10_114451_create_files.sql。
// dir 中已有的迁移跳过，所以升级框架后再次发布只复制新增的迁移；已发布的文件可以修改，但不要改名。
// 写入之前检查全部文件：文件名格式、goose 注解，以及版本不与其他迁移重复；有问题时一个文件也不写。
func Publish(dir string, sources ...fs.FS) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("migrate: publish directory is required")
	}
	if len(sources) == 0 {
		return nil, errors.New("migrate: at least one migration source is required")
	}
	existing, err := migrationFiles(dir)
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte)
	versions := make(map[int64]string)
	for i, source := range sources {
		if source == nil {
			return nil, errors.Newf("migrate: source %d is nil", i)
		}
		names, err := fs.Glob(source, "*.sql")
		if err != nil {
			return nil, errors.Wrapf(err, "migrate: read source %d", i)
		}
		if len(names) == 0 {
			return nil, errors.Newf("migrate: source %d has no migrations", i)
		}
		for _, name := range names {
			version, _, err := parseFileName(name)
			if err != nil {
				return nil, err
			}
			if other, ok := versions[version]; ok {
				return nil, errors.Newf("migrate: %s and %s have the same version", other, name)
			}
			versions[version] = name
			data, err := fs.ReadFile(source, name)
			if err != nil {
				return nil, errors.Wrapf(err, "migrate: read %s", name)
			}
			if !bytes.Contains(data, []byte("-- +goose Up")) {
				return nil, errors.Newf("migrate: %s is missing goose Up annotation", name)
			}
			if published, ok := existing[version]; ok && published != name {
				return nil, errors.Newf("migrate: %s has the same version as %s in %s", name, published, dir)
			}
			files[name] = data
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, errors.Wrap(err, "migrate: create publish directory")
	}
	var written []string
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if version, _, _ := parseFileName(name); existing[version] != "" {
			continue
		}
		if err := publishFile(filepath.Join(dir, name), files[name]); err != nil {
			return written, errors.Wrapf(err, "migrate: publish %s", name)
		}
		written = append(written, name)
	}
	return written, nil
}

// migrationFiles 返回 dir 中迁移的版本和文件名；dir 不存在时为空。文件名不符合格式时返回错误。
func migrationFiles(dir string) (map[int64]string, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	files := make(map[int64]string, len(names))
	for _, path := range names {
		name := filepath.Base(path)
		version, _, err := parseFileName(name)
		if err != nil {
			return nil, err
		}
		files[version] = name
	}
	return files, nil
}

// publishFile 独占创建文件，绝不覆盖已有迁移；写入失败时删除半成品。
func publishFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
