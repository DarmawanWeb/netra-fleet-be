package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirSizeAndDisk(t *testing.T) {
	dir := t.TempDir()
	for i, n := range []int{10, 20, 30} {
		_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o700)
		_ = os.WriteFile(filepath.Join(dir, "sub", string(rune('a'+i))), make([]byte, n), 0o600)
	}
	if size, files, complete := DirSize(dir, 100); size != 60 || files != 3 || !complete {
		t.Fatalf("size %d files %d complete %v", size, files, complete)
	}
	if _, files, complete := DirSize(dir, 2); files != 2 || complete {
		t.Fatalf("limit: files %d complete %v", files, complete)
	}
	free, total, err := Disk(dir)
	if err != nil || total == 0 || free > total {
		t.Fatalf("disk: %d of %d (%v)", free, total, err)
	}
}
