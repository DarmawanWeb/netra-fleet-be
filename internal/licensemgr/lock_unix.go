//go:build unix

package licensemgr

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockDir keeps a second manager off the same data folder: both would keep their own view of it.
func lockDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "manager.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd()) // #nosec G115 -- a file descriptor fits in an int
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s is in use by another license manager", dir)
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
