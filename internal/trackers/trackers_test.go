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
		{Mode: db.TrackerModeAuthorize, Pattern: "good.org"},
		{Mode: db.TrackerModeDeny, Pattern: "*.bad.org"},
	}
	auth := NewPolicy(db.TrackerModeAuthorize, rules)
	if auth.Classify([]string{"good.org"}) != StateOK ||
		auth.Classify([]string{"other.org"}) != StateDenied ||
		auth.Classify([]string{"good.org", "other.org"}) != StateWarning {
		t.Error("authorize mode misclassified")
	}
	deny := NewPolicy(db.TrackerModeDeny, rules)
	if deny.Classify([]string{"x.bad.org"}) != StateDenied ||
		deny.Classify([]string{"x.bad.org", "ok.org"}) != StateWarning ||
		deny.Classify(nil) != StateOK {
		t.Error("deny mode misclassified")
	}
	if NewPolicy(db.TrackerModeAuthorize, nil).Blocked("anything.org") {
		t.Error("empty authorize list must allow all")
	}
}
