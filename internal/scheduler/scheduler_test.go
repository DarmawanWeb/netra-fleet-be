package scheduler

import (
	"testing"
	"time"

	"github.com/DarmawanRobotics/netra-fleet-be/internal/store"
)

func TestDue(t *testing.T) {
	jkt, _ := time.LoadLocation("Asia/Jakarta")
	s := store.Schedule{Days: []int64{1, 3}, TimeOfDay: "09:30", Enabled: true}
	monday := time.Date(2026, 10, 5, 9, 32, 0, 0, jkt)
	cases := []struct {
		now  time.Time
		want bool
	}{
		{monday, true},
		{monday.Add(-3 * time.Minute), false},
		{monday.Add(10 * time.Minute), false},
		{monday.AddDate(0, 0, 1), false},
	}
	for _, c := range cases {
		if got := Due(s, c.now); got != c.want {
			t.Errorf("Due(%v) = %v, want %v", c.now, got, c.want)
		}
	}
	date := "2026-10-06"
	once := store.Schedule{OnceOn: &date, TimeOfDay: "09:30", Enabled: true}
	if Due(once, monday) || !Due(once, monday.AddDate(0, 0, 1)) {
		t.Error("one-off schedule fired on the wrong day")
	}
	s.Enabled = false
	if Due(s, monday) {
		t.Error("disabled schedule fired")
	}
}
