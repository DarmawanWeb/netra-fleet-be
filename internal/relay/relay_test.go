package relay

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeGateway holds robots in memory and lets the test answer commands.
type fakeGateway struct {
	mu        sync.Mutex
	robots    map[string]*float64
	skews     map[string]time.Duration
	pending   map[string]func(Ack)
	delivered []Command
}

func newFakeGateway(ids ...string) *fakeGateway {
	g := &fakeGateway{robots: map[string]*float64{}, skews: map[string]time.Duration{}, pending: map[string]func(Ack){}}
	for _, id := range ids {
		g.robots[id] = nil
	}
	return g
}

var errNotHere = errors.New("robot offline")

func (g *fakeGateway) Deliver(robotID string, cmd Command, ack func(Ack)) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.robots[robotID]; !ok {
		return nil, errNotHere
	}
	g.delivered = append(g.delivered, cmd)
	if ack != nil {
		g.pending[cmd.ID] = ack
	}
	return func() {
		g.mu.Lock()
		delete(g.pending, cmd.ID)
		g.mu.Unlock()
	}, nil
}

func (g *fakeGateway) Robots() []Robot {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []Robot{}
	for id, skew := range g.robots {
		out = append(out, Robot{ID: id, SkewS: skew})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (g *fakeGateway) ClockSkew(robotID string) (time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	skew, ok := g.skews[robotID]
	return skew, ok
}

func (g *fakeGateway) set(id string, online bool, skew *float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if online {
		g.robots[id] = skew
	} else {
		delete(g.robots, id)
	}
}

// answer acks a pending command once it arrives (run it in a goroutine).
func (g *fakeGateway) answer(t *testing.T, id string, a Ack) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		f := g.pending[id]
		delete(g.pending, id)
		g.mu.Unlock()
		if f != nil {
			f(a)
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Errorf("command %s never arrived", id)
}

func (g *fakeGateway) waiting() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.pending)
}

func (g *fakeGateway) sent() []Command {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Command(nil), g.delivered...)
}

func TestLocal(t *testing.T) {
	g := newFakeGateway("l1w-01")
	g.skews["l1w-01"] = 3 * time.Second
	var live [][]byte
	wakes, alerts := make(chan struct{}, 1), make(chan Alert, 1)
	l := NewLocal(Handlers{
		Dashboards: func(b []byte) { live = append(live, b) },
		Gateway:    g,
		Wake:       func() { wakes <- struct{}{} },
		Alert:      func(a Alert) { alerts <- a },
	})

	l.Broadcast(map[string]any{"type": "route", "id": 3})
	if len(live) != 1 || string(live[0]) != `{"id":3,"type":"route"}` {
		t.Fatalf("live %q", live)
	}
	l.Broadcast(func() {}) // not JSON: dropped
	if len(live) != 1 {
		t.Fatal("an unencodable message reached the dashboards")
	}

	if _, err := l.Command(context.Background(), "l1w-01", Command{ID: "pause-1", Name: "pause"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Command(context.Background(), "gone", Command{ID: "pause-2", Name: "pause"}); !errors.Is(err, errNotHere) {
		t.Fatalf("offline robot: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go g.answer(t, "set_people-1", Ack{OK: false, Msg: "no model"})
	a, err := l.Command(ctx, "l1w-01", Command{ID: "set_people-1", Name: "set_people", Wait: true})
	if err != nil || a.OK || a.Msg != "no model" {
		t.Fatalf("ack %+v %v", a, err)
	}
	short, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	if _, err := l.Command(short, "l1w-01", Command{ID: "set_people-2", Name: "set_people", Wait: true}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	if g.waiting() != 0 {
		t.Fatal("a timed-out command is still pending")
	}

	if !l.Online("l1w-01") || l.Online("gone") || l.OnlineCount() != 1 {
		t.Fatal("presence")
	}
	if skew, ok := l.ClockSkew("l1w-01"); !ok || skew != 3*time.Second {
		t.Fatalf("skew %v %v", skew, ok)
	}
	l.Wake()
	<-wakes
	l.Alert(Alert{Kind: AlertAnomaly, ID: 9})
	if got := <-alerts; got.ID != 9 {
		t.Fatalf("alert %+v", got)
	}
	l.RobotsChanged()
	l.LicenseChanged()
	if l.Ready() != nil {
		t.Fatal("local relay not ready")
	}
}
