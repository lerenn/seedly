package tlscert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns"
	"github.com/go-acme/lego/v4/registration"
)

const (
	renewBefore   = 30 * 24 * time.Hour
	checkInterval = 12 * time.Hour
)

// ACMEOptions configures Let's Encrypt issuance through DNS-01.
type ACMEOptions struct {
	Domain string
	Email  string
	// DNSProvider is a lego DNS provider code (e.g. "cloudflare", "ovh");
	// its credentials come from the provider's usual environment variables.
	DNSProvider string
	// CacheDir stores the account key and the issued certificate.
	CacheDir string
	// CAURL overrides the ACME directory (default: Let's Encrypt production).
	CAURL string
}

// obtainFunc issues a certificate; replaced in tests.
type obtainFunc func(o ACMEOptions) (certPEM, keyPEM []byte, err error)

// FromACME ensures a valid certificate exists in CacheDir (issuing it when
// missing or close to expiry) and keeps renewing it until ctx is cancelled.
func FromACME(ctx context.Context, o ACMEOptions) (*Source, error) {
	return fromACME(ctx, o, obtainLego, checkInterval)
}

func fromACME(ctx context.Context, o ACMEOptions, obtain obtainFunc, interval time.Duration) (*Source, error) {
	if o.Domain == "" || o.DNSProvider == "" || o.CacheDir == "" {
		return nil, errors.New("acme: domain, dns provider and cache dir are required")
	}
	if err := os.MkdirAll(o.CacheDir, 0o700); err != nil {
		return nil, err
	}
	certFile := filepath.Join(o.CacheDir, "cert.pem")
	keyFile := filepath.Join(o.CacheDir, "key.pem")

	renew := func() error {
		if !needsRenewal(certFile, o.Domain, time.Now()) {
			return nil
		}
		log.Printf("acme: obtaining certificate for %s", o.Domain)
		certPEM, keyPEM, err := obtain(o)
		if err != nil {
			return err
		}
		if err := writeAtomic(keyFile, keyPEM, 0o600); err != nil {
			return err
		}
		return writeAtomic(certFile, certPEM, 0o644)
	}
	if err := renew(); err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}
	src, err := FromFiles(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := renew(); err != nil {
					log.Printf("acme: renewal failed: %v", err)
				}
			}
		}
	}()
	return src, nil
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// needsRenewal reports whether the certificate at path is missing, invalid,
// for another domain, or expires within renewBefore.
func needsRenewal(path, domain string, now time.Time) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return true
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	if cert.VerifyHostname(domain) != nil {
		return true
	}
	return now.Add(renewBefore).After(cert.NotAfter)
}

type acmeUser struct {
	email string
	key   crypto.PrivateKey
	reg   *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

func loadOrCreateAccountKey(path string) (*ecdsa.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		if block, _ := pem.Decode(data); block != nil {
			return x509.ParseECPrivateKey(block.Bytes)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return key, writeAtomic(path, pemBytes, 0o600)
}

func obtainLego(o ACMEOptions) ([]byte, []byte, error) {
	key, err := loadOrCreateAccountKey(filepath.Join(o.CacheDir, "account.key"))
	if err != nil {
		return nil, nil, err
	}
	user := &acmeUser{email: o.Email, key: key}
	cfg := lego.NewConfig(user)
	if o.CAURL != "" {
		cfg.CADirURL = o.CAURL
	}
	cfg.Certificate.KeyType = certcrypto.EC256
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	prov, err := dns.NewDNSChallengeProviderByName(o.DNSProvider)
	if err != nil {
		return nil, nil, fmt.Errorf("dns provider %q: %w", o.DNSProvider, err)
	}
	if err := client.Challenge.SetDNS01Provider(prov); err != nil {
		return nil, nil, err
	}
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return nil, nil, fmt.Errorf("register: %w", err)
	}
	user.reg = reg
	res, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: []string{o.Domain},
		Bundle:  true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("obtain: %w", err)
	}
	return res.Certificate, res.PrivateKey, nil
}
