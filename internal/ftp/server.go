// Package ftp serves the per-user file trees over FTP/FTPS.
package ftp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/lerenn/seedly/internal/auth"
	"github.com/lerenn/seedly/internal/db"
	"github.com/lerenn/seedly/internal/files"
)

// Authenticator validates FTP credentials.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) (*db.User, error)
}

// Options configures the FTP server.
type Options struct {
	ListenAddr string
	// PublicHost is the IP or hostname announced in passive replies.
	PublicHost string
	// PassiveStart and PassiveEnd bound the passive data ports (0 = any port).
	PassiveStart, PassiveEnd int
	// TLS returns the TLS config for explicit FTPS. When nil, TLS is disabled.
	TLS func() (*tls.Config, error)
	// AllowPlaintext lets clients authenticate without TLS.
	AllowPlaintext bool
}

// Server is a running FTP server.
type Server struct {
	srv *ftpserver.FtpServer
}

type driver struct {
	opts   Options
	auth   Authenticator
	lister files.TorrentLister
}

// Start listens and serves in the background.
func Start(opts Options, authn Authenticator, lister files.TorrentLister) (*Server, error) {
	if opts.TLS == nil && !opts.AllowPlaintext {
		return nil, errors.New("ftp: TLS is not configured and plaintext is not allowed")
	}
	d := &driver{opts: opts, auth: authn, lister: lister}
	srv := ftpserver.NewFtpServer(d)
	if err := srv.Listen(); err != nil {
		return nil, fmt.Errorf("ftp listen: %w", err)
	}
	go func() {
		if err := srv.Serve(); err != nil {
			log.Printf("ftp: serve stopped: %v", err)
		}
	}()
	return &Server{srv: srv}, nil
}

// Addr returns the control listener address.
func (s *Server) Addr() string { return s.srv.Addr() }

// Close stops the server.
func (s *Server) Close() error { return s.srv.Stop() }

func (d *driver) GetSettings() (*ftpserver.Settings, error) {
	st := &ftpserver.Settings{
		ListenAddr: d.opts.ListenAddr,
		Banner:     "Seedly FTP",
	}
	if d.opts.PassiveStart > 0 {
		st.PassiveTransferPortRange = &ftpserver.PortRange{Start: d.opts.PassiveStart, End: d.opts.PassiveEnd}
	}
	if h := d.opts.PublicHost; h != "" {
		st.PublicIPResolver = func(ftpserver.ClientContext) (string, error) { return resolveIPv4(h) }
	}
	if d.opts.TLS != nil && !d.opts.AllowPlaintext {
		st.TLSRequired = ftpserver.MandatoryEncryption
	}
	return st, nil
}

func resolveIPv4(host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address for %q", host)
}

func (d *driver) ClientConnected(ftpserver.ClientContext) (string, error) {
	return "Seedly FTP", nil
}

func (d *driver) ClientDisconnected(ftpserver.ClientContext) {}

func (d *driver) AuthUser(_ ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	u, err := d.auth.Authenticate(context.Background(), user, pass)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return nil, errors.New("invalid credentials")
		}
		return nil, err
	}
	// Everyone only sees their own files; only admins may modify them.
	tree := files.NewTree(d.lister, u.ID, u.Role != db.RoleAdmin)
	return &treeFS{tree: tree}, nil
}

func (d *driver) GetTLSConfig() (*tls.Config, error) {
	if d.opts.TLS == nil {
		return nil, errors.New("tls not configured")
	}
	return d.opts.TLS()
}

// ParsePortRange parses "start-end".
func ParsePortRange(s string) (int, int, error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("invalid port range %q (want start-end)", s)
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(a))
	end, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || start < 1 || end < start || end > 65535 {
		return 0, 0, fmt.Errorf("invalid port range %q", s)
	}
	return start, end, nil
}
