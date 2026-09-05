package torrent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/lerenn/seedly/internal/db"
)

type Engine struct {
	client        *torrent.Client
	db            *db.DB
	metaPath      string
	downloadsPath string
	mu            sync.Mutex
	byID          map[int64]*torrent.Torrent
}

type LiveStats struct {
	Progress       float64 `json:"progress"`
	Downloaded     int64   `json:"downloaded"`
	Uploaded       int64   `json:"uploaded"`
	DownloadRate   int64   `json:"download_rate"`
	UploadRate     int64   `json:"upload_rate"`
	Peers          int     `json:"peers"`
	TotalLength    int64   `json:"total_length"`
	BytesCompleted int64   `json:"bytes_completed"`
	Complete       bool    `json:"complete"`
	FileCount      int     `json:"file_count"`
}

type TorrentView struct {
	db.Torrent
	Stats LiveStats `json:"stats"`
}

func New(database *db.DB, metaPath, downloadsPath string) (*Engine, error) {
	if err := os.MkdirAll(metaPath, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(downloadsPath, 0o755); err != nil {
		return nil, err
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = downloadsPath
	cfg.DefaultStorage = storage.NewFile(downloadsPath)
	cfg.Seed = true
	cfg.ListenPort = 0
	cfg.NoDefaultPortForwarding = true

	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("torrent client: %w", err)
	}

	return &Engine{
		client:        client,
		db:            database,
		metaPath:      metaPath,
		downloadsPath: downloadsPath,
		byID:          make(map[int64]*torrent.Torrent),
	}, nil
}

func (e *Engine) Close() {
	e.client.Close()
}

func (e *Engine) Reload(ctx context.Context) error {
	all, err := e.db.ListAllTorrents(ctx)
	if err != nil {
		return err
	}
	for _, t := range all {
		if t.Status == db.StatusError || t.Status == db.StatusPaused {
			continue
		}
		tt, err := e.addFromMetaFile(t.MetaPath, t.DataPath)
		if err != nil {
			_ = e.db.UpdateTorrentStatus(ctx, t.ID, db.StatusError, err.Error(), nil)
			continue
		}
		tt.DownloadAll()
		e.mu.Lock()
		e.byID[t.ID] = tt
		e.mu.Unlock()
		go e.watchCompletion(t.ID, tt)
	}
	return nil
}

func (e *Engine) AddFromTorrentFile(ctx context.Context, ownerID int64, torrentBytes []byte) (*TorrentView, error) {
	mi, err := metainfo.Load(bytes.NewReader(torrentBytes))
	if err != nil {
		return nil, fmt.Errorf("parse torrent: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, fmt.Errorf("torrent info: %w", err)
	}
	infoHash := mi.HashInfoBytes().HexString()

	metaFile := filepath.Join(e.metaPath, fmt.Sprintf("%d_%s.torrent", ownerID, infoHash))
	dataDir := filepath.Join(e.downloadsPath, fmt.Sprintf("%d_%s", ownerID, infoHash))
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(metaFile, torrentBytes, 0o644); err != nil {
		return nil, err
	}

	rec, err := e.db.CreateTorrent(ctx, db.Torrent{
		OwnerID:  ownerID,
		InfoHash: infoHash,
		Name:     info.Name,
		MetaPath: metaFile,
		DataPath: dataDir,
		Status:   db.StatusDownloading,
	})
	if err != nil {
		_ = os.Remove(metaFile)
		return nil, err
	}

	tt, err := e.addFromMetaFile(metaFile, dataDir)
	if err != nil {
		_ = e.db.UpdateTorrentStatus(ctx, rec.ID, db.StatusError, err.Error(), nil)
		return nil, err
	}
	tt.DownloadAll()

	e.mu.Lock()
	e.byID[rec.ID] = tt
	e.mu.Unlock()
	go e.watchCompletion(rec.ID, tt)

	return e.view(rec), nil
}

func (e *Engine) addFromMetaFile(metaPath, dataDir string) (*torrent.Torrent, error) {
	mi, err := metainfo.LoadFromFile(metaPath)
	if err != nil {
		return nil, err
	}
	spec := torrent.TorrentSpecFromMetaInfo(mi)
	spec.Storage = storage.NewFile(dataDir)
	tt, _, err := e.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, err
	}
	<-tt.GotInfo()
	return tt, nil
}

func (e *Engine) watchCompletion(id int64, tt *torrent.Torrent) {
	select {
	case <-tt.Complete().On():
		now := time.Now().UTC()
		_ = e.db.UpdateTorrentStatus(context.Background(), id, db.StatusSeeding, "", &now)
	case <-tt.Closed():
	}
}

func (e *Engine) Pause(ctx context.Context, id int64) error {
	e.mu.Lock()
	tt, ok := e.byID[id]
	if ok {
		delete(e.byID, id)
	}
	e.mu.Unlock()
	if ok {
		tt.Drop()
	}
	return e.db.UpdateTorrentStatus(ctx, id, db.StatusPaused, "", nil)
}

func (e *Engine) Resume(ctx context.Context, id int64) error {
	rec, err := e.db.GetTorrentByID(ctx, id)
	if err != nil {
		return err
	}
	e.mu.Lock()
	_, exists := e.byID[id]
	e.mu.Unlock()
	if exists {
		if rec.CompletedAt != nil {
			return e.db.UpdateTorrentStatus(ctx, id, db.StatusSeeding, "", nil)
		}
		return e.db.UpdateTorrentStatus(ctx, id, db.StatusDownloading, "", nil)
	}
	tt, err := e.addFromMetaFile(rec.MetaPath, rec.DataPath)
	if err != nil {
		_ = e.db.UpdateTorrentStatus(ctx, id, db.StatusError, err.Error(), nil)
		return err
	}
	tt.DownloadAll()
	e.mu.Lock()
	e.byID[id] = tt
	e.mu.Unlock()
	go e.watchCompletion(id, tt)

	if tt.Info() != nil && tt.BytesCompleted() >= tt.Length() && tt.Length() > 0 {
		now := time.Now().UTC()
		return e.db.UpdateTorrentStatus(ctx, id, db.StatusSeeding, "", &now)
	}
	return e.db.UpdateTorrentStatus(ctx, id, db.StatusDownloading, "", nil)
}

func (e *Engine) Delete(ctx context.Context, id int64, removeData bool) error {
	rec, err := e.db.GetTorrentByID(ctx, id)
	if err != nil {
		return err
	}
	e.mu.Lock()
	tt, ok := e.byID[id]
	if ok {
		delete(e.byID, id)
	}
	e.mu.Unlock()
	if ok {
		tt.Drop()
	}
	if err := e.db.DeleteTorrent(ctx, id); err != nil {
		return err
	}
	_ = os.Remove(rec.MetaPath)
	if removeData {
		_ = os.RemoveAll(rec.DataPath)
	}
	return nil
}

func (e *Engine) Get(ctx context.Context, id int64) (*TorrentView, error) {
	rec, err := e.db.GetTorrentByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return e.view(rec), nil
}

func (e *Engine) ListForOwner(ctx context.Context, ownerID int64) ([]TorrentView, error) {
	recs, err := e.db.ListTorrentsByOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]TorrentView, 0, len(recs))
	for i := range recs {
		out = append(out, *e.view(&recs[i]))
	}
	return out, nil
}

func (e *Engine) LiveTorrent(id int64) (*torrent.Torrent, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tt, ok := e.byID[id]
	return tt, ok
}

func (e *Engine) view(rec *db.Torrent) *TorrentView {
	v := &TorrentView{Torrent: *rec}
	e.mu.Lock()
	tt, ok := e.byID[rec.ID]
	e.mu.Unlock()
	if !ok || tt.Info() == nil {
		v.Stats = LiveStats{}
		if rec.Status == db.StatusSeeding || rec.CompletedAt != nil {
			v.Stats.Complete = true
			v.Stats.Progress = 1
		}
		return v
	}
	stats := tt.Stats()
	completed := tt.BytesCompleted()
	total := tt.Length()
	progress := float64(0)
	if total > 0 {
		progress = float64(completed) / float64(total)
	}
	complete := total > 0 && completed >= total
	if complete && rec.Status == db.StatusDownloading {
		now := time.Now().UTC()
		_ = e.db.UpdateTorrentStatus(context.Background(), rec.ID, db.StatusSeeding, "", &now)
		v.Status = db.StatusSeeding
		v.CompletedAt = &now
	}
	v.Stats = LiveStats{
		Progress:       progress,
		Downloaded:     stats.BytesReadData.Int64(),
		Uploaded:       stats.BytesWrittenData.Int64(),
		DownloadRate:   0,
		UploadRate:     0,
		Peers:          stats.ActivePeers,
		TotalLength:    total,
		BytesCompleted: completed,
		Complete:       complete,
		FileCount:      len(tt.Files()),
	}
	return v
}
