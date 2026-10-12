package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

const (
	SettingQuotaBytes  = "quota_bytes"
	SettingBufferBytes = "buffer_bytes"
)

// GetInt64Setting returns the stored integer for key, or 0 when unset.
func (d *DB) GetInt64Setting(ctx context.Context, key string) (int64, error) {
	var v string
	err := d.SQL.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, nil
	}
	return n, nil
}

func (d *DB) SetInt64Setting(ctx context.Context, key string, value int64) error {
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, strconv.FormatInt(value, 10),
	)
	return err
}
