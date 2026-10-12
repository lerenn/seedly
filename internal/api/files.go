package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"

	"github.com/lerenn/seedly/internal/db"
	"github.com/lerenn/seedly/internal/files"
)

// treeFor returns the file tree of the requested owner. Everyone gets their
// own files; admins may pass owner_id to act on another user's files. Only
// admins can modify files.
func (s *Server) treeFor(w http.ResponseWriter, r *http.Request) (*files.Tree, bool) {
	user := currentUser(r)
	ownerID := user.ID
	if p := r.URL.Query().Get("owner_id"); p != "" {
		if user.Role != db.RoleAdmin {
			writeError(w, http.StatusForbidden, "forbidden")
			return nil, false
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid owner_id")
			return nil, false
		}
		ownerID = id
	}
	return files.NewTree(s.db, ownerID, user.Role != db.RoleAdmin), true
}

func writeFilesError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, fs.ErrPermission):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, fs.ErrExist):
		writeError(w, http.StatusConflict, "already exists")
	case errors.Is(err, files.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid path")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	tree, ok := s.treeFor(w, r)
	if !ok {
		return
	}
	p := files.Clean(r.URL.Query().Get("path"))
	entries, err := tree.List(r.Context(), p)
	if err != nil {
		writeFilesError(w, err)
		return
	}
	if entries == nil {
		entries = []files.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     p,
		"writable": !tree.ReadOnly(),
		"entries":  entries,
	})
}

func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	tree, ok := s.treeFor(w, r)
	if !ok {
		return
	}
	p := files.Clean(r.URL.Query().Get("path"))
	f, err := tree.Open(r.Context(), p)
	if err != nil {
		writeFilesError(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeFilesError(w, err)
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusBadRequest, "not a file")
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(p)}))
	http.ServeContent(w, r, path.Base(p), info.ModTime(), f)
}

func (s *Server) handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	tree, ok := s.treeFor(w, r)
	if !ok {
		return
	}
	dir := files.Clean(r.URL.Query().Get("path"))
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected multipart form")
		return
	}
	uploaded := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart body")
			return
		}
		name := path.Base(files.Clean(part.FileName()))
		if part.FileName() == "" || name == "/" {
			continue
		}
		f, err := tree.OpenFile(r.Context(), path.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			writeFilesError(w, err)
			return
		}
		_, err = io.Copy(f, part)
		cerr := f.Close()
		if err == nil {
			err = cerr
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "upload failed")
			return
		}
		uploaded++
	}
	writeJSON(w, http.StatusOK, map[string]int{"uploaded": uploaded})
}

func (s *Server) handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	tree, ok := s.treeFor(w, r)
	if !ok {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := tree.Mkdir(r.Context(), body.Path); err != nil {
		writeFilesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "created"})
}

func (s *Server) handleFilesRename(w http.ResponseWriter, r *http.Request) {
	tree, ok := s.treeFor(w, r)
	if !ok {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := tree.Rename(r.Context(), body.From, body.To); err != nil {
		writeFilesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "renamed"})
}

func (s *Server) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	tree, ok := s.treeFor(w, r)
	if !ok {
		return
	}
	if err := tree.Remove(r.Context(), r.URL.Query().Get("path"), true); err != nil {
		writeFilesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
