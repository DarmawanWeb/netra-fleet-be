//go:build windows

package sysinfo

import (
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// Disk returns the free and total bytes of the volume holding path.
func Disk(path string) (free, total uint64, err error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var avail, all, totalFree uint64
	r, _, callErr := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&avail)),
		uintptr(unsafe.Pointer(&all)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return 0, 0, callErr
	}
	return avail, all, nil
}
