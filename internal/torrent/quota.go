package torrent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lerenn/seedly/internal/db"
	"github.com/lerenn/seedly/internal/disk"
)

// Quota is the best-effort disk budget for Seedly's data directory.
// A zero Max disables eviction.
type Quota struct {
	Max    int64
	Buffer int64
}

func (e *Engine) quota(ctx context.Context) (Quota, error) {
	max, err := e.db.GetInt64Setting(ctx, db.SettingQuotaBytes)
	if err != nil {
		return Quota{}, err
	}
	buf, err := e.db.GetInt64Setting(ctx, db.SettingBufferBytes)
	if err != nil {
		return Quota{}, err
	}
	return Quota{Max: max, Buffer: buf}, nil
}

// UsedBytes is the size of Seedly's data directory (downloads and metadata).
func (e *Engine) UsedBytes() (int64, error) {
	n, err := disk.SeedlyBytes(e.downloadsPath, e.metaPath)
	return int64(n), err
}

type usageCandidate struct {
	id    int64
	owner int64
	size  int64
	added int64
}

// makeRoom evicts torrents until incoming bytes fit below Max-Buffer. Each
// round removes the oldest completed torrent of the user holding the most
// space. Nothing is removed when the torrent cannot fit even after evicting
// every candidate.
func (e *Engine) makeRoom(ctx context.Context, ownerID, incoming int64, name string) error {
	q, err := e.quota(ctx)
	if err != nil {
		return err
	}
	if q.Max <= 0 {
		return nil
	}
	if incoming > q.Max {
		return fmt.Errorf("%q (%d bytes) is too large for this seedbox: quota is %d bytes", name, incoming, q.Max)
	}
	target := q.Max - q.Buffer
	if target < 0 {
		target = 0
	}

	all, err := e.db.ListAllTorrents(ctx)
	if err != nil {
		return err
	}
	var used int64
	perUser := map[int64]int64{ownerID: incoming}
	byUser := map[int64][]usageCandidate{}
	for _, t := range all {
		size := dirBytes(t.DataPath)
		used += size
		perUser[t.OwnerID] += size
		if t.Status == db.StatusSeeding || (t.CompletedAt != nil && t.Status != db.StatusError) {
			byUser[t.OwnerID] = append(byUser[t.OwnerID], usageCandidate{
				id: t.ID, owner: t.OwnerID, size: size, added: t.CreatedAt.UnixNano(),
			})
		}
	}
	// Metadata files count toward the data directory too.
	if meta, err := dirSizeOf(e.metaPath); err == nil {
		used += meta
	}

	projected := used + incoming
	if projected <= target {
		return nil
	}

	for _, list := range byUser {
		sort.Slice(list, func(i, j int) bool { return list[i].added < list[j].added })
	}

	var victims []usageCandidate
	for projected > target {
		best := int64(-1)
		for owner, list := range byUser {
			if len(list) == 0 {
				continue
			}
			if best == -1 || perUser[owner] > perUser[best] || (perUser[owner] == perUser[best] && owner < best) {
				best = owner
			}
		}
		if best == -1 {
			break
		}
		v := byUser[best][0]
		byUser[best] = byUser[best][1:]
		victims = append(victims, v)
		projected -= v.size
		perUser[best] -= v.size
	}

	if projected > q.Max {
		return fmt.Errorf("%q (%d bytes) is too large for this seedbox: no more torrents can be removed to make room within the %d byte quota", name, incoming, q.Max)
	}
	for _, v := range victims {
		if err := e.Delete(ctx, v.id, true); err != nil {
			return fmt.Errorf("evict torrent %d: %w", v.id, err)
		}
	}
	return nil
}

func dirBytes(path string) int64 {
	n, _ := dirSizeOf(path)
	return n
}

func dirSizeOf(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}
