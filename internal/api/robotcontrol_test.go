package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/config"
	"github.com/DarmawanWeb/netra-fleet-be/internal/hub"
	"github.com/gorilla/websocket"
)

// fakeRobot connects a WebSocket robot to the hub and answers each command with reply(name, args).
func fakeRobot(t *testing.T, s *Server, id string, reply func(name string, args map[string]any) (bool, string, bool)) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.hub.AddRobot(id, hub.NewConn(ws))
	}))
	client, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for i := 0; i < 50 && !s.hub.Online(id); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	go func() {
		for {
			var msg struct {
				ID   string         `json:"id"`
				Name string         `json:"name"`
				Args map[string]any `json:"args"`
			}
			if client.ReadJSON(&msg) != nil {
				return
			}
			ok, text, answer := reply(msg.Name, msg.Args)
			if answer {
				raw, _ := json.Marshal(msg.ID)
				s.resolveCommand(context.Background(), string(raw), ok, text)
			}
		}
	}()
	return func() { client.Close(); srv.Close() }
}

func controlServer() *Server {
	return &Server{
		hub: hub.New(), pending: map[string]pendingCommand{},
		cfg: config.Config{InspectionModels: []string{"qwen3-vl:2b-instruct", "moondream:latest"}},
	}
}

func post(s *Server, h http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.SetPathValue("id", "l1w-01")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestInspectionCommandWaitsForTheAck(t *testing.T) {
	s := controlServer()
	var got map[string]any
	stop := fakeRobot(t, s, "l1w-01", func(name string, args map[string]any) (bool, string, bool) {
		got = args
		return name == "set_inspection", "inspection on (moondream:latest, every 5 s)", true
	})
	defer stop()
	rec := post(s, s.robotInspection, "/api/v1/robots/l1w-01/inspection", `{"enabled":true,"model":"moondream:latest","interval":5}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if got["enabled"] != true || got["model"] != "moondream:latest" || got["interval"] != 5.0 {
		t.Fatalf("robot got %v", got)
	}
}

func TestRobotRefusalIsReturned(t *testing.T) {
	s := controlServer()
	stop := fakeRobot(t, s, "l1w-01", func(string, map[string]any) (bool, string, bool) {
		return false, "people model not installed", true
	})
	defer stop()
	rec := post(s, s.robotPeople, "/api/v1/robots/l1w-01/people", `{"enabled":true}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "people model not installed") {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

func TestAckTimeout(t *testing.T) {
	s := controlServer()
	old := ackTimeout
	ackTimeout = 100 * time.Millisecond
	defer func() { ackTimeout = old }()
	stop := fakeRobot(t, s, "l1w-01", func(string, map[string]any) (bool, string, bool) { return false, "", false })
	defer stop()
	rec := post(s, s.robotPeople, "/api/v1/robots/l1w-01/people", `{"enabled":false}`)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if len(s.pending) != 0 {
		t.Fatal("timed-out command left pending")
	}
}

func TestControlValidation(t *testing.T) {
	s := controlServer()
	cases := []struct {
		h    http.HandlerFunc
		body string
		want int
	}{
		{s.robotInspection, `{}`, http.StatusBadRequest},
		{s.robotInspection, `{"model":"llava:34b"}`, http.StatusBadRequest},
		{s.robotInspection, `{"interval":7200}`, http.StatusBadRequest},
		{s.robotInspection, `{"interval":-1}`, http.StatusConflict}, // valid, robot offline
		{s.robotInspection, `{"interval":0}`, http.StatusConflict},
		{s.robotPeople, `{}`, http.StatusBadRequest},
		{s.robotPeople, `{"enabled":true}`, http.StatusConflict},
	}
	for _, c := range cases {
		if rec := post(s, c.h, "/x", c.body); rec.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.body, rec.Code, c.want, rec.Body.String())
		}
	}
}
