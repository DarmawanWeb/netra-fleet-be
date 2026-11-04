package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DarmawanWeb/netra-fleet-be/internal/role"
)

func TestRoutesFollowTheRoles(t *testing.T) {
	web := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	cases := []struct {
		roles                  role.Set
		dashboard, robot, page int
	}{
		{role.All, 401, 401, http.StatusTeapot},
		{role.API, 401, http.StatusTeapot, http.StatusTeapot}, // robot paths fall to the dashboard files
		{role.Gateway, 404, 401, 404},
		{role.Worker, 404, 404, 404},
		{role.API | role.Gateway, 401, 401, http.StatusTeapot},
	}
	for _, c := range cases {
		s := New(testConfig(), nil, nil, nil, nil, nil, nil)
		s.SetRoles(c.roles)
		s.SetWeb(web)
		h := s.Handler()
		status := func(path string) int {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			return rec.Code
		}
		if got := status("/api/v1/robots"); got != c.dashboard {
			t.Errorf("%s: dashboard route %d, want %d", c.roles, got, c.dashboard)
		}
		if got := status("/api/v1/robot/ws"); got != c.robot {
			t.Errorf("%s: robot route %d, want %d", c.roles, got, c.robot)
		}
		if got := status("/dashboard"); got != c.page {
			t.Errorf("%s: dashboard page %d, want %d", c.roles, got, c.page)
		}
		for _, path := range []string{"/healthz", "/metrics"} {
			if got := status(path); got != http.StatusOK {
				t.Errorf("%s: %s %d", c.roles, path, got)
			}
		}
	}
}
