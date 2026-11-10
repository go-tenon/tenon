//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package migrate

import (
	"errors"
	"os"
)

func lockPublish(string) (*os.File, error) {
	return nil, errors.New("migration publishing requires filesystem locks on Unix or Windows")
}
