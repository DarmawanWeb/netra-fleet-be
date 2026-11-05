package bus

import "encoding/json"

// Publisher sends domain events as JSON for other systems; without a client it does nothing.
// Events published while the client is disconnected are dropped.
type Publisher struct{ c *Client }

// NewPublisher publishes through c; a nil c disables publishing.
func NewPublisher(c *Client) *Publisher { return &Publisher{c: c} }

// Enabled reports whether a NATS connection is configured.
func (p *Publisher) Enabled() bool { return p != nil && p.c != nil }

// Publish sends v as JSON to subject; a failure drops the event.
func (p *Publisher) Publish(subject string, v any) {
	if !p.Enabled() {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = p.c.Publish(subject, data)
}
