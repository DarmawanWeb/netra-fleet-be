// Package bus publishes domain events to NATS (core protocol, publish only, auto-reconnect).
package bus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"time"
)

// Publisher sends JSON messages to NATS subjects; a zero URL makes it a no-op.
type Publisher struct {
	url  string
	mu   sync.Mutex
	conn net.Conn
	w    *bufio.Writer
	next time.Time
}

// New returns a publisher for nats://[user:pass@]host:port (empty disables it).
func New(rawURL string) *Publisher { return &Publisher{url: rawURL} }

// Enabled reports whether a NATS URL is configured.
func (p *Publisher) Enabled() bool { return p.url != "" }

func (p *Publisher) connect() error {
	u, err := url.Parse(p.url)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", u.Host, 3*time.Second)
	if err != nil {
		return err
	}
	opts := map[string]any{"verbose": false, "pedantic": false, "name": "netra-api", "lang": "go", "version": "1"}
	if u.User != nil {
		opts["user"] = u.User.Username()
		opts["pass"], _ = u.User.Password()
	}
	payload, _ := json.Marshal(opts)
	go drain(conn) // discard INFO/PONG/-ERR, answer PING
	w := bufio.NewWriterSize(conn, 32<<10)
	if _, err := fmt.Fprintf(w, "CONNECT %s\r\n", payload); err != nil {
		conn.Close()
		return err
	}
	p.conn, p.w = conn, w
	return w.Flush()
}

func drain(conn net.Conn) {
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if len(line) >= 4 && line[:4] == "PING" {
			_, _ = conn.Write([]byte("PONG\r\n"))
		}
	}
}

// Publish sends v as JSON to subject; failures drop the message and reconnect later.
func (p *Publisher) Publish(subject string, v any) {
	if p.url == "" {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		if time.Now().Before(p.next) {
			return
		}
		if err := p.connect(); err != nil {
			p.next = time.Now().Add(5 * time.Second)
			slog.Warn("nats unavailable", "err", err)
			return
		}
	}
	_ = p.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err = fmt.Fprintf(p.w, "PUB %s %d\r\n%s\r\n", subject, len(data), data); err == nil {
		err = p.w.Flush()
	}
	if err != nil {
		p.conn.Close()
		p.conn = nil
	}
}
