package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/bus"
)

// The subjects between the roles. They start with _NETRA so they stay out of the domain events
// (netra.>) other systems subscribe to.
const (
	SubjectLive          = "_NETRA.live"           // a dashboard message; api processes subscribe
	SubjectCommand       = "_NETRA.command."       // + robot ID: request to the gateway holding the robot
	SubjectPresence      = "_NETRA.presence"       // a gateway's robots, on every change and every heartbeat
	SubjectPresenceQuery = "_NETRA.presence.query" // an api process asks every gateway for its robots
	SubjectWake          = "_NETRA.wake"           // the pipeline has work; worker processes subscribe
	SubjectAlert         = "_NETRA.alert"          // Telegram work; one worker of the queue group takes it
	SubjectLicense       = "_NETRA.license"        // the license changed in the database
	queueWorker          = "worker"
)

// Options tune the NATS relay; zero values take the defaults.
type Options struct {
	Node        string        // this process in presence messages (default host-pid)
	Heartbeat   time.Duration // how often a gateway repeats its robots (default 10 s)
	Expiry      time.Duration // a gateway's robots count as offline this long after its last message (default 35 s)
	SendTimeout time.Duration // how long a command that does not wait for its ack waits for the gateway (default 5 s)
}

func (o *Options) defaults() {
	if o.Node == "" {
		host, _ := os.Hostname()
		o.Node = host + "-" + strconv.Itoa(os.Getpid())
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 10 * time.Second
	}
	if o.Expiry <= o.Heartbeat {
		o.Expiry = max(35*time.Second, 3*o.Heartbeat+o.Heartbeat/2)
	}
	if o.SendTimeout <= 0 {
		o.SendTimeout = 5 * time.Second
	}
}

// presence is what a gateway publishes on SubjectPresence: all of its robots.
type presence struct {
	Gateway string  `json:"gateway"`
	Robots  []Robot `json:"robots"`
}

type commandRequest struct {
	Command
	TimeoutMS int64 `json:"timeout_ms,omitempty"` // Wait: how long the sender waits for the ack
}

// commandReply answers a command request once: sent (no wait), ack (the robot's answer) or offline.
type commandReply struct {
	Status string `json:"status"`
	OK     bool   `json:"ok,omitempty"`
	Msg    string `json:"msg,omitempty"`
	Error  string `json:"error,omitempty"`
}

type gatewayRobots struct {
	robots  map[string]bool
	expires time.Time
}

// NATS is the relay of a split installation.
type NATS struct {
	c *bus.Client
	h Handlers
	o Options

	mu       sync.Mutex
	gateways map[string]gatewayRobots     // api: the robots of each gateway
	skews    map[string]time.Duration     // api: last clock offset reported per robot
	commands map[string]*bus.Subscription // gateway: the command subject of each robot connected here
	subs     []*bus.Subscription

	changed chan struct{}
	stop    chan struct{}
	wg      sync.WaitGroup
	once    sync.Once
}

// NewNATS relays over c between this process's handlers and the other processes.
func NewNATS(c *bus.Client, h Handlers, o Options) *NATS {
	o.defaults()
	return &NATS{c: c, h: h, o: o, gateways: map[string]gatewayRobots{}, skews: map[string]time.Duration{},
		commands: map[string]*bus.Subscription{}, changed: make(chan struct{}, 1), stop: make(chan struct{})}
}

// Node is the name of this process in presence messages.
func (n *NATS) Node() string { return n.o.Node }

// Start subscribes to what this process's handlers take and starts the gateway's heartbeat.
func (n *NATS) Start() error {
	sub := func(subject string, f func(bus.Msg)) error {
		s, err := n.c.Subscribe(subject, f)
		if err == nil {
			n.subs = append(n.subs, s)
		}
		return err
	}
	var errs []error
	if n.h.Dashboards != nil {
		errs = append(errs, sub(SubjectLive, func(m bus.Msg) { n.h.Dashboards(m.Data) }),
			sub(SubjectPresence, n.presence))
		n.c.OnConnect(n.askPresence)
	}
	if n.h.Gateway != nil {
		errs = append(errs, sub(SubjectPresenceQuery, func(bus.Msg) { n.RobotsChanged() }))
		n.c.OnConnect(n.RobotsChanged)
		n.wg.Add(1)
		go n.heartbeat()
	}
	if n.h.Wake != nil {
		errs = append(errs, sub(SubjectWake, func(bus.Msg) { n.h.Wake() }))
	}
	if n.h.Alert != nil {
		s, err := n.c.QueueSubscribe(SubjectAlert, queueWorker, func(m bus.Msg) {
			var a Alert
			if json.Unmarshal(m.Data, &a) == nil {
				go n.h.Alert(a)
			}
		})
		if err == nil {
			n.subs = append(n.subs, s)
		}
		errs = append(errs, err)
	}
	if n.h.License != nil {
		errs = append(errs, sub(SubjectLicense, func(m bus.Msg) {
			var from struct {
				Node string `json:"node"`
			}
			if json.Unmarshal(m.Data, &from) == nil && from.Node != n.o.Node {
				n.h.License()
			}
		}))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if n.c.Connected() { // OnConnect only runs for the next connections
		if n.h.Dashboards != nil {
			n.askPresence()
		}
		n.RobotsChanged()
	}
	return nil
}

// Close stops the heartbeat, tells the api processes this gateway's robots are gone and waits
// until the server has it.
func (n *NATS) Close() {
	n.once.Do(func() {
		close(n.stop)
		n.wg.Wait()
		if n.h.Gateway != nil {
			n.mu.Lock()
			for id, s := range n.commands {
				_ = s.Unsubscribe()
				delete(n.commands, id)
			}
			n.mu.Unlock()
			n.publishJSON(SubjectPresence, presence{Gateway: n.o.Node, Robots: []Robot{}})
		}
		for _, s := range n.subs {
			_ = s.Unsubscribe()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = n.c.Flush(ctx)
	})
}

func (n *NATS) publishJSON(subject string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	if err := n.c.Publish(subject, data); err != nil && !errors.Is(err, bus.ErrNotConnected) {
		slog.Warn("relay publish", "subject", subject, "err", err)
	}
}

// Broadcast publishes a live message for every api process; it is dropped while NATS is down.
func (n *NATS) Broadcast(msg any) { n.publishJSON(SubjectLive, msg) }

// Command asks the gateway holding the robot to deliver cmd. Nobody subscribed to the robot's
// subject means no gateway holds it: ErrOffline at once.
func (n *NATS) Command(ctx context.Context, robotID string, cmd Command) (Ack, error) {
	req := commandRequest{Command: cmd}
	if cmd.Wait {
		if dl, ok := ctx.Deadline(); ok {
			req.TimeoutMS = max(1, time.Until(dl).Milliseconds())
		}
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, n.o.SendTimeout)
		defer cancel()
	}
	data, err := json.Marshal(req)
	if err != nil {
		return Ack{}, err
	}
	m, err := n.c.Request(ctx, SubjectCommand+robotID, data)
	switch {
	case errors.Is(err, bus.ErrNoResponders):
		return Ack{}, ErrOffline
	case errors.Is(err, context.DeadlineExceeded) && !cmd.Wait:
		return Ack{}, fmt.Errorf("the gateway of %s did not answer", robotID)
	case err != nil:
		return Ack{}, err
	}
	var rep commandReply
	if err := json.Unmarshal(m.Data, &rep); err != nil {
		return Ack{}, fmt.Errorf("gateway reply: %w", err)
	}
	switch rep.Status {
	case "ack":
		return Ack{OK: rep.OK, Msg: rep.Msg}, nil
	case "sent":
		return Ack{}, nil
	default:
		return Ack{}, ErrOffline
	}
}

// serveCommand is the gateway end of Command for one robot.
func (n *NATS) serveCommand(robotID string) func(bus.Msg) {
	return func(m bus.Msg) {
		var req commandRequest
		if m.Reply == "" || json.Unmarshal(m.Data, &req) != nil {
			return
		}
		reply := func(r commandReply) {
			data, _ := json.Marshal(r)
			if err := m.Respond(data); err != nil {
				slog.Warn("relay command reply", "robot", robotID, "command", req.ID, "err", err)
			}
		}
		var ack func(Ack)
		if req.Wait {
			ack = func(a Ack) { reply(commandReply{Status: "ack", OK: a.OK, Msg: a.Msg}) }
		}
		cancel, err := n.h.Gateway.Deliver(robotID, req.Command, ack)
		switch {
		case err != nil:
			reply(commandReply{Status: "offline", Error: err.Error()})
		case !req.Wait:
			reply(commandReply{Status: "sent"})
		default:
			wait := time.Duration(req.TimeoutMS) * time.Millisecond
			if wait <= 0 || wait > time.Minute {
				wait = 10 * time.Second
			}
			time.AfterFunc(wait, cancel)
		}
	}
}

// RobotsChanged makes the gateway subscribe to the commands of its robots and publish them.
func (n *NATS) RobotsChanged() {
	if n.h.Gateway == nil {
		return
	}
	select {
	case n.changed <- struct{}{}:
	default:
	}
}

func (n *NATS) heartbeat() {
	defer n.wg.Done()
	t := time.NewTicker(n.o.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-n.stop:
			return
		case <-n.changed:
		case <-t.C:
		}
		n.syncRobots()
	}
}

// syncRobots subscribes to the command subject of every robot connected here (before announcing
// it, so a command sent after the announcement finds the subscription) and publishes the list.
func (n *NATS) syncRobots() {
	robots := n.h.Gateway.Robots()
	want := make(map[string]bool, len(robots))
	n.mu.Lock()
	for _, r := range robots {
		want[r.ID] = true
		if n.commands[r.ID] == nil {
			s, err := n.c.Subscribe(SubjectCommand+r.ID, n.serveCommand(r.ID))
			if err != nil {
				slog.Warn("relay subscribe", "robot", r.ID, "err", err)
				continue
			}
			n.commands[r.ID] = s
		}
	}
	for id, s := range n.commands {
		if !want[id] {
			_ = s.Unsubscribe()
			delete(n.commands, id)
		}
	}
	n.mu.Unlock()
	n.publishJSON(SubjectPresence, presence{Gateway: n.o.Node, Robots: robots})
}

func (n *NATS) askPresence() { n.publishJSON(SubjectPresenceQuery, struct{}{}) }

// presence records the robots of one gateway (api end).
func (n *NATS) presence(m bus.Msg) {
	var p presence
	if json.Unmarshal(m.Data, &p) != nil || p.Gateway == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(p.Robots) == 0 {
		delete(n.gateways, p.Gateway)
		return
	}
	g := gatewayRobots{robots: make(map[string]bool, len(p.Robots)), expires: time.Now().Add(n.o.Expiry)}
	for _, r := range p.Robots {
		g.robots[r.ID] = true
		if skew, ok := skewOf(r); ok {
			n.skews[r.ID] = skew
		}
	}
	n.gateways[p.Gateway] = g
}

// online returns the robots of the gateways heard from recently; n.mu is held.
func (n *NATS) online() map[string]bool {
	now := time.Now()
	out := map[string]bool{}
	for name, g := range n.gateways {
		if now.After(g.expires) {
			delete(n.gateways, name)
			continue
		}
		for id := range g.robots {
			out[id] = true
		}
	}
	return out
}

// Online reports whether a gateway reported the robot recently.
func (n *NATS) Online(robotID string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.online()[robotID]
}

// OnlineCount is the number of robots the gateways report.
func (n *NATS) OnlineCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.online())
}

// ClockSkew is the last clock offset a gateway reported for the robot.
func (n *NATS) ClockSkew(robotID string) (time.Duration, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	skew, ok := n.skews[robotID]
	return skew, ok
}

// Wake tells the workers that an anomaly is queued.
func (n *NATS) Wake() {
	if err := n.c.Publish(SubjectWake, nil); err != nil && !errors.Is(err, bus.ErrNotConnected) {
		slog.Warn("relay wake", "err", err)
	}
}

// Alert hands the alert to one worker.
func (n *NATS) Alert(a Alert) { n.publishJSON(SubjectAlert, a) }

// LicenseChanged tells the other processes to reload the license.
func (n *NATS) LicenseChanged() {
	n.publishJSON(SubjectLicense, map[string]string{"node": n.o.Node})
}

// Ready reports whether NATS is connected.
func (n *NATS) Ready() error {
	if !n.c.Connected() {
		return bus.ErrNotConnected
	}
	return nil
}
