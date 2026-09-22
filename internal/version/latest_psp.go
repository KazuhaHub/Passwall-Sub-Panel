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

// latestPSPListURL is the FALLBACK, used only when /releases/latest turns out to
// belong to a different major than this build. GitHub's "Latest release" marker
// is ONE repo-wide pointer with no notion of a release line, so once V4 ships a
// stable it owns that pointer and /releases/latest stops answering the question
// a V3 panel is actually asking ("is there a newer V3?"). Scanning the release
// list is the only way left to answer it.
//
// It is a fallback and not the primary source because it is expensive: measured
// on this repo, per_page=100 returns ~1.8 MiB (the API has no field selection,
// so every release drags its full body and asset list along). While /releases/
// latest is still V3's — which it is today — this request is never made.
const latestPSPListURL = "https://api.github.com/repos/KazuhaHub/passwall-sub-panel/releases?per_page=100"

// Response caps. The single-release payload is small; the list is not, and the
// 1 MiB cap that fits the former would silently TRUNCATE the latter into
// unparseable JSON, which is why they are separate numbers rather than one
// shared constant.
const (
	latestPSPBodyLimit     = 1 << 20 // 1 MiB — /releases/latest, one release
	latestPSPListBodyLimit = 8 << 20 // 8 MiB — /releases, up to 100 releases
)

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

// LatestPSPRefreshError returns the last fetch error (nil on success / not yet
// run) and LatestPSPRefreshAt the last successful-fetch time — parity with the
// 3X-UI sibling accessors for ops visibility.
func LatestPSPRefreshError() error {
	latestPSPFetchMu.Lock()
	defer latestPSPFetchMu.Unlock()
	return latestPSPLastErr
}

func LatestPSPRefreshAt() time.Time {
	latestPSPFetchMu.Lock()
	defer latestPSPFetchMu.Unlock()
	return latestPSPLastAt
}

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
	body, err := io.ReadAll(io.LimitReader(resp.Body, latestPSPBodyLimit))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	var release pspReleaseEntry
	if err := json.Unmarshal(body, &release); err != nil {
		return fmt.Errorf("decode release JSON: %w", err)
	}
	if tag, ok := acceptLatestPSPStableForMajor(release.TagName, release.Prerelease, ownMajor()); ok {
		SetLatestPSP(tag)
		return nil
	}
	// /releases/latest exists but is not this line's. Either another major owns
	// the repo-wide marker, or the newest stable is unparseable. Fall back to
	// scanning the list for the newest stable of OUR major; leave LatestPSP()
	// untouched when that finds nothing, so a transient miss cannot erase a
	// previously-known good answer.
	return fetchLatestPSPFromList(fetchCtx)
}

// pspReleaseEntry is the slice of a GitHub release payload this file reads. The
// rest of the object is ignored, which is also why the list fallback is costly:
// the API sends all of it regardless.
type pspReleaseEntry struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
}

// fetchLatestPSPFromList scans /releases for the highest STABLE release sharing
// this build's major and installs it.
//
// Highest by version, not first in the list: GitHub orders releases by creation
// date, and a backported patch cut after a newer minor would sit earlier there
// while being an older version. Position is not order.
func fetchLatestPSPFromList(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestPSPListURL, nil)
	if err != nil {
		return fmt.Errorf("build list request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch PSP release list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch PSP release list: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, latestPSPListBodyLimit))
	if err != nil {
		return fmt.Errorf("read list body: %w", err)
	}
	var releases []pspReleaseEntry
	if err := json.Unmarshal(body, &releases); err != nil {
		return fmt.Errorf("decode release list JSON: %w", err)
	}
	if tag, ok := pickLatestStableForMajor(releases, ownMajor()); ok {
		SetLatestPSP(tag)
	}
	return nil
}

// pickLatestStableForMajor returns the highest-versioned stable release of the
// given major from a release list, or ("", false) when the list holds none.
func pickLatestStableForMajor(releases []pspReleaseEntry, major int) (string, bool) {
	bestTag := ""
	var best [3]int
	for _, r := range releases {
		if r.Draft {
			continue
		}
		tag, ok := acceptLatestPSPStableForMajor(r.TagName, r.Prerelease, major)
		if !ok {
			continue
		}
		v, ok := parseSemver(tag)
		if !ok {
			continue
		}
		if bestTag == "" || cmpSemver(v, best) > 0 {
			bestTag, best = tag, v
		}
	}
	return bestTag, bestTag != ""
}

// ownMajor is this build's own major, or 0 when it cannot be derived (a source
// build is the literal "dev"). 0 means "do not filter by major" — a dev build
// keeps the pre-major-filter behaviour of reporting whatever the newest stable
// is. That is harmless: pspBehindStable cannot parse "dev" either, so a dev
// build never shows an update nudge regardless of what is installed here.
func ownMajor() int {
	if m, ok := pspMajor(Version); ok {
		return m
	}
	return 0
}

// acceptLatestPSPStable decides whether a /releases/latest payload yields a
// usable latest-STABLE tag. Stable-only, defended two ways: GitHub's prerelease
// flag AND the tag string itself (a "-beta"/"-rc"/... suffix). The tag check is
// the load-bearing one — a release published WITHOUT the prerelease flag set
// (e.g. an older beta cut before the workflow began marking pre-releases) could
// still arrive here, and must never be treated as a stable. Anything empty,
// pre-release, or not a parseable semver yields ("", false) so the self-update
// nudge only ever points at a real stable release.
func acceptLatestPSPStable(tagName string, prerelease bool) (string, bool) {
	return acceptLatestPSPStableForMajor(tagName, prerelease, 0)
}

// acceptLatestPSPStableForMajor is acceptLatestPSPStable plus the major gate.
// major 0 disables the gate (see ownMajor).
//
// THE GATE IS WHY THIS EXISTS. GitHub's "Latest release" is one repo-wide
// pointer, and this repo publishes two unrelated lines from it — V3 maintenance
// and V4. Without the gate a V3 panel compares itself against whatever tag that
// pointer happens to hold, so the moment a V4 stable takes it, every deployed V3
// panel reads 3.9.x < 4.y.z and shows "an update is available" pointing at a
// release with a different database model and a node backend V3 cannot run. The
// nudge would be inviting operators across a major boundary they deliberately
// stayed on this side of.
//
// That has not happened yet only by accident: parseSemver rejects more than
// three segments (compat.go) and a "release/" prefix is not a version at all, so
// the four-segment "v4.0.1.10" and "release/4.0.1" tags V4 currently publishes
// both fail to parse and are discarded before any comparison. A single
// three-segment v4 stable would end that, silently and for every deployed panel
// at once. The gate makes the safety deliberate instead of incidental.
func acceptLatestPSPStableForMajor(tagName string, prerelease bool, major int) (string, bool) {
	if tagName == "" || prerelease || IsPrerelease(tagName) {
		return "", false
	}
	v, ok := parseSemver(tagName)
	if !ok {
		return "", false
	}
	if major > 0 && v[0] != major {
		return "", false
	}
	return tagName, true
}
