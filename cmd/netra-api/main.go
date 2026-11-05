// Command netra-api runs the NETRA patrol command center (the Docker image's entry point); the
// installers ship the same service as `netra serve`. -roles (or NETRA_ROLES) runs only some of
// its roles, for a split installation.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // time zones without OS tzdata in the container

	"github.com/DarmawanWeb/netra-fleet-be/internal/app"
	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
)

func main() {
	roles := flag.String("roles", "", "roles to run: all, or a comma list of api, gateway and worker (default NETRA_ROLES, else all)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := config.Load()
	if *roles != "" {
		cfg.Roles = *roles
	}
	if err := app.Serve(ctx, cfg); err != nil {
		slog.Error("netra-api", "err", err)
		os.Exit(1)
	}
}
