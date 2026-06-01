// Package geo resolves IPs to display regions for the access logs using a
// local .mmdb database — fully offline, no per-IP external calls, no cache
// table (the memory-mapped DB is the lookup). Exactly ONE database is active at
// a time (no merging of sources, so two databases can never "conflict"); when
// several .mmdb files are present the admin picks the active one via the
// geo_ip_db_file setting (otherwise the first by name is used). The active file
// is hot-reloaded when it changes on disk (e.g. after an auto-update), so a new
// or refreshed database takes effect without a restart.
package geo

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/geoip"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Service owns the active mmdb reader and resolves IPs against it.
type Service struct {
	settings ports.SettingsRepo
	dir      string // <ConfigDir>/geoip

	mu         sync.RWMutex
	reader     *geoip.Reader
	activePath string
	activeMod  time.Time

	// Async-update state, guarded by upMu (NOT mu — Update takes mu during the
	// file swap, and the admin polls UpdateState while that runs). A manual
	// "update now" and the 12h auto-update both go through StartUpdate, so only
	// one download runs at a time and they can't race on the .part temp file.
	upMu     sync.Mutex
	updating bool
	lastErr  string    // last completed update's error ("" on success)
	lastFile string    // last successfully written database filename
	lastAt   time.Time // when the last update completed (zero = never run)
}

// New creates the service and ensures the geoip dir exists so admins have a
// place to drop a .mmdb (or the auto-updater can write one).
func New(settings ports.SettingsRepo, configDir string) *Service {
	s := &Service{settings: settings, dir: filepath.Join(configDir, "geoip")}
	_ = os.MkdirAll(s.dir, 0o755)
	return s
}

// Dir returns the directory where .mmdb files live.
func (s *Service) Dir() string { return s.dir }

// Lookup resolves the given IPs against the active database. Returns an empty
// (non-nil) map when the feature is disabled or no database is loaded.
func (s *Service) Lookup(ctx context.Context, ips []string) map[string]domain.GeoLocation {
	out := map[string]domain.GeoLocation{}
	if s == nil {
		return out
	}
	set, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil || !set.GeoIPEnabled {
		return out
	}
	r := s.ensureReader(set.GeoIPDBFile)
	if r == nil {
		return out
	}
	seen := make(map[string]bool, len(ips))
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" || seen[ip] || !geoip.IsResolvable(ip) {
			continue
		}
		seen[ip] = true
		if loc, lerr := r.Lookup(ip); lerr == nil && !loc.Empty() {
			out[ip] = loc
		}
	}
	return out
}

// listMMDB returns the *.mmdb filenames in the geoip dir, sorted.
func (s *Service) listMMDB() []string {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".mmdb") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// resolveActivePath returns the absolute path of the active database, honoring
// the admin's chosen file when it exists, else the first .mmdb by name. Empty
// when none is present. filepath.Base on the chosen name blocks path traversal.
func (s *Service) resolveActivePath(chosen string) string {
	chosen = strings.TrimSpace(chosen)
	if chosen != "" {
		p := filepath.Join(s.dir, filepath.Base(chosen))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if files := s.listMMDB(); len(files) > 0 {
		return filepath.Join(s.dir, files[0])
	}
	return ""
}

// ensureReader returns a reader for the active database, (re)opening it when the
// active file or its mtime changed since last load (hot-reload).
func (s *Service) ensureReader(chosen string) *geoip.Reader {
	path := s.resolveActivePath(chosen)
	if path == "" {
		s.mu.Lock()
		if s.reader != nil {
			_ = s.reader.Close()
			s.reader, s.activePath, s.activeMod = nil, "", time.Time{}
		}
		s.mu.Unlock()
		return nil
	}
	var mod time.Time
	if fi, err := os.Stat(path); err == nil {
		mod = fi.ModTime()
	}
	s.mu.RLock()
	if s.reader != nil && s.activePath == path && s.activeMod.Equal(mod) {
		r := s.reader
		s.mu.RUnlock()
		return r
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activePath == path && s.activeMod.Equal(mod) {
		// Either the current good reader, or a previously-failed attempt at this
		// exact (path, mtime): don't re-Open until the file actually changes.
		return s.reader
	}
	nr, err := geoip.Open(path)
	if err != nil {
		log.Warn("geo: open mmdb failed", "path", path, "err", err)
		// Record the attempt so subsequent Lookups short-circuit above instead
		// of re-Opening and re-logging on every call — retry only when the
		// file's mtime changes or the admin picks another. Drop any stale
		// reader: serving a different database's data would mislead.
		if s.reader != nil {
			_ = s.reader.Close()
		}
		s.reader, s.activePath, s.activeMod = nil, path, mod
		return nil
	}
	if s.reader != nil {
		_ = s.reader.Close()
	}
	s.reader, s.activePath, s.activeMod = nr, path, mod
	log.Info("geo: loaded mmdb", "file", filepath.Base(path), "type", nr.Info().Type, "granularity", nr.Info().Granularity)
	return s.reader
}

// reload forces the active reader to be (re)opened on the next ensureReader by
// clearing the cached mtime. Called after an auto-update replaces the file.
func (s *Service) reload() {
	s.mu.Lock()
	s.activeMod = time.Time{}
	s.mu.Unlock()
}

// ---- status (admin UI) ----

// DBStatus describes one .mmdb file found in the geoip dir.
type DBStatus struct {
	File        string `json:"file"`
	Type        string `json:"type"`        // Metadata.DatabaseType
	Granularity string `json:"granularity"` // "city" | "country"
	BuildEpoch  int64  `json:"build_epoch"` // Unix seconds the DB was built
	Active      bool   `json:"active"`
	Error       string `json:"error,omitempty"` // set if the file failed to open
}

// Status reports the geo config + every database present, for the admin view.
type Status struct {
	Enabled   bool        `json:"enabled"`
	Dir       string      `json:"dir"`
	Active    string      `json:"active"` // active filename, "" if none
	Available []DBStatus  `json:"available"`
	Update    UpdateState `json:"update"` // background-update progress + last result
}

// Status scans the geoip dir and reports each database's metadata + which is
// active. Opens each file only briefly to read metadata.
func (s *Service) Status(ctx context.Context) Status {
	set, _ := s.settings.Load(ctx, ports.UISettings{})
	activePath := s.resolveActivePath(set.GeoIPDBFile)
	activeName := ""
	if activePath != "" {
		activeName = filepath.Base(activePath)
	}
	st := Status{Enabled: set.GeoIPEnabled, Dir: s.dir, Active: activeName, Available: []DBStatus{}, Update: s.UpdateState()}
	for _, name := range s.listMMDB() {
		row := DBStatus{File: name, Active: name == activeName}
		if r, err := geoip.Open(filepath.Join(s.dir, name)); err == nil {
			info := r.Info()
			row.Type, row.Granularity, row.BuildEpoch = info.Type, info.Granularity, int64(info.BuildEpoch)
			_ = r.Close()
		} else {
			row.Error = err.Error()
		}
		st.Available = append(st.Available, row)
	}
	return st
}
