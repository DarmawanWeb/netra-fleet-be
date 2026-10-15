package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DarmawanWeb/netra-fleet-be/internal/hub"
)

func TestRobotCommandNames(t *testing.T) {
	s := &Server{hub: hub.New(), pending: map[string]pendingCommand{}}
	cases := []struct {
		name string
		want int
	}{
		{"estop", http.StatusConflict}, // accepted, but the robot is not connected
		{"estop_release", http.StatusConflict},
		{"pause", http.StatusConflict},
		{"start_patrol", http.StatusBadRequest}, // only through POST /patrols
		{"self_destruct", http.StatusBadRequest},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/robots/l1w-01/command", strings.NewReader(`{"name":"`+c.name+`"}`))
		req.SetPathValue("id", "l1w-01")
		rec := httptest.NewRecorder()
		s.robotCommand(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
}
