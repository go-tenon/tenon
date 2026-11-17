package migrate

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"

	"github.com/cockroachdb/errors"
)

// 应用目录中的迁移文件名与 Laravel 相同：2026_11_17_123456_create_articles_table.sql。
// 去掉下划线的时间 20261117123456 是迁移的版本，goose 用它记录哪些迁移已经执行。
var (
	fileName  = regexp.MustCompile(`^(\d{4})_(\d{2})_(\d{2})_(\d{6})_(.+\.sql)$`)
	gooseName = regexp.MustCompile(`^(\d{14})_(.+\.sql)$`)
)

// lastVersion 是文件名能表示的最大版本 9999_99_99_999999。
const lastVersion = 99999999999999

// parseFileName 返回应用迁移文件名中的版本和版本之后的部分，例如 create_articles_table.sql。
func parseFileName(name string) (int64, string, error) {
	m := fileName.FindStringSubmatch(name)
	if m == nil {
		return 0, "", errors.Newf("migrate: migration %q must be named like 2026_11_17_123456_create_articles_table.sql", name)
	}
	version, _ := strconv.ParseInt(m[1]+m[2]+m[3]+m[4], 10, 64)
	if version == 0 {
		return 0, "", errors.Newf("migrate: migration %q has version 0", name)
	}
	return version, m[5], nil
}

// formatVersion 把版本写成文件名的开头，例如 20261117123456 写成 2026_11_17_123456。
func formatVersion(version int64) string {
	v := fmt.Sprintf("%014d", version)
	return v[:4] + "_" + v[4:6] + "_" + v[6:8] + "_" + v[8:]
}

// FileName 返回 goose 迁移路径（Source.Path）对应的应用文件名：goose 看到的
// 20261117123456_create_articles_table.sql 是应用目录中的 2026_11_17_123456_create_articles_table.sql，
// 其他文件名原样返回。
func FileName(p string) string {
	base := path.Base(p)
	if m := gooseName.FindStringSubmatch(base); m != nil {
		version, _ := strconv.ParseInt(m[1], 10, 64)
		return formatVersion(version) + "_" + m[2]
	}
	return base
}

// gooseFS 让 goose 读取应用的迁移：goose 把第一个下划线之前的数字当作版本，
// 因此根目录中的 2026_11_17_123456_create_articles_table.sql 以 20261117123456_create_articles_table.sql
// 的名字出现；其他文件（例如功能包自带的 0001_create_files.sql）保持原名。
type gooseFS struct{ fsys fs.FS }

func (g gooseFS) Open(name string) (fs.File, error) {
	if m := gooseName.FindStringSubmatch(name); m != nil {
		f, err := g.fsys.Open(FileName(name))
		if !errors.Is(err, fs.ErrNotExist) {
			return f, err
		}
	}
	return g.fsys.Open(name)
}

func (g gooseFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(g.fsys, name)
	if err != nil || name != "." {
		return entries, err
	}
	for i, e := range entries {
		if version, rest, err := parseFileName(e.Name()); err == nil && !e.IsDir() {
			entries[i] = renamedEntry{e, fmt.Sprintf("%014d_%s", version, rest)}
		}
	}
	return entries, nil
}

// renamedEntry 是换了名字的目录项。
type renamedEntry struct {
	fs.DirEntry
	name string
}

func (e renamedEntry) Name() string { return e.name }

func (e renamedEntry) Info() (fs.FileInfo, error) {
	info, err := e.DirEntry.Info()
	if err != nil {
		return nil, err
	}
	return renamedInfo{info, e.name}, nil
}

type renamedInfo struct {
	fs.FileInfo
	name string
}

func (i renamedInfo) Name() string { return i.name }
