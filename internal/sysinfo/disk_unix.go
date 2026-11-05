//go:build !windows

package sysinfo

import "syscall"

// Disk returns the free and total bytes of the file system holding path.
func Disk(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize) // #nosec G115 -- block sizes are positive
	return st.Bavail * bsize, st.Blocks * bsize, nil
}
