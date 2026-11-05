// Package bustest is a NATS-like server for tests: the subset of the core protocol NETRA uses
// (INFO, CONNECT with user and password, PING and PONG, SUB with queue groups, UNSUB, PUB with a
// reply subject, MSG, and the "no responders" HMSG 503 for requests nobody listens to).
package bustest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Config sets up a server: User and Pass, when set, are required in CONNECT; MaxPayload is
// announced in INFO (default 1 MiB).
type Config struct {
	User, Pass string
	MaxPayload int
}

// Server listens on 127.0.0.1 until the test ends.
type Server struct {
	cfg  Config
	t    testing.TB
	addr string

	mu          sync.Mutex
	ln          net.Listener
	clients     map[*client]struct{}
	ignorePings bool
	rr          int // round robin over queue members
	connects    int
}

type client struct {
	conn         net.Conn
	wmu          sync.Mutex
	subs         map[string]sub // by sid
	noResponders bool
	pongs        chan struct{}
}

type sub struct{ subject, queue, sid string }

// New starts a server.
func New(t testing.TB) *Server { return NewWith(t, Config{}) }

// NewWith starts a server with a configuration.
func NewWith(t testing.TB, cfg Config) *Server {
	if cfg.MaxPayload <= 0 {
		cfg.MaxPayload = 1 << 20
	}
	s := &Server{t: t, cfg: cfg, clients: map[*client]struct{}{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.addr = ln.Addr().String()
	s.start(ln)
	t.Cleanup(s.Stop)
	return s
}

// URL is the nats:// address of the server.
func (s *Server) URL() string { return "nats://" + s.addr }

func (s *Server) start(ln net.Listener) {
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
}

// Stop closes the listener and every connection.
func (s *Server) Stop() {
	s.mu.Lock()
	if s.ln != nil {
		_ = s.ln.Close()
		s.ln = nil
	}
	s.mu.Unlock()
	s.DropClients()
}

// Restart listens again on the same address after Stop.
func (s *Server) Restart() {
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ { // the port may linger for a moment
		if ln, err = net.Listen("tcp", s.addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		s.t.Fatal(err)
	}
	s.start(ln)
}

// DropClients closes every client connection, as a server restart would.
func (s *Server) DropClients() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		_ = c.conn.Close()
		delete(s.clients, c)
	}
}

// IgnorePings makes the server stop answering PINGs, like a hung server.
func (s *Server) IgnorePings(v bool) {
	s.mu.Lock()
	s.ignorePings = v
	s.mu.Unlock()
}

// Connects is the number of connections that completed CONNECT.
func (s *Server) Connects() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connects
}

// Interest is the number of subscriptions on exactly this subject.
func (s *Server) Interest(subject string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for c := range s.clients {
		for _, sb := range c.subs {
			if sb.subject == subject {
				n++
			}
		}
	}
	return n
}

// WaitInterest waits until a subject has n subscriptions.
func (s *Server) WaitInterest(subject string, n int) {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.Interest(subject) != n {
		if time.Now().After(deadline) {
			s.t.Fatalf("subject %s: %d subscriptions, want %d", subject, s.Interest(subject), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// PingClients sends a PING to every client and waits for each PONG.
func (s *Server) PingClients(ctx context.Context) error {
	s.mu.Lock()
	var list []*client
	for c := range s.clients {
		list = append(list, c)
	}
	s.mu.Unlock()
	for _, c := range list {
		c.write("PING\r\n")
		select {
		case <-c.pongs:
		case <-ctx.Done():
			return errors.New("a client did not answer PING")
		}
	}
	return nil
}

// Inject sends a raw protocol text to every client (for headers and odd frames).
func (s *Server) Inject(raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		c.write(raw)
	}
}

func (c *client) write(text string) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(c.conn, text)
}

func (s *Server) serve(conn net.Conn) {
	c := &client{conn: conn, subs: map[string]sub{}, pongs: make(chan struct{}, 16)}
	defer func() {
		s.remove(c)
		_ = conn.Close()
	}()
	info, _ := json.Marshal(map[string]any{"server_id": "bustest", "version": "2.10.0", "proto": 1,
		"headers": true, "max_payload": s.cfg.MaxPayload, "auth_required": s.cfg.User != ""})
	c.write("INFO " + string(info) + "\r\n")
	br := bufio.NewReader(conn)
	connected := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		op, rest, _ := strings.Cut(line, " ")
		f := strings.Fields(rest)
		switch strings.ToUpper(op) {
		case "CONNECT":
			var opts struct {
				User         string `json:"user"`
				Pass         string `json:"pass"`
				NoResponders bool   `json:"no_responders"`
				Headers      bool   `json:"headers"`
			}
			if json.Unmarshal([]byte(rest), &opts) != nil {
				c.write("-ERR 'Invalid Connect'\r\n")
				return
			}
			if s.cfg.User != "" && (opts.User != s.cfg.User || opts.Pass != s.cfg.Pass) {
				c.write("-ERR 'Authorization Violation'\r\n")
				return
			}
			c.noResponders = opts.NoResponders && opts.Headers
			connected = true
			s.mu.Lock()
			s.clients[c] = struct{}{}
			s.connects++
			s.mu.Unlock()
		case "PING":
			s.mu.Lock()
			ignore := s.ignorePings && connected
			s.mu.Unlock()
			if !ignore {
				c.write("PONG\r\n")
			}
		case "PONG":
			select {
			case c.pongs <- struct{}{}:
			default:
			}
		case "SUB":
			if len(f) != 2 && len(f) != 3 {
				c.write("-ERR 'Invalid Subscription'\r\n")
				continue
			}
			sb := sub{subject: f[0], sid: f[len(f)-1]}
			if len(f) == 3 {
				sb.queue = f[1]
			}
			s.mu.Lock()
			c.subs[sb.sid] = sb
			s.mu.Unlock()
		case "UNSUB":
			if len(f) < 1 {
				continue
			}
			s.mu.Lock()
			delete(c.subs, f[0])
			s.mu.Unlock()
		case "PUB":
			if len(f) != 2 && len(f) != 3 {
				c.write("-ERR 'Invalid Publish'\r\n")
				return
			}
			size, err := strconv.Atoi(f[len(f)-1])
			if err != nil || size < 0 {
				return
			}
			if size > s.cfg.MaxPayload {
				c.write("-ERR 'Maximum Payload Violation'\r\n")
				return
			}
			buf := make([]byte, size+2)
			if _, err := io.ReadFull(br, buf); err != nil {
				return
			}
			reply := ""
			if len(f) == 3 {
				reply = f[1]
			}
			s.route(c, f[0], reply, buf[:size])
		default:
			c.write("-ERR 'Unknown Protocol Operation'\r\n")
		}
	}
}

func (s *Server) remove(c *client) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
}

type target struct {
	c   *client
	sid string
}

// route delivers a publication: every plain subscription, one member of each queue group, or a
// 503 to the publisher's inbox when a request has nobody to answer it.
func (s *Server) route(from *client, subject, reply string, data []byte) {
	s.mu.Lock()
	var plain []target
	groups := map[string][]target{}
	for c := range s.clients {
		for _, sb := range c.subs {
			if !Match(sb.subject, subject) {
				continue
			}
			if sb.queue == "" {
				plain = append(plain, target{c, sb.sid})
			} else {
				groups[sb.queue] = append(groups[sb.queue], target{c, sb.sid})
			}
		}
	}
	for _, members := range groups {
		s.rr++
		plain = append(plain, members[s.rr%len(members)])
	}
	var inbox []target
	if len(plain) == 0 && reply != "" && from.noResponders {
		for _, sb := range from.subs {
			if Match(sb.subject, reply) {
				inbox = append(inbox, target{from, sb.sid})
			}
		}
	}
	s.mu.Unlock()
	for _, t := range plain {
		head := fmt.Sprintf("MSG %s %s %d\r\n", subject, t.sid, len(data))
		if reply != "" {
			head = fmt.Sprintf("MSG %s %s %s %d\r\n", subject, t.sid, reply, len(data))
		}
		t.c.write(head + string(data) + "\r\n")
	}
	for _, t := range inbox {
		const status = "NATS/1.0 503\r\n\r\n"
		t.c.write(fmt.Sprintf("HMSG %s %s %d %d\r\n%s\r\n", reply, t.sid, len(status), len(status), status))
	}
}

// Match reports whether a subscription subject (with * and > wildcards) covers subject.
func Match(pattern, subject string) bool {
	p, s := strings.Split(pattern, "."), strings.Split(subject, ".")
	for i, tok := range p {
		switch {
		case tok == ">":
			return len(s) > i
		case i >= len(s):
			return false
		case tok != "*" && tok != s[i]:
			return false
		}
	}
	return len(p) == len(s)
}
