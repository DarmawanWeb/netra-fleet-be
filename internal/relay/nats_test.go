package relay

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/bus"
	"github.com/DarmawanWeb/netra-fleet-be/internal/bus/bustest"
)

func client(t *testing.T, srv *bustest.Server) *bus.Client {
	t.Helper()
	c, err := bus.Dial(srv.URL(), bus.Options{ReconnectMin: 10 * time.Millisecond, ReconnectMax: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitConnected(ctx); err != nil {
		t.Fatal(err)
	}
	return c
}

func start(t *testing.T, srv *bustest.Server, node string, h Handlers) *NATS {
	t.Helper()
	n := NewNATS(client(t, srv), h, Options{Node: node, Heartbeat: 50 * time.Millisecond, Expiry: 300 * time.Millisecond,
		SendTimeout: time.Second})
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	return n
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// liveBox collects the live messages of an api process.
type liveBox struct {
	mu   sync.Mutex
	msgs []string
}

func (b *liveBox) add(m []byte) {
	b.mu.Lock()
	b.msgs = append(b.msgs, string(m))
	b.mu.Unlock()
}

func (b *liveBox) has(s string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.msgs {
		if m == s {
			return true
		}
	}
	return false
}

func TestNATSLiveAndPresence(t *testing.T) {
	srv := bustest.New(t)
	var api1, api2 liveBox
	a1 := start(t, srv, "api-1", Handlers{Dashboards: api1.add})
	a2 := start(t, srv, "api-2", Handlers{Dashboards: api2.add})
	g := newFakeGateway()
	gw := start(t, srv, "gw-1", Handlers{Gateway: g})
	srv.WaitInterest(SubjectLive, 2)

	gw.Broadcast(map[string]any{"type": "robot_online", "robot_id": "l1w-01", "online": true})
	const want = `{"online":true,"robot_id":"l1w-01","type":"robot_online"}`
	eventually(t, "live message on every api process", func() bool { return api1.has(want) && api2.has(want) })

	skew := 7200.5
	g.set("l1w-01", true, &skew)
	gw.RobotsChanged()
	eventually(t, "robot online on the api processes", func() bool { return a1.Online("l1w-01") && a2.Online("l1w-01") })
	if d, ok := a1.ClockSkew("l1w-01"); !ok || d != 7200500*time.Millisecond || a1.OnlineCount() != 1 {
		t.Fatalf("skew %v %v count %d", d, ok, a1.OnlineCount())
	}
	srv.WaitInterest(SubjectCommand+"l1w-01", 1)

	g.set("l1w-01", false, nil)
	gw.RobotsChanged()
	eventually(t, "robot offline after disconnect", func() bool { return !a1.Online("l1w-01") })
	srv.WaitInterest(SubjectCommand+"l1w-01", 0)
	if _, ok := a1.ClockSkew("l1w-01"); !ok {
		t.Fatal("the last clock offset is kept after the robot left")
	}

	// a new api process asks the gateways instead of waiting for the next heartbeat
	g.set("l1w-02", true, nil)
	gw.RobotsChanged()
	late := start(t, srv, "api-3", Handlers{Dashboards: func([]byte) {}})
	eventually(t, "robots of a running gateway on a new api process", func() bool { return late.Online("l1w-02") })

	// a gateway that goes silent expires; one that closes says so at once
	g2 := newFakeGateway("x30-01")
	gw2 := start(t, srv, "gw-2", Handlers{Gateway: g2})
	eventually(t, "second gateway", func() bool { return a1.Online("x30-01") && a1.OnlineCount() == 2 })
	// a robot's upload may reach another gateway than its telemetry: that one knows the offset too
	behind := 30.0
	g.set("l1w-02", true, &behind)
	gw.RobotsChanged()
	eventually(t, "offset measured by another gateway", func() bool {
		d, ok := gw2.ClockSkew("l1w-02")
		return ok && d == 30*time.Second
	})
	gw2.Close()
	eventually(t, "robots of a closed gateway", func() bool { return !a1.Online("x30-01") })
}

func TestNATSPresenceExpires(t *testing.T) {
	srv := bustest.New(t)
	a := start(t, srv, "api", Handlers{Dashboards: func([]byte) {}})
	srv.WaitInterest(SubjectPresence, 1)
	c := client(t, srv)
	data, _ := json.Marshal(presence{Gateway: "crashed", Robots: []Robot{{ID: "l1w-01"}}})
	if err := c.Publish(SubjectPresence, data); err != nil {
		t.Fatal(err)
	}
	eventually(t, "robot of a crashed gateway online", func() bool { return a.Online("l1w-01") })
	eventually(t, "robot of a crashed gateway expired", func() bool { return !a.Online("l1w-01") })
}

func TestNATSCommands(t *testing.T) {
	srv := bustest.New(t)
	api := start(t, srv, "api", Handlers{Dashboards: func([]byte) {}})
	g := newFakeGateway("l1w-01")
	gw := start(t, srv, "gw", Handlers{Gateway: g})
	srv.WaitInterest(SubjectCommand+"l1w-01", 1)
	ctx := context.Background()

	if _, err := api.Command(ctx, "l1w-01", Command{ID: "start_patrol-1", Name: "start_patrol",
		Args: map[string]any{"route_id": 4}, PatrolID: 12}); err != nil {
		t.Fatal(err)
	}
	sent := g.sent()
	if len(sent) != 1 || sent[0].PatrolID != 12 || sent[0].Args["route_id"] != 4.0 || sent[0].Wait {
		t.Fatalf("gateway got %+v", sent)
	}

	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	go g.answer(t, "set_inspection-1", Ack{OK: true, Msg: "inspection on"})
	a, err := api.Command(wait, "l1w-01", Command{ID: "set_inspection-1", Name: "set_inspection", Wait: true})
	if err != nil || !a.OK || a.Msg != "inspection on" {
		t.Fatalf("ack %+v %v", a, err)
	}

	short, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()
	if _, err := api.Command(short, "l1w-01", Command{ID: "set_people-1", Name: "set_people", Wait: true}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unanswered command: %v", err)
	}
	eventually(t, "the gateway forgets a command nobody waits for", func() bool { return g.waiting() == 0 })

	// no gateway subscribed: no responders
	if _, err := api.Command(ctx, "nobody", Command{ID: "pause-1", Name: "pause"}); !errors.Is(err, ErrOffline) {
		t.Fatalf("robot without gateway: %v", err)
	}
	// the gateway still subscribed but the robot just left
	g.set("l1w-01", false, nil)
	if _, err := api.Command(ctx, "l1w-01", Command{ID: "pause-2", Name: "pause"}); !errors.Is(err, ErrOffline) {
		t.Fatalf("robot that left: %v", err)
	}
	gw.RobotsChanged()
	srv.WaitInterest(SubjectCommand+"l1w-01", 0)
}

func TestNATSWorkerWakeAlertsAndLicense(t *testing.T) {
	srv := bustest.New(t)
	var wakes, alerts1, alerts2, licA, licW atomic.Int32
	api := start(t, srv, "api", Handlers{Dashboards: func([]byte) {}, License: func() { licA.Add(1) }})
	w1 := start(t, srv, "worker-1", Handlers{Wake: func() { wakes.Add(1) }, Alert: func(Alert) { alerts1.Add(1) },
		License: func() { licW.Add(1) }})
	start(t, srv, "worker-2", Handlers{Alert: func(Alert) { alerts2.Add(1) }})
	gw := start(t, srv, "gw", Handlers{Gateway: newFakeGateway()})
	srv.WaitInterest(SubjectAlert, 2)
	srv.WaitInterest(SubjectLicense, 2)
	srv.WaitInterest(SubjectWake, 1)

	gw.Wake()
	eventually(t, "worker woken", func() bool { return wakes.Load() == 1 })
	for i := 0; i < 6; i++ {
		gw.Alert(Alert{Kind: AlertFinding, ID: int64(i), Inspection: 3, Chat: "-100"})
	}
	eventually(t, "every alert taken once", func() bool { return alerts1.Load()+alerts2.Load() == 6 })
	time.Sleep(50 * time.Millisecond)
	if alerts1.Load()+alerts2.Load() != 6 {
		t.Fatal("an alert went to both workers")
	}

	api.LicenseChanged()
	eventually(t, "worker reloads the license", func() bool { return licW.Load() == 1 })
	time.Sleep(50 * time.Millisecond)
	if licA.Load() != 0 {
		t.Fatal("the process that changed the license reloaded it")
	}
	if api.Ready() != nil || w1.Ready() != nil {
		t.Fatal("relay not ready while connected")
	}
}

func TestNATSReconnect(t *testing.T) {
	srv := bustest.New(t)
	var live liveBox
	api := start(t, srv, "api", Handlers{Dashboards: live.add})
	g := newFakeGateway("l1w-01")
	gw := start(t, srv, "gw", Handlers{Gateway: g})
	eventually(t, "online", func() bool { return api.Online("l1w-01") })

	srv.Stop()
	eventually(t, "not ready while NATS is down", func() bool { return api.Ready() != nil })
	if _, err := api.Command(context.Background(), "l1w-01", Command{ID: "pause-1", Name: "pause"}); err == nil {
		t.Fatal("command sent without NATS")
	}
	srv.Restart()
	srv.WaitInterest(SubjectCommand+"l1w-01", 1) // the gateway subscribes again
	srv.WaitInterest(SubjectLive, 1)
	if _, err := api.Command(context.Background(), "l1w-01", Command{ID: "pause-2", Name: "pause"}); err != nil {
		t.Fatalf("command after reconnect: %v", err)
	}
	gw.Broadcast(map[string]string{"type": "hello"})
	eventually(t, "live after reconnect", func() bool { return live.has(`{"type":"hello"}`) })
}
