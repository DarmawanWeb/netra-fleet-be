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
	"github.com/DarmawanWeb/netra-fleet-be/internal/notify"
	"github.com/DarmawanWeb/netra-fleet-be/internal/scheduler"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
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
	srv := api.New(cfg, st, hub.New(), notify.NewTelegram(cfg.TelegramToken, cfg.TelegramChatID, cfg.TelegramAPI), det, bus.New(cfg.NatsURL))
	go srv.RunPipeline(ctx)
	go scheduler.Run(ctx, st, cfg.TimeZone, srv.StartPatrol)

	httpServer := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	slog.Info("netra-api listening", "addr", cfg.Addr, "tz", cfg.TimeZone.String())
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("http", "err", err)
		os.Exit(1)
	}
}
