// Package trackers implements tracker authorize/deny matching on tracker hosts.
package trackers

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/lerenn/seedly/internal/db"
)

// State is how a torrent fares against the active policy.
type State string

const (
	StateOK      State = "ok"
	StateWarning State = "warning" // some trackers blocked, at least one allowed
	StateDenied  State = "denied"  // every tracker blocked
)

// Host returns the lowercase hostname of a tracker announce URL ("" if unparsable).
func Host(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// NormalizePattern reduces user input (host, or full URL) to a lowercase host pattern.
func NormalizePattern(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[i:], "]") {
		s = s[:i]
	}
	s = strings.Trim(s, "[]")
	if s == "" || strings.Trim(s, "*") == "" {
		return "", errors.New("pattern must name a host (wildcards allowed, e.g. *.example.org)")
	}
	if strings.ContainsAny(s, " \t") {
		return "", errors.New("pattern must not contain spaces")
	}
	return s, nil
}

// Match reports whether host matches pattern, where `*` matches any run of characters.
func Match(pattern, host string) bool {
	parts := strings.Split(strings.ToLower(pattern), "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	re, err := regexp.Compile("^" + strings.Join(parts, ".*") + "$")
	if err != nil {
		return false
	}
	return re.MatchString(strings.ToLower(host))
}

// Policy is the active mode with the rule patterns of that mode.
type Policy struct {
	Mode     db.TrackerMode
	Patterns []string
}

func NewPolicy(mode db.TrackerMode, rules []db.TrackerRule) Policy {
	p := Policy{Mode: mode}
	for _, r := range rules {
		if r.Mode == mode {
			p.Patterns = append(p.Patterns, r.Pattern)
		}
	}
	return p
}

func (p Policy) matches(host string) bool {
	for _, pat := range p.Patterns {
		if Match(pat, host) {
			return true
		}
	}
	return false
}

// Blocked reports whether a tracker host is denied. An empty authorize list means
// "not configured" and allows everything.
func (p Policy) Blocked(host string) bool {
	if p.Mode == db.TrackerModeAuthorize {
		return len(p.Patterns) > 0 && !p.matches(host)
	}
	return p.matches(host)
}

// Classify evaluates a torrent's tracker hosts. Torrents without trackers are OK.
func (p Policy) Classify(hosts []string) State {
	blocked := 0
	for _, h := range hosts {
		if p.Blocked(h) {
			blocked++
		}
	}
	switch {
	case blocked == 0:
		return StateOK
	case blocked == len(hosts):
		return StateDenied
	default:
		return StateWarning
	}
}

// Hosts returns the distinct hosts of the given tracker URLs.
func Hosts(urls []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, u := range urls {
		h := Host(u)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}
