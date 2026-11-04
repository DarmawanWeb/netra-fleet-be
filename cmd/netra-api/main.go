// Command netra-api runs the NETRA patrol command-center backend.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // time zones without OS tzdata in the container

	"github.com/DarmawanWeb/netra-fleet-be/internal/api"
	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/bus"
	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
	"github.com/DarmawanWeb/netra-fleet-be/internal/detect"
	"github.com/DarmawanWeb/netra-fleet-be/internal/hub"
	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
	"github.com/DarmawanWeb/netra-fleet-be/internal/notify"
	"github.com/DarmawanWeb/netra-fleet-be/internal/scheduler"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
	"github.com/DarmawanWeb/netra-fleet-be/internal/version"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	if cfg.AdminPassword != "" {
		if err := st.EnsureUser(ctx, cfg.AdminUser, auth.HashPassword(cfg.AdminPassword), "admin"); err != nil {
			slog.Error("admin user", "err", err)
			os.Exit(1)
		}
	}

	var det detect.Detector = detect.PassThrough{}
	if cfg.FinalDetectorURL != "" {
		det = detect.NewHTTP(cfg.FinalDetectorURL)
	}
	lic, err := loadLicense(ctx, cfg, st)
	if err != nil {
		slog.Error("license", "err", err)
		os.Exit(1)
	}
	srv := api.New(cfg, st, hub.New(), notify.NewTelegram(cfg.TelegramToken, cfg.TelegramChatID, cfg.TelegramAPI), det,
		bus.New(cfg.NatsURL), lic)
	go srv.RunPipeline(ctx)
	go scheduler.Run(ctx, st, cfg.TimeZone, srv.StartPatrol)

	httpServer := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	slog.Info("netra-api listening", "addr", cfg.Addr, "tz", cfg.TimeZone.String(), "version", version.String(),
		"installation", lic.Installation())
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("http", "err", err)
		os.Exit(1)
	}
}

// loadLicense applies the newest valid license of the license file, NETRA_LICENSE and the one
// uploaded in the dashboard. Without one, netra-api runs the core only.
func loadLicense(ctx context.Context, cfg config.Config, st *store.Store) (*license.State, error) {
	keys, err := license.TrustedKeys()
	if err != nil {
		return nil, err
	}
	installation, err := st.EnsureSetting(ctx, store.SettingInstallation, license.NewInstallationID())
	if err != nil {
		return nil, err
	}
	lic := license.NewState(keys, installation)
	var cands []license.Candidate
	if cfg.LicenseFile != "" {
		raw, err := os.ReadFile(cfg.LicenseFile)
		if err != nil {
			slog.Warn("license file", "path", cfg.LicenseFile, "err", err)
		}
		cands = append(cands, license.Candidate{Text: string(raw), Source: "file"})
	}
	cands = append(cands, license.Candidate{Text: cfg.License, Source: "env"})
	uploaded, err := st.UploadedLicense(ctx)
	if err != nil {
		slog.Warn("uploaded license", "err", err)
	}
	cands = append(cands, license.Candidate{Text: uploaded, Source: "upload"})
	lic.Load(cands...)

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
