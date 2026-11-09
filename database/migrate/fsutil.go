package migrate

import (
	"io/fs"

	"github.com/cockroachdb/errors"
)

// Sub 返回 root 下 dir 目录的 FS，便于 go:embed 目录后交给 goose。
func Sub(root fs.FS, dir string) (fs.FS, error) {
	sub, err := fs.Sub(root, dir)
	if err != nil {
		return nil, errors.Wrapf(err, "migrate: sub %s", dir)
	}
	return sub, nil
}
