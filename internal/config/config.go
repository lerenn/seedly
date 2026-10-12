package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr    string
	DBPath        string
	MetaPath      string
	DownloadsPath string
	WebPath       string
	AdminUsername string
	AdminPassword string
	SessionTTL    time.Duration
	CookieSecure  bool
	CookieName    string

	FTPEnabled        bool
	FTPListen         string
	FTPPublicHost     string
	FTPPassivePorts   string
	FTPAllowPlaintext bool
	FTPTLSCertFile    string
	FTPTLSKeyFile     string
	ACMEDomain        string
	ACMEEmail         string
	ACMEDNSProvider   string
	ACMECacheDir      string
	ACMECAURL         string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:    envOr("SEEDLY_LISTEN", ":8080"),
		DBPath:        envOr("SEEDLY_DB_PATH", "/data/db/seedly.db"),
		MetaPath:      envOr("SEEDLY_META_PATH", "/data/meta"),
		DownloadsPath: envOr("SEEDLY_DOWNLOADS_PATH", "/data/downloads"),
		WebPath:       envOr("SEEDLY_WEB_PATH", "web/dist"),
		AdminUsername: envOr("SEEDLY_ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("SEEDLY_ADMIN_PASSWORD"),
		CookieName:    envOr("SEEDLY_COOKIE_NAME", "seedly_session"),
		CookieSecure:  envBool("SEEDLY_COOKIE_SECURE", false),
		SessionTTL:    envDuration("SEEDLY_SESSION_TTL", 7*24*time.Hour),

		FTPEnabled:        envBool("SEEDLY_FTP_ENABLED", false),
		FTPListen:         envOr("SEEDLY_FTP_LISTEN", ":2121"),
		FTPPublicHost:     os.Getenv("SEEDLY_FTP_PUBLIC_HOST"),
		FTPPassivePorts:   envOr("SEEDLY_FTP_PASV_PORTS", "30000-30009"),
		FTPAllowPlaintext: envBool("SEEDLY_FTP_ALLOW_PLAINTEXT", false),
		FTPTLSCertFile:    os.Getenv("SEEDLY_FTP_TLS_CERT_FILE"),
		FTPTLSKeyFile:     os.Getenv("SEEDLY_FTP_TLS_KEY_FILE"),
		ACMEDomain:        os.Getenv("SEEDLY_FTP_TLS_ACME_DOMAIN"),
		ACMEEmail:         os.Getenv("SEEDLY_FTP_TLS_ACME_EMAIL"),
		ACMEDNSProvider:   os.Getenv("SEEDLY_FTP_TLS_ACME_DNS_PROVIDER"),
		ACMECacheDir:      envOr("SEEDLY_FTP_TLS_ACME_CACHE_DIR", "/data/certs"),
		ACMECAURL:         os.Getenv("SEEDLY_FTP_TLS_ACME_CA_URL"),
	}
	if cfg.AdminPassword == "" {
		return cfg, fmt.Errorf("SEEDLY_ADMIN_PASSWORD is required")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
