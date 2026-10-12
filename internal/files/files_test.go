package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lerenn/seedly/internal/db"
)

type fakeLister map[int64][]db.Torrent

func (f fakeLister) ListTorrentsByOwner(_ context.Context, id int64) ([]db.Torrent, error) {
	return f[id], nil
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (fakeLister, string) {
	t.Helper()
	root := t.TempDir()
	d1 := filepath.Join(root, "1_aaaaaaaa")
	d2 := filepath.Join(root, "1_bbbbbbbb")
	d3 := filepath.Join(root, "2_cccccccc")
	write(t, filepath.Join(d1, "Movie", "movie.mkv"), "video")
	write(t, filepath.Join(d1, ".torrent.db"), "internal")
	write(t, filepath.Join(d2, "single.iso"), "iso")
	write(t, filepath.Join(d3, "other", "secret.txt"), "secret")
	return fakeLister{
		1: {{InfoHash: "aaaaaaaa", DataPath: d1}, {InfoHash: "bbbbbbbb", DataPath: d2}},
		2: {{InfoHash: "cccccccc", DataPath: d3}},
	}, root
}

func names(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func TestRootListsOnlyOwnTorrents(t *testing.T) {
	l, _ := fixture(t)
	tr := NewTree(l, 1, true)
	es, err := tr.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	got := names(es)
	if len(got) != 2 || got[0] != "Movie" || got[1] != "single.iso" {
		t.Fatalf("unexpected root: %v", got)
	}
}

func TestTraversalAndForeignAccessRejected(t *testing.T) {
	l, _ := fixture(t)
	tr := NewTree(l, 1, false)
	ctx := context.Background()
	for _, p := range []string{"/other/secret.txt", "/Movie/../../2_cccccccc/other/secret.txt", "/../etc/passwd"} {
		if _, err := tr.Stat(ctx, p); err == nil {
			t.Errorf("Stat(%q) succeeded", p)
		}
	}
	// ".." cannot climb out of a torrent entry.
	if rp, err := tr.RealPath(ctx, "/Movie/../../x"); err == nil && !filepath.IsAbs(rp) {
		t.Errorf("bad path %q", rp)
	}
}

func TestReadOnlyRejectsMutations(t *testing.T) {
	l, _ := fixture(t)
	tr := NewTree(l, 1, true)
	ctx := context.Background()
	if err := tr.Mkdir(ctx, "/Movie/new"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("mkdir: %v", err)
	}
	if err := tr.Remove(ctx, "/Movie/movie.mkv", false); !errors.Is(err, ErrReadOnly) {
		t.Errorf("remove: %v", err)
	}
	if err := tr.Rename(ctx, "/Movie/movie.mkv", "/Movie/x.mkv"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("rename: %v", err)
	}
	if _, err := tr.OpenFile(ctx, "/Movie/new.txt", os.O_WRONLY|os.O_CREATE, 0o644); !errors.Is(err, ErrReadOnly) {
		t.Errorf("create: %v", err)
	}
}

func TestWritableOperations(t *testing.T) {
	l, root := fixture(t)
	tr := NewTree(l, 1, false)
	ctx := context.Background()
	if err := tr.Mkdir(ctx, "/Movie/sub"); err != nil {
		t.Fatal(err)
	}
	f, err := tr.OpenFile(ctx, "/Movie/sub/a.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := tr.Rename(ctx, "/Movie/sub/a.txt", "/Movie/b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "1_aaaaaaaa", "Movie", "b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Rename(ctx, "/single.iso", "/renamed.iso"); err != nil {
		t.Fatal(err)
	}
	if err := tr.Mkdir(ctx, "/toplevel"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("top-level mkdir: %v", err)
	}
	if err := tr.Remove(ctx, "/Movie", true); err != nil {
		t.Fatal(err)
	}
	es, _ := tr.List(ctx, "/")
	if got := names(es); len(got) != 1 || got[0] != "renamed.iso" {
		t.Fatalf("root after ops: %v", got)
	}
}

func TestDuplicateTopNamesDisambiguated(t *testing.T) {
	root := t.TempDir()
	d1, d2 := filepath.Join(root, "a"), filepath.Join(root, "b")
	write(t, filepath.Join(d1, "dup", "x"), "1")
	write(t, filepath.Join(d2, "dup", "y"), "2")
	l := fakeLister{1: {{InfoHash: "11111111", DataPath: d1}, {InfoHash: "22222222", DataPath: d2}}}
	es, _ := NewTree(l, 1, true).List(context.Background(), "/")
	if got := names(es); len(got) != 2 || got[0] != "dup" || got[1] != "dup [22222222]" {
		t.Fatalf("got %v", got)
	}
}
