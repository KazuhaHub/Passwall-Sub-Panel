package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// latestXUIURL is the GitHub release-latest endpoint Passwall Panel queries
// to learn the most recent 3X-UI tag. Centralized here so the "update
// available" indicator does NOT fan out one fetch per panel — one
// PSP-wide call drives every server row's badge. /releases/latest
// transparently follows GitHub's "latest non-prerelease" semantic.
const latestXUIURL = "https://api.github.com/repos/MHSanaei/3x-ui/releases/latest"

// latestXUIThrottle is intentionally much longer than the compat-JSON
// throttle (60 s): new 3X-UI releases ship roughly weekly, the "new
// version available" badge can comfortably lag by half an hour, and the
// looser cadence keeps us well under GitHub's anonymous 60/hour rate
// limit even with admin page-refresh churn.
const latestXUIThrottle = 30 * time.Minute

// latestXUIFetchTimeout caps each fetch independently of the compat
// fetch path (they share http.DefaultClient but otherwise have
// separate cancellation lifetimes).
const latestXUIFetchTimeout = 8 * time.Second

var (
	latestXUITag      atomic.Value // string; "" until cache load or first fetch lands
	latestXUIFetchMu  sync.Mutex
	latestXUILastAt   time.Time
	latestXUIInflight bool
	latestXUILastErr  error
)

// LatestXUI returns the most recently observed 3X-UI release tag (form
// matches what GitHub returns — usually "v3.1.0"). Empty string when
// neither cache nor live fetch has produced a value yet; DTO/UI callers
// treat empty as "update_available unknown" and hide the badge.
func LatestXUI() string {
	if v, ok := latestXUITag.Load().(string); ok {
		return v
	}
	return ""
}

// SetLatestXUI installs a tag string. Exposed for the on-disk cache
// loader so the value is available immediately at boot before any
// network fetch runs.
func SetLatestXUI(tag string) {
	latestXUITag.Store(tag)
}

// IsXUIUpdateAvailable reports whether LatestXUI() is strictly newer than
// panelVersion. Returns false for unparseable inputs and for the "no
// latest yet" case — the badge only fires when we're confident, since a
// false positive would push admin into a needless 3X-UI restart.
func IsXUIUpdateAvailable(panelVersion string) bool {
	latest := LatestXUI()
	if latest == "" || panelVersion == "" {
		return false
	}
	pv, ok := parseSemver(panelVersion)
	if !ok {
		return false
	}
	lv, ok := parseSemver(latest)
	if !ok {
		return false
	}
	return cmpSemver(lv, pv) > 0
}

// RefreshLatestXUI fetches the latest 3X-UI release tag from GitHub and
// installs it via SetLatestXUI. Throttled (latestXUIThrottle) AND
// single-flight: N concurrent callers (Servers-page open firing
// per-panel Test) collapse to at most one network call across the
// throttle window. Returns nil when short-circuited; only the call that
// actually attempted the fetch returns a non-nil error.
func RefreshLatestXUI(ctx context.Context) error {
	latestXUIFetchMu.Lock()
	if latestXUIInflight {
		latestXUIFetchMu.Unlock()
		return nil
	}
	if !latestXUILastAt.IsZero() && time.Since(latestXUILastAt) < latestXUIThrottle {
		latestXUIFetchMu.Unlock()
		return nil
	}
	latestXUIInflight = true
	latestXUIFetchMu.Unlock()

	err := fetchLatestXUI(ctx)

	latestXUIFetchMu.Lock()
	latestXUIInflight = false
	latestXUILastErr = err
	if err == nil {
		latestXUILastAt = time.Now()
	}
	latestXUIFetchMu.Unlock()
	return err
}

func fetchLatestXUI(ctx context.Context) error {
	fetchCtx, cancel := context.WithTimeout(ctx, latestXUIFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, latestXUIURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch latest 3X-UI release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch latest 3X-UI release: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return fmt.Errorf("decode release JSON: %w", err)
	}
	if release.TagName == "" {
		return fmt.Errorf("empty tag_name in release JSON")
	}
	if _, ok := parseSemver(release.TagName); !ok {
		return fmt.Errorf("unparseable tag_name %q", release.TagName)
	}
	SetLatestXUI(release.TagName)
	_ = saveLatestXUICache(release.TagName)
	return nil
}

// LatestXUIRefreshAt returns the wall clock of the most recent successful
// fetch; zero value when never succeeded. Exposed for the UI / debug
// surfaces that want to display "checked X minutes ago".
func LatestXUIRefreshAt() time.Time {
	latestXUIFetchMu.Lock()
	defer latestXUIFetchMu.Unlock()
	return latestXUILastAt
}

// LatestXUIRefreshError returns the most recent fetch error; nil on
// success or when never attempted. Surfaces to log / admin status pages.
func LatestXUIRefreshError() error {
	latestXUIFetchMu.Lock()
	defer latestXUIFetchMu.Unlock()
	return latestXUILastErr
}
