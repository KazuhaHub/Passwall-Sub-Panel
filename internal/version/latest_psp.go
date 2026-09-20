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

// releaseTagNamespace is where product-scheme tags live. Named here so the one
// place that must distinguish the schemes does not spell the prefix inline.
const releaseTagNamespace = "release/"

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
	if tag, ok := acceptLatestPSPStable(release.TagName, release.Prerelease); ok {
		SetLatestPSP(tag)
	}
	return nil
}

// acceptLatestPSPStable decides whether a /releases/latest payload yields a
// usable latest-STABLE tag. Stable-only, defended two ways: GitHub's prerelease
// flag AND — for LEGACY tags only — the tag string itself. Anything empty,
// pre-release, or not a parseable semver yields ("", false) so the self-update
// nudge only ever points at a real stable release.
//
// THE TAG-TEXT TEST IS SCOPED TO THE LEGACY SCHEME, deliberately. It exists for
// a real historical gap: an older beta cut before the workflow began setting the
// prerelease flag would arrive here without it, and must never be treated as
// stable. That reasoning depends on a v-prefixed version, where a hyphen has
// always meant a pre-release.
//
// Under the product scheme a tag is release/MAJOR.MINOR.PATCH — three integers
// in an explicit namespace, with no hyphen at all — so running the historical
// test there would find nothing to reject and would accept a testing candidate
// GitHub had correctly flagged. The exemption is scoped to that NAMESPACE, not
// to "anything without a v": a tag that is neither scheme keeps the fail-safe
// test, because for an unrecognised form the characters are all there is to go
// on. There the explicit flag decides, which is what the migration plan
// requires: validate the historical form with the historical rule and the new
// form with its own scheme, rather than by inference from the tag text.
//
// THE EXEMPTION HAS NO OBSERVABLE EFFECT YET, and saying so is part of it:
// parseSemver still refuses any release/… tag, so a product tag is rejected a
// line later whatever this test does. It becomes load-bearing when V04 teaches
// the panel to read a product tag, and the test below records the contract it
// must then satisfy.
//
// The scheme test is the namespace check github.com/KazuhaHub/passwall-node's
// releaseid uses (release/… is a product tag, v… is legacy). It is written here
// as a prefix rather than imported because it is a two-line shape check, not the
// ordering rule — those are the ones that must have one implementation.
func acceptLatestPSPStable(tagName string, prerelease bool) (string, bool) {
	if tagName == "" || prerelease {
		return "", false
	}
	if !strings.HasPrefix(tagName, releaseTagNamespace) && IsPrerelease(tagName) {
		return "", false
	}
	if _, ok := parseSemver(tagName); !ok {
		return "", false
	}
	return tagName, true
}
