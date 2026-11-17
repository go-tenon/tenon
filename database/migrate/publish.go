package migrate

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
)

const sourceMarker = "-- tenon: "

var sourceName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// Source 是一个迁移来源。Name 是稳定标识；FS 根目录直接包含带数字版本前缀的 SQL，例如 0001_create_files.sql，
// 版本只决定来源内的顺序。已发布的源文件不改名，升级只追加更高版本。
type Source struct {
	Name string
	FS   fs.FS
}

type publication struct {
	name    string // 去掉版本的源文件名，例如 create_files.sql
	marker  string
	version int64
	data    []byte
}

// Publish 把 sources 中尚未发布的迁移复制到 dir，返回本次写入的文件名。
// 与 Laravel 发布包的迁移相同，新文件名是 UTC 时间加去掉版本的源文件名，例如 0001_create_files.sql 发布为
// 2026_11_17_123456_create_files.sql，版本保证大于目录中已有的最大版本。
// 每个文件首行写入来源标记（如 "-- tenon: files/0001_create_files"），
// 目录中已有相同标记的迁移会被跳过，应用可以自由修改或重命名已发布的文件。
func Publish(dir string, sources ...Source) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("migrate: publish directory is required")
	}
	items, err := publications(sources)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, errors.Wrap(err, "migrate: create publish directory")
	}
	published, maxVersion, err := publishedSources(dir)
	if err != nil {
		return nil, err
	}
	var pending []publication
	for _, item := range items {
		if !published[item.marker] {
			pending = append(pending, item)
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}
	// 写入之前先算出全部版本，版本不够用时一个文件也不写。
	names := make([]string, len(pending))
	for i, item := range pending {
		if maxVersion, err = nextVersion(maxVersion); err != nil {
			return nil, err
		}
		names[i] = formatVersion(maxVersion) + "_" + item.name
	}

	var written []string
	for i, item := range pending {
		name := names[i]
		content := append([]byte(sourceMarker+item.marker+"\n"), item.data...)
		if err := publishFile(filepath.Join(dir, name), content); err != nil {
			return written, errors.Wrapf(err, "migrate: publish %s", name)
		}
		written = append(written, name)
	}
	return written, nil
}

// publications 在写入前读取并校验全部来源的迁移，按来源顺序、来源内按版本排序。
func publications(sources []Source) ([]publication, error) {
	if len(sources) == 0 {
		return nil, errors.New("migrate: at least one migration source is required")
	}
	seen := make(map[string]bool)
	var items []publication
	for _, source := range sources {
		if !sourceName.MatchString(source.Name) {
			return nil, errors.Newf("migrate: invalid source name %q", source.Name)
		}
		if seen[source.Name] {
			return nil, errors.Newf("migrate: duplicate source %q", source.Name)
		}
		seen[source.Name] = true
		if source.FS == nil {
			return nil, errors.Newf("migrate: source %s has nil filesystem", source.Name)
		}
		files := source.FS
		names, err := fs.Glob(files, "*.sql")
		if err != nil {
			return nil, errors.Wrapf(err, "migrate: read source %s", source.Name)
		}
		if len(names) == 0 {
			return nil, errors.Newf("migrate: source %s has no migrations", source.Name)
		}
		var group []publication
		versions := make(map[int64]string)
		for _, name := range names {
			version, description, err := sourceVersion(name)
			if err != nil {
				return nil, err
			}
			if previous, ok := versions[version]; ok {
				return nil, errors.Newf("migrate: source %s has duplicate version in %s and %s", source.Name, previous, name)
			}
			versions[version] = name
			data, err := fs.ReadFile(files, name)
			if err != nil {
				return nil, errors.Wrapf(err, "migrate: read %s/%s", source.Name, name)
			}
			if !bytes.Contains(data, []byte("-- +goose Up")) {
				return nil, errors.Newf("migrate: %s/%s is missing goose Up annotation", source.Name, name)
			}
			group = append(group, publication{
				name: description, version: version, data: data,
				marker: source.Name + "/" + strings.TrimSuffix(name, ".sql"),
			})
		}
		slices.SortFunc(group, func(a, b publication) int { return int(min(max(a.version-b.version, -1), 1)) })
		items = append(items, group...)
	}
	return items, nil
}

// sourceVersion 返回来源中迁移文件名的版本和版本之后的部分，例如 0001_create_files.sql 返回 1 和 create_files.sql。
func sourceVersion(name string) (int64, string, error) {
	versionText, description, ok := strings.Cut(name, "_")
	version, err := strconv.ParseInt(versionText, 10, 64)
	if !ok || description == ".sql" || err != nil || version <= 0 || strings.ContainsAny(versionText, "+-") {
		return 0, "", errors.Newf("migrate: invalid SQL migration filename %q", name)
	}
	return version, description, nil
}

// publishedSources 返回目录中已发布的来源标记和最大版本。
func publishedSources(dir string) (map[string]bool, int64, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, 0, err
	}
	published := make(map[string]bool)
	var maxVersion int64
	for _, path := range names {
		version, _, err := parseFileName(filepath.Base(path))
		if err != nil {
			return nil, 0, err
		}
		maxVersion = max(maxVersion, version)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, 0, errors.Wrapf(err, "migrate: read %s", path)
		}
		line, _, _ := strings.Cut(string(data), "\n")
		if marker, ok := strings.CutPrefix(strings.TrimSpace(line), sourceMarker); ok {
			published[strings.TrimSpace(marker)] = true
		}
	}
	return published, maxVersion, nil
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
