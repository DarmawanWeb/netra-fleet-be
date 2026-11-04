package role

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Set
		str  string
	}{
		{"", All, "all"},
		{"all", All, "all"},
		{" ALL ", All, "all"},
		{"api", API, "api"},
		{"gateway,api", API | Gateway, "api,gateway"},
		{"worker, gateway ,", Gateway | Worker, "gateway,worker"},
		{"api,gateway,worker", All, "all"},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil || got != c.want || got.String() != c.str {
			t.Errorf("Parse(%q) = %v (%s), %v; want %v (%s)", c.in, got, got, err, c.want, c.str)
		}
	}
	for _, bad := range []string{"apis", "api,web", "all,api", ",", "api;gateway"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestHas(t *testing.T) {
	s := API | Worker
	if !s.Has(API) || !s.Has(Worker) || s.Has(Gateway) || s.Has(All) || s.Has(0) || s.All() {
		t.Fatalf("set %s", s)
	}
	if !All.Has(Gateway) || !All.Has(API|Worker) {
		t.Fatal("all lacks a role")
	}
}
