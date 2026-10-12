package ftp

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/lerenn/seedly/internal/files"
	"github.com/spf13/afero"
)

// treeFS adapts a files.Tree to afero.Fs for the FTP server.
type treeFS struct {
	tree *files.Tree
}

var _ afero.Fs = (*treeFS)(nil)

func (f *treeFS) ctx() context.Context { return context.Background() }

func (f *treeFS) Name() string { return "seedly" }

func (f *treeFS) Create(name string) (afero.File, error) {
	return f.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
}

func (f *treeFS) Open(name string) (afero.File, error) {
	return f.OpenFile(name, os.O_RDONLY, 0)
}

func (f *treeFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if files.Clean(name) == "/" && flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) == 0 {
		return &rootDir{fs: f}, nil
	}
	h, err := f.tree.OpenFile(f.ctx(), name, flag, perm)
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (f *treeFS) Mkdir(name string, _ os.FileMode) error { return f.tree.Mkdir(f.ctx(), name) }

func (f *treeFS) MkdirAll(name string, perm os.FileMode) error {
	cur := ""
	for _, part := range strings.Split(strings.TrimPrefix(files.Clean(name), "/"), "/") {
		if part == "" {
			continue
		}
		cur += "/" + part
		if _, err := f.tree.Stat(f.ctx(), cur); err == nil {
			continue
		}
		if err := f.Mkdir(cur, perm); err != nil {
			return err
		}
	}
	return nil
}

func (f *treeFS) Remove(name string) error { return f.tree.Remove(f.ctx(), name, false) }

func (f *treeFS) RemoveAll(name string) error { return f.tree.Remove(f.ctx(), name, true) }

func (f *treeFS) Rename(oldname, newname string) error {
	return f.tree.Rename(f.ctx(), oldname, newname)
}

func (f *treeFS) Stat(name string) (os.FileInfo, error) {
	e, err := f.tree.Stat(f.ctx(), name)
	if err != nil {
		return nil, err
	}
	return entryInfo{e}, nil
}

// Chmod, Chown and Chtimes are accepted and ignored: permissions are managed
// by Seedly, not by clients.
func (f *treeFS) Chmod(string, os.FileMode) error            { return nil }
func (f *treeFS) Chown(string, int, int) error               { return nil }
func (f *treeFS) Chtimes(string, time.Time, time.Time) error { return nil }

type entryInfo struct{ e files.Entry }

func (i entryInfo) Name() string       { return i.e.Name }
func (i entryInfo) Size() int64        { return i.e.Size }
func (i entryInfo) ModTime() time.Time { return i.e.ModTime }
func (i entryInfo) IsDir() bool        { return i.e.IsDir }
func (i entryInfo) Sys() any           { return nil }
func (i entryInfo) Mode() fs.FileMode {
	if i.e.IsDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}

// rootDir is the virtual root directory.
type rootDir struct {
	fs      *treeFS
	entries []os.FileInfo
	loaded  bool
}

func (r *rootDir) load() error {
	if r.loaded {
		return nil
	}
	es, err := r.fs.tree.List(r.fs.ctx(), "/")
	if err != nil {
		return err
	}
	for _, e := range es {
		r.entries = append(r.entries, entryInfo{e})
	}
	r.loaded = true
	return nil
}

func (r *rootDir) Readdir(count int) ([]os.FileInfo, error) {
	if err := r.load(); err != nil {
		return nil, err
	}
	if count <= 0 {
		out := r.entries
		r.entries = nil
		return out, nil
	}
	if len(r.entries) == 0 {
		return nil, io.EOF
	}
	n := min(count, len(r.entries))
	out := r.entries[:n]
	r.entries = r.entries[n:]
	return out, nil
}

func (r *rootDir) Readdirnames(n int) ([]string, error) {
	infos, err := r.Readdir(n)
	var out []string
	for _, i := range infos {
		out = append(out, i.Name())
	}
	return out, err
}

func (r *rootDir) Name() string { return path.Join("/") }
func (r *rootDir) Stat() (os.FileInfo, error) {
	return entryInfo{files.Entry{Name: "/", IsDir: true, ModTime: time.Now()}}, nil
}
func (r *rootDir) Close() error                       { return nil }
func (r *rootDir) Sync() error                        { return nil }
func (r *rootDir) Read([]byte) (int, error)           { return 0, fs.ErrInvalid }
func (r *rootDir) ReadAt([]byte, int64) (int, error)  { return 0, fs.ErrInvalid }
func (r *rootDir) Seek(int64, int) (int64, error)     { return 0, fs.ErrInvalid }
func (r *rootDir) Write([]byte) (int, error)          { return 0, fs.ErrPermission }
func (r *rootDir) WriteAt([]byte, int64) (int, error) { return 0, fs.ErrPermission }
func (r *rootDir) WriteString(string) (int, error)    { return 0, fs.ErrPermission }
func (r *rootDir) Truncate(int64) error               { return fs.ErrPermission }
