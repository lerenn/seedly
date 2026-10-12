package torrent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/lerenn/seedly/internal/db"
	"github.com/lerenn/seedly/internal/trackers"
)

type Engine struct {
	client        *torrent.Client
	db            *db.DB
	metaPath      string
	downloadsPath string
	mu            sync.Mutex
	byID          map[int64]*liveTorrent
	flushMu       sync.Mutex
	addMu         sync.Mutex
	done          chan struct{}
}

// statsFlushInterval bounds how much transfer accounting is lost on a crash.
const statsFlushInterval = 30 * time.Second

// liveTorrent tracks how much of the client's per-session transfer counters
// has already been added to the torrent's persisted totals.
type liveTorrent struct {
	tt          *torrent.Torrent
	flushedUp   int64
	flushedDown int64
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

	e := &Engine{
		client:        client,
		db:            database,
		metaPath:      metaPath,
		downloadsPath: downloadsPath,
		byID:          make(map[int64]*liveTorrent),
		done:          make(chan struct{}),
	}
	go e.flushLoop()
	return e, nil
}

func (e *Engine) Close() {
	close(e.done)
	e.flushAll()
	e.client.Close()
}

func (e *Engine) flushLoop() {
	ticker := time.NewTicker(statsFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.flushAll()
		case <-e.done:
			return
		}
	}
}

func (e *Engine) flushAll() {
	e.mu.Lock()
	live := make(map[int64]*liveTorrent, len(e.byID))
	for id, lt := range e.byID {
		live[id] = lt
	}
	e.mu.Unlock()
	for id, lt := range live {
		e.flush(id, lt)
	}
}

// flush adds the transfer bytes accumulated since the last flush to the
// torrent's persisted totals.
func (e *Engine) flush(id int64, lt *liveTorrent) {
	e.flushMu.Lock()
	defer e.flushMu.Unlock()
	stats := lt.tt.Stats()
	up, down := stats.BytesWrittenData.Int64(), stats.BytesReadData.Int64()
	e.mu.Lock()
	deltaUp, deltaDown := up-lt.flushedUp, down-lt.flushedDown
	e.mu.Unlock()
	if deltaUp == 0 && deltaDown == 0 {
		return
	}
	if err := e.db.AddTorrentTransfer(context.Background(), id, deltaUp, deltaDown); err != nil {
		return
	}
	e.mu.Lock()
	lt.flushedUp, lt.flushedDown = up, down
	e.mu.Unlock()
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
		e.byID[t.ID] = &liveTorrent{tt: tt}
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

	trackerURLs := mi.UpvertedAnnounceList().DistinctValues()
	policy, err := e.policy(ctx)
	if err != nil {
		return nil, err
	}
	if hosts := trackers.Hosts(trackerURLs); len(hosts) > 0 && policy.Classify(hosts) == trackers.StateDenied {
		return nil, fmt.Errorf("%q uses only unauthorized trackers: %s", info.Name, strings.Join(hosts, ", "))
	}

	// Serialize adds so concurrent uploads can't both claim the same free space.
	e.addMu.Lock()
	defer e.addMu.Unlock()

	// Meta and data paths are keyed by owner + info hash, so a re-add would write
	// over an existing torrent's files. Reject it up front instead.
	exists, err := e.db.TorrentExists(ctx, ownerID, infoHash)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("%q is already added", info.Name)
	}

	if err := e.makeRoom(ctx, ownerID, info.TotalLength(), info.Name); err != nil {
		return nil, err
	}

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
		// Keep the meta file if a concurrent add already claimed this info hash.
		if dup, checkErr := e.db.TorrentExists(ctx, ownerID, infoHash); checkErr != nil || !dup {
			_ = os.Remove(metaFile)
		}
		return nil, err
	}

	if err := e.db.SetTorrentTrackers(ctx, rec.ID, trackerURLs); err != nil {
		_ = e.Delete(ctx, rec.ID, true)
		return nil, err
	}

	tt, err := e.addFromMetaFile(metaFile, dataDir)
	if err != nil {
		_ = e.db.UpdateTorrentStatus(ctx, rec.ID, db.StatusError, err.Error(), nil)
		return nil, err
	}
	tt.DownloadAll()

	e.mu.Lock()
	e.byID[rec.ID] = &liveTorrent{tt: tt}
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
	// Don't announce to blocked trackers on torrents that still have an allowed one.
	if policy, err := e.policy(context.Background()); err == nil {
		spec.Trackers = filterTrackers(spec.Trackers, policy)
	}
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
	lt, ok := e.byID[id]
	if ok {
		delete(e.byID, id)
	}
	e.mu.Unlock()
	if ok {
		e.flush(id, lt)
		lt.tt.Drop()
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
	e.byID[id] = &liveTorrent{tt: tt}
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
	lt, ok := e.byID[id]
	if ok {
		delete(e.byID, id)
	}
	e.mu.Unlock()
	if ok {
		lt.tt.Drop()
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
	lt, ok := e.byID[id]
	if !ok {
		return nil, false
	}
	return lt.tt, true
}

func (e *Engine) view(rec *db.Torrent) *TorrentView {
	v := &TorrentView{Torrent: *rec}
	e.mu.Lock()
	lt, ok := e.byID[rec.ID]
	var flushedUp, flushedDown int64
	if ok {
		flushedUp, flushedDown = lt.flushedUp, lt.flushedDown
	}
	e.mu.Unlock()
	if !ok || lt.tt.Info() == nil {
		v.Stats = LiveStats{
			Downloaded: rec.DownloadedBytes,
			Uploaded:   rec.UploadedBytes,
		}
		if rec.Status == db.StatusSeeding || rec.CompletedAt != nil {
			v.Stats.Complete = true
			v.Stats.Progress = 1
		}
		return v
	}
	tt := lt.tt
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
		Downloaded:     rec.DownloadedBytes + stats.BytesReadData.Int64() - flushedDown,
		Uploaded:       rec.UploadedBytes + stats.BytesWrittenData.Int64() - flushedUp,
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
