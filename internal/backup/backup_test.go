package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDump writes a pg_dump stand-in that records its arguments and PGPASSWORD.
func fakeDump(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	script := filepath.Join(dir, "pg_dump")
	body := "#!/bin/sh\necho \"$PGPASSWORD $*\" > " + record + "\nfor a in \"$@\"; do case $a in --file=*) echo dump > \"${a#--file=}\";; esac; done\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script, record
}

func TestRunAndPrune(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	tool, record := fakeDump(t)
	dir := t.TempDir()
	now := time.Date(2026, 11, 4, 2, 30, 0, 0, time.UTC)
	b := &Backups{Dir: dir, PGDump: tool, URL: "postgres://netra:s3cret@127.0.0.1:5433/netra?sslmode=disable",
		KeepDays: 7, Now: func() time.Time { return now }}
	old := filepath.Join(dir, "netra-20261020-023000.dump")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(old, now.AddDate(0, 0, -15), now.AddDate(0, 0, -15))
	other := filepath.Join(dir, "notes.txt")
	_ = os.WriteFile(other, []byte("keep"), 0o600)

	info, err := b.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "netra-20261104-023000.dump" || info.Size == 0 {
		t.Fatalf("info %+v", info)
	}
	args, _ := os.ReadFile(record)
	if !strings.HasPrefix(string(args), "s3cret ") || strings.Contains(string(args)[7:], "s3cret") ||
		!strings.Contains(string(args), "--format=custom") {
		t.Fatalf("pg_dump call: %s", args)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("an old backup was kept")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("a file that is not a backup was deleted")
	}
	list, _ := b.List()
	if len(list) != 1 || list[0].Name != info.Name {
		t.Fatalf("list %+v", list)
	}
	if _, ok := b.Path("../etc/passwd"); ok {
		t.Fatal("path traversal accepted")
	}
	if p, ok := b.Path(info.Name); !ok || filepath.Dir(p) != dir {
		t.Fatalf("path %q", p)
	}
}

func TestNoTool(t *testing.T) {
	if _, err := (&Backups{Dir: t.TempDir()}).Run(context.Background()); err != ErrNoTool {
		t.Fatalf("without pg_dump: %v", err)
	}
	if _, _, err := parseClock("25:00"); err == nil {
		t.Fatal("bad time accepted")
	}
}
