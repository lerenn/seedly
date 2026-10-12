// Package tlscert provides TLS certificates from files on disk, optionally
// obtained and renewed from Let's Encrypt (ACME DNS-01).
package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"
)

// Source serves a certificate loaded from a cert/key file pair and reloads it
// whenever either file changes (e.g. after a renewal).
type Source struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	certMod time.Time
	keyMod  time.Time
}

// FromFiles loads the key pair and returns a reloading source.
func FromFiles(certFile, keyFile string) (*Source, error) {
	s := &Source{certFile: certFile, keyFile: keyFile}
	if _, err := s.current(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Source) current() (*tls.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ci, err := os.Stat(s.certFile)
	if err != nil {
		return s.fallback(err)
	}
	ki, err := os.Stat(s.keyFile)
	if err != nil {
		return s.fallback(err)
	}
	if s.cert != nil && ci.ModTime().Equal(s.certMod) && ki.ModTime().Equal(s.keyMod) {
		return s.cert, nil
	}
	pair, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
	if err != nil {
		return s.fallback(err)
	}
	if len(pair.Certificate) > 0 {
		if leaf, err := x509.ParseCertificate(pair.Certificate[0]); err == nil {
			pair.Leaf = leaf
		}
	}
	s.cert, s.certMod, s.keyMod = &pair, ci.ModTime(), ki.ModTime()
	return s.cert, nil
}

// fallback keeps serving the last good certificate if a reload fails.
func (s *Source) fallback(err error) (*tls.Certificate, error) {
	if s.cert != nil {
		return s.cert, nil
	}
	return nil, fmt.Errorf("load certificate: %w", err)
}

// GetCertificate implements tls.Config.GetCertificate.
func (s *Source) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return s.current()
}

// Config returns a server TLS config backed by the source.
func (s *Source) Config() (*tls.Config, error) {
	if _, err := s.current(); err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: s.GetCertificate,
	}, nil
}
