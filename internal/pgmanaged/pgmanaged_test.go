package pgmanaged

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestCluster runs a real cluster: set NETRA_TEST_PG_BIN to a PostgreSQL bin directory (e.g.
// /usr/lib/postgresql/16/bin) and run as a user other than root.
func TestCluster(t *testing.T) {
	bin := os.Getenv("NETRA_TEST_PG_BIN")
	if bin == "" || os.Geteuid() == 0 {
		t.Skip("set NETRA_TEST_PG_BIN and run as a regular user")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	first := New(bin, dir, 55433)
	if err := first.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Stop() }()
	ping := func(s *Server) {
		db, err := sql.Open("postgres", s.URL())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("query: %v", err)
		}
	}
	ping(first)

	// A second process (a CLI command while the service runs) uses the running cluster and
	// leaves it running.
	second := New(bin, dir, 55433)
	if err := second.Start(ctx); err != nil {
		t.Fatal(err)
	}
	ping(second)
	if second.started || second.Stop() != nil {
		t.Fatal("the second process must not own the cluster")
	}
	ping(first)

	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	if first.running(ctx) {
		t.Fatal("still running after stop")
	}
	restarted := New(bin, dir, 55433)
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	ping(restarted)
	if err := restarted.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("only meaningful as root")
	}
	if err := New("/nonexistent", t.TempDir(), 5433).Start(context.Background()); err != ErrRoot {
		t.Fatalf("root: %v", err)
	}
}
