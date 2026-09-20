package version

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	cacheDirMu sync.RWMutex
	cacheDir   string
)

// SetCacheDir registers the directory where the cache file lives. Called
// once during app.Build with cfg.DataDir. Empty value disables on-disk
// caching (writes become no-ops; loads return immediately).
func SetCacheDir(dir string) {
	cacheDirMu.Lock()
	cacheDir = dir
	cacheDirMu.Unlock()
}

func getCacheDir() string {
	cacheDirMu.RLock()
	defer cacheDirMu.RUnlock()
	return cacheDir
}

// latestXUICacheFile is the on-disk cache for the global 3X-UI latest
// release tag. A separate file from the policy snapshot because the tag is
// PSP-version-independent — it only depends on what has been published upstream
// — so the applicability check that gates the snapshot across PSP majors would
// needlessly discard this one too.
const latestXUICacheFile = "latest-xui-cache.json"

const latestSUICacheFile = "latest-sui-cache.json"

type latestXUICachePayload struct {
	Tag      string    `json:"tag"`
	CachedAt time.Time `json:"cached_at"`
}

// LoadLatestXUICache reads the on-disk latest-XUI cache (if any) and
// installs the cached tag via SetLatestXUI. Boot path calls this BEFORE
// any RefreshLatestXUI so Passwall Panel starts with the last-known tag
// even when offline (admin can still see the "update available" badge
// based on the cached snapshot, rather than waiting for the first
// network round-trip). Missing file is not an error.
func LoadLatestXUICache() error {
	dir := getCacheDir()
	if dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, latestXUICacheFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read latest-xui cache: %w", err)
	}
	var p latestXUICachePayload
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("decode latest-xui cache: %w", err)
	}
	if _, ok := parseSemver(p.Tag); !ok {
		return fmt.Errorf("cached 3X-UI tag %q is unparseable", p.Tag)
	}
	SetLatestXUI(p.Tag)
	return nil
}

// saveLatestXUICache persists the just-fetched tag to disk. Same
// atomic-temp-rename idiom as storePolicySnapshot so a concurrent boot
// loader never sees a half-written file.
func saveLatestXUICache(tag string) error {
	dir := getCacheDir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure cache dir: %w", err)
	}
	payload := latestXUICachePayload{Tag: tag, CachedAt: time.Now()}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	target := filepath.Join(dir, latestXUICacheFile)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write tmp cache: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename cache: %w", err)
	}
	return nil
}

// LoadLatestSUICache restores the optional stable S-UI snapshot before any
// background fetch. Like the upstream 3X-UI tag, it is PSP-major-independent.
func LoadLatestSUICache() error {
	dir := getCacheDir()
	if dir == "" {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(dir, latestSUICacheFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read latest-sui cache: %w", err)
	}
	var payload latestXUICachePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode latest-sui cache: %w", err)
	}
	tag, ok := acceptLatestPSPStable(payload.Tag, false)
	if !ok {
		return fmt.Errorf("cached S-UI tag %q is not a usable stable version", payload.Tag)
	}
	SetLatestSUI(tag)
	return nil
}

func saveLatestSUICache(tag string) error {
	dir := getCacheDir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure latest-sui cache dir: %w", err)
	}
	body, err := json.MarshalIndent(latestXUICachePayload{Tag: tag, CachedAt: time.Now()}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode latest-sui cache: %w", err)
	}
	target := filepath.Join(dir, latestSUICacheFile)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write latest-sui cache: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename latest-sui cache: %w", err)
	}
	return nil
}
