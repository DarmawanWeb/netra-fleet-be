package bus

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/bus/bustest"
)

func fast() Options {
	return Options{PingInterval: time.Second, ReconnectMin: 10 * time.Millisecond, ReconnectMax: 50 * time.Millisecond,
		Timeout: time.Second}
}

func dial(t *testing.T, url string, o Options) *Client {
	t.Helper()
	c, err := Dial(url, o)
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

// inbox collects the messages of a subscription.
type inbox struct {
	mu   sync.Mutex
	msgs []Msg
	ch   chan struct{}
}

func newInbox() *inbox { return &inbox{ch: make(chan struct{}, 1024)} }

func (b *inbox) add(m Msg) {
	b.mu.Lock()
	b.msgs = append(b.msgs, m)
	b.mu.Unlock()
	b.ch <- struct{}{}
}

func (b *inbox) wait(t *testing.T, n int) []Msg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		b.mu.Lock()
		if len(b.msgs) >= n {
			out := append([]Msg(nil), b.msgs...)
			b.mu.Unlock()
			return out
		}
		b.mu.Unlock()
		select {
		case <-b.ch:
		case <-deadline:
			t.Fatalf("got %d messages, want %d", b.count(), n)
		}
	}
}

func (b *inbox) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.msgs)
}

func flush(t *testing.T, c *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDialURL(t *testing.T) {
	for _, bad := range []string{"localhost:4222", "tls://localhost:4222", "nats://", "http://x"} {
		if _, err := Dial(bad, Options{}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	c, err := Dial("nats://u:secret@127.0.0.1", Options{ReconnectMax: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.addr != "127.0.0.1:4222" || c.user != "u" || c.pass != "secret" {
		t.Fatalf("parsed %s %s %s", c.addr, c.user, c.pass)
	}
}

func TestPublishSubscribe(t *testing.T) {
	srv := bustest.New(t)
	a, b := dial(t, srv.URL(), fast()), dial(t, srv.URL(), fast())
	one, all := newInbox(), newInbox()
	if _, err := b.Subscribe("robot.*", one.add); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Subscribe("robot.>", all.add); err != nil {
		t.Fatal(err)
	}
	flush(t, b)
	payload := []byte("line one\r\nline two \x00 binary")
	for _, subject := range []string{"robot.l1w", "robot.l1w.telemetry", "other"} {
		if err := a.Publish(subject, payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Publish("robot.empty", nil); err != nil {
		t.Fatal(err)
	}
	got := all.wait(t, 3)
	if got[0].Subject != "robot.l1w" || !bytes.Equal(got[0].Data, payload) || got[1].Subject != "robot.l1w.telemetry" ||
		got[2].Subject != "robot.empty" || len(got[2].Data) != 0 {
		t.Fatalf("robot.> got %+v", got)
	}
	flush(t, a)
	if m := one.wait(t, 2); len(m) != 2 || m[1].Subject != "robot.empty" {
		t.Fatalf("robot.* got %+v", m)
	}
	if err := a.Publish("bad subject", nil); !errors.Is(err, ErrBadSubject) {
		t.Fatalf("bad subject: %v", err)
	}
}

func TestQueueGroup(t *testing.T) {
	srv := bustest.New(t)
	pub := dial(t, srv.URL(), fast())
	var got atomic.Int32
	for i := 0; i < 2; i++ {
		c := dial(t, srv.URL(), fast())
		if _, err := c.QueueSubscribe("job", "worker", func(Msg) { got.Add(1) }); err != nil {
			t.Fatal(err)
		}
		flush(t, c)
	}
	everyone := newInbox()
	if _, err := pub.Subscribe("job", everyone.add); err != nil {
		t.Fatal(err)
	}
	flush(t, pub)
	for i := 0; i < 10; i++ {
		_ = pub.Publish("job", []byte{byte(i)})
	}
	everyone.wait(t, 10)
	time.Sleep(50 * time.Millisecond)
	if n := got.Load(); n != 10 {
		t.Fatalf("queue members got %d messages, want each of 10 once", n)
	}
}

func TestRequestReply(t *testing.T) {
	srv := bustest.New(t)
	requester, responder := dial(t, srv.URL(), fast()), dial(t, srv.URL(), fast())
	if _, err := responder.Subscribe("echo", func(m Msg) { _ = m.Respond(append([]byte("re: "), m.Data...)) }); err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Subscribe("silent", func(Msg) {}); err != nil {
		t.Fatal(err)
	}
	flush(t, responder)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ { // the inbox subscription is reused
		m, err := requester.Request(ctx, "echo", []byte("hi"))
		if err != nil || string(m.Data) != "re: hi" {
			t.Fatalf("reply %q, %v", m.Data, err)
		}
	}
	if _, err := requester.Request(ctx, "nobody.home", nil); !errors.Is(err, ErrNoResponders) {
		t.Fatalf("no responders: %v", err)
	}
	short, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if _, err := requester.Request(short, "silent", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unanswered request: %v", err)
	}
	requester.rmu.Lock()
	left := len(requester.replies)
	requester.rmu.Unlock()
	if left != 0 {
		t.Fatalf("%d requests left waiting", left)
	}
	if err := (Msg{}).Respond(nil); err == nil {
		t.Fatal("responded without a reply subject")
	}
}

func TestUnsubscribe(t *testing.T) {
	srv := bustest.New(t)
	c := dial(t, srv.URL(), fast())
	box := newInbox()
	s, err := c.Subscribe("news", box.add)
	if err != nil {
		t.Fatal(err)
	}
	flush(t, c)
	_ = c.Publish("news", []byte("1"))
	box.wait(t, 1)
	if err := s.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	if err := s.Unsubscribe(); err != nil { // twice is harmless
		t.Fatal(err)
	}
	flush(t, c)
	srv.WaitInterest("news", 0)
	_ = c.Publish("news", []byte("2"))
	flush(t, c)
	time.Sleep(30 * time.Millisecond)
	if box.count() != 1 {
		t.Fatalf("got %d messages after unsubscribing", box.count())
	}
}

func TestReconnectSubscribesAgain(t *testing.T) {
	srv := bustest.New(t)
	c := dial(t, srv.URL(), fast())
	var connects atomic.Int32
	c.OnConnect(func() { connects.Add(1) })
	box := newInbox()
	if _, err := c.Subscribe("live", box.add); err != nil {
		t.Fatal(err)
	}
	if _, err := c.QueueSubscribe("alert", "worker", box.add); err != nil {
		t.Fatal(err)
	}
	flush(t, c)

	srv.DropClients()
	srv.WaitInterest("live", 1)
	srv.WaitInterest("alert", 1)
	other := dial(t, srv.URL(), fast())
	_ = other.Publish("live", []byte("after"))
	_ = other.Publish("alert", []byte("job"))
	got := map[string]string{} // two subscriptions: no order between them
	for _, m := range box.wait(t, 2) {
		got[m.Subject] = string(m.Data)
	}
	if got["live"] != "after" || got["alert"] != "job" {
		t.Fatalf("after reconnect: %v", got)
	}
	for i := 0; i < 100 && connects.Load() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if connects.Load() != 1 {
		t.Fatalf("OnConnect ran %d times", connects.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := other.Request(ctx, "nobody", nil); !errors.Is(err, ErrNoResponders) {
		t.Fatal(err)
	}
}

func TestReconnectAfterServerRestart(t *testing.T) {
	srv := bustest.New(t)
	c := dial(t, srv.URL(), fast())
	box := newInbox()
	if _, err := c.Subscribe("live", box.add); err != nil {
		t.Fatal(err)
	}
	flush(t, c)
	srv.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for c.Connected() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.Publish("live", []byte("lost")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("publish while down: %v", err)
	}
	if err := c.Flush(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("flush while down: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // a few failed attempts
	srv.Restart()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitConnected(ctx); err != nil {
		t.Fatal(err)
	}
	srv.WaitInterest("live", 1)
	_ = c.Publish("live", []byte("back"))
	if m := box.wait(t, 1); string(m[0].Data) != "back" {
		t.Fatalf("got %q", m[0].Data)
	}
}

func TestPings(t *testing.T) {
	srv := bustest.New(t)
	o := fast()
	o.PingInterval = 40 * time.Millisecond
	c := dial(t, srv.URL(), o)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.PingClients(ctx); err != nil { // the client answers the server's PING
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // keep-alive PINGs are answered: no reconnect
	if srv.Connects() != 1 {
		t.Fatalf("%d connections while the server answered", srv.Connects())
	}
	srv.IgnorePings(true) // a hung server: two unanswered PINGs drop the connection
	deadline := time.Now().Add(3 * time.Second)
	for srv.Connects() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.Connects() < 2 {
		t.Fatal("the client kept a connection whose server stopped answering")
	}
	srv.IgnorePings(false)
	if err := c.WaitConnected(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorization(t *testing.T) {
	srv := bustest.NewWith(t, bustest.Config{User: "netra", Pass: "s3cret"})
	c, err := Dial(srv.URL(), fast())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var se *ServerError
	if err := c.WaitConnected(ctx); !errors.As(err, &se) || !strings.Contains(se.Msg, "Authorization") {
		t.Fatalf("refused login: %v", err)
	}
	dial(t, strings.Replace(srv.URL(), "nats://", "nats://netra:s3cret@", 1), fast())
}

func TestMaxPayload(t *testing.T) {
	srv := bustest.NewWith(t, bustest.Config{MaxPayload: 16})
	c := dial(t, srv.URL(), fast())
	if err := c.Publish("big", bytes.Repeat([]byte("x"), 17)); !errors.Is(err, ErrMaxPayload) {
		t.Fatalf("oversized message: %v", err)
	}
	if err := c.Publish("small", []byte("ok")); err != nil || !c.Connected() {
		t.Fatalf("connection lost after a refused message: %v", err)
	}
}

func TestClose(t *testing.T) {
	srv := bustest.New(t)
	c := dial(t, srv.URL(), fast())
	var calls atomic.Int32
	if _, err := c.Subscribe("x", func(Msg) { calls.Add(1) }); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c.Close()
	if err := c.Publish("x", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("publish after close: %v", err)
	}
	if _, err := c.Subscribe("y", func(Msg) {}); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscribe after close: %v", err)
	}
	if err := c.WaitConnected(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("wait after close: %v", err)
	}
}

// TestHeadersAndServerFrames talks to a hand-written server: a message with headers, an INFO
// update, +OK and -ERR lines between messages.
func TestHeadersAndServerFrames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("INFO {\"headers\":true,\"max_payload\":1024}\r\n"))
		br := bufio.NewReader(conn)
		var sid string
		for sid == "" {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "PING"):
				_, _ = conn.Write([]byte("PONG\r\n"))
			case strings.HasPrefix(line, "SUB "):
				sid = strings.Fields(line)[2]
			}
		}
		hdr := "NATS/1.0\r\nX-Trace: 1\r\n\r\n"
		body := "payload"
		_, _ = conn.Write([]byte("+OK\r\nINFO {\"max_payload\":2048}\r\n-ERR 'Permissions Violation'\r\n" +
			"HMSG data " + sid + " reply.to " + strconv.Itoa(len(hdr)) + " " + strconv.Itoa(len(hdr)+len(body)) + "\r\n" + hdr + body + "\r\n" +
			"MSG data " + sid + " 2\r\nok\r\n"))
		_, _ = br.ReadString('\n') // hold the connection until the client closes
	}()
	c, err := Dial("nats://"+ln.Addr().String(), fast())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	box := newInbox()
	if _, err := c.Subscribe("data", box.add); err != nil {
		t.Fatal(err)
	}
	m := box.wait(t, 2)
	if string(m[0].Data) != "payload" || m[0].Reply != "reply.to" || m[0].status != "" || string(m[1].Data) != "ok" {
		t.Fatalf("got %+v", m)
	}
	c.wmu.Lock()
	maxPayload := c.info.MaxPayload
	c.wmu.Unlock()
	if maxPayload != 2048 {
		t.Fatalf("max payload %d after INFO", maxPayload)
	}
}

func TestHeaderStatus(t *testing.T) {
	for in, want := range map[string]string{
		"NATS/1.0 503\r\n\r\n":                 "503",
		"NATS/1.0 408 Request Timeout\r\n\r\n": "408",
		"NATS/1.0\r\nKey: v\r\n\r\n":           "",
		"HTTP/1.1 200\r\n\r\n":                 "",
		"":                                     "",
	} {
		if got := headerStatus([]byte(in)); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestPublisher(t *testing.T) {
	var none *Publisher
	none.Publish("x", 1) // nil publisher: no-op
	if none.Enabled() || NewPublisher(nil).Enabled() {
		t.Fatal("publisher without a client is enabled")
	}
	NewPublisher(nil).Publish("x", map[string]int{"a": 1})

	srv := bustest.New(t)
	c := dial(t, srv.URL(), fast())
	box := newInbox()
	if _, err := c.Subscribe("netra.anomaly.queued", box.add); err != nil {
		t.Fatal(err)
	}
	flush(t, c)
	p := NewPublisher(c)
	p.Publish("netra.anomaly.queued", map[string]any{"id": 7})
	if m := box.wait(t, 1); string(m[0].Data) != `{"id":7}` {
		t.Fatalf("got %s", m[0].Data)
	}
}
