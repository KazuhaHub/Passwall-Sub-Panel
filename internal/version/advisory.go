package version

import (
	"strconv"
	"sync/atomic"
)

// XUIAdvisory is an admin-facing heads-up shown BEFORE a remote 3X-UI upgrade to
// a specific version — surfacing breaking changes, especially ones that also
// restart/upgrade the bundled xray-core (3X-UI's /updatePanel pulls a whole new
// panel build, xray-core included). Sourced from the remotely-updatable compat
// JSON's "xui_advisories" map, so a new release's advisory ships WITHOUT a PSP
// rebuild — same mechanism that lets max_tested_xui widen with zero release.
type XUIAdvisory struct {
	Severity    string `json:"severity"`     // "info" | "warning" — UI styles the banner
	AffectsXray bool   `json:"affects_xray"` // true → also warn "this restarts/updates Xray"
	Text        string `json:"text"`         // the heads-up body, admin-facing
}

// activeAdvisories holds the version→advisory map loaded from the compat JSON by
// RefreshRemoteCompat, keyed by canonical "maj.min.patch" (no leading "v").
// Empty/unset until the first successful refresh. atomic.Value because the
// background refresh writes while preview handlers read concurrently. Advisories
// are runtime-only (NOT persisted to the on-disk compat cache): the upgrade
// preview force-refreshes the JSON right before it looks one up, so they're
// always fresh when it matters and never need to survive a restart.
var activeAdvisories atomic.Value // map[string]XUIAdvisory

// SetActiveAdvisories installs the advisory map (keys already canonicalized by
// the caller). Pass nil to clear — LookupXUIAdvisory then reports "no advisory"
// for everything. Not validated here; canonAdvisories rekeys before install.
func SetActiveAdvisories(m map[string]XUIAdvisory) {
	if m == nil {
		m = map[string]XUIAdvisory{}
	}
	activeAdvisories.Store(m)
}

// LookupXUIAdvisory returns the advisory for the given 3X-UI version (accepts
// "v3.5.0" or "3.5.0") and whether one exists. Lookup is by EXACT canonical
// major.minor.patch — an advisory is pinned to the specific release it warns
// about, so 3.5.0's note never leaks onto 3.5.1.
func LookupXUIAdvisory(version string) (XUIAdvisory, bool) {
	key, ok := canonSemverKey(version)
	if !ok {
		return XUIAdvisory{}, false
	}
	m, _ := activeAdvisories.Load().(map[string]XUIAdvisory)
	a, ok := m[key]
	return a, ok
}

// canonSemverKey normalizes a version to "maj.min.patch" (dropping a leading "v"
// and any pre-release/build suffix) for advisory-map keys. Minor-only "3.5"
// canonicalizes to "3.5.0". Returns false on unparseable input.
func canonSemverKey(v string) (string, bool) {
	sv, ok := parseSemver(v)
	if !ok {
		return "", false
	}
	return strconv.Itoa(sv[0]) + "." + strconv.Itoa(sv[1]) + "." + strconv.Itoa(sv[2]), true
}

// canonAdvisories rekeys a raw advisory map (version → advisory) by canonical
// major.minor.patch, dropping entries whose key doesn't parse. Keeps install
// robust to a hand-authored key like "v3.5.0" or "3.5". Returns nil for empty
// input so SetActiveAdvisories stores an empty (not nil) map.
func canonAdvisories(raw map[string]XUIAdvisory) map[string]XUIAdvisory {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]XUIAdvisory, len(raw))
	for k, v := range raw {
		if key, ok := canonSemverKey(k); ok {
			out[key] = v
		}
	}
	return out
}
