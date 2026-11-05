// Package sysinfo reports the disk use of an installation for its system page.
package sysinfo

import (
	"io/fs"
	"path/filepath"
)

// DirSize sums the sizes of the files under dir, stopping after limit files (complete is then
// false).
func DirSize(dir string, limit int) (size int64, files int, complete bool) {
	complete = true
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable parts are skipped
		}
		if d.IsDir() {
			return nil
		}
		if files >= limit {
			complete = false
			return filepath.SkipAll
		}
		if info, err := d.Info(); err == nil {
			size += info.Size()
			files++
		}
		return nil
	})
	return size, files, complete
}
