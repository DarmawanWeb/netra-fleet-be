// Package relay carries what crosses the roles of NETRA: live messages for the dashboards, robot
// commands and their acks, which robots are online, the wake-up of the anomaly pipeline, Telegram
// alerts and license changes. Local does it inside one process (every role together); NATS does
// it between the processes of a split installation.
package relay

import (
	"context"
	"errors"
	"time"
)

// ErrOffline is returned for a command to a robot that no gateway holds.
var ErrOffline = errors.New("robot offline")

// Command is a command for a robot, as the robot receives it ({"type": "command", "id", "name",
// "args"}) plus what the server keeps until the ack.
type Command struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Args     map[string]any `json:"args,omitempty"`
	PatrolID int64          `json:"patrol_id,omitempty"` // the patrol whose status the ack changes
	Wait     bool           `json:"wait,omitempty"`      // the sender waits for the ack
}

// Ack is a robot's answer to a command.
type Ack struct {
	OK  bool   `json:"ok"`
	Msg string `json:"msg"`
}

// Robot is a robot connected to a gateway, with the offset of its clock (server time minus robot
// time, in seconds) once its telemetry has shown it.
type Robot struct {
	ID    string   `json:"id"`
	SkewS *float64 `json:"skew_s,omitempty"`
}

// Alert kinds.
const (
	AlertAnomaly = "anomaly" // a confirmed anomaly whose rule notifies Telegram
	AlertFinding = "finding" // a substation finding at the alert priority
)

// Alert is Telegram work for the worker.
type Alert struct {
	Kind       string `json:"kind"`
	ID         int64  `json:"id"`                      // the anomaly or the finding
	Inspection int64  `json:"inspection_id,omitempty"` // finding: the inspection that rated it
	Chat       string `json:"chat,omitempty"`          // finding: the site's chat
}

// Gateway is the robot end of the relay: the robot connections of one process.
type Gateway interface {
	// Deliver sends cmd to a robot connected to this process (an error such as ErrOffline
	// otherwise). With ack set, the robot's answer is passed to ack once, unless cancel is called
	// first; cancel forgets the command.
	Deliver(robotID string, cmd Command, ack func(Ack)) (cancel func(), err error)
	// Robots lists the robots connected to this process.
	Robots() []Robot
	// ClockSkew is the last measured clock offset of a robot, also after it disconnected.
	ClockSkew(robotID string) (time.Duration, bool)
}

// Handlers are the parts of a process the relay delivers to; a nil one is a role the process
// does not run.
type Handlers struct {
	Dashboards func(msg []byte) // api: a live message (JSON) for the dashboards connected here
	Gateway    Gateway          // gateway: the robots connected here
	Wake       func()           // worker: the anomaly pipeline has work
	Alert      func(Alert)      // worker: an alert to send
	License    func()           // any process: another one changed the license in the database
}

// Relay is what the handlers of every role use to reach the others.
type Relay interface {
	// Broadcast sends a live message to the dashboards of every api process.
	Broadcast(msg any)
	// Command delivers cmd to the gateway holding the robot. With cmd.Wait it waits for the ack
	// until ctx ends (then ctx.Err()); without, it returns once the command is on its way.
	Command(ctx context.Context, robotID string, cmd Command) (Ack, error)
	// Online, OnlineCount and ClockSkew tell where robots are, as the gateways report it.
	Online(robotID string) bool
	OnlineCount() int
	ClockSkew(robotID string) (time.Duration, bool)
	// RobotsChanged tells the relay that the robots of this process connected, disconnected or
	// showed a new clock offset.
	RobotsChanged()
	// Wake wakes the anomaly pipeline of the worker.
	Wake()
	// Alert hands an alert to a worker.
	Alert(a Alert)
	// LicenseChanged tells the other processes to reload the license from the database.
	LicenseChanged()
	// Ready reports whether the relay can reach the other roles.
	Ready() error
}

func skewOf(r Robot) (time.Duration, bool) {
	if r.SkewS == nil {
		return 0, false
	}
	return time.Duration(*r.SkewS * float64(time.Second)), true
}

// Seconds is d in seconds, for Robot.SkewS.
func Seconds(d time.Duration) *float64 {
	v := d.Seconds()
	return &v
}
