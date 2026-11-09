package migrate

import (
	"bytes"
	"io/fs"
	"os"
	"strings"

	"github.com/cockroachdb/errors"
)

// CheckPublished 比较两个目录中的迁移 SQL，已发布文件须保持名称与内容一致。
func CheckPublished(dir, baseline string) error {
	return CheckFS(os.DirFS(dir), os.DirFS(baseline))
}

// CheckFS 递归比较迁移 SQL 的相对路径与字节内容，live 可追加文件。
func CheckFS(live, baseline fs.FS) error {
	info, err := fs.Stat(live, ".")
	if err != nil {
		return errors.Wrap(err, "migrate: current directory")
	}
	if !info.IsDir() {
		return errors.New("migrate: current path must be a directory")
	}
	baseNames, err := sqlNames(baseline)
	if err != nil {
		return err
	}
	for _, name := range baseNames {
		want, err := fs.ReadFile(baseline, name)
		if err != nil {
			return err
		}
		got, err := fs.ReadFile(live, name)
		if err != nil {
			return errors.Wrapf(err, "migrate: missing published %s", name)
		}
		if !bytes.Equal(got, want) {
			return errors.Newf("migrate: modified published migration %s", name)
		}
	}
	return nil
}

func sqlNames(fsys fs.FS) ([]string, error) {
	var names []string
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(path, ".sql") {
			names = append(names, path)
		}
		return nil
	})
	return names, err
}
