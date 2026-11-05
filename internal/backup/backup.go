// Package backup makes the daily database backups of an installation: pg_dump in its custom
// format into the backups directory, keeping a number of days.
package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Info describes one backup file.
type Info struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
}

// Backups makes and lists the backups of one database.
type Backups struct {
	Dir      string // where the .dump files go
	PGDump   string // the pg_dump program
	URL      string // the database
	KeepDays int    // backups older than this are deleted; 0: keep all
	Now      func() time.Time
}

// Pattern is the name of a backup file.
var Pattern = regexp.MustCompile(`^netra-\d{8}-\d{6}\.dump$`)

// ErrNoTool means no pg_dump was found, so backups are off.
var ErrNoTool = errors.New("pg_dump not found")

// FindTool returns pg_dump (or pg_restore) from bin when given, else from PATH.
func FindTool(bin, name string) (string, error) {
	if bin != "" {
		p := filepath.Join(bin, name)
		if _, err := exec.LookPath(p); err == nil {
			return p, nil
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", ErrNoTool
	}
	return p, nil
}

// Command builds a pg_dump or pg_restore command for the database url: the password goes in
// PGPASSWORD, not on the command line where other users could read it.
func Command(ctx context.Context, tool, dbURL string, args ...string) (*exec.Cmd, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return nil, err
	}
	env := os.Environ()
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
			u.User = url.User(u.User.Username())
		}
	}
	cmd := exec.CommandContext(ctx, tool, append(args, "--dbname="+u.String())...) // #nosec G204 -- our tool and URL
	cmd.Env = env
	return cmd, nil
}

// Run makes a backup now and deletes the ones older than KeepDays.
func (b *Backups) Run(ctx context.Context) (Info, error) {
	if b.PGDump == "" {
		return Info{}, ErrNoTool
	}
	if err := os.MkdirAll(b.Dir, 0o750); err != nil {
		return Info{}, err
	}
	now := b.now()
	name := "netra-" + now.Format("20060102-150405") + ".dump"
	tmp := filepath.Join(b.Dir, "."+name+".part")
	cmd, err := Command(ctx, b.PGDump, b.URL, "--format=custom", "--no-owner", "--file="+tmp)
	if err != nil {
		return Info{}, err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return Info{}, fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(string(out)))
	}
	final := filepath.Join(b.Dir, name)
	if err := os.Rename(tmp, final); err != nil {
		return Info{}, err
	}
	b.prune(now)
	st, err := os.Stat(final)
	if err != nil {
		return Info{}, err
	}
	return Info{Name: name, Size: st.Size(), Created: st.ModTime()}, nil
}

func (b *Backups) prune(now time.Time) {
	if b.KeepDays <= 0 {
		return
	}
	list, err := b.List()
	if err != nil {
		return
	}
	cutoff := now.AddDate(0, 0, -b.KeepDays)
	for _, i := range list {
		if i.Created.Before(cutoff) {
			_ = os.Remove(filepath.Join(b.Dir, i.Name))
		}
	}
}

// List returns the backups, newest first.
func (b *Backups) List() ([]Info, error) {
	entries, err := os.ReadDir(b.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Info{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Info{}
	for _, e := range entries {
		if e.IsDir() || !Pattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Info{Name: e.Name(), Size: info.Size(), Created: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// Path returns the file of a backup by name, refusing names that are not backups.
func (b *Backups) Path(name string) (string, bool) {
	if !Pattern.MatchString(name) {
		return "", false
	}
	p := filepath.Join(b.Dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// Schedule makes a backup every day at hh:mm in loc until ctx ends.
func (b *Backups) Schedule(ctx context.Context, at string, loc *time.Location) {
	hour, minute, err := parseClock(at)
	if err != nil {
		slog.Warn("backups off", "err", err)
		return
	}
	for {
		now := b.now().In(loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now)):
		}
		if info, err := b.Run(ctx); err != nil {
			slog.Error("backup", "err", err)
		} else {
			slog.Info("backup", "file", info.Name, "bytes", info.Size)
		}
	}
}

func parseClock(at string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(at))
	if err != nil {
		return 0, 0, fmt.Errorf("backup time %q is not HH:MM", at)
	}
	return t.Hour(), t.Minute(), nil
}

func (b *Backups) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}
