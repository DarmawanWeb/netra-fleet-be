// Package bus speaks the NATS core text protocol over TCP (no TLS, no dependency): publish,
// subscribe with queue groups, request and reply through one inbox subscription, PING and PONG
// both ways, and reconnects with backoff that subscribe again. It carries the domain events
// (Publisher) and the messages between the roles of a split installation (internal/relay).
package bus

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	mrand "math/rand"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Errors of the client.
var (
	ErrNotConnected = errors.New("nats: not connected")
	ErrClosed       = errors.New("nats: client closed")
	ErrNoResponders = errors.New("nats: no responders")
	ErrMaxPayload   = errors.New("nats: message larger than the server accepts")
	ErrBadSubject   = errors.New("nats: invalid subject")
)

// ServerError is an -ERR the server answered the connection with, such as a refused login.
type ServerError struct{ Msg string }

func (e *ServerError) Error() string { return "nats: server refused the connection: " + e.Msg }

// Msg is a message received on a subscription.
type Msg struct {
	Subject string
	Reply   string
	Data    []byte
	status  string // status of a message with headers: "503" when a request found no responder
	c       *Client
}

// Respond publishes data to the reply subject of a request.
func (m Msg) Respond(data []byte) error {
	if m.Reply == "" || m.c == nil {
		return errors.New("nats: message has no reply subject")
	}
	return m.c.publish(m.Reply, "", data)
}

// Options tune a client; zero values take the defaults.
type Options struct {
	Name         string        // connection name the server shows (default "netra")
	PingInterval time.Duration // client PINGs; two unanswered ones drop the connection (default 20 s)
	ReconnectMin time.Duration // wait after the first failed attempt, doubled each time (default 250 ms)
	ReconnectMax time.Duration // longest wait between attempts (default 5 s)
	Timeout      time.Duration // dial, handshake and write timeout (default 3 s)
}

func (o *Options) defaults() {
	if o.Name == "" {
		o.Name = "netra"
	}
	if o.PingInterval <= 0 {
		o.PingInterval = 20 * time.Second
	}
	if o.ReconnectMin <= 0 {
		o.ReconnectMin = 250 * time.Millisecond
	}
	if o.ReconnectMax < o.ReconnectMin {
		o.ReconnectMax = max(5*time.Second, o.ReconnectMin)
	}
	if o.Timeout <= 0 {
		o.Timeout = 3 * time.Second
	}
}

type serverInfo struct {
	MaxPayload int  `json:"max_payload"`
	Headers    bool `json:"headers"`
}

// Client is one connection to a NATS server, kept up in the background.
type Client struct {
	addr, user, pass, token string
	opts                    Options

	wmu       sync.Mutex // the connection, the writer and the fields below
	conn      net.Conn
	w         *bufio.Writer
	info      serverInfo
	up        chan struct{} // closed while connected
	pongs     []chan error  // one per PING in flight, nil for the keep-alive ones
	pingsOut  int
	lastErr   error
	onConnect []func()
	closed    bool

	smu  sync.Mutex // subs
	subs map[uint64]*Subscription
	sid  uint64

	inboxOnce sync.Once
	inbox     string // _INBOX.<random>. ; replies arrive on inbox + token
	inboxErr  error
	rmu       sync.Mutex
	replies   map[string]chan Msg
	reqs      atomic.Uint64

	dropped atomic.Uint64
	done    chan struct{}
	wg      sync.WaitGroup
}

// Dial checks nats://[user:pass@ | token@]host[:port] and starts connecting in the background;
// use WaitConnected to wait for the first connection.
func Dial(rawURL string, opts Options) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("nats url: %w", err)
	}
	if u.Scheme != "nats" || u.Hostname() == "" {
		return nil, fmt.Errorf("nats url %q: use nats://host:port (TLS is not supported)", u.Redacted())
	}
	opts.defaults()
	c := &Client{addr: u.Host, opts: opts, up: make(chan struct{}), subs: map[uint64]*Subscription{},
		replies: map[string]chan Msg{}, done: make(chan struct{})}
	if u.Port() == "" {
		c.addr = net.JoinHostPort(u.Hostname(), "4222")
	}
	if u.User != nil {
		if pass, ok := u.User.Password(); ok {
			c.user, c.pass = u.User.Username(), pass
		} else {
			c.token = u.User.Username()
		}
	}
	c.wg.Add(1)
	go c.run()
	return c, nil
}

// Connected reports whether the client is connected now.
func (c *Client) Connected() bool {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.conn != nil
}

// WaitConnected waits for a connection. It gives up early when the server refused the client
// (a *ServerError), else when ctx ends, with the last connection error.
func (c *Client) WaitConnected(ctx context.Context) error {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		c.wmu.Lock()
		up, connected, last, closed := c.up, c.conn != nil, c.lastErr, c.closed
		c.wmu.Unlock()
		var se *ServerError
		switch {
		case connected:
			return nil
		case closed:
			return ErrClosed
		case errors.As(last, &se):
			return last
		}
		select {
		case <-up:
		case <-tick.C:
		case <-ctx.Done():
			if last != nil {
				return fmt.Errorf("%w: %w", ctx.Err(), last)
			}
			return ctx.Err()
		}
	}
}

// OnConnect calls f, in its own goroutine, after every connection the client makes from now on
// (the first one too when it is not made yet), once the subscriptions are in place.
func (c *Client) OnConnect(f func()) {
	c.wmu.Lock()
	c.onConnect = append(c.onConnect, f)
	c.wmu.Unlock()
}

// Close stops the client: it flushes what is buffered, closes the connection and ends the
// subscriptions.
func (c *Client) Close() {
	c.wmu.Lock()
	if c.closed {
		c.wmu.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	conn := c.conn
	if conn != nil {
		_ = conn.SetWriteDeadline(time.Now().Add(c.opts.Timeout))
		_ = c.w.Flush()
	}
	c.wmu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	c.wg.Wait()
	c.smu.Lock()
	for sid, s := range c.subs {
		close(s.ch)
		delete(c.subs, sid)
	}
	c.smu.Unlock()
}

// run keeps the connection up until Close.
func (c *Client) run() {
	defer c.wg.Done()
	wait := c.opts.ReconnectMin
	failures := 0
	var lastLog time.Time
	for {
		select {
		case <-c.done:
			return
		default:
		}
		conn, br, err := c.connect()
		if err != nil {
			c.wmu.Lock()
			c.lastErr = err
			c.wmu.Unlock()
			failures++
			if failures == 1 || time.Since(lastLog) > time.Minute {
				slog.Warn("nats unavailable", "server", c.addr, "attempts", failures, "err", err)
				lastLog = time.Now()
			}
			select {
			case <-c.done:
				return
			case <-time.After(jitter(wait)):
			}
			wait = min(wait*2, c.opts.ReconnectMax)
			continue
		}
		slog.Info("nats connected", "server", c.addr, "name", c.opts.Name)
		failures, wait = 0, c.opts.ReconnectMin
		c.serve(conn, br)
	}
}

func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*mrand.Float64())) // #nosec G404 -- backoff spread
}

// connect dials, does the INFO / CONNECT / PING / PONG handshake, subscribes again and marks the
// client connected.
func (c *Client) connect() (net.Conn, *bufio.Reader, error) {
	conn, err := net.DialTimeout("tcp", c.addr, c.opts.Timeout)
	if err != nil {
		return nil, nil, err
	}
	br, info, err := c.handshake(conn)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	c.wmu.Lock()
	if c.closed {
		c.wmu.Unlock()
		_ = conn.Close()
		return nil, nil, ErrClosed
	}
	c.conn, c.w, c.info = conn, bufio.NewWriterSize(conn, 32<<10), info
	c.pingsOut, c.pongs, c.lastErr = 0, nil, nil
	c.smu.Lock()
	for _, s := range c.subs {
		c.writeSub(s)
	}
	c.smu.Unlock()
	err = c.flushLocked()
	close(c.up)
	callbacks := append([]func(){}, c.onConnect...)
	c.wmu.Unlock()
	if err != nil {
		_ = conn.Close() // the read loop ends at once and the client reconnects
	}
	for _, f := range callbacks {
		go f()
	}
	return conn, br, nil
}

func (c *Client) handshake(conn net.Conn) (*bufio.Reader, serverInfo, error) {
	var info serverInfo
	_ = conn.SetDeadline(time.Now().Add(c.opts.Timeout))
	br := bufio.NewReaderSize(conn, 64<<10)
	line, err := readLine(br)
	if err != nil {
		return nil, info, err
	}
	op, rest, _ := strings.Cut(line, " ")
	if !strings.EqualFold(op, "INFO") || json.Unmarshal([]byte(rest), &info) != nil {
		return nil, info, fmt.Errorf("nats: expected INFO from the server, got %q", clip(line))
	}
	opts := map[string]any{"verbose": false, "pedantic": false, "name": c.opts.Name, "lang": "go",
		"version": "netra", "protocol": 1, "headers": true, "no_responders": true}
	switch {
	case c.user != "":
		opts["user"], opts["pass"] = c.user, c.pass
	case c.token != "":
		opts["auth_token"] = c.token
	}
	payload, _ := json.Marshal(opts)
	if _, err := fmt.Fprintf(conn, "CONNECT %s\r\nPING\r\n", payload); err != nil {
		return nil, info, err
	}
	for {
		line, err := readLine(br)
		if err != nil {
			return nil, info, err
		}
		op, rest, _ := strings.Cut(line, " ")
		switch strings.ToUpper(op) {
		case "PONG":
			_ = conn.SetDeadline(time.Time{})
			return br, info, nil
		case "PING":
			if _, err := io.WriteString(conn, "PONG\r\n"); err != nil {
				return nil, info, err
			}
		case "-ERR":
			return nil, info, &ServerError{Msg: strings.Trim(strings.TrimSpace(rest), "'")}
		case "INFO", "+OK":
		default:
			return nil, info, fmt.Errorf("nats: unexpected %q while connecting", clip(line))
		}
	}
}

func readLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func clip(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

// serve reads from a connection until it breaks, with keep-alive pings.
func (c *Client) serve(conn net.Conn, br *bufio.Reader) {
	stop := make(chan struct{})
	go c.pinger(conn, stop)
	err := c.readLoop(conn, br)
	close(stop)
	c.wmu.Lock()
	if c.conn == conn {
		c.conn, c.w = nil, nil
		for _, p := range c.pongs {
			if p != nil {
				p <- ErrNotConnected
			}
		}
		c.pongs, c.lastErr = nil, err
		c.up = make(chan struct{})
	}
	closed := c.closed
	c.wmu.Unlock()
	_ = conn.Close()
	if !closed {
		slog.Warn("nats connection lost", "server", c.addr, "err", err)
	}
}

func (c *Client) pinger(conn net.Conn, stop chan struct{}) {
	t := time.NewTicker(c.opts.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		c.wmu.Lock()
		if c.conn != conn {
			c.wmu.Unlock()
			return
		}
		if c.pingsOut >= 2 {
			c.wmu.Unlock()
			slog.Warn("nats server stopped answering pings", "server", c.addr)
			_ = conn.Close()
			return
		}
		c.pingsOut++
		c.pongs = append(c.pongs, nil)
		err := c.writeLocked("PING\r\n", nil)
		c.wmu.Unlock()
		if err != nil {
			_ = conn.Close()
			return
		}
	}
}

func (c *Client) readLoop(conn net.Conn, br *bufio.Reader) error {
	for {
		line, err := readLine(br)
		if err != nil {
			return err
		}
		op, rest, _ := strings.Cut(line, " ")
		switch strings.ToUpper(op) {
		case "MSG":
			if err := c.readMsg(br, strings.Fields(rest), false); err != nil {
				return err
			}
		case "HMSG":
			if err := c.readMsg(br, strings.Fields(rest), true); err != nil {
				return err
			}
		case "PING":
			c.wmu.Lock()
			if c.conn == conn {
				err = c.writeLocked("PONG\r\n", nil)
			}
			c.wmu.Unlock()
			if err != nil {
				return err
			}
		case "PONG":
			c.wmu.Lock()
			c.pingsOut = 0
			if len(c.pongs) > 0 {
				if p := c.pongs[0]; p != nil {
					p <- nil
				}
				c.pongs = c.pongs[1:]
			}
			c.wmu.Unlock()
		case "-ERR":
			slog.Warn("nats server error", "server", c.addr, "err", strings.Trim(strings.TrimSpace(rest), "'"))
		case "INFO":
			var info serverInfo
			if json.Unmarshal([]byte(rest), &info) == nil && info.MaxPayload > 0 {
				c.wmu.Lock()
				c.info.MaxPayload = info.MaxPayload
				c.wmu.Unlock()
			}
		case "+OK", "":
		default:
			return fmt.Errorf("nats: unknown protocol line %q", clip(line))
		}
	}
}

// readMsg reads MSG <subject> <sid> [reply] <size> or HMSG <subject> <sid> [reply] <header size>
// <total size>, then the payload.
func (c *Client) readMsg(br *bufio.Reader, f []string, headers bool) error {
	n := 4
	if headers {
		n = 5
	}
	if len(f) != n-1 && len(f) != n {
		return fmt.Errorf("nats: bad message line %q", strings.Join(f, " "))
	}
	m := Msg{Subject: f[0], c: c}
	sid, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return fmt.Errorf("nats: bad sid %q", f[1])
	}
	if len(f) == n {
		m.Reply = f[2]
	}
	total, err := strconv.Atoi(f[len(f)-1])
	if err != nil || total < 0 || total > math.MaxInt32 {
		return fmt.Errorf("nats: bad size %q", f[len(f)-1])
	}
	hdr := 0
	if headers {
		hdr, err = strconv.Atoi(f[len(f)-2])
		if err != nil || hdr < 0 || hdr > total {
			return fmt.Errorf("nats: bad header size %q", f[len(f)-2])
		}
	}
	buf := make([]byte, total+2)
	if _, err := io.ReadFull(br, buf); err != nil {
		return err
	}
	if headers {
		m.status = headerStatus(buf[:hdr])
	}
	m.Data = buf[hdr:total]
	c.deliver(sid, m)
	return nil
}

// headerStatus returns the status code of a header block "NATS/1.0 503\r\n...", or "".
func headerStatus(h []byte) string {
	line, _, _ := strings.Cut(string(h), "\r\n")
	rest, ok := strings.CutPrefix(line, "NATS/1.0")
	if !ok {
		return ""
	}
	code, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
	return code
}

func (c *Client) deliver(sid uint64, m Msg) {
	c.smu.Lock()
	defer c.smu.Unlock()
	s := c.subs[sid]
	if s == nil {
		return
	}
	select {
	case s.ch <- m:
	default:
		if n := c.dropped.Add(1); n == 1 || n%1000 == 0 {
			slog.Warn("nats: slow subscriber, messages dropped", "subject", s.subject, "dropped", n)
		}
	}
}

// writeLocked writes a protocol line and an optional payload and flushes; wmu is held.
func (c *Client) writeLocked(line string, payload []byte) error {
	if c.conn == nil {
		return ErrNotConnected
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.opts.Timeout))
	_, err := c.w.WriteString(line)
	if err == nil && payload != nil {
		if _, err = c.w.Write(payload); err == nil {
			_, err = c.w.WriteString("\r\n")
		}
	}
	if err == nil {
		err = c.w.Flush()
	}
	if err != nil {
		_ = c.conn.Close() // the read loop sees it and the client reconnects
	}
	return err
}

func (c *Client) flushLocked() error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.opts.Timeout))
	return c.w.Flush()
}

// writeSub buffers the SUB of s; wmu and smu are held.
func (c *Client) writeSub(s *Subscription) {
	if s.queue != "" {
		_, _ = fmt.Fprintf(c.w, "SUB %s %s %d\r\n", s.subject, s.queue, s.sid)
	} else {
		_, _ = fmt.Fprintf(c.w, "SUB %s %d\r\n", s.subject, s.sid)
	}
}

func validSubject(s string) bool {
	return s != "" && !strings.ContainsAny(s, " \t\r\n") && !strings.HasPrefix(s, ".") &&
		!strings.HasSuffix(s, ".") && !strings.Contains(s, "..")
}

// Publish sends data to subject. It fails at once while the client is disconnected.
func (c *Client) Publish(subject string, data []byte) error { return c.publish(subject, "", data) }

func (c *Client) publish(subject, reply string, data []byte) error {
	if !validSubject(subject) || (reply != "" && !validSubject(reply)) {
		return ErrBadSubject
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if c.conn == nil {
		return ErrNotConnected
	}
	if c.info.MaxPayload > 0 && len(data) > c.info.MaxPayload {
		return ErrMaxPayload
	}
	line := "PUB " + subject + " " + strconv.Itoa(len(data)) + "\r\n"
	if reply != "" {
		line = "PUB " + subject + " " + reply + " " + strconv.Itoa(len(data)) + "\r\n"
	}
	if data == nil {
		data = []byte{}
	}
	return c.writeLocked(line, data)
}

// Flush waits until the server has processed everything sent so far (a PING answered).
func (c *Client) Flush(ctx context.Context) error {
	ch := make(chan error, 1)
	c.wmu.Lock()
	if c.conn == nil {
		c.wmu.Unlock()
		return ErrNotConnected
	}
	c.pongs = append(c.pongs, ch)
	err := c.writeLocked("PING\r\n", nil)
	c.wmu.Unlock()
	if err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscription delivers the messages of one subject to a handler, in order, from its own
// goroutine. Up to 1024 messages wait for a slow handler; more are dropped.
type Subscription struct {
	c       *Client
	sid     uint64
	subject string
	queue   string
	ch      chan Msg
}

// Subject is what the subscription listens to.
func (s *Subscription) Subject() string { return s.subject }

// Subscribe calls h for every message on subject (wildcards * and > allowed).
func (c *Client) Subscribe(subject string, h func(Msg)) (*Subscription, error) {
	return c.subscribe(subject, "", h)
}

// QueueSubscribe is Subscribe in a queue group: each message goes to one member of the group.
func (c *Client) QueueSubscribe(subject, queue string, h func(Msg)) (*Subscription, error) {
	if !validSubject(queue) || strings.ContainsAny(queue, "*>") {
		return nil, ErrBadSubject
	}
	return c.subscribe(subject, queue, h)
}

func (c *Client) subscribe(subject, queue string, h func(Msg)) (*Subscription, error) {
	if !validSubject(subject) {
		return nil, ErrBadSubject
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	c.smu.Lock()
	c.sid++
	s := &Subscription{c: c, sid: c.sid, subject: subject, queue: queue, ch: make(chan Msg, 1024)}
	c.subs[s.sid] = s
	if c.conn != nil {
		c.writeSub(s)
	}
	c.smu.Unlock()
	if c.conn != nil {
		_ = c.flushLocked() // on failure the reconnect subscribes again
	}
	go func() {
		for m := range s.ch {
			h(m)
		}
	}()
	return s, nil
}

// Unsubscribe stops the subscription; its handler gets no further message.
func (s *Subscription) Unsubscribe() error {
	c := s.c
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.smu.Lock()
	_, ok := c.subs[s.sid]
	if ok {
		delete(c.subs, s.sid)
		close(s.ch)
	}
	c.smu.Unlock()
	if !ok || c.conn == nil {
		return nil
	}
	return c.writeLocked("UNSUB "+strconv.FormatUint(s.sid, 10)+"\r\n", nil)
}

// Request publishes data to subject and waits for the first reply until ctx ends. A request
// nobody listens to fails with ErrNoResponders (NATS 2.2 or later tells the client at once).
func (c *Client) Request(ctx context.Context, subject string, data []byte) (Msg, error) {
	inbox, err := c.ensureInbox()
	if err != nil {
		return Msg{}, err
	}
	token := strconv.FormatUint(c.reqs.Add(1), 36)
	ch := make(chan Msg, 1)
	c.rmu.Lock()
	c.replies[token] = ch
	c.rmu.Unlock()
	defer func() {
		c.rmu.Lock()
		delete(c.replies, token)
		c.rmu.Unlock()
	}()
	if err := c.publish(subject, inbox+token, data); err != nil {
		return Msg{}, err
	}
	select {
	case m := <-ch:
		if m.status == "503" {
			return Msg{}, ErrNoResponders
		}
		return m, nil
	case <-ctx.Done():
		return Msg{}, ctx.Err()
	}
}

// ensureInbox subscribes once to _INBOX.<random>.*, where every reply of this client arrives.
func (c *Client) ensureInbox() (string, error) {
	c.inboxOnce.Do(func() {
		b := make([]byte, 11)
		if _, err := rand.Read(b); err != nil {
			c.inboxErr = err
			return
		}
		prefix := "_INBOX." + hex.EncodeToString(b) + "."
		_, err := c.Subscribe(prefix+"*", func(m Msg) {
			token := strings.TrimPrefix(m.Subject, prefix)
			c.rmu.Lock()
			ch := c.replies[token]
			delete(c.replies, token)
			c.rmu.Unlock()
			if ch != nil {
				ch <- m
			}
		})
		c.inbox, c.inboxErr = prefix, err
	})
	return c.inbox, c.inboxErr
}
