package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DarmawanWeb/netra-fleet-be/internal/api"
	"github.com/DarmawanWeb/netra-fleet-be/internal/bus"
	"github.com/DarmawanWeb/netra-fleet-be/internal/bus/bustest"
	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
	"github.com/DarmawanWeb/netra-fleet-be/internal/hub"
	"github.com/DarmawanWeb/netra-fleet-be/internal/role"
)

// TestSplitSetupsRefused checks the refusals that come before the database is opened.
func TestSplitSetupsRefused(t *testing.T) {
	cases := []struct {
		cfg  config.Config
		want string
	}{
		{config.Config{Roles: "api,web"}, `unknown role "web"`},
		{config.Config{Roles: "all,worker"}, "cannot be combined"},
		{config.Config{Roles: "gateway"}, "needs NETRA_NATS_URL"},
		{config.Config{Roles: "api,worker", NatsURL: "nats://localhost:4222", PGBin: "/usr/lib/postgresql/16/bin"},
			"cannot use NETRA_PG_BIN"},
		{config.Config{Roles: "worker", NatsURL: "localhost:4222"}, "use nats://host:port"},
	}
	for _, c := range cases {
		err := Serve(context.Background(), c.cfg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.cfg.Roles, err, c.want)
		}
	}
	if err := checkRoles(config.Config{}, role.All); err != nil {
		t.Fatalf("all-in-one refused: %v", err)
	}
}

func TestDialNATS(t *testing.T) {
	ctx := context.Background()
	if nc, err := dialNATS(ctx, config.Config{}, role.All); nc != nil || err != nil {
		t.Fatal("no URL must mean no NATS")
	}
	// all-in-one: NATS only carries domain events, a bad URL turns them off
	if nc, err := dialNATS(ctx, config.Config{NatsURL: "localhost:4222"}, role.All); nc != nil || err != nil {
		t.Fatalf("all-in-one with a bad URL: %v %v", nc, err)
	}
	locked := bustest.NewWith(t, bustest.Config{User: "netra", Pass: "right"})
	_, err := dialNATS(ctx, config.Config{NatsURL: strings.Replace(locked.URL(), "nats://", "nats://netra:wrong@", 1)},
		role.API)
	var refused *bus.ServerError
	if !errors.As(err, &refused) {
		t.Fatalf("refused login: %v", err)
	}
	open := bustest.New(t)
	nc, err := dialNATS(ctx, config.Config{NatsURL: open.URL()}, role.Worker)
	if err != nil || !nc.Connected() {
		t.Fatalf("split process: %v", err)
	}
	nc.Close()
}

func TestRelayHandlersFollowTheRoles(t *testing.T) {
	srv := api.New(config.Config{}, nil, hub.New(), nil, nil, nil, nil)
	reload := func() {}
	h := relayHandlers(srv, role.Gateway|role.Worker, reload)
	if h.Dashboards != nil || h.Gateway == nil || h.Wake == nil || h.Alert == nil || h.License == nil {
		t.Fatalf("gateway and worker: %+v", h)
	}
	h = relayHandlers(srv, role.API, reload)
	if h.Dashboards == nil || h.Gateway != nil || h.Wake != nil || h.Alert != nil || h.License == nil {
		t.Fatalf("api: %+v", h)
	}
}
