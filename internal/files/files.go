// Package files exposes a per-user virtual file tree over the torrent data
// directories. The root lists the content of every torrent owned by the user;
// everything below it is the real file system content. The same tree backs the
// FTP server and the HTTP file manager.
package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lerenn/seedly/internal/db"
)

var (
	ErrNotFound = fs.ErrNotExist
	ErrReadOnly = fs.ErrPermission
	ErrInvalid  = errors.New("invalid path")
	ErrExists   = fs.ErrExist
)

// TorrentLister returns the torrents owned by a user.
type TorrentLister interface {
	ListTorrentsByOwner(ctx context.Context, ownerID int64) ([]db.Torrent, error)
}

// Tree is the virtual file tree of one owner.
type Tree struct {
	lister   TorrentLister
	ownerID  int64
	readOnly bool
}

// NewTree returns the tree of ownerID. A read-only tree rejects every mutation.
func NewTree(lister TorrentLister, ownerID int64, readOnly bool) *Tree {
	return &Tree{lister: lister, ownerID: ownerID, readOnly: readOnly}
}

// ReadOnly reports whether the tree rejects mutations.
func (t *Tree) ReadOnly() bool { return t.readOnly }

// Entry describes one file or directory.
type Entry struct {
	Name    string    `json:"name"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modified"`
}

// Clean normalizes a client-supplied path to an absolute slash path.
func Clean(p string) string {
	return path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
}

// topEntry is a root entry: a name mapped to a real path.
type topEntry struct {
	name string
	real string
	// base is the torrent data directory that contains real.
	base string
}

// roots lists the virtual root entries, sorted by name.
func (t *Tree) roots(ctx context.Context) ([]topEntry, error) {
	torrents, err := t.lister.ListTorrentsByOwner(ctx, t.ownerID)
	if err != nil {
		return nil, err
	}
	var out []topEntry
	seen := map[string]bool{}
	for _, tor := range torrents {
		items, err := os.ReadDir(tor.DataPath)
		if err != nil {
			continue
		}
		for _, it := range items {
			n := it.Name()
			// anacrolix keeps its piece completion database next to the data.
			if strings.HasPrefix(n, ".torrent") {
				continue
			}
			name := n
			if seen[name] {
				short := tor.InfoHash
				if len(short) > 8 {
					short = short[:8]
				}
				name = n + " [" + short + "]"
			}
			seen[name] = true
			out = append(out, topEntry{name: name, real: filepath.Join(tor.DataPath, n), base: tor.DataPath})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// resolved is the result of resolving a virtual path.
type resolved struct {
	root bool   // the virtual root itself
	top  bool   // a direct child of the root
	real string // real path (empty for the root)
	base string // torrent data dir containing real
}

func (t *Tree) resolve(ctx context.Context, name string) (resolved, error) {
	p := Clean(name)
	if p == "/" {
		return resolved{root: true}, nil
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	roots, err := t.roots(ctx)
	if err != nil {
		return resolved{}, err
	}
	for _, r := range roots {
		if r.name != parts[0] {
			continue
		}
		rest := parts[1:]
		if len(rest) == 0 {
			return resolved{top: true, real: r.real, base: r.base}, nil
		}
		real := filepath.Join(append([]string{r.real}, rest...)...)
		return resolved{real: real, base: r.base}, nil
	}
	// Unknown top-level name: only meaningful as a rename target.
	return resolved{}, ErrNotFound
}

func entryOf(info fs.FileInfo, name string) Entry {
	return Entry{Name: name, IsDir: info.IsDir(), Size: info.Size(), ModTime: info.ModTime()}
}

// Stat returns the entry at name.
func (t *Tree) Stat(ctx context.Context, name string) (Entry, error) {
	r, err := t.resolve(ctx, name)
	if err != nil {
		return Entry{}, err
	}
	if r.root {
		return Entry{Name: "/", IsDir: true, ModTime: time.Now()}, nil
	}
	info, err := os.Stat(r.real)
	if err != nil {
		return Entry{}, err
	}
	return entryOf(info, path.Base(Clean(name))), nil
}

// List returns the entries of the directory at name, directories first.
func (t *Tree) List(ctx context.Context, name string) ([]Entry, error) {
	r, err := t.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	var out []Entry
	if r.root {
		roots, err := t.roots(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range roots {
			info, err := os.Stat(e.real)
			if err != nil {
				continue
			}
			out = append(out, entryOf(info, e.name))
		}
	} else {
		items, err := os.ReadDir(r.real)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			info, err := it.Info()
			if err != nil {
				continue
			}
			out = append(out, entryOf(info, it.Name()))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// RealPath returns the on-disk path of a file or directory (not the root).
func (t *Tree) RealPath(ctx context.Context, name string) (string, error) {
	r, err := t.resolve(ctx, name)
	if err != nil {
		return "", err
	}
	if r.root {
		return "", ErrInvalid
	}
	return r.real, nil
}

// Open opens a file for reading.
func (t *Tree) Open(ctx context.Context, name string) (*os.File, error) {
	return t.OpenFile(ctx, name, os.O_RDONLY, 0)
}

// OpenFile opens a file; flags that write are rejected on read-only trees and
// at the virtual root.
func (t *Tree) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (*os.File, error) {
	r, err := t.resolve(ctx, name)
	if err != nil && !(errors.Is(err, ErrNotFound) && flag&os.O_CREATE != 0) {
		return nil, err
	}
	writes := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	if writes {
		if t.readOnly {
			return nil, ErrReadOnly
		}
		// New top-level files would not belong to any torrent.
		if err != nil || r.root {
			return nil, ErrReadOnly
		}
	}
	if r.root {
		return nil, ErrInvalid
	}
	if perm == 0 {
		perm = 0o644
	}
	return os.OpenFile(r.real, flag, perm)
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// writable resolves name for a mutation of an existing entry or a child of an
// existing directory.
func (t *Tree) writable(ctx context.Context, name string) (resolved, error) {
	if t.readOnly {
		return resolved{}, ErrReadOnly
	}
	r, err := t.resolve(ctx, name)
	if err != nil {
		return resolved{}, err
	}
	if r.root {
		return resolved{}, ErrReadOnly
	}
	return r, nil
}

// Mkdir creates a directory below an existing torrent entry.
func (t *Tree) Mkdir(ctx context.Context, name string) error {
	if t.readOnly {
		return ErrReadOnly
	}
	// The parent must exist and not be the root.
	parent := path.Dir(Clean(name))
	if parent == "/" {
		return ErrReadOnly
	}
	r, err := t.writable(ctx, name)
	if err != nil {
		return err
	}
	return os.Mkdir(r.real, 0o755)
}

// Remove deletes a file or directory. Non-empty directories need recursive.
func (t *Tree) Remove(ctx context.Context, name string, recursive bool) error {
	r, err := t.writable(ctx, name)
	if err != nil {
		return err
	}
	if recursive {
		return os.RemoveAll(r.real)
	}
	return os.Remove(r.real)
}

// Rename moves an entry. A top-level entry can only be renamed to another
// top-level name, and stays in its torrent data directory.
func (t *Tree) Rename(ctx context.Context, from, to string) error {
	src, err := t.writable(ctx, from)
	if err != nil {
		return err
	}
	to = Clean(to)
	if to == "/" {
		return ErrReadOnly
	}
	var dst string
	if path.Dir(to) == "/" {
		// Renaming at the root: only valid for a top-level source.
		if !src.top {
			return ErrReadOnly
		}
		dst = filepath.Join(src.base, path.Base(to))
	} else {
		if src.top {
			return ErrReadOnly
		}
		d, err := t.writable(ctx, to)
		if err != nil {
			return err
		}
		dst = d.real
	}
	if exists(dst) {
		return ErrExists
	}
	return os.Rename(src.real, dst)
}
