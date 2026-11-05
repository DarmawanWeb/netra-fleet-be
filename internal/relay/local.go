package relay

import (
	"context"
	"encoding/json"
	"time"
)

// Local is the relay of a process that runs every role: each call goes straight to the handler.
type Local struct{ h Handlers }

// NewLocal relays between the handlers of one process; all of Dashboards, Gateway, Wake and Alert
// are needed.
func NewLocal(h Handlers) *Local { return &Local{h: h} }

// Broadcast sends msg to the dashboards of this process.
func (l *Local) Broadcast(msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	l.h.Dashboards(b)
}

// Command hands cmd to the robot connections of this process.
func (l *Local) Command(ctx context.Context, robotID string, cmd Command) (Ack, error) {
	if !cmd.Wait {
		_, err := l.h.Gateway.Deliver(robotID, cmd, nil)
		return Ack{}, err
	}
	done := make(chan Ack, 1)
	cancel, err := l.h.Gateway.Deliver(robotID, cmd, func(a Ack) {
		select {
		case done <- a:
		default:
		}
	})
	if err != nil {
		return Ack{}, err
	}
	select {
	case a := <-done:
		return a, nil
	case <-ctx.Done():
		cancel()
		return Ack{}, ctx.Err()
	}
}

// Online reports whether the robot is connected to this process.
func (l *Local) Online(robotID string) bool {
	for _, r := range l.h.Gateway.Robots() {
		if r.ID == robotID {
			return true
		}
	}
	return false
}

// OnlineCount is the number of robots connected to this process.
func (l *Local) OnlineCount() int { return len(l.h.Gateway.Robots()) }

// ClockSkew is the robot's clock offset measured by this process.
func (l *Local) ClockSkew(robotID string) (time.Duration, bool) {
	return l.h.Gateway.ClockSkew(robotID)
}

// RobotsChanged needs nothing: Online reads the connections themselves.
func (l *Local) RobotsChanged() {}

// Wake wakes the pipeline of this process.
func (l *Local) Wake() { l.h.Wake() }

// Alert sends the alert from a goroutine, so the caller does not wait for Telegram.
func (l *Local) Alert(a Alert) { go l.h.Alert(a) }

// LicenseChanged needs nothing: the only process already applied it.
func (l *Local) LicenseChanged() {}

// Ready is always true in one process.
func (l *Local) Ready() error { return nil }
