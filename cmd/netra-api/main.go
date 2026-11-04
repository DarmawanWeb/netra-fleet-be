// Command netra-api runs the NETRA patrol command center (the Docker image's entry point); the
// installers ship the same service as `netra serve`.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // time zones without OS tzdata in the container

	"github.com/DarmawanWeb/netra-fleet-be/internal/app"
	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Serve(ctx, config.Load()); err != nil {
		slog.Error("netra-api", "err", err)
		os.Exit(1)
	}
}
