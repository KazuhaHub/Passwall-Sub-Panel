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

// One official stable-release snapshot feeds every S-UI row. This is an
// availability hint, not a compatibility claim or remote-upgrade capability.
// Use the shared SSRF-guarded httpClient and the same bounded, low-frequency
// GitHub cadence as the existing 3X-UI release probe.
const latestSUIURL = "https://api.github.com/repos/alireza0/s-ui/releases/latest"
const latestSUIThrottle = 30 * time.Minute
const latestSUIFetchTimeout = 8 * time.Second

var (
	latestSUITag        atomic.Value // string; empty until cache load or a valid fetch
	latestSUIFetchMu    sync.Mutex
	latestSUILastAt     time.Time
	latestSUILastTryAt  time.Time
	latestSUIInflight   bool
	latestSUIFlightDone chan struct{}
	latestSUILastErr    error
)

func LatestSUI() string {
	if tag, ok := latestSUITag.Load().(string); ok {
		return tag
	}
	return ""
}

// SetLatestSUI is also used by the optional on-disk cache loader at boot.
func SetLatestSUI(tag string) { latestSUITag.Store(tag) }

// IsSUIUpdateAvailable compares an observed panel version against a stable
// release, including a beta being behind its own stable. Empty/dev/unknown
// observations never produce an upgrade hint; being different is not enough.
func IsSUIUpdateAvailable(panelVersion string) bool {
	latest, ok := acceptLatestPSPStable(LatestSUI(), false)
	return ok && pspBehindStable(panelVersion, latest)
}

// RefreshLatestSUI is single-flight and throttles failed attempts too: opening
// Servers during a GitHub outage must not turn every page poll into a retry.
// A failed fetch never erases the useful last-known-good tag/cache.
func RefreshLatestSUI(ctx context.Context) error {
	latestSUIFetchMu.Lock()
	if latestSUIInflight || (!latestSUILastTryAt.IsZero() && time.Since(latestSUILastTryAt) < latestSUIThrottle) {
		latestSUIFetchMu.Unlock()
		return nil
	}
	latestSUIInflight = true
	latestSUIFlightDone = make(chan struct{})
	latestSUILastTryAt = time.Now()
	latestSUIFetchMu.Unlock()

	err := fetchLatestSUI(ctx)

	latestSUIFetchMu.Lock()
	latestSUIInflight = false
	latestSUILastErr = err
	if err == nil {
		latestSUILastAt = time.Now()
	}
	close(latestSUIFlightDone)
	latestSUIFlightDone = nil
	latestSUIFetchMu.Unlock()
	return err
}

// LatestSUIRelease supplies a usable stable tag for the standalone metadata
// request. Cached tags can be returned immediately; List/Test arrange optional
// background refreshes separately. A cold request starts a refresh or waits for
// the existing flight, without cancelling another request's fetch when its own
// context ends. No request waits longer than the normal eight-second budget.
func LatestSUIRelease(ctx context.Context) (string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, latestSUIFetchTimeout)
	defer cancel()
	if err := requestCtx.Err(); err != nil {
		return "", err
	}
	if tag, ok := acceptLatestPSPStable(LatestSUI(), false); ok {
		return tag, nil
	}
	if err := RefreshLatestSUI(requestCtx); err != nil {
		return "", err
	}
	latestSUIFetchMu.Lock()
	done := latestSUIFlightDone
	latestSUIFetchMu.Unlock()
	if done != nil {
		select {
		case <-requestCtx.Done():
			return "", requestCtx.Err()
		case <-done:
		}
	}
	if err := requestCtx.Err(); err != nil {
		return "", err
	}
	if tag, ok := acceptLatestPSPStable(LatestSUI(), false); ok {
		return tag, nil
	}
	if err := LatestSUIRefreshError(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("latest S-UI release metadata is unavailable")
}

func fetchLatestSUI(ctx context.Context) error {
	fetchCtx, cancel := context.WithTimeout(ctx, latestSUIFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, latestSUIURL, nil)
	if err != nil {
		return fmt.Errorf("build latest S-UI request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch latest S-UI release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch latest S-UI release: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("read latest S-UI release: %w", err)
	}
	if len(body) > 1<<20 {
		return fmt.Errorf("latest S-UI release exceeds size limit")
	}
	var release struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return fmt.Errorf("decode latest S-UI release: %w", err)
	}
	// Defend the stable-only promise against accidental prerelease/draft
	// payloads and beta tags published without their GitHub prerelease flag.
	tag, ok := acceptLatestPSPStable(release.TagName, release.Prerelease || release.Draft)
	if !ok {
		return fmt.Errorf("latest S-UI release has no usable stable tag")
	}
	SetLatestSUI(tag)
	_ = saveLatestSUICache(tag)
	return nil
}

func LatestSUIRefreshAt() time.Time {
	latestSUIFetchMu.Lock()
	defer latestSUIFetchMu.Unlock()
	return latestSUILastAt
}

func LatestSUIRefreshError() error {
	latestSUIFetchMu.Lock()
	defer latestSUIFetchMu.Unlock()
	return latestSUILastErr
}
