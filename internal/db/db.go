package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

type DB struct {
	SQL *sql.DB
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetConnMaxLifetime(0)

	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("read schema: %w", err)
	}
	if _, err := sqlDB.Exec(string(schema)); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &DB{SQL: sqlDB}, nil
}

func (d *DB) Close() error {
	return d.SQL.Close()
}

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type Session struct {
	ID        string
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
}

type TorrentStatus string

const (
	StatusDownloading TorrentStatus = "downloading"
	StatusSeeding     TorrentStatus = "seeding"
	StatusPaused      TorrentStatus = "paused"
	StatusError       TorrentStatus = "error"
)

type Torrent struct {
	ID           int64         `json:"id"`
	OwnerID      int64         `json:"owner_id"`
	InfoHash     string        `json:"info_hash"`
	Name         string        `json:"name"`
	MetaPath     string        `json:"-"`
	DataPath     string        `json:"-"`
	Status       TorrentStatus `json:"status"`
	ErrorMessage string        `json:"error_message,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	CompletedAt  *time.Time    `json:"completed_at,omitempty"`
}

func (d *DB) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := d.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (d *DB) CreateUser(ctx context.Context, username, passwordHash string, role Role) (*User, error) {
	res, err := d.SQL.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`,
		username, passwordHash, string(role),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return d.GetUserByID(ctx, id)
}

func (d *DB) GetUserByID(ctx context.Context, id int64) (*User, error) {
	u := &User{}
	var role string
	err := d.SQL.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.Role = Role(role)
	return u, nil
}

func (d *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	u := &User{}
	var role string
	err := d.SQL.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.Role = Role(role)
	return u, nil
}

func (d *DB) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := d.SQL.QueryContext(ctx,
		`SELECT id, username, password_hash, role, created_at FROM users ORDER BY username`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var role string
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &role, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Role = Role(role)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (d *DB) UpdateUsername(ctx context.Context, id int64, username string) error {
	_, err := d.SQL.ExecContext(ctx, `UPDATE users SET username = ? WHERE id = ?`, username, id)
	return err
}

func (d *DB) DeleteUser(ctx context.Context, id int64) error {
	_, err := d.SQL.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

func (d *DB) CountAdmins(ctx context.Context) (int64, error) {
	var n int64
	err := d.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = ?`, string(RoleAdmin)).Scan(&n)
	return n, err
}

func (d *DB) DeleteSessionsByUserID(ctx context.Context, userID int64) error {
	_, err := d.SQL.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (d *DB) CreateSession(ctx context.Context, s Session) error {
	_, err := d.SQL.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES (?, ?, ?, ?)`,
		s.ID, s.UserID, s.TokenHash, s.ExpiresAt.UTC(),
	)
	return err
}

func (d *DB) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	s := &Session{}
	err := d.SQL.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at FROM sessions WHERE token_hash = ?`, tokenHash,
	).Scan(&s.ID, &s.UserID, &s.TokenHash, &s.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (d *DB) DeleteSession(ctx context.Context, id string) error {
	_, err := d.SQL.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (d *DB) DeleteExpiredSessions(ctx context.Context) error {
	_, err := d.SQL.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC())
	return err
}

func (d *DB) CreateTorrent(ctx context.Context, t Torrent) (*Torrent, error) {
	res, err := d.SQL.ExecContext(ctx, `
		INSERT INTO torrents (owner_id, info_hash, name, meta_path, data_path, status, error_message, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.OwnerID, t.InfoHash, t.Name, t.MetaPath, t.DataPath, string(t.Status), t.ErrorMessage, nullTime(t.CompletedAt),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return d.GetTorrentByID(ctx, id)
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func (d *DB) GetTorrentByID(ctx context.Context, id int64) (*Torrent, error) {
	t := &Torrent{}
	var status string
	var completedAt sql.NullTime
	err := d.SQL.QueryRowContext(ctx, `
		SELECT id, owner_id, info_hash, name, meta_path, data_path, status, COALESCE(error_message, ''), created_at, completed_at
		FROM torrents WHERE id = ?`, id,
	).Scan(&t.ID, &t.OwnerID, &t.InfoHash, &t.Name, &t.MetaPath, &t.DataPath, &status, &t.ErrorMessage, &t.CreatedAt, &completedAt)
	if err != nil {
		return nil, err
	}
	t.Status = TorrentStatus(status)
	if completedAt.Valid {
		ct := completedAt.Time
		t.CompletedAt = &ct
	}
	return t, nil
}

func (d *DB) ListTorrentsByOwner(ctx context.Context, ownerID int64) ([]Torrent, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, owner_id, info_hash, name, meta_path, data_path, status, COALESCE(error_message, ''), created_at, completed_at
		FROM torrents WHERE owner_id = ? ORDER BY created_at DESC`, ownerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTorrents(rows)
}

func (d *DB) ListAllTorrents(ctx context.Context) ([]Torrent, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT id, owner_id, info_hash, name, meta_path, data_path, status, COALESCE(error_message, ''), created_at, completed_at
		FROM torrents ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTorrents(rows)
}

func scanTorrents(rows *sql.Rows) ([]Torrent, error) {
	var out []Torrent
	for rows.Next() {
		var t Torrent
		var status string
		var completedAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.InfoHash, &t.Name, &t.MetaPath, &t.DataPath, &status, &t.ErrorMessage, &t.CreatedAt, &completedAt); err != nil {
			return nil, err
		}
		t.Status = TorrentStatus(status)
		if completedAt.Valid {
			ct := completedAt.Time
			t.CompletedAt = &ct
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) UpdateTorrentStatus(ctx context.Context, id int64, status TorrentStatus, errMsg string, completedAt *time.Time) error {
	_, err := d.SQL.ExecContext(ctx, `
		UPDATE torrents SET status = ?, error_message = ?, completed_at = COALESCE(?, completed_at) WHERE id = ?`,
		string(status), errMsg, nullTime(completedAt), id,
	)
	return err
}

func (d *DB) DeleteTorrent(ctx context.Context, id int64) error {
	_, err := d.SQL.ExecContext(ctx, `DELETE FROM torrents WHERE id = ?`, id)
	return err
}
