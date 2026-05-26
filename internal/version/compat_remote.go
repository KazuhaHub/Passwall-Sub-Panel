package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// defaultRemoteCompatURLBase is the GitHub raw path under which per-major
// compat JSON files live. Each PSP build pulls the file matching its own
// major (v3.x → v3.json, v4.x → v4.json) — the major number is appended
// at fetch time. Per-major split (v3.6.0-beta.7) replaces the v3.6.0-beta.5
// single-file model: each file is naturally bounded by "how many minors
// a single major ships" (~10), maintainers only ever edit the active-
// major file, and bumping to a new major (v4) is just "create v4.json,
// leave v3.json frozen".
const defaultRemoteCompatURLBase = "https://raw.githubusercontent.com/KazuhaHub/passwall-sub-panel/main/docs/compat/"

// remoteFetchThrottle gates how often RefreshRemoteCompat actually hits the
// network. The Test handler triggers a refresh on every "test connection"
// click; when admin opens the Servers page the frontend fires N parallel
// testServer calls (one per panel) — without throttling all N would hit
// GitHub raw simultaneously. 60s lets one fetch serve every panel on a
// page open and is well under the lifetime of an admin session.
const remoteFetchThrottle = 60 * time.Second

// httpFetchTimeout caps each remote fetch so a slow / hanging GitHub raw
// can never block the Test handler's response path for long.
const httpFetchTimeout = 8 * time.Second

// pspMajorRe extracts the major number from PSP's own version.Version
// (forms: "v3.6.0", "v3.6.0-beta.7", "3.6.99", "dev"). Used to pick which
// per-major JSON file to fetch and to validate the file's `major` field.
var pspMajorRe = regexp.MustCompile(`^v?(\d+)\.`)

// schemaVersion is what the on-disk JSON files must carry. Bumped to 2
// when the v3.6.0-beta.7 redesign switched from a single-file map keyed
// by "vX.Y" to per-major files + entries array + psp_min/psp_max range
// per row. A future PSP that wants to support a newer schema can still
// read v2 by branching on this field.
const schemaVersion = 2

// remoteCompatPayload mirrors docs/compat/v<MAJOR>.json (schema_version 2):
//
//	{
//	  "schema_version": 2,
//	  "major": 3,
//	  "updated_at": "...",
//	  "entries": [
//	    {
//	      "psp_min": "v3.6.0",
//	      "psp_max": "v3.6.99",
//	      "min_xui": "3.1.0",
//	      "max_tested_xui": "3.1.0",
//	      "notes": "..."
//	    }
//	  ]
//	}
//
// Unknown fields are tolerated (Go json default) so an old PSP can still
// consume a newer JSON as long as the v2 essentials are present.
type remoteCompatPayload struct {
	SchemaVersion int                    `json:"schema_version"`
	Major         int                    `json:"major"`
	UpdatedAt     string                 `json:"updated_at"`
	Entries       []remoteCompatPSPEntry `json:"entries"`
}

// remoteCompatPSPEntry covers one PSP version range. psp_min / psp_max
// are closed-interval semver endpoints (stable form: "vX.Y.Z", no
// pre-release suffix — matching the convention that pre-release builds
// fall into the same row as the stable they target).
type remoteCompatPSPEntry struct {
	PSPMin       string `json:"psp_min"`
	PSPMax       string `json:"psp_max"`
	MinXUI       string `json:"min_xui"`
	MaxTestedXUI string `json:"max_tested_xui"`
	Notes        string `json:"notes,omitempty"`
}

// refreshState — two distinct concurrency mechanisms:
//   - refreshInflight: classic single-flight, at most one fetch in flight.
//     N parallel callers (Servers-page open) collapse to one network call.
//   - refreshLastAt: 60s throttle, ONLY advanced on success. A failed
//     fetch leaves it unchanged so the next caller can immediately retry
//     instead of waiting out the throttle.
var (
	refreshMu        sync.Mutex
	refreshLastAt    time.Time
	refreshLastError error
	refreshInflight  bool
)

// RefreshRemoteCompat fetches the per-major compat JSON for THIS PSP build,
// finds the entry containing the current version, and installs its
// max_tested_xui via SetActiveMaxTestedXUI. Returns nil on success OR
// when short-circuited by single-flight / throttle. Returns a non-nil
// error only when this call actually attempted the fetch and it failed.
//
// urlOverride is for tests / admin override; "" uses the default per-major
// URL computed from version.Version.
func RefreshRemoteCompat(ctx context.Context, urlOverride string) error {
	url := urlOverride
	if url == "" {
		var err error
		url, err = defaultURLForCurrentVersion()
		if err != nil {
			return err
		}
	}

	refreshMu.Lock()
	if refreshInflight {
		refreshMu.Unlock()
		return nil
	}
	if !refreshLastAt.IsZero() && time.Since(refreshLastAt) < remoteFetchThrottle {
		refreshMu.Unlock()
		return nil
	}
	refreshInflight = true
	refreshMu.Unlock()

	err := fetchAndApply(ctx, url)

	refreshMu.Lock()
	refreshInflight = false
	refreshLastError = err
	if err == nil {
		refreshLastAt = time.Now()
	}
	refreshMu.Unlock()
	return err
}

// LastRefreshError returns whatever the most recent fetch produced (nil on
// success, fetch/parse error otherwise). Surfaces to admin UI / docs.
func LastRefreshError() error {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	return refreshLastError
}

// LastRefreshAt returns the wall-clock of the most recent SUCCESSFUL fetch
// (zero value when never attempted or only failures so far).
func LastRefreshAt() time.Time {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	return refreshLastAt
}

// defaultURLForCurrentVersion returns the GitHub raw URL of the per-major
// JSON file matching THIS PSP build's major. "dev" / unparseable PSP
// version → error (RefreshRemoteCompat surfaces it; dev builds get
// CompatUnknown until admin uses force override, which is the documented
// trade-off).
func defaultURLForCurrentVersion() (string, error) {
	major, ok := pspMajor(Version)
	if !ok {
		return "", fmt.Errorf("cannot derive PSP major from version %q (dev build?) — compat refresh disabled, use force override to upgrade panels", Version)
	}
	return defaultRemoteCompatURLBase + "v" + strconv.Itoa(major) + ".json", nil
}

// pspMajor extracts the integer major from PSP's own version string.
// Returns 0/false for "dev" or anything else parseSemver-incompatible.
func pspMajor(v string) (int, bool) {
	m := pspMajorRe.FindStringSubmatch(v)
	if len(m) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func fetchAndApply(ctx context.Context, url string) error {
	fetchCtx, cancel := context.WithTimeout(ctx, httpFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// Reuses the shared safehttp-guarded client declared in
	// latest_xui.go (same package). Pre-v3.6.1-beta.3 this used
	// http.DefaultClient, which would happily follow an admin-supplied
	// urlOverride into loopback / link-local addresses.
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	var payload remoteCompatPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if payload.SchemaVersion != schemaVersion {
		return fmt.Errorf("compat JSON schema_version %d, this PSP build only supports %d", payload.SchemaVersion, schemaVersion)
	}
	currentMajor, ok := pspMajor(Version)
	if !ok {
		return fmt.Errorf("cannot derive PSP major from version %q", Version)
	}
	if payload.Major != currentMajor {
		// Self-validation: PSP fetched v<currentMajor>.json but the
		// file's `major` field says something else. Either GitHub
		// served a wrong file or admin accidentally pushed v3.json
		// content to v4.json. Refuse to apply so we don't install
		// the wrong major's range.
		return fmt.Errorf("compat JSON declares major=%d but this PSP is major=%d (wrong file at URL?)", payload.Major, currentMajor)
	}
	entry, ok := lookupForPSPVersion(payload, Version)
	if !ok {
		return fmt.Errorf("no compat entry covers PSP %q in %d entries (range gap — bump the JSON)",
			Version, len(payload.Entries))
	}
	if _, ok := parseSemver(entry.MaxTestedXUI); !ok {
		return fmt.Errorf("compat entry [%s..%s] has unparseable max_tested_xui %q",
			entry.PSPMin, entry.PSPMax, entry.MaxTestedXUI)
	}
	SetActiveMaxTestedXUI(entry.MaxTestedXUI)
	_ = saveCompatCache(entry.MaxTestedXUI)
	return nil
}

// lookupForPSPVersion iterates entries in document order and returns the
// FIRST one whose [psp_min, psp_max] closed interval contains pspVersion.
// "First match wins" is the documented semantics — admin authoring the
// JSON puts narrower / newer ranges earlier so a more-specific entry
// shadows a broader one.
func lookupForPSPVersion(payload remoteCompatPayload, pspVersion string) (remoteCompatPSPEntry, bool) {
	pv, ok := parseSemver(pspVersion)
	if !ok {
		return remoteCompatPSPEntry{}, false
	}
	for _, e := range payload.Entries {
		lo, lok := parseSemver(e.PSPMin)
		hi, hok := parseSemver(e.PSPMax)
		if !lok || !hok {
			// Malformed entry — skip rather than fail the whole
			// lookup so one bad row doesn't black out the file.
			continue
		}
		if cmpSemver(lo, hi) > 0 {
			// Inverted range (psp_min > psp_max) — admin error, skip.
			continue
		}
		if cmpSemver(pv, lo) >= 0 && cmpSemver(pv, hi) <= 0 {
			return e, true
		}
	}
	return remoteCompatPSPEntry{}, false
}

// errNoRefreshYet reserved for future callers that want to distinguish
// "never refreshed" from "refreshed but no override active".
var errNoRefreshYet = errors.New("remote compat not yet fetched")
