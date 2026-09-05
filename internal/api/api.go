package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/lerenn/seedly/internal/auth"
	"github.com/lerenn/seedly/internal/config"
	"github.com/lerenn/seedly/internal/db"
	"github.com/lerenn/seedly/internal/disk"
	"github.com/lerenn/seedly/internal/download"
	"github.com/lerenn/seedly/internal/torrent"
)

type Server struct {
	cfg    config.Config
	auth   *auth.Service
	db     *db.DB
	engine *torrent.Engine
	web    http.Handler
}

func New(cfg config.Config, authSvc *auth.Service, database *db.DB, engine *torrent.Engine, web http.Handler) *Server {
	return &Server{cfg: cfg, auth: authSvc, db: database, engine: engine, web: web}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api", func(r chi.Router) {
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/auth/me", s.handleMe)
			r.Get("/disk", s.handleDiskUsage)
			r.Get("/users", s.handleListUsers)
			r.Post("/users", s.handleCreateUser)
			r.Patch("/users/{id}", s.handleUpdateUser)
			r.Delete("/users/{id}", s.handleDeleteUser)
			r.Get("/torrents", s.handleListTorrents)
			r.Post("/torrents", s.handleUploadTorrent)
			r.Get("/torrents/{id}", s.handleGetTorrent)
			r.Post("/torrents/{id}/pause", s.handlePauseTorrent)
			r.Post("/torrents/{id}/resume", s.handleResumeTorrent)
			r.Delete("/torrents/{id}", s.handleDeleteTorrent)
			r.Get("/torrents/{id}/download", s.handleDownloadTorrent)
		})
	})

	if s.web != nil {
		r.NotFound(s.web.ServeHTTP)
	}
	return r
}

type ctxKey int

const userKey ctxKey = 1

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.cfg.CookieName)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		user, err := s.auth.UserFromToken(r.Context(), c.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func currentUser(r *http.Request) *db.User {
	u, _ := r.Context().Value(userKey).(*db.User)
	return u
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	token, user, err := s.auth.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
		Expires:  time.Now().Add(s.cfg.SessionTTL),
	})
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cfg.CookieName); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}

func (s *Server) handleDiskUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := disk.UsageFor(s.cfg.DownloadsPath, s.cfg.MetaPath, s.cfg.DBPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user.Role != db.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	users, err := s.db.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list users failed")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	role := db.RoleUser
	if body.Role == string(db.RoleAdmin) {
		role = db.RoleAdmin
	}
	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}
	u, err := s.auth.CreateUser(r.Context(), actor, body.Username, body.Password, role)
	if err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Username    *string `json:"username"`
		DisplayName *string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	var u *db.User

	if body.Username != nil {
		u, err = s.auth.RenameUser(r.Context(), actor, id, *body.Username)
		if err != nil {
			if errors.Is(err, auth.ErrForbidden) {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "user not found")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if body.DisplayName != nil {
		u, err = s.auth.UpdateDisplayName(r.Context(), actor, id, *body.DisplayName)
		if err != nil {
			if errors.Is(err, auth.ErrForbidden) {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "user not found")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if u == nil {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.auth.DeleteUser(r.Context(), actor, id); err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleListTorrents(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	ownerID := user.ID
	if ownerParam := r.URL.Query().Get("owner_id"); ownerParam != "" {
		if user.Role != db.RoleAdmin {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		id, err := strconv.ParseInt(ownerParam, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid owner_id")
			return
		}
		ownerID = id
	}
	list, err := s.engine.ListForOwner(r.Context(), ownerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list torrents failed")
		return
	}
	if list == nil {
		list = []torrent.TorrentView{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUploadTorrent(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("torrent")
	if err != nil {
		writeError(w, http.StatusBadRequest, "torrent file required")
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".torrent") {
		writeError(w, http.StatusBadRequest, "file must be .torrent")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read failed")
		return
	}
	view, err := s.engine.AddFromTorrentFile(r.Context(), user.ID, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleGetTorrent(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.loadAuthorizedTorrent(w, r)
	if !ok {
		return
	}
	view, err := s.engine.Get(r.Context(), rec.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handlePauseTorrent(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.loadAuthorizedTorrent(w, r)
	if !ok {
		return
	}
	if err := s.engine.Pause(r.Context(), rec.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	view, _ := s.engine.Get(r.Context(), rec.ID)
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleResumeTorrent(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.loadAuthorizedTorrent(w, r)
	if !ok {
		return
	}
	if err := s.engine.Resume(r.Context(), rec.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	view, _ := s.engine.Get(r.Context(), rec.ID)
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleDeleteTorrent(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.loadAuthorizedTorrent(w, r)
	if !ok {
		return
	}
	removeData := r.URL.Query().Get("remove_data") != "false"
	if err := s.engine.Delete(r.Context(), rec.ID, removeData); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleDownloadTorrent(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.loadAuthorizedTorrent(w, r)
	if !ok {
		return
	}
	view, err := s.engine.Get(r.Context(), rec.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}
	if !view.Stats.Complete && view.Status != db.StatusSeeding && view.CompletedAt == nil {
		writeError(w, http.StatusConflict, "torrent not complete")
		return
	}
	tt, live := s.engine.LiveTorrent(rec.ID)
	if live && tt.Info() != nil {
		if err := download.WriteContent(w, tt, rec.DataPath); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if err := download.WriteFromDisk(w, rec.Name, rec.DataPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) loadAuthorizedTorrent(w http.ResponseWriter, r *http.Request) (*db.Torrent, bool) {
	user := currentUser(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return nil, false
	}
	rec, err := s.db.GetTorrentByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "torrent not found")
		return nil, false
	}
	if user.Role != db.RoleAdmin && rec.OwnerID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return nil, false
	}
	return rec, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
