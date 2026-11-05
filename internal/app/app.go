// Package app runs NETRA: the database (external or the installation's own), the API, the
// dashboard, the robot gateway, the scheduler, the anomaly pipeline and the backups, as one
// service or split by role (NETRA_ROLES) across processes that talk over NATS.
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/api"
	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/backup"
	"github.com/DarmawanWeb/netra-fleet-be/internal/bus"
	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
	"github.com/DarmawanWeb/netra-fleet-be/internal/detect"
	"github.com/DarmawanWeb/netra-fleet-be/internal/hub"
	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
	"github.com/DarmawanWeb/netra-fleet-be/internal/notify"
	"github.com/DarmawanWeb/netra-fleet-be/internal/pgmanaged"
	"github.com/DarmawanWeb/netra-fleet-be/internal/relay"
	"github.com/DarmawanWeb/netra-fleet-be/internal/role"
	"github.com/DarmawanWeb/netra-fleet-be/internal/scheduler"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
	"github.com/DarmawanWeb/netra-fleet-be/internal/tlsutil"
	"github.com/DarmawanWeb/netra-fleet-be/internal/version"
	"github.com/DarmawanWeb/netra-fleet-be/internal/web"
)

// Database is an open store and how it was reached.
type Database struct {
	Store   *store.Store
	URL     string
	Managed *pgmanaged.Server // nil for an external database
}

// Close closes the store and stops the private PostgreSQL when this process started it.
func (d *Database) Close() {
	d.Store.Close()
	if d.Managed != nil {
		if err := d.Managed.Stop(); err != nil {
			slog.Error("stop database", "err", err)
		}
	}
}

// OpenDatabase starts the installation's private PostgreSQL when NETRA_PG_BIN is set (or uses
// NETRA_DATABASE_URL), applies the migrations and returns the store.
func OpenDatabase(ctx context.Context, cfg config.Config) (*Database, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}
	db := &Database{URL: cfg.DatabaseURL}
	if cfg.PGBin != "" {
		pg := pgmanaged.New(cfg.PGBin, cfg.DataDir, cfg.PGPort)
		if err := pg.Start(ctx); err != nil {
			return nil, err
		}
		db.Managed, db.URL = pg, pg.URL()
	}
	st, err := store.Open(ctx, db.URL)
	if err != nil {
		if db.Managed != nil {
			_ = db.Managed.Stop()
		}
		return nil, err
	}
	db.Store = st
	return db, nil
}

// PGTool finds a PostgreSQL program (pg_dump, pg_restore): the bundled one, else NETRA_PG_BIN,
// else PATH.
func PGTool(cfg config.Config, name string) (string, error) {
	return backup.FindTool(cfg.PGBin, name)
}

// Serve runs NETRA until ctx ends: every role in this process (NETRA_ROLES=all, the default),
// or some of them, the others running in other processes reached over NATS.
func Serve(ctx context.Context, cfg config.Config) error {
	started := time.Now()
	roles, err := role.Parse(cfg.Roles)
	if err != nil {
		return err
	}
	if err := checkRoles(cfg, roles); err != nil {
		return err
	}
	nc, err := dialNATS(ctx, cfg, roles)
	if err != nil {
		return err
	}
	if nc != nil {
		defer nc.Close()
	}
	db, err := OpenDatabase(ctx, cfg)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()
	st := db.Store
	if cfg.AdminPassword != "" && roles.Has(role.API) {
		if err := st.EnsureUser(ctx, cfg.AdminUser, auth.HashPassword(cfg.AdminPassword), "admin"); err != nil {
			return fmt.Errorf("admin user: %w", err)
		}
	}
	lic, err := LoadLicense(ctx, cfg, st)
	if err != nil {
		return fmt.Errorf("license: %w", err)
	}
	srv := api.New(cfg, st, hub.New(), notify.NewTelegram(cfg.TelegramToken, cfg.TelegramChatID, cfg.TelegramAPI),
		Detector(cfg), bus.NewPublisher(nc), lic)
	srv.SetRoles(roles)
	if !roles.All() {
		rl := relay.NewNATS(nc, relayHandlers(srv, roles, func() { reloadLicense(cfg, st, lic) }), relay.Options{})
		if err := rl.Start(); err != nil {
			return fmt.Errorf("relay: %w", err)
		}
		defer rl.Close()
		srv.SetRelay(rl)
		slog.Info("split installation", "roles", roles.String(), "node", rl.Node())
	}

	rt := api.Runtime{StartedAt: started, ManagedDB: db.Managed != nil, DataDir: cfg.DataDir, BackupAt: cfg.BackupAt}
	if cfg.BackupKeepDays > 0 && (roles.Has(role.API) || roles.Has(role.Worker)) {
		if tool, err := PGTool(cfg, "pg_dump"); err == nil {
			rt.Backups = &backup.Backups{Dir: filepath.Join(cfg.DataDir, "backups"), PGDump: tool, URL: db.URL,
				KeepDays: cfg.BackupKeepDays}
			if roles.Has(role.Worker) {
				go rt.Backups.Schedule(ctx, cfg.BackupAt, cfg.TimeZone)
			}
		} else {
			slog.Info("built-in backups off: no pg_dump on this host")
		}
	}

	ok := false
	if roles.Has(role.API) {
		files, found, err := web.Files(cfg.WebDir)
		if err != nil {
			return fmt.Errorf("dashboard files: %w", err)
		}
		if ok = found; ok {
			srv.SetWeb(web.Handler(files))
		}
	}

	httpServer := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	certFile, keyFile, err := tlsFiles(cfg)
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	rt.TLS = certFile != ""
	srv.SetRuntime(rt)

	if roles.Has(role.Worker) {
		go srv.RunPipeline(ctx)
		go scheduler.Run(ctx, st, cfg.TimeZone, srv.StartPatrol)
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()

	slog.Info("netra listening", "addr", cfg.Addr, "roles", roles.String(), "tls", rt.TLS, "dashboard", ok,
		"managed_db", rt.ManagedDB, "tz", cfg.TimeZone.String(), "version", version.String(),
		"installation", lic.Installation())
	if rt.TLS {
		httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		err = httpServer.ListenAndServeTLS(certFile, keyFile)
	} else {
		err = httpServer.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// checkRoles refuses a split setup that cannot work.
func checkRoles(cfg config.Config, roles role.Set) error {
	if roles.All() {
		return nil
	}
	if cfg.NatsURL == "" {
		return fmt.Errorf("NETRA_ROLES=%s needs NETRA_NATS_URL: the processes of a split installation talk over NATS",
			roles)
	}
	if cfg.PGBin != "" {
		return fmt.Errorf("NETRA_ROLES=%s cannot use NETRA_PG_BIN: the private database belongs to one process, "+
			"split roles share one PostgreSQL through NETRA_DATABASE_URL", roles)
	}
	return nil
}

// dialNATS connects to NETRA_NATS_URL. With every role in this process NATS only carries the
// domain events, so a bad URL disables them; a split process needs NATS and waits for it, and
// gives up only when the server refuses it (an unreachable server is retried in the background).
func dialNATS(ctx context.Context, cfg config.Config, roles role.Set) (*bus.Client, error) {
	if cfg.NatsURL == "" {
		return nil, nil
	}
	nc, err := bus.Dial(cfg.NatsURL, bus.Options{Name: "netra " + roles.String()})
	if roles.All() {
		if err != nil {
			slog.Error("nats disabled", "err", err)
			return nil, nil
		}
		return nc, nil
	}
	if err != nil {
		return nil, fmt.Errorf("NETRA_NATS_URL: %w", err)
	}
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := nc.WaitConnected(wait); err != nil {
		var refused *bus.ServerError
		if errors.As(err, &refused) {
			nc.Close()
			return nil, fmt.Errorf("NETRA_NATS_URL: %w", err)
		}
		slog.Warn("nats not reachable yet, retrying in the background", "err", err)
	}
	return nc, nil
}

// relayHandlers keeps the ends of the relay of the roles this process runs.
func relayHandlers(srv *api.Server, roles role.Set, reloadLicense func()) relay.Handlers {
	h := srv.Handlers()
	if !roles.Has(role.API) {
		h.Dashboards = nil
	}
	if !roles.Has(role.Gateway) {
		h.Gateway = nil
	}
	if !roles.Has(role.Worker) {
		h.Wake, h.Alert = nil, nil
	}
	h.License = reloadLicense
	return h
}

// Detector picks the final detection: an external service (NETRA_FINAL_DETECTOR_URL), the
// built-in one on Ollama (NETRA_OLLAMA_URL), or the robot's own result.
func Detector(cfg config.Config) detect.Detector {
	switch {
	case cfg.FinalDetectorURL != "":
		return detect.NewHTTP(cfg.FinalDetectorURL)
	case cfg.OllamaURL != "":
		return detect.NewOllama(cfg.OllamaURL, cfg.AIModel, cfg.AILanguage, cfg.AISite, cfg.AICategories,
			cfg.AIMaxSide, cfg.AITimeout, cfg.AIKeepAlive, cfg.AIConcurrency)
	default:
		return detect.PassThrough{}
	}
}

// tlsFiles returns the certificate and key to serve HTTPS with, or none.
func tlsFiles(cfg config.Config) (string, string, error) {
	switch {
	case cfg.TLSCert != "" || cfg.TLSKey != "":
		if cfg.TLSCert == "" || cfg.TLSKey == "" {
			return "", "", errors.New("NETRA_TLS_CERT and NETRA_TLS_KEY go together")
		}
		return cfg.TLSCert, cfg.TLSKey, nil
	case strings.EqualFold(cfg.TLS, "self-signed"):
		return tlsutil.SelfSigned(filepath.Join(cfg.DataDir, "tls"), cfg.TLSHosts)
	case cfg.TLS == "" || strings.EqualFold(cfg.TLS, "off"):
		return "", "", nil
	default:
		return "", "", fmt.Errorf("NETRA_TLS=%q: use self-signed, off, or NETRA_TLS_CERT and NETRA_TLS_KEY", cfg.TLS)
	}
}

// LoadLicense applies the newest valid license of the license file, NETRA_LICENSE and the one
// uploaded in the dashboard. Without one, NETRA runs the core only.
func LoadLicense(ctx context.Context, cfg config.Config, st *store.Store) (*license.State, error) {
	keys, err := license.TrustedKeys()
	if err != nil {
		return nil, err
	}
	installation, err := st.EnsureSetting(ctx, store.SettingInstallation, license.NewInstallationID())
	if err != nil {
		return nil, err
	}
	lic := license.NewState(keys, installation)
	lic.Load(licenseCandidates(ctx, cfg, st)...)

	info := lic.Info()
	switch {
	case len(keys) == 0:
		slog.Warn("license: no trusted key built in, running the core only")
	case info.ID == "":
		slog.Warn("license: none valid, running the core only", "status", info.Status, "err", info.Error)
	default:
		slog.Info("license", "id", info.ID, "client", info.Client, "edition", info.Edition, "tier", info.Tier,
			"status", info.Status, "features", info.Features, "map", info.Map, "max_robots", info.MaxRobots,
			"expires", info.ExpiresAt, "source", info.Source, "bound", info.Installation != "")
		if info.ExpiresAt != nil && time.Until(*info.ExpiresAt) < 30*24*time.Hour {
			slog.Warn("license expires soon", "expires", info.ExpiresAt, "grace_ends", info.GraceEnds)
		}
	}
	return lic, nil
}

// licenseCandidates are the license file, NETRA_LICENSE and the upload kept in the database.
func licenseCandidates(ctx context.Context, cfg config.Config, st *store.Store) []license.Candidate {
	var cands []license.Candidate
	if cfg.LicenseFile != "" {
		raw, err := os.ReadFile(cfg.LicenseFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("license file", "path", cfg.LicenseFile, "err", err)
		}
		cands = append(cands, license.Candidate{Text: string(raw), Source: "file"})
	}
	cands = append(cands, license.Candidate{Text: cfg.License, Source: "env"})
	uploaded, err := st.UploadedLicense(ctx)
	if err != nil {
		slog.Warn("uploaded license", "err", err)
	}
	return append(cands, license.Candidate{Text: uploaded, Source: "upload"})
}

// reloadLicense applies the license again after another process of a split installation changed it.
func reloadLicense(cfg config.Config, st *store.Store, lic *license.State) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lic.Load(licenseCandidates(ctx, cfg, st)...)
	info := lic.Info()
	slog.Info("license reloaded", "id", info.ID, "status", info.Status, "features", info.Features)
}
