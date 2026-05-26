package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// DefaultRemoteCompatURL is the GitHub raw URL of the canonical compat JSON.
// Self-hosted forks running their own validations can ship a different URL
// in settings (admin-tunable in a future patch); for now this is hardcoded
// since the upstream repo is the single source of truth.
const DefaultRemoteCompatURL = "https://raw.githubusercontent.com/KazuhaHub/passwall-sub-panel/main/docs/compat/xui-compat.json"

// remoteFetchThrottle gates how often RefreshRemoteCompat actually hits the
// network. The Test handler triggers a refresh on every "test connection"
// click; when admin opens the Servers page the frontend fires N parallel
// testServer calls (one per panel) — without throttling all N would hit
// GitHub raw simultaneously. 60s is well under the lifetime of an admin
// session and lets one fetch serve every panel on a page open.
const remoteFetchThrottle = 60 * time.Second

// httpFetchTimeout caps each remote fetch so a slow / hanging GitHub raw
// can never block the Test handler's response path for long.
const httpFetchTimeout = 8 * time.Second

// pspMajorMinorRe extracts the "v3.6" key from PSP's own version.Version
// (which carries forms like "v3.6.0", "v3.6.0-beta.5", "dev"). The remote
// JSON keys are per major.minor — a v3.6.0-beta.x dev/RC and a v3.6.0
// stable share the same compat row.
var pspMajorMinorRe = regexp.MustCompile(`^v?(\d+)\.(\d+)`)

// remoteCompatPayload mirrors docs/compat/xui-compat.json. Schema version 1:
//
//	{
//	  "schema_version": 1,
//	  "updated_at": "...",
//	  "psp_compat": {
//	    "v3.6": { "min_xui": "3.1.0", "max_tested_xui": "3.1.0" },
//	    "v3.7": { "min_xui": "3.1.0", "max_tested_xui": "4.0.0" }
//	  }
//	}
//
// Future fields land here as additive options; unknown fields are tolerated
// (Go json default) so an old PSP can still consume a newer JSON.
type remoteCompatPayload struct {
	SchemaVersion int                            `json:"schema_version"`
	UpdatedAt     string                         `json:"updated_at"`
	PSPCompat     map[string]remoteCompatPSPEntry `json:"psp_compat"`
}

type remoteCompatPSPEntry struct {
	MinXUI        string `json:"min_xui"`         // floor (must equal or exceed code-level MinXUI const)
	MaxTestedXUI  string `json:"max_tested_xui"`  // ceiling — the one we actually install
	Notes         string `json:"notes,omitempty"` // free-form, surfaced in logs only
}

// refreshState is the goroutine-shared throttle / single-flight gate.
//
// Two distinct mechanisms protect the upstream:
//   - refreshInflight: classic single-flight — at most one fetch in
//     progress at any time. N parallel testServers from one Servers-page
//     open collapse to one network call.
//   - refreshLastAt: 60s throttle, but ONLY advanced on a SUCCESSFUL
//     fetch. A failed fetch leaves refreshLastAt unchanged so the next
//     caller (e.g. admin's next "test" click after a brief network blip)
//     can immediately retry. v3.6.0-beta.5 advanced lastAt on failure
//     too, which produced a 60s lock-out whenever GitHub was briefly
//     unreachable — fixed in v3.6.0-beta.6.
var (
	refreshMu        sync.Mutex
	refreshLastAt    time.Time
	refreshLastError error
	refreshInflight  bool
)

// RefreshRemoteCompat fetches the compat JSON, finds the row matching this
// PSP build's major.minor (e.g. "v3.6"), and installs it via
// SetActiveMaxTestedXUI. Returns nil on a successful fetch OR when
// short-circuited by single-flight / throttle. Returns a non-nil error
// only when this call actually attempted the fetch and it failed.
func RefreshRemoteCompat(ctx context.Context, url string) error {
	if url == "" {
		url = DefaultRemoteCompatURL
	}

	refreshMu.Lock()
	if refreshInflight {
		// Another goroutine is currently fetching. Skip — its result
		// will land in refreshLastError and be visible to subsequent
		// callers (via LastRefreshError). The current admin click
		// doesn't need to block waiting for it.
		refreshMu.Unlock()
		return nil
	}
	if !refreshLastAt.IsZero() && time.Since(refreshLastAt) < remoteFetchThrottle {
		// Recent success within throttle window — short-circuit.
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
		// Only advance on success so a failed fetch doesn't lock
		// retries out for the throttle window. Failure → next caller
		// (still single-flight protected) can immediately retry.
		refreshLastAt = time.Now()
	}
	refreshMu.Unlock()
	return err
}

// LastRefreshError returns whatever the most recent fetch produced (nil on
// success, fetch/parse error otherwise). Used by admin UI / health docs
// to surface "remote compat fetch failed last time at <last_at>".
func LastRefreshError() error {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	return refreshLastError
}

// LastRefreshAt returns the wall-clock of the most recent attempt
// (success or failure). Zero value if never attempted.
func LastRefreshAt() time.Time {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	return refreshLastAt
}

func fetchAndApply(ctx context.Context, url string) error {
	fetchCtx, cancel := context.WithTimeout(ctx, httpFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
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
	entry, key, ok := lookupForPSPVersion(payload, Version)
	if !ok {
		return fmt.Errorf("no compat entry for PSP %q (looked for key %q in %d entries)",
			Version, key, len(payload.PSPCompat))
	}
	if _, ok := parseSemver(entry.MaxTestedXUI); !ok {
		return fmt.Errorf("compat entry %q has unparseable max_tested_xui %q", key, entry.MaxTestedXUI)
	}
	SetActiveMaxTestedXUI(entry.MaxTestedXUI)
	// Persist the just-fetched range so the next PSP boot starts with it
	// without requiring network. Best-effort — a cache write failure is
	// not worth failing the whole refresh, the in-memory state is already
	// installed.
	_ = saveCompatCache(entry.MaxTestedXUI)
	return nil
}

// lookupForPSPVersion extracts "v<major>.<minor>" from pspVersion and looks
// it up in payload. Returns the key it tried so error messages stay
// debuggable when no row matches.
func lookupForPSPVersion(payload remoteCompatPayload, pspVersion string) (remoteCompatPSPEntry, string, bool) {
	m := pspMajorMinorRe.FindStringSubmatch(pspVersion)
	if len(m) < 3 {
		return remoteCompatPSPEntry{}, "", false
	}
	key := "v" + m[1] + "." + m[2]
	entry, ok := payload.PSPCompat[key]
	return entry, key, ok
}

// errNoRefreshYet is returned for callers that want to distinguish "no
// refresh ever attempted" from "last refresh succeeded but produced no
// override" — unused for now but reserved.
var errNoRefreshYet = errors.New("remote compat not yet fetched")
