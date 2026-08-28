package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr     string
	DBPath         string
	MetaPath       string
	DownloadsPath  string
	WebPath        string
	AdminUsername  string
	AdminPassword  string
	SessionTTL     time.Duration
	CookieSecure   bool
	CookieName     string
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
