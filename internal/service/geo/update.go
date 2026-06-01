package geo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/geoip"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const (
	geoDownloadTimeout  = 3 * time.Minute
	geoMaxDownloadBytes = 256 << 20 // cap; GeoLite2-City .mmdb is ~70 MiB
)

// UpdateState reports the background updater's status for the admin UI: whether
// a download is in flight and the result of the last completed run.
type UpdateState struct {
	Updating bool   `json:"updating"`
	LastErr  string `json:"last_error,omitempty"`
	LastFile string `json:"last_file,omitempty"`
	LastAt   int64  `json:"last_at,omitempty"` // unix seconds of last completion, 0 = never
}

// StartUpdate kicks off a database refresh in the background and returns at
// once, so the HTTP "update now" request doesn't sit through a multi-minute
// download — a reverse proxy in front of the panel would otherwise cut that off
// with a 502. The admin UI polls Status to watch progress and read the result
// (success file or error). Returns an error only if an update is already
// running; both the manual trigger and the 12h auto-update go through here, so
// they serialise and can't race on the .part temp file. The download runs on a
// detached context (not the request's) so finishing the HTTP call can't cancel
// it.
func (s *Service) StartUpdate() error {
	if s == nil {
		return fmt.Errorf("geo service not configured")
	}
	s.upMu.Lock()
	if s.updating {
		s.upMu.Unlock()
		return fmt.Errorf("an update is already running")
	}
	s.updating = true
	s.upMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), geoDownloadTimeout+30*time.Second)
		defer cancel()
		file, err := s.Update(ctx)
		s.upMu.Lock()
		s.updating = false
		s.lastAt = time.Now()
		if err != nil {
			s.lastErr = err.Error()
			log.Warn("geo: update failed", "err", err)
		} else {
			s.lastErr, s.lastFile = "", file
		}
		s.upMu.Unlock()
	}()
	return nil
}

// UpdateState returns a snapshot of the background updater's status.
func (s *Service) UpdateState() UpdateState {
	if s == nil {
		return UpdateState{}
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()
	st := UpdateState{Updating: s.updating, LastErr: s.lastErr, LastFile: s.lastFile}
	if !s.lastAt.IsZero() {
		st.LastAt = s.lastAt.Unix()
	}
	return st
}

// Update downloads the configured source's database, validates it as a real
// .mmdb, atomically replaces the active file in the geoip dir, and hot-reloads.
// The panel only ever downloads a PUBLIC database — no user IPs are involved.
// Required for MaxMind's GeoLite2 30-day-update EULA. Returns the written file.
func (s *Service) Update(ctx context.Context) (string, error) {
	if s == nil {
		return "", fmt.Errorf("geo service not configured")
	}
	set, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return "", err
	}
	urls, target, err := candidateURLs(set)
	if err != nil {
		return "", err
	}

	var raw []byte
	var dlErr error
	for _, u := range urls {
		if raw, dlErr = download(ctx, u); dlErr == nil {
			break
		}
	}
	if dlErr != nil {
		return "", fmt.Errorf("download: %w", dlErr)
	}

	mmdb, err := extractMMDB(raw)
	if err != nil {
		return "", fmt.Errorf("extract: %w", err)
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	tmp := filepath.Join(s.dir, target+".part")
	if err := os.WriteFile(tmp, mmdb, 0o644); err != nil {
		return "", err
	}
	// Validate it parses as a real mmdb BEFORE swapping the live file in, so a
	// truncated/HTML-error download can't replace a working database.
	if r, oerr := geoip.Open(tmp); oerr != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("downloaded file is not a valid .mmdb: %w", oerr)
	} else {
		_ = r.Close()
	}

	final := filepath.Join(s.dir, target)
	// Close the live reader and rename under the lock so no concurrent Lookup
	// holds the target file open during the replace (Windows can't rename over
	// an mmap'd file); clearing activePath forces the next Lookup to reopen.
	s.mu.Lock()
	if s.reader != nil {
		_ = s.reader.Close()
		s.reader = nil
	}
	renameErr := os.Rename(tmp, final)
	s.activePath, s.activeMod = "", time.Time{}
	s.mu.Unlock()
	if renameErr != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("replace: %w", renameErr)
	}
	log.Info("geo: database updated", "source", set.GeoIPUpdateSource, "file", target, "bytes", len(mmdb))
	return target, nil
}

// knownUpdateSources is the canonical set of selectable geo-database download
// sources. candidateURLs implements exactly one branch per entry; the settings
// handler validates against this same set via IsValidUpdateSource, so the API
// whitelist and the downloader can never drift (TestUpdateSourcesNoDrift guards
// that every entry here is actually handled by candidateURLs).
var knownUpdateSources = []string{"maxmind", "dbip", "ipinfo", "custom"}

// IsValidUpdateSource reports whether src is a selectable update source. The
// empty string is accepted and defaults to "maxmind" inside candidateURLs.
func IsValidUpdateSource(src string) bool {
	src = strings.TrimSpace(src)
	if src == "" {
		return true
	}
	for _, s := range knownUpdateSources {
		if s == src {
			return true
		}
	}
	return false
}

// candidateURLs returns the download URL(s) and the target filename for the
// configured source. DB-IP is month-stamped with no stable "latest", so we
// return the current and previous month and try each in order.
func candidateURLs(set ports.UISettings) (urls []string, target string, err error) {
	src := strings.TrimSpace(set.GeoIPUpdateSource)
	if src == "" {
		src = "maxmind"
	}
	token := strings.TrimSpace(set.GeoIPUpdateToken)
	switch src {
	case "maxmind":
		if token == "" {
			return nil, "", fmt.Errorf("maxmind update requires a license key")
		}
		edition := strings.TrimSpace(set.GeoIPUpdateEdition)
		if edition == "" {
			edition = "GeoLite2-City"
		}
		u := "https://download.maxmind.com/app/geoip_download?edition_id=" + url.QueryEscape(edition) +
			"&license_key=" + url.QueryEscape(token) + "&suffix=tar.gz"
		return []string{u}, filepath.Base(edition) + ".mmdb", nil
	case "ipinfo":
		if token == "" {
			return nil, "", fmt.Errorf("ipinfo update requires a token")
		}
		return []string{"https://ipinfo.io/data/ipinfo_lite.mmdb?token=" + url.QueryEscape(token)}, "ipinfo-lite.mmdb", nil
	case "dbip":
		now := time.Now()
		return []string{dbipURL(now), dbipURL(prevMonthOf(now))}, "dbip-city-lite.mmdb", nil
	case "custom":
		u := strings.TrimSpace(set.GeoIPUpdateURL)
		if u == "" {
			return nil, "", fmt.Errorf("custom update requires a download URL")
		}
		return []string{u}, customTarget(u), nil
	default:
		return nil, "", fmt.Errorf("unknown update source %q", src)
	}
}

func dbipURL(t time.Time) string {
	return "https://download.db-ip.com/free/dbip-city-lite-" + t.Format("2006-01") + ".mmdb.gz"
}

// prevMonthOf returns a time in the month before t's. It uses "1st of this
// month minus a day" rather than t.AddDate(0,-1,0): the latter normalises a
// 31st (e.g. 2026-03-31 → 2026-03-03) back into the SAME month, which would
// silently make the DB-IP fallback URL identical to the current month's.
func prevMonthOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, 0, -1)
}

// customTarget derives a .mmdb filename from an arbitrary URL (basename, minus
// any .gz/.tar wrapper). filepath.Base blocks path traversal.
func customTarget(rawURL string) string {
	u := rawURL
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	base := filepath.Base(u)
	base = strings.TrimSuffix(base, ".gz")
	base = strings.TrimSuffix(base, ".tar")
	if !strings.HasSuffix(strings.ToLower(base), ".mmdb") || base == ".mmdb" {
		base = "custom.mmdb"
	}
	return base
}

func download(ctx context.Context, rawURL string) ([]byte, error) {
	hc := safehttp.NewClient(geoDownloadTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, geoMaxDownloadBytes))
}

// extractMMDB unwraps a downloaded payload into raw .mmdb bytes, handling a
// plain .mmdb, a gzip'd .mmdb, or a .tar.gz / .tar containing one (MaxMind).
func extractMMDB(raw []byte) ([]byte, error) {
	data := raw
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b { // gzip magic
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		un, err := io.ReadAll(io.LimitReader(gr, geoMaxDownloadBytes))
		_ = gr.Close()
		if err != nil {
			return nil, err
		}
		data = un
	}
	if mmdb, ok := tarFindMMDB(data); ok { // MaxMind ships .tar.gz
		return mmdb, nil
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty payload")
	}
	return data, nil // plain (or gunzipped) .mmdb
}

// tarFindMMDB returns the first *.mmdb regular file inside a tar archive.
func tarFindMMDB(data []byte) ([]byte, bool) {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, false
		}
		if h.Typeflag == tar.TypeReg && strings.HasSuffix(strings.ToLower(h.Name), ".mmdb") {
			b, err := io.ReadAll(io.LimitReader(tr, geoMaxDownloadBytes))
			if err != nil {
				return nil, false
			}
			return b, true
		}
	}
}
