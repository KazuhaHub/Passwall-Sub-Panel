package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// latestPSPURL is the GitHub release-latest endpoint for PSP's OWN repo. GitHub's
// /releases/latest returns the most recent NON-prerelease, NON-draft release — so
// this is the latest STABLE PSP version by construction; beta / rc / pre-release
// tags are never returned. That's deliberate: the self-update nudge only ever
// points at a stable release (the admin opted into betas manually; we don't push
// them to a newer beta). Reuses the SSRF-guarded httpClient from latest_xui.go.
const latestPSPURL = "https://api.github.com/repos/KazuhaHub/passwall-sub-panel/releases/latest"

// latestPSPThrottle mirrors the 3X-UI cadence: a new-stable badge can lag half an
// hour, and the loose interval keeps PSP well under GitHub's anonymous rate limit.
const latestPSPThrottle = 30 * time.Minute

const latestPSPFetchTimeout = 8 * time.Second

var (
	latestPSPTag      atomic.Value // string; "" until first successful fetch
	latestPSPFetchMu  sync.Mutex
	latestPSPLastAt   time.Time
	latestPSPInflight bool
	latestPSPLastErr  error
)

// LatestPSP returns the most recently observed latest STABLE PSP release tag
// (e.g. "v3.7.0"). Empty until a fetch lands; callers treat empty as "unknown"
// and show no update nudge.
func LatestPSP() string {
	if v, ok := latestPSPTag.Load().(string); ok {
		return v
	}
	return ""
}

// SetLatestPSP installs a tag string (test hook / future cache loader).
func SetLatestPSP(tag string) { latestPSPTag.Store(tag) }

// IsPrerelease reports whether a PSP version string is a pre-release build
// (carries a "-beta"/"-rc"/... suffix). Drives the UI channel indicator
// (stable = green, pre-release = yellow) and the self-update comparison below.
func IsPrerelease(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v = strings.TrimPrefix(v, "V")
	if i := strings.IndexByte(v, '+'); i >= 0 { // drop build metadata first
		v = v[:i]
	}
	return strings.IndexByte(v, '-') >= 0
}

// IsPSPUpdateAvailable reports whether THIS build is behind the latest stable
// release. Prerelease-aware (parseSemver alone drops the suffix, so a beta would
// otherwise compare EQUAL to its stable): same base version + this build is a
// pre-release ⇒ behind (semver: 3.7.0-beta.16 < 3.7.0). Returns false for "dev"
// / unparseable / no-latest-yet so the nudge only fires when we're confident.
func IsPSPUpdateAvailable() bool {
	return pspBehindStable(Version, LatestPSP())
}

func pspBehindStable(current, latestStable string) bool {
	if current == "" || latestStable == "" {
		return false
	}
	cur, ok1 := parseSemver(current)
	lat, ok2 := parseSemver(latestStable)
	if !ok1 || !ok2 {
		return false
	}
	switch cmpSemver(cur, lat) {
	case -1:
		return true // older base release
	case 1:
		return false // ahead of the latest stable
	default:
		// Same base version: behind only when THIS build is a pre-release and the
		// target is a stable (the common "running v3.7.0-beta.N, v3.7.0 shipped").
		return IsPrerelease(current) && !IsPrerelease(latestStable)
	}
}

// RefreshLatestPSP fetches the latest stable PSP release tag from GitHub and
// installs it. Throttled + single-flight, same shape as RefreshLatestXUI.
func RefreshLatestPSP(ctx context.Context) error {
	latestPSPFetchMu.Lock()
	if latestPSPInflight {
		latestPSPFetchMu.Unlock()
		return nil
	}
	if !latestPSPLastAt.IsZero() && time.Since(latestPSPLastAt) < latestPSPThrottle {
		latestPSPFetchMu.Unlock()
		return nil
	}
	latestPSPInflight = true
	latestPSPFetchMu.Unlock()

	err := fetchLatestPSP(ctx)

	latestPSPFetchMu.Lock()
	latestPSPInflight = false
	latestPSPLastErr = err
	if err == nil {
		latestPSPLastAt = time.Now()
	}
	latestPSPFetchMu.Unlock()
	return err
}

func fetchLatestPSP(ctx context.Context) error {
	fetchCtx, cancel := context.WithTimeout(ctx, latestPSPFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, latestPSPURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch latest PSP release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// No published stable release yet (repo only has tags / pre-releases).
		// Not an error worth surfacing — just leave LatestPSP() empty (no nudge).
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch latest PSP release: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	var release struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return fmt.Errorf("decode release JSON: %w", err)
	}
	// Stable-only, defended two ways: the GitHub `prerelease` flag AND the tag
	// string itself (a "-beta"/"-rc" suffix). The tag check is the load-bearing
	// one — if a release was published WITHOUT the prerelease flag set (e.g. an
	// older beta cut before the workflow marked pre-releases), /releases/latest
	// could still hand us a beta tag; we must never treat that as a stable.
	if release.TagName == "" || release.Prerelease || IsPrerelease(release.TagName) {
		return nil
	}
	if _, ok := parseSemver(release.TagName); !ok {
		return fmt.Errorf("unparseable tag_name %q", release.TagName)
	}
	SetLatestPSP(release.TagName)
	return nil
}
