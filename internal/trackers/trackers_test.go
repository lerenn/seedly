package trackers

import (
	"testing"

	"github.com/lerenn/seedly/internal/db"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"tracker.example.org", "tracker.example.org", true},
		{"*.example.org", "tracker.example.org", true},
		{"*.example.org", "example.org", false},
		{"tracker.*", "tracker.foo.net", true},
		{"TRACKER.example.org", "tracker.EXAMPLE.org", true},
		{"a.b", "axb", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.host); got != c.want {
			t.Errorf("Match(%q,%q)=%v want %v", c.pattern, c.host, got, c.want)
		}
	}
}

func TestNormalizePattern(t *testing.T) {
	for in, want := range map[string]string{
		"udp://Tracker.Example.org:6969/announce": "tracker.example.org",
		"*.example.org":    "*.example.org",
		" example.org:80 ": "example.org",
	} {
		got, err := NormalizePattern(in)
		if err != nil || got != want {
			t.Errorf("NormalizePattern(%q)=%q,%v want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "*", "udp://"} {
		if _, err := NormalizePattern(in); err == nil {
			t.Errorf("NormalizePattern(%q) expected error", in)
		}
	}
}

func TestPolicy(t *testing.T) {
	rules := []db.TrackerRule{
		{Mode: db.TrackerModeAuthorize, Pattern: "*.good.org"},
		{Mode: db.TrackerModeDeny, Pattern: "bad.good.org"},
	}
	p := NewPolicy(rules)
	if p.Classify([]string{"a.good.org"}) != StateOK ||
		p.Classify([]string{"other.org"}) != StateDenied ||
		p.Classify([]string{"bad.good.org"}) != StateDenied ||
		p.Classify([]string{"a.good.org", "other.org"}) != StateWarning ||
		p.Classify(nil) != StateOK {
		t.Error("combined policy misclassified")
	}
	if NewPolicy(nil).Blocked("anything.org") {
		t.Error("no rules must allow all")
	}
	deny := NewPolicy([]db.TrackerRule{{Mode: db.TrackerModeDeny, Pattern: "*.bad.org"}})
	if !deny.Blocked("x.bad.org") || deny.Blocked("ok.org") {
		t.Error("deny-only policy misclassified")
	}
}
