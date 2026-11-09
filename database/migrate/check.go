package migrate

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cockroachdb/errors"
)

// CheckPublished 比较 dir 中已发布迁移与 baseline 目录（同名文件内容必须一致）。
// dir 可有追加的新文件；baseline 中的每个文件都必须在 dir 中且字节相同。
// 这是源码 CI 检查，不是数据库历史校验。
func CheckPublished(dir, baseline string) error {
	baseEntries, err := listSQL(baseline)
	if err != nil {
		return errors.Wrap(err, "migrate: baseline")
	}
	liveEntries, err := listSQL(dir)
	if err != nil {
		return errors.Wrap(err, "migrate: dir")
	}
	live := map[string][]byte{}
	for _, e := range liveEntries {
		live[e.name] = e.data
	}
	var problems []string
	for _, e := range baseEntries {
		got, ok := live[e.name]
		if !ok {
			problems = append(problems, "missing published migration "+e.name)
			continue
		}
		if !bytes.Equal(got, e.data) {
			problems = append(problems, "modified published migration "+e.name)
		}
	}
	if len(problems) > 0 {
		return errors.Newf("migrate: published baseline mismatch:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

type sqlFile struct {
	name string
	data []byte
}

func listSQL(dir string) ([]sqlFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []sqlFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, sqlFile{name: e.Name(), data: data})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// CheckFS 对 embed/测试用 fs.FS 做同样的已发布文件比较。
func CheckFS(live, baseline fs.FS) error {
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
	sort.Strings(names)
	return names, err
}
