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
// (e.g. "v4.0.2"). Empty until a fetch lands; callers treat empty as
// "unknown" and show no update nudge.
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

// ProductTagNamespace is where product-scheme tags live. Exported because more
// than one place has to recognise the address form, and each of them spelling the
// prefix inline is how the two come to disagree about which namespace it is.
//
// A `v` PREFIX, WHICH IS ALSO HOW A GO MODULE VERSION BEGINS. That was the whole
// reason for the namespace the product line used to live under, and the reason it
// is only readable now is that no build here is a dependency: the module version
// of this repository is a separate identity that nothing resolves, and every tag
// a caller of this package meets — GitHub's tag_name, a release document, an
// asset URL — is a product release. A four-segment version, which is what a fix
// release carries, is not a valid Go version under any namespace.
const ProductTagNamespace = "v"

// HistoricalTagNamespace is where the four releases published before the address
// changed live, and it is read rather than published.
//
// IT CANNOT BE RETIRED, because the tags cannot move: `release/4.0.0` through
// `release/4.0.1.2` are on GitHub permanently, the panel lists them, and a node
// still installs from them. It is a closed set — nothing new is published here —
// and it carries the one property the current namespace does not: a slash, which
// occupies two path entries in a download URL.
const HistoricalTagNamespace = "release/"

// IsPSPUpdateAvailable reports whether THIS build is behind the latest stable
// release. Returns false for "dev" / unparseable / no-latest-yet so the nudge
// only fires when we're confident.
func IsPSPUpdateAvailable() bool {
	return pspBehindStable(Version, LatestPSP())
}

func pspBehindStable(current, latestStable string) bool {
	if current == "" || latestStable == "" {
		return false
	}
	// latestStable is a TAG as GitHub reported it, and current is a VERSION. They
	// are never the same string, and comparing them as they arrive finds nothing
	// newer and the nudge never appears.
	latestVersion, ok := VersionOfReleaseTag(latestStable)
	if !ok {
		return false
	}
	// THE PROJECT'S OWN ORDER, not a local three-integer parse. They agreed while
	// every version was three integers; a build component makes them disagree —
	// the parse refuses `4.0.0.1` outright, so a build carrying one would never be
	// nudged, and only the builds the reordering work exists to distinguish would
	// silently stop being told about anything. Both strings are checked as
	// versions first, so neither `dev` nor a stray tag reaches the comparator.
	if !IsReleaseVersion(current) || !IsReleaseVersion(latestVersion) {
		return false
	}
	// A final version sorts above beta/rc candidates of the same base.
	return CompareRelease(current, latestVersion) < 0
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
	// Current-build approval is a separate fact from latest-stable. Failure to
	// read it never turns a numeric identity into a claim of stability.
	channelCtx, cancelChannel := context.WithTimeout(ctx, latestPSPFetchTimeout)
	_ = refreshBuildChannel(channelCtx)
	cancelChannel()

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

// acceptLatestPSPStable requires both stable metadata and a suffix-free identity.
// Historical numeric stable releases remain eligible.
func acceptLatestPSPStable(tagName string, prerelease bool) (string, bool) {
	if tagName == "" || prerelease {
		return "", false
	}
	if !IsReleaseTag(tagName) || strings.Contains(tagName, "-") {
		return "", false
	}
	return tagName, true
}
