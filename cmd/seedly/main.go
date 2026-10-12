package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lerenn/seedly/internal/api"
	"github.com/lerenn/seedly/internal/auth"
	"github.com/lerenn/seedly/internal/config"
	"github.com/lerenn/seedly/internal/db"
	"github.com/lerenn/seedly/internal/ftp"
	"github.com/lerenn/seedly/internal/tlscert"
	"github.com/lerenn/seedly/internal/torrent"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	authSvc := auth.New(database, cfg.SessionTTL)
	ctx := context.Background()
	if err := authSvc.BootstrapAdmin(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}

	engine, err := torrent.New(database, cfg.MetaPath, cfg.DownloadsPath)
	if err != nil {
		log.Fatalf("torrent engine: %v", err)
	}
	defer engine.Close()
	if err := engine.IndexTrackers(ctx); err != nil {
		log.Fatalf("index trackers: %v", err)
	}
	if err := engine.Reload(ctx); err != nil {
		log.Fatalf("reload torrents: %v", err)
	}

	if cfg.FTPEnabled {
		ftpSrv, err := startFTP(ctx, cfg, authSvc, database)
		if err != nil {
			log.Fatalf("ftp: %v", err)
		}
		defer ftpSrv.Close()
		log.Printf("seedly ftp listening on %s", ftpSrv.Addr())
	}

	webHandler := spaHandler(cfg.WebPath)
	srv := api.New(cfg, authSvc, database, engine, webHandler)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("seedly listening on %s", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

func startFTP(ctx context.Context, cfg config.Config, authSvc *auth.Service, database *db.DB) (*ftp.Server, error) {
	opts := ftp.Options{
		ListenAddr:     cfg.FTPListen,
		PublicHost:     cfg.FTPPublicHost,
		AllowPlaintext: cfg.FTPAllowPlaintext,
	}
	start, end, err := ftp.ParsePortRange(cfg.FTPPassivePorts)
	if err != nil {
		return nil, err
	}
	opts.PassiveStart, opts.PassiveEnd = start, end

	var src *tlscert.Source
	switch {
	case cfg.ACMEDomain != "":
		src, err = tlscert.FromACME(ctx, tlscert.ACMEOptions{
			Domain:      cfg.ACMEDomain,
			Email:       cfg.ACMEEmail,
			DNSProvider: cfg.ACMEDNSProvider,
			CacheDir:    cfg.ACMECacheDir,
			CAURL:       cfg.ACMECAURL,
		})
	case cfg.FTPTLSCertFile != "" || cfg.FTPTLSKeyFile != "":
		src, err = tlscert.FromFiles(cfg.FTPTLSCertFile, cfg.FTPTLSKeyFile)
	}
	if err != nil {
		return nil, err
	}
	if src != nil {
		opts.TLS = src.Config
	} else if !cfg.FTPAllowPlaintext {
		return nil, errors.New("no TLS configured: set SEEDLY_FTP_TLS_ACME_DOMAIN or SEEDLY_FTP_TLS_CERT_FILE/KEY_FILE, or SEEDLY_FTP_ALLOW_PLAINTEXT=true")
	}
	return ftp.Start(opts, authSvc, database)
}

func spaHandler(webPath string) http.Handler {
	root := filepath.Clean(webPath)
	fileServer := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := filepath.Clean("/" + r.URL.Path)
		path := filepath.Join(root, rel)
		if path != root && !strings.HasPrefix(path, root+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		index := filepath.Join(root, "index.html")
		if _, err := os.Stat(index); err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, index)
	})
}
