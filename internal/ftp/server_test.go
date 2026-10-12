package ftp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lerenn/seedly/internal/auth"
	"github.com/lerenn/seedly/internal/db"

	jftp "github.com/jlaffaye/ftp"
)

type fakeAuth struct{ users map[string]*db.User }

func (f fakeAuth) Authenticate(_ context.Context, u, p string) (*db.User, error) {
	if usr, ok := f.users[u]; ok && p == "pw-"+u {
		return usr, nil
	}
	return nil, auth.ErrInvalidCredentials
}

type fakeLister map[int64][]db.Torrent

func (f fakeLister) ListTorrentsByOwner(_ context.Context, id int64) ([]db.Torrent, error) {
	return f[id], nil
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

type env struct {
	addr string
	data string
}

func setup(t *testing.T, opts Options) env {
	t.Helper()
	data := t.TempDir()
	d1 := filepath.Join(data, "1_aaaa")
	d2 := filepath.Join(data, "2_bbbb")
	for p, c := range map[string]string{
		filepath.Join(d1, "Movie", "movie.mkv"): "hello video",
		filepath.Join(d2, "Private", "x.txt"):   "other user",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lister := fakeLister{
		1: {{InfoHash: "aaaa", DataPath: d1}},
		2: {{InfoHash: "bbbb", DataPath: d2}},
	}
	authn := fakeAuth{users: map[string]*db.User{
		"admin":  {ID: 1, Username: "admin", Role: db.RoleAdmin},
		"member": {ID: 1, Username: "member", Role: db.RoleUser},
		"other":  {ID: 2, Username: "other", Role: db.RoleUser},
	}}
	opts.ListenAddr = "127.0.0.1:0"
	if opts.PublicHost == "" {
		opts.PublicHost = "127.0.0.1"
	}
	srv, err := Start(opts, authn, lister)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return env{addr: srv.Addr(), data: data}
}

func tlsOpts(t *testing.T) Options {
	cfg := &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}, MinVersion: tls.VersionTLS12}
	// Same instance every time, like tlscert.Source: data connections must be
	// able to resume the control connection's session.
	return Options{TLS: func() (*tls.Config, error) { return cfg, nil }}
}

func dial(t *testing.T, addr, user string, withTLS bool) *jftp.ServerConn {
	t.Helper()
	opts := []jftp.DialOption{jftp.DialWithTimeout(5 * time.Second)}
	if withTLS {
		opts = append(opts, jftp.DialWithExplicitTLS(&tls.Config{InsecureSkipVerify: true}))
	}
	c, err := jftp.Dial(addr, opts...)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Quit() })
	if err := c.Login(user, "pw-"+user); err != nil {
		t.Fatalf("login %s: %v", user, err)
	}
	return c
}

func TestListAndDownloadOverExplicitTLS(t *testing.T) {
	e := setup(t, tlsOpts(t))
	c := dial(t, e.addr, "member", true)

	entries, err := c.List("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "Movie" {
		t.Fatalf("root entries: %+v", entries)
	}
	r, err := c.Retr("/Movie/movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	if string(b) != "hello video" {
		t.Fatalf("content %q", b)
	}
}

func TestUsersCannotSeeEachOther(t *testing.T) {
	e := setup(t, tlsOpts(t))
	c := dial(t, e.addr, "other", true)
	entries, err := c.List("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "Private" {
		t.Fatalf("entries: %+v", entries)
	}
	if _, err := c.Retr("/Movie/movie.mkv"); err == nil {
		t.Fatal("foreign file readable")
	}
}

func TestMemberIsReadOnly(t *testing.T) {
	e := setup(t, tlsOpts(t))
	c := dial(t, e.addr, "member", true)
	if err := c.Stor("/Movie/new.txt", bytes.NewReader([]byte("x"))); err == nil {
		t.Error("member upload succeeded")
	}
	if err := c.Delete("/Movie/movie.mkv"); err == nil {
		t.Error("member delete succeeded")
	}
	if err := c.MakeDir("/Movie/d"); err == nil {
		t.Error("member mkdir succeeded")
	}
}

func TestAdminCanModify(t *testing.T) {
	e := setup(t, tlsOpts(t))
	c := dial(t, e.addr, "admin", true)
	if err := c.MakeDir("/Movie/sub"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stor("/Movie/sub/up.txt", bytes.NewReader([]byte("uploaded"))); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(e.data, "1_aaaa", "Movie", "sub", "up.txt"))
	if err != nil || string(got) != "uploaded" {
		t.Fatalf("upload: %q %v", got, err)
	}
	if err := c.Rename("/Movie/sub/up.txt", "/Movie/renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("/Movie/renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDir("/Movie/sub"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stor("/top.txt", bytes.NewReader([]byte("x"))); err == nil {
		t.Error("top-level upload succeeded")
	}
}

func TestPlaintextLoginRefusedWhenTLSRequired(t *testing.T) {
	e := setup(t, tlsOpts(t))
	c, err := jftp.Dial(e.addr, jftp.DialWithTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Quit()
	if err := c.Login("member", "pw-member"); err == nil {
		t.Fatal("plaintext login accepted")
	}
}

func TestWrongPassword(t *testing.T) {
	e := setup(t, tlsOpts(t))
	c, err := jftp.Dial(e.addr, jftp.DialWithTimeout(5*time.Second),
		jftp.DialWithExplicitTLS(&tls.Config{InsecureSkipVerify: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Quit()
	if err := c.Login("member", "nope"); err == nil {
		t.Fatal("bad password accepted")
	}
}

func TestStartRefusesWithoutTLSOrPlaintextOptIn(t *testing.T) {
	if _, err := Start(Options{ListenAddr: "127.0.0.1:0"}, fakeAuth{}, fakeLister{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestParsePortRange(t *testing.T) {
	if a, b, err := ParsePortRange("30000-30009"); err != nil || a != 30000 || b != 30009 {
		t.Fatalf("%d %d %v", a, b, err)
	}
	for _, bad := range []string{"", "30000", "5-1", "a-b", "0-5"} {
		if _, _, err := ParsePortRange(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDataConnectionResumesTLSSession(t *testing.T) {
	e := setup(t, tlsOpts(t))
	var handshakes, resumed int
	cfg := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "localhost", // session cache key shared by control and data connections
		ClientSessionCache: tls.NewLRUClientSessionCache(8),
		VerifyConnection: func(cs tls.ConnectionState) error {
			handshakes++
			if cs.DidResume {
				resumed++
			}
			return nil
		},
	}
	c, err := jftp.Dial(e.addr, jftp.DialWithTimeout(5*time.Second), jftp.DialWithExplicitTLS(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Quit()
	if err := c.Login("member", "pw-member"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List("/"); err != nil {
		t.Fatal(err)
	}
	if handshakes < 2 || resumed < 1 {
		t.Fatalf("data connection did not resume the session (handshakes=%d resumed=%d)", handshakes, resumed)
	}
}
