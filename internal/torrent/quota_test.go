package torrent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lerenn/seedly/internal/db"
)

func TestMakeRoomEvictsOldestOfHeaviestUser(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "db", "seedly.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	e := &Engine{
		db:            database,
		metaPath:      filepath.Join(dir, "meta"),
		downloadsPath: filepath.Join(dir, "dl"),
		byID:          map[int64]*liveTorrent{},
	}

	a, _ := database.CreateUser(ctx, "a", "x", db.RoleUser)
	b, _ := database.CreateUser(ctx, "b", "x", db.RoleAdmin)

	add := func(owner int64, name string, size int, age int) int64 {
		p := filepath.Join(e.downloadsPath, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "f"), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		done := time.Now()
		rec, err := database.CreateTorrent(ctx, db.Torrent{
			OwnerID: owner, InfoHash: name, Name: name, MetaPath: filepath.Join(dir, name+".torrent"),
			DataPath: p, Status: db.StatusSeeding, CompletedAt: &done,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.SQL.Exec(`UPDATE torrents SET created_at = ? WHERE id = ?`,
			time.Now().Add(-time.Duration(age)*time.Hour).UTC(), rec.ID)
		if err != nil {
			t.Fatal(err)
		}
		return rec.ID
	}
	exists := func(id int64) bool {
		ok, _ := database.GetTorrentByID(ctx, id)
		return ok != nil
	}

	a1 := add(a.ID, "a1", 100, 10)
	a2 := add(a.ID, "a2", 100, 5)
	b1 := add(b.ID, "b1", 50, 20)

	// 250 used, quota 300, buffer 50 → target 250; adding 60 must free 60+.
	_ = database.SetInt64Setting(ctx, db.SettingQuotaBytes, 300)
	_ = database.SetInt64Setting(ctx, db.SettingBufferBytes, 50)

	// Too big for the whole quota: nothing is removed.
	err = e.makeRoom(ctx, b.ID, 400, "huge")
	if err == nil || !strings.Contains(fmt.Sprint(err), "too large") {
		t.Fatalf("want too large error, got %v", err)
	}
	if !exists(a1) || !exists(a2) || !exists(b1) {
		t.Fatal("torrents removed on rejection")
	}

	if err := e.makeRoom(ctx, b.ID, 60, "new"); err != nil {
		t.Fatal(err)
	}
	if exists(a1) {
		t.Fatal("oldest torrent of heaviest user should be evicted")
	}
	if !exists(a2) || !exists(b1) {
		t.Fatal("other torrents must survive")
	}
}
