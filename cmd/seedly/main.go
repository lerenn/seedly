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
	if err := engine.Reload(ctx); err != nil {
		log.Fatalf("reload torrents: %v", err)
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
