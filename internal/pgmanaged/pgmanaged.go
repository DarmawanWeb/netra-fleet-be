// Package pgmanaged runs a private PostgreSQL for an installation that has no database server
// of its own (the Windows and macOS installers ship PostgreSQL binaries): the cluster lives in
// the installation's data directory, listens on 127.0.0.1 only, and is created, started and
// stopped by netra.
package pgmanaged

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// Database and role netra uses in the private cluster.
const (
	Database = "netra"
	Role     = "netra"
)

// Server is a private PostgreSQL cluster.
type Server struct {
	Bin     string // directory of initdb, pg_ctl, postgres, pg_dump, pg_restore
	DataDir string // the cluster (DataDir/pgdata of the installation)
	Port    int
	started bool // started by this process, so stopped by it
	secret  string
}

// New describes the cluster of an installation's data directory.
func New(bin, installDataDir string, port int) *Server {
	return &Server{Bin: bin, DataDir: filepath.Join(installDataDir, "pgdata"), Port: port}
}

// Tool is the path of a PostgreSQL program of the bundle.
func (s *Server) Tool(name string) string { return filepath.Join(s.Bin, name) }

func (s *Server) secretFile() string { return filepath.Join(filepath.Dir(s.DataDir), "pg.secret") }
func (s *Server) logFile() string    { return filepath.Join(filepath.Dir(s.DataDir), "postgresql.log") }

// URL is the connection string of the database.
func (s *Server) URL() string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(Role, s.secret), Host: "127.0.0.1:" + strconv.Itoa(s.Port),
		Path: "/" + Database, RawQuery: "sslmode=disable"}
	return u.String()
}

// ErrRoot means PostgreSQL was asked to run as root, which it refuses.
var ErrRoot = errors.New("PostgreSQL does not run as root: run netra as its own user")

// Start creates the cluster on first use, starts it unless it runs already, and creates the
// database. It returns once the database accepts connections.
func (s *Server) Start(ctx context.Context) error {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		return ErrRoot
	}
	if err := os.MkdirAll(filepath.Dir(s.DataDir), 0o750); err != nil {
		return err
	}
	if err := s.loadSecret(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, "PG_VERSION")); errors.Is(err, os.ErrNotExist) {
		if err := s.initdb(ctx); err != nil {
			return err
		}
	}
	if err := s.writeConf(); err != nil {
		return err
	}
	if !s.running(ctx) {
		out, err := s.run(ctx, "pg_ctl", "-D", s.DataDir, "-l", s.logFile(), "-w", "-t", "120", "start")
		if err != nil {
			return fmt.Errorf("start PostgreSQL: %w: %s (log: %s)", err, out, s.logFile())
		}
		s.started = true
	}
	return s.ensureDatabase(ctx)
}

// Stop stops the cluster if this process started it.
func (s *Server) Stop() error {
	if !s.started {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := s.run(ctx, "pg_ctl", "-D", s.DataDir, "-m", "fast", "-w", "-t", "60", "stop")
	if err != nil {
		return fmt.Errorf("stop PostgreSQL: %w: %s", err, out)
	}
	s.started = false
	return nil
}

func (s *Server) running(ctx context.Context) bool {
	_, err := s.run(ctx, "pg_ctl", "-D", s.DataDir, "status")
	return err == nil
}

// loadSecret reads the role's password, making one at the first start.
func (s *Server) loadSecret() error {
	raw, err := os.ReadFile(s.secretFile())
	if err == nil {
		s.secret = strings.TrimSpace(string(raw))
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	s.secret = hex.EncodeToString(b)
	return os.WriteFile(s.secretFile(), []byte(s.secret+"\n"), 0o600)
}

func (s *Server) initdb(ctx context.Context) error {
	pw, err := os.CreateTemp(filepath.Dir(s.DataDir), "pwfile-*")
	if err != nil {
		return err
	}
	defer os.Remove(pw.Name())
	if _, err := pw.WriteString(s.secret + "\n"); err != nil {
		_ = pw.Close()
		return err
	}
	if err := pw.Close(); err != nil {
		return err
	}
	out, err := s.run(ctx, "initdb", "-D", s.DataDir, "-U", Role, "--pwfile="+pw.Name(), "-A", "scram-sha-256",
		"-E", "UTF8", "--locale=C")
	if err != nil {
		return fmt.Errorf("initdb: %w: %s", err, out)
	}
	conf := filepath.Join(s.DataDir, "postgresql.conf")
	f, err := os.OpenFile(conf, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("\n# netra writes its settings here at every start\ninclude_if_exists = 'netra.conf'\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// writeConf sets the port and keeps the server on the loopback address only, without a Unix
// socket (none is needed, and its default directory may not be writable).
func (s *Server) writeConf() error {
	conf := fmt.Sprintf("port = %d\nlisten_addresses = '127.0.0.1'\nunix_socket_directories = ''\n"+
		"max_connections = 50\nshared_buffers = 128MB\n", s.Port)
	return os.WriteFile(filepath.Join(s.DataDir, "netra.conf"), []byte(conf), 0o600)
}

func (s *Server) ensureDatabase(ctx context.Context) error {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(Role, s.secret), Host: "127.0.0.1:" + strconv.Itoa(s.Port),
		Path: "/postgres", RawQuery: "sslmode=disable"}
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, Database).
		Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.ExecContext(ctx, `CREATE DATABASE `+Database)
	return err
}

func (s *Server) run(ctx context.Context, tool string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, s.Tool(tool), args...) // #nosec G204 -- our own bundled binaries
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
