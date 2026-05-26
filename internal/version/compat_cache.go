package version

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// compatCacheFile is the on-disk snapshot of the most recently successful
// remote-compat fetch. PSP boots straight off this file so a network blip
// (GitHub raw unreachable, container starting before egress is wired up)
// doesn't leave admin staring at "compat: unknown" until the first manual
// Test click. The file is written by RefreshRemoteCompat on success and
// read by LoadCompatCache at boot.
//
// Filename and location: <DataDir>/compat-cache.json — same DataDir that
// holds panel.db, so backup/restore semantics line up.
const compatCacheFile = "compat-cache.json"

type compatCachePayload struct {
	MaxTestedXUI string    `json:"max_tested_xui"`
	CachedAt     time.Time `json:"cached_at"`
	PSPVersion   string    `json:"psp_version"` // helps detect "cache from a different PSP build"
}

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

// LoadCompatCache reads the on-disk cache (if any) and installs the
// cached max_tested_xui into the active state. Boot path calls this
// BEFORE any RefreshRemoteCompat so PSP starts with the last-known-
// good range even when offline. Missing file is not an error.
//
// PSP-version mismatch (cache from a different build) → ignore the
// cached value. Hard requirement: a PSP v3.7 release with a different
// MinXUI shouldn't run with v3.6's cached compat data.
func LoadCompatCache() error {
	dir := getCacheDir()
	if dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, compatCacheFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh install / first run after upgrade
		}
		return fmt.Errorf("read compat cache: %w", err)
	}
	var p compatCachePayload
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("decode compat cache: %w", err)
	}
	if p.PSPVersion != Version {
		// Cache from a different PSP build — don't trust the cached
		// range. RefreshRemoteCompat will replace it on first call.
		return fmt.Errorf("cache PSP version %q != current %q (ignored)", p.PSPVersion, Version)
	}
	if _, ok := parseSemver(p.MaxTestedXUI); !ok {
		return fmt.Errorf("cached max_tested_xui %q is unparseable", p.MaxTestedXUI)
	}
	SetActiveMaxTestedXUI(p.MaxTestedXUI)
	return nil
}

// latestXUICacheFile is the on-disk cache for the global 3X-UI latest
// release tag. Separate file from compat-cache.json because the tag is
// PSP-version-independent — it only depends on what's been published
// upstream — so the PSP-version mismatch check that invalidates
// compat-cache across PSP upgrades would needlessly nuke this one too.
const latestXUICacheFile = "latest-xui-cache.json"

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
// atomic-temp-rename idiom as saveCompatCache so a concurrent boot
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

// saveCompatCache writes the just-fetched range to disk. Atomic via
// temp+rename so a concurrent reader never sees a half-written file. No-op
// when no cache dir is registered. Called from fetchAndApply on success.
func saveCompatCache(maxTestedXUI string) error {
	dir := getCacheDir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure cache dir: %w", err)
	}
	payload := compatCachePayload{
		MaxTestedXUI: maxTestedXUI,
		CachedAt:     time.Now(),
		PSPVersion:   Version,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	target := filepath.Join(dir, compatCacheFile)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write tmp cache: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup; the rename failure is the real signal
		return fmt.Errorf("rename cache: %w", err)
	}
	return nil
}
