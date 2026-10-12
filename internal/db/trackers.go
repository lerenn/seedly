package db

import (
	"context"
	"database/sql"
	"time"
)

type TrackerMode string

const (
	TrackerModeAuthorize TrackerMode = "authorize"
	TrackerModeDeny      TrackerMode = "deny"
)

// TrackerMode is the kind of a rule: authorize (allow list) or deny (block list).
func (m TrackerMode) Valid() bool {
	return m == TrackerModeAuthorize || m == TrackerModeDeny
}

type TrackerRule struct {
	ID        int64       `json:"id"`
	Mode      TrackerMode `json:"mode"`
	Pattern   string      `json:"pattern"`
	CreatedAt time.Time   `json:"created_at"`
}

func (d *DB) ListTrackerRules(ctx context.Context) ([]TrackerRule, error) {
	rows, err := d.SQL.QueryContext(ctx,
		`SELECT id, mode, pattern, created_at FROM tracker_rules ORDER BY pattern`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrackerRule{}
	for rows.Next() {
		var r TrackerRule
		var mode string
		if err := rows.Scan(&r.ID, &mode, &r.Pattern, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Mode = TrackerMode(mode)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) GetTrackerRule(ctx context.Context, id int64) (*TrackerRule, error) {
	var r TrackerRule
	var mode string
	err := d.SQL.QueryRowContext(ctx,
		`SELECT id, mode, pattern, created_at FROM tracker_rules WHERE id = ?`, id,
	).Scan(&r.ID, &mode, &r.Pattern, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	r.Mode = TrackerMode(mode)
	return &r, nil
}

func (d *DB) CreateTrackerRule(ctx context.Context, mode TrackerMode, pattern string) (*TrackerRule, error) {
	res, err := d.SQL.ExecContext(ctx,
		`INSERT INTO tracker_rules (mode, pattern) VALUES (?, ?)`, string(mode), pattern)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return d.GetTrackerRule(ctx, id)
}

func (d *DB) UpdateTrackerRule(ctx context.Context, id int64, pattern string) error {
	res, err := d.SQL.ExecContext(ctx, `UPDATE tracker_rules SET pattern = ? WHERE id = ?`, pattern, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (d *DB) DeleteTrackerRule(ctx context.Context, id int64) error {
	res, err := d.SQL.ExecContext(ctx, `DELETE FROM tracker_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (d *DB) SetTorrentTrackers(ctx context.Context, torrentID int64, urls []string) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM torrent_trackers WHERE torrent_id = ?`, torrentID); err != nil {
		return err
	}
	for _, u := range urls {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO torrent_trackers (torrent_id, url) VALUES (?, ?)`, torrentID, u); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListTorrentTrackers returns tracker URLs keyed by torrent id.
func (d *DB) ListTorrentTrackers(ctx context.Context) (map[int64][]string, error) {
	rows, err := d.SQL.QueryContext(ctx, `SELECT torrent_id, url FROM torrent_trackers ORDER BY url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var u string
		if err := rows.Scan(&id, &u); err != nil {
			return nil, err
		}
		out[id] = append(out[id], u)
	}
	return out, rows.Err()
}
