package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/backup"
	"github.com/DarmawanWeb/netra-fleet-be/internal/sysinfo"
	"github.com/DarmawanWeb/netra-fleet-be/internal/version"
)

// Runtime is how the server runs, for the system page.
type Runtime struct {
	StartedAt time.Time
	ManagedDB bool // the private PostgreSQL of the installation
	TLS       bool
	DataDir   string
	Backups   *backup.Backups // nil: built-in backups off
	BackupAt  string
}

// SetRuntime tells the server how it runs.
func (s *Server) SetRuntime(rt Runtime) { s.rt = rt }

// dataSize caches the size of the data directory: walking many photos takes a while.
type dataSize struct {
	mu       sync.Mutex
	at       time.Time
	size     int64
	files    int
	complete bool
}

func (d *dataSize) get(dir string) (int64, int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Since(d.at) > 5*time.Minute {
		d.size, d.files, d.complete = sysinfo.DirSize(dir, 500_000)
		d.at = time.Now()
	}
	return d.size, d.files, d.complete
}

// system is the administrator's view of the installation: version, database, disk, backups.
func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbSize, err := s.store.DatabaseSize(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	robots, err := s.store.CountRobots(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	online := 0
	if s.relay != nil {
		online = s.relay.OnlineCount()
	}
	dir, _ := filepath.Abs(s.rt.DataDir)
	size, files, complete := s.sizes.get(dir)
	disk := map[string]any{"path": dir, "data_bytes": size, "data_files": files, "data_complete": complete}
	if free, total, err := sysinfo.Disk(dir); err == nil {
		disk["free_bytes"], disk["total_bytes"] = free, total
	}
	backups := map[string]any{"enabled": s.rt.Backups != nil, "list": []backup.Info{}}
	if b := s.rt.Backups; b != nil {
		list, err := b.List()
		if err != nil {
			fail(w, err)
			return
		}
		backups["list"], backups["keep_days"], backups["at"], backups["dir"] = list, b.KeepDays, s.rt.BackupAt, b.Dir
	}
	uptime := 0.0
	if !s.rt.StartedAt.IsZero() {
		uptime = time.Since(s.rt.StartedAt).Seconds()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": version.String(), "installation_id": s.lic.Installation(), "started_at": s.rt.StartedAt,
		"uptime_s": uptime, "time_zone": s.cfg.TimeZone.String(), "tls": s.rt.TLS,
		"database": map[string]any{"size_bytes": dbSize, "managed": s.rt.ManagedDB},
		"disk":     disk, "backups": backups,
		"robots":  map[string]any{"registered": robots, "online": online},
		"license": s.lic.Info(), "map_mode": s.lic.MapMode(),
	})
}

// runBackup makes a database backup now.
func (s *Server) runBackup(w http.ResponseWriter, r *http.Request) {
	if s.rt.Backups == nil {
		writeError(w, http.StatusConflict, "built-in backups are off on this installation")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	info, err := s.rt.Backups.Run(ctx)
	if errors.Is(err, backup.ErrNoTool) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

// backupFile downloads a backup.
func (s *Server) backupFile(w http.ResponseWriter, r *http.Request) {
	if s.rt.Backups == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	p, ok := s.rt.Backups.Path(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(p)+`"`)
	http.ServeFile(w, r, p)
}

// ready answers 200 once the database answers too (for installers and monitoring).
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "database unavailable"})
		return
	}
	if err := s.relay.Ready(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "nats unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "version": version.String()})
}
