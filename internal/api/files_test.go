package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lerenn/seedly/internal/auth"
	"github.com/lerenn/seedly/internal/config"
	"github.com/lerenn/seedly/internal/db"
)

type filesEnv struct {
	srv      *httptest.Server
	data     string
	adminID  int64
	memberID int64

	adminTorrent, memberTorrent int64
	cookies                     map[string]*http.Cookie
}

func newFilesEnv(t *testing.T) *filesEnv {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "db", "seedly.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	svc := auth.New(database, time.Hour)
	ctx := context.Background()
	if err := svc.BootstrapAdmin(ctx, "admin", "adminpw"); err != nil {
		t.Fatal(err)
	}
	admin, _ := database.GetUserByUsername(ctx, "admin")
	member, err := svc.CreateUser(ctx, admin, "member", "memberpw", db.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	ids := map[int64]int64{}
	for _, o := range []struct {
		id   int64
		hash string
		file string
	}{{admin.ID, "aaaa", "admin-file.txt"}, {member.ID, "bbbb", "member-file.txt"}} {
		d := filepath.Join(data, "x_"+o.hash)
		if err := os.MkdirAll(filepath.Join(d, "Show"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "Show", o.file), []byte("content of "+o.file), 0o644); err != nil {
			t.Fatal(err)
		}
		rec, err := database.CreateTorrent(ctx, db.Torrent{
			OwnerID: o.id, InfoHash: o.hash, Name: "Show", DataPath: d, Status: db.StatusSeeding,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[o.id] = rec.ID
	}
	cfg := config.Config{CookieName: "s", SessionTTL: time.Hour, LoginMaxFailures: 5, LoginBan: time.Minute}
	ts := httptest.NewServer(New(cfg, svc, database, nil, nil).Handler())
	t.Cleanup(ts.Close)
	e := &filesEnv{srv: ts, data: data, adminID: admin.ID, memberID: member.ID, cookies: map[string]*http.Cookie{},
		adminTorrent: ids[admin.ID], memberTorrent: ids[member.ID]}
	for u, p := range map[string]string{"admin": "adminpw", "member": "memberpw"} {
		body, _ := json.Marshal(map[string]string{"username": u, "password": p})
		resp, err := http.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("login %s: %v %v", u, err, resp)
		}
		e.cookies[u] = resp.Cookies()[0]
	}
	return e
}

func (e *filesEnv) tpath(id int64, rest string) string {
	return fmt.Sprintf("/api/torrents/%d/%s", id, rest)
}

func (e *filesEnv) do(t *testing.T, user, method, path string, body io.Reader, ct string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.AddCookie(e.cookies[user])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestFilesListScopedToTorrent(t *testing.T) {
	e := newFilesEnv(t)
	code, b := e.do(t, "member", "GET", e.tpath(e.memberTorrent, "files?path=/"), nil, "")
	if code != 200 || !strings.Contains(string(b), "member-file.txt") || strings.Contains(string(b), "admin-file.txt") {
		t.Fatalf("%d %s", code, b)
	}
	if !strings.Contains(string(b), `"writable":false`) {
		t.Errorf("member should be read-only: %s", b)
	}
	code, b = e.do(t, "admin", "GET", e.tpath(e.adminTorrent, "files?path=/"), nil, "")
	if code != 200 || !strings.Contains(string(b), "admin-file.txt") || !strings.Contains(string(b), `"writable":true`) {
		t.Fatalf("%d %s", code, b)
	}
}

func TestFilesForeignTorrentForbidden(t *testing.T) {
	e := newFilesEnv(t)
	code, _ := e.do(t, "member", "GET", e.tpath(e.adminTorrent, "files"), nil, "")
	if code != http.StatusForbidden {
		t.Errorf("member on admin torrent: %d", code)
	}
	// Admins may act on any user's torrent (View as).
	code, b := e.do(t, "admin", "GET", e.tpath(e.memberTorrent, "files"), nil, "")
	if code != 200 || !strings.Contains(string(b), "member-file.txt") {
		t.Errorf("admin on member torrent: %d %s", code, b)
	}
}

func TestFilesDownloadAndTraversal(t *testing.T) {
	e := newFilesEnv(t)
	code, b := e.do(t, "member", "GET", e.tpath(e.memberTorrent, "files/download?path=/member-file.txt"), nil, "")
	if code != 200 || string(b) != "content of member-file.txt" {
		t.Fatalf("%d %q", code, b)
	}
	code, _ = e.do(t, "member", "GET", e.tpath(e.memberTorrent, "files/download?path=/../../x_aaaa/Show/admin-file.txt"), nil, "")
	if code != http.StatusNotFound {
		t.Errorf("traversal: %d", code)
	}
}

func TestFilesMemberCannotWrite(t *testing.T) {
	e := newFilesEnv(t)
	code, _ := e.do(t, "member", "DELETE", e.tpath(e.memberTorrent, "files?path=/member-file.txt"), nil, "")
	if code != http.StatusForbidden {
		t.Errorf("delete: %d", code)
	}
	code, _ = e.do(t, "member", "POST", e.tpath(e.memberTorrent, "files/mkdir"), strings.NewReader(`{"path":"/d"}`), "application/json")
	if code != http.StatusForbidden {
		t.Errorf("mkdir: %d", code)
	}
}

func TestFilesAdminWriteFlow(t *testing.T) {
	e := newFilesEnv(t)
	code, _ := e.do(t, "admin", "POST", e.tpath(e.adminTorrent, "files/mkdir"), strings.NewReader(`{"path":"/d"}`), "application/json")
	if code != 200 {
		t.Fatalf("mkdir: %d", code)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", "up.txt")
	fw.Write([]byte("uploaded"))
	mw.Close()
	code, b := e.do(t, "admin", "POST", e.tpath(e.adminTorrent, "files/upload?path=/d"), &buf, mw.FormDataContentType())
	if code != 200 || !strings.Contains(string(b), `"uploaded":1`) {
		t.Fatalf("upload: %d %s", code, b)
	}
	code, _ = e.do(t, "admin", "POST", e.tpath(e.adminTorrent, "files/rename"),
		strings.NewReader(`{"from":"/d/up.txt","to":"/up2.txt"}`), "application/json")
	if code != 200 {
		t.Fatalf("rename: %d", code)
	}
	got, err := os.ReadFile(filepath.Join(e.data, "x_aaaa", "Show", "up2.txt"))
	if err != nil || string(got) != "uploaded" {
		t.Fatalf("file: %q %v", got, err)
	}
	code, _ = e.do(t, "admin", "DELETE", e.tpath(e.adminTorrent, "files?path=/d"), nil, "")
	if code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.data, "x_aaaa", "Show", "d")); !os.IsNotExist(err) {
		t.Errorf("dir still exists: %v", err)
	}
	code, _ = e.do(t, "admin", "DELETE", e.tpath(e.adminTorrent, "files?path=/"), nil, "")
	if code != http.StatusForbidden {
		t.Errorf("deleting the root: %d", code)
	}
}

func TestLoginThrottled(t *testing.T) {
	e := newFilesEnv(t)
	login := func(pw, realIP string) int {
		body, _ := json.Marshal(map[string]string{"username": "admin", "password": pw})
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/auth/login", bytes.NewReader(body))
		if realIP != "" {
			req.Header.Set("X-Real-IP", realIP)
		}
		// A spoofable header must not let a client dodge the limiter.
		req.Header.Set("True-Client-IP", fmt.Sprintf("9.9.9.%d", time.Now().UnixNano()%250))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for i := 0; i < 5; i++ {
		if c := login("wrong", "1.1.1.1"); c != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, c)
		}
	}
	if c := login("adminpw", "1.1.1.1"); c != http.StatusTooManyRequests {
		t.Fatalf("expected 429 even with right password, got %d", c)
	}
	if c := login("adminpw", "2.2.2.2"); c != http.StatusOK {
		t.Fatalf("other client blocked: %d", c)
	}
}
