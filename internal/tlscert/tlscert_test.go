package tlscert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func genPair(t *testing.T, domain string, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
}

func TestSourceReloadsOnChange(t *testing.T) {
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	c1, k1 := genPair(t, "a.example", time.Now().Add(time.Hour))
	os.WriteFile(cf, c1, 0o644)
	os.WriteFile(kf, k1, 0o600)
	src, err := FromFiles(cf, kf)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := src.GetCertificate(nil)

	c2, k2 := genPair(t, "b.example", time.Now().Add(time.Hour))
	os.WriteFile(kf, k2, 0o600)
	os.WriteFile(cf, c2, 0o644)
	future := time.Now().Add(time.Minute)
	os.Chtimes(cf, future, future)
	os.Chtimes(kf, future, future)

	second, err := src.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Leaf.DNSNames[0] != "a.example" || second.Leaf.DNSNames[0] != "b.example" {
		t.Fatalf("got %v then %v", first.Leaf.DNSNames, second.Leaf.DNSNames)
	}
}

func TestNeedsRenewal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.pem")
	if !needsRenewal(p, "a.example", time.Now()) {
		t.Error("missing file should need renewal")
	}
	c, _ := genPair(t, "a.example", time.Now().Add(90*24*time.Hour))
	os.WriteFile(p, c, 0o644)
	if needsRenewal(p, "a.example", time.Now()) {
		t.Error("fresh cert should not need renewal")
	}
	if !needsRenewal(p, "other.example", time.Now()) {
		t.Error("wrong domain should need renewal")
	}
	if !needsRenewal(p, "a.example", time.Now().Add(70*24*time.Hour)) {
		t.Error("expiring cert should need renewal")
	}
}

func TestFromACMEObtainsWhenMissingAndRenews(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	obtain := func(o ACMEOptions) ([]byte, []byte, error) {
		calls++
		// First issue is nearly expired so the renewal loop must replace it.
		na := time.Now().Add(24 * time.Hour)
		if calls > 1 {
			na = time.Now().Add(90 * 24 * time.Hour)
		}
		c, k := genPair(t, o.Domain, na)
		return c, k, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := fromACME(ctx, ACMEOptions{Domain: "a.example", DNSProvider: "x", CacheDir: dir}, obtain, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Config(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls < 2 {
		t.Fatalf("renewal did not run (calls=%d)", calls)
	}
}

func TestFromACMEValidatesOptions(t *testing.T) {
	if _, err := FromACME(context.Background(), ACMEOptions{}); err == nil {
		t.Fatal("expected error")
	}
}
