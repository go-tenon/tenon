//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package migrate

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockPublish(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
