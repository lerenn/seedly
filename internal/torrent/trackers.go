package torrent

import (
	"context"
	"sort"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lerenn/seedly/internal/db"
	"github.com/lerenn/seedly/internal/trackers"
)

func (e *Engine) policy(ctx context.Context) (trackers.Policy, error) {
	rules, err := e.db.ListTrackerRules(ctx)
	if err != nil {
		return trackers.Policy{}, err
	}
	return trackers.NewPolicy(rules), nil
}

// filterTrackers drops blocked trackers, unless that would leave the torrent with none
// (such torrents are rejected or flagged elsewhere).
func filterTrackers(tiers [][]string, p trackers.Policy) [][]string {
	var out [][]string
	for _, tier := range tiers {
		var kept []string
		for _, u := range tier {
			if !p.Blocked(trackers.Host(u)) {
				kept = append(kept, u)
			}
		}
		if len(kept) > 0 {
			out = append(out, kept)
		}
	}
	if len(out) == 0 {
		return tiers
	}
	return out
}

// IndexTrackers records tracker URLs for torrents added before they were tracked.
func (e *Engine) IndexTrackers(ctx context.Context) error {
	all, err := e.db.ListAllTorrents(ctx)
	if err != nil {
		return err
	}
	known, err := e.db.ListTorrentTrackers(ctx)
	if err != nil {
		return err
	}
	for _, t := range all {
		if _, ok := known[t.ID]; ok {
			continue
		}
		mi, err := metainfo.LoadFromFile(t.MetaPath)
		if err != nil {
			continue
		}
		urls := mi.UpvertedAnnounceList().DistinctValues()
		if len(urls) == 0 {
			continue
		}
		if err := e.db.SetTorrentTrackers(ctx, t.ID, urls); err != nil {
			return err
		}
	}
	return nil
}

type TrackerTorrent struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

type TrackerRow struct {
	Host          string           `json:"host"`
	TorrentCount  int              `json:"torrent_count"`
	UserCount     int              `json:"user_count"`
	Blocked       bool             `json:"blocked"`
	DangerCount   int              `json:"danger_count"`
	WarningCount  int              `json:"warning_count"`
	DangerTorrent []TrackerTorrent `json:"danger_torrents"`
}

type TrackerOverview struct {
	Authorize []db.TrackerRule `json:"authorize"`
	Deny      []db.TrackerRule `json:"deny"`
	Present   []TrackerRow     `json:"present"`
}

type torrentTrackerState struct {
	torrent db.Torrent
	hosts   []string
	state   trackers.State
}

func (e *Engine) evaluate(ctx context.Context) (trackers.Policy, []torrentTrackerState, error) {
	policy, err := e.policy(ctx)
	if err != nil {
		return policy, nil, err
	}
	all, err := e.db.ListAllTorrents(ctx)
	if err != nil {
		return policy, nil, err
	}
	urls, err := e.db.ListTorrentTrackers(ctx)
	if err != nil {
		return policy, nil, err
	}
	out := make([]torrentTrackerState, 0, len(all))
	for _, t := range all {
		hosts := trackers.Hosts(urls[t.ID])
		out = append(out, torrentTrackerState{torrent: t, hosts: hosts, state: policy.Classify(hosts)})
	}
	return policy, out, nil
}

func (e *Engine) TrackerOverview(ctx context.Context) (*TrackerOverview, error) {
	policy, states, err := e.evaluate(ctx)
	if err != nil {
		return nil, err
	}
	rules, err := e.db.ListTrackerRules(ctx)
	if err != nil {
		return nil, err
	}
	users, err := e.db.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, u := range users {
		names[u.ID] = u.Username
	}

	ov := &TrackerOverview{
		Authorize: []db.TrackerRule{},
		Deny:      []db.TrackerRule{},
		Present:   []TrackerRow{},
	}
	for _, r := range rules {
		if r.Mode == db.TrackerModeAuthorize {
			ov.Authorize = append(ov.Authorize, r)
		} else {
			ov.Deny = append(ov.Deny, r)
		}
	}

	rows := map[string]*TrackerRow{}
	owners := map[string]map[int64]bool{}
	for _, st := range states {
		for _, h := range st.hosts {
			row := rows[h]
			if row == nil {
				row = &TrackerRow{Host: h, Blocked: policy.Blocked(h), DangerTorrent: []TrackerTorrent{}}
				rows[h] = row
				owners[h] = map[int64]bool{}
			}
			row.TorrentCount++
			owners[h][st.torrent.OwnerID] = true
			if !row.Blocked {
				continue
			}
			if st.state == trackers.StateDenied {
				row.DangerCount++
				row.DangerTorrent = append(row.DangerTorrent, TrackerTorrent{
					ID: st.torrent.ID, Name: st.torrent.Name, Owner: names[st.torrent.OwnerID],
				})
			} else {
				row.WarningCount++
			}
		}
	}
	for h, row := range rows {
		row.UserCount = len(owners[h])
		ov.Present = append(ov.Present, *row)
	}
	sort.Slice(ov.Present, func(i, j int) bool { return ov.Present[i].Host < ov.Present[j].Host })
	return ov, nil
}

// PurgeTracker deletes (with data) every torrent whose trackers are all blocked and
// that uses the given host. It returns the number of torrents removed.
func (e *Engine) PurgeTracker(ctx context.Context, host string) (int, error) {
	_, states, err := e.evaluate(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, st := range states {
		if st.state != trackers.StateDenied {
			continue
		}
		for _, h := range st.hosts {
			if h == host {
				if err := e.Delete(ctx, st.torrent.ID, true); err != nil {
					return n, err
				}
				n++
				break
			}
		}
	}
	return n, nil
}
