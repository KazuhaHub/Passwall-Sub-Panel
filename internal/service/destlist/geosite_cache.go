package destlist

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"golang.org/x/sync/singleflight"
)

const geositeURL = "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat_plain.yml"
const maxChecksumBytes = 4 << 10

// The catalog is immutable after publication. Readers never wait on network or
// filesystem I/O; a failed replacement retains the prior catalog and timestamp.
type GeositeCache struct {
	fetcher   *Fetcher
	path      string
	flights   singleflight.Group
	mu        sync.RWMutex
	catalog   *Catalog
	updatedAt time.Time
	lastError string
}

func NewGeositeCache(dataDir string) *GeositeCache {
	c := &GeositeCache{fetcher: NewFetcher(), path: filepath.Join(dataDir, "destlists", "dlc_plain.yml")}
	file, err := os.Open(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c
	}
	if err != nil {
		c.lastError = "dest_geosite_cache_read_failed"
		return c
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, MaxRemoteBytes+1))
	if err != nil {
		c.lastError = "dest_geosite_cache_read_failed"
		return c
	}
	catalog, err := parseCatalog(raw)
	if err != nil {
		c.lastError = "dest_geosite_cache_invalid"
		return c
	}
	info, err := file.Stat()
	if err != nil {
		c.lastError = "dest_geosite_cache_read_failed"
		return c
	}
	c.catalog, c.updatedAt = catalog, info.ModTime().UTC()
	return c
}

func (c *GeositeCache) Cached() (*Catalog, time.Time, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.catalog == nil {
		return nil, time.Time{}, domain.ErrUnavailable
	}
	return c.catalog, c.updatedAt, nil
}
func (c *GeositeCache) LastError() string { c.mu.RLock(); defer c.mu.RUnlock(); return c.lastError }

func (c *GeositeCache) Refresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result := c.beginRefresh(ctx)
	select {
	case <-ctx.Done():
		// A caller owning a tracked refresh must not drain while download or
		// disk-cache cleanup continues in singleflight's goroutine.
		<-result
		return ctx.Err()
	case outcome := <-result:
		return outcome.Err
	}
}

func (c *GeositeCache) beginRefresh(ctx context.Context) <-chan singleflight.Result {
	return c.flights.DoChan("catalog", func() (any, error) {
		err := c.refresh(ctx)
		if err != nil {
			c.mu.Lock()
			c.lastError = err.Error()
			c.mu.Unlock()
		}
		return nil, err
	})
}

func (c *GeositeCache) refresh(ctx context.Context) error {
	raw, _, err := c.fetcher.download(ctx, geositeURL, MaxRemoteBytes)
	if err != nil {
		return err
	}
	checksum, _, err := c.fetcher.download(ctx, geositeURL+".sha256sum", maxChecksumBytes)
	if err != nil {
		return err
	}
	catalog, err := ParseGeosite(raw, checksum)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeGeositeCache(c.path, raw); err != nil {
		return &FetchError{Reason: "cache_write_failed"}
	}
	c.mu.Lock()
	c.catalog, c.updatedAt, c.lastError = catalog, time.Now().UTC(), ""
	c.mu.Unlock()
	return nil
}

func writeGeositeCache(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".dlc-*.tmp")
	if err != nil {
		return err
	}
	// os.CreateTemp uses 0600. The temporary file is always on the same volume.
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
