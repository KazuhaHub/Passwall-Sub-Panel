package version

import (
	"fmt"
	"sync/atomic"
)

// S-UI compatibility bounds — the S-UI counterpart of compat.go's 3X-UI gate.
//
// DELIBERATE ASYMMETRY WITH 3X-UI: there is no compiled MinSUI const. The
// 3X-UI floor is a code-level FACT (PSP calls /clients/* endpoints that older
// panels simply do not expose, so the binary provably cannot drive them), which
// is why MinXUI is hardcoded and drift-guarded by a test. No equivalent verified
// fact exists for S-UI yet: the adapter gates features by CAPABILITY, not by
// panel version, and no S-UI release has been validated against PSP. Inventing a
// const here would assert a compatibility claim nobody has checked, so BOTH S-UI
// bounds come exclusively from the published compat JSON.
//
// Consequence: until docs/compat/v<major>.json carries a `sui_entries` row for
// this PSP build, ActiveMaxTestedSUI() is "" and CheckSUI reports CompatUnknown
// for every S-UI panel — the honest answer, and the same "refuse to invent a
// default" stance CheckXUI takes before its first successful remote fetch.
// Publishing a verified row lights the gate up with NO PSP release, exactly like
// widening max_tested_xui does today.
//
// See docs/compat/v3.json's sui_entries_doc for the fill-in procedure.

// activeMaxTestedSUI holds the highest S-UI version verified against this PSP
// build, loaded from the compat JSON's sui_entries by RefreshRemoteCompat.
// Empty = no verified range published yet (CheckSUI → CompatUnknown).
// atomic.Value because the background refresh writes while handlers read.
var activeMaxTestedSUI atomic.Value // string

// activeMinSUI holds the operational S-UI floor from the same entry. Empty =
// no floor published; CheckSUI then never reports CompatTooOld, because a floor
// nobody has verified must not be used to tell an admin their panel is too old.
var activeMinSUI atomic.Value // string

// ActiveMaxTestedSUI returns the verified S-UI ceiling currently in effect, or
// "" when no sui_entries row has been loaded. Callers treat "" as "no S-UI
// compat data published" and stay silent rather than rendering a permanent
// "unknown" badge admins would have to dismiss.
func ActiveMaxTestedSUI() string {
	if v, ok := activeMaxTestedSUI.Load().(string); ok {
		return v
	}
	return ""
}

// ActiveMinSUI returns the operational S-UI floor, or "" when none is
// published. Unlike ActiveMinXUI there is no compiled backstop to clamp
// against — see the asymmetry note above.
func ActiveMinSUI() string {
	if v, ok := activeMinSUI.Load().(string); ok {
		return v
	}
	return ""
}

// SetActiveMaxTestedSUI installs the JSON-loaded ceiling. Pass "" to clear,
// which RefreshRemoteCompat does when the payload carries no matching
// sui_entries row — so removing a row (or serving an older JSON) reverts to
// "unknown" instead of leaving a stale ceiling in place. Values are
// parse-checked by the caller before installation.
func SetActiveMaxTestedSUI(v string) { activeMaxTestedSUI.Store(v) }

// SetActiveMinSUI installs the JSON-loaded floor; "" clears it.
func SetActiveMinSUI(v string) { activeMinSUI.Store(v) }

// CheckSUI compares an S-UI panel version against the published compat range,
// reusing CompatStatus so admin UI and logs treat both panel kinds uniformly.
//
// Returns CompatUnknown when the version is unparseable OR no ceiling has been
// published. When a ceiling exists but no floor does, a version at/below the
// ceiling is Supported and one above it is Untested — CompatTooOld is only ever
// reported against a floor that was actually published.
func CheckSUI(panelVersion string) CompatStatus {
	pv, ok := parseSemver(panelVersion)
	if !ok {
		return CompatUnknown
	}
	maxStr := ActiveMaxTestedSUI()
	if maxStr == "" {
		return CompatUnknown
	}
	maxV, ok := parseSemver(maxStr)
	if !ok {
		return CompatUnknown
	}
	if minStr := ActiveMinSUI(); minStr != "" {
		if minV, ok := parseSemver(minStr); ok && cmpSemver(pv, minV) < 0 {
			return CompatTooOld
		}
	}
	if cmpSemver(pv, maxV) > 0 {
		return CompatUntested
	}
	return CompatSupported
}

// CompatMessageSUI is CompatMessage's S-UI counterpart: a human-readable
// explanation for log lines and admin UI tooltips.
func CompatMessageSUI(panelVersion string, status CompatStatus) string {
	switch status {
	case CompatSupported:
		if min := ActiveMinSUI(); min != "" {
			return fmt.Sprintf("S-UI %s is within PSP's verified range [%s, %s]",
				panelVersion, min, ActiveMaxTestedSUI())
		}
		return fmt.Sprintf("S-UI %s is at or below PSP's last verified version %s",
			panelVersion, ActiveMaxTestedSUI())
	case CompatTooOld:
		return fmt.Sprintf("S-UI %s is older than PSP's minimum verified version %s — inbound and client management may fail; please upgrade the S-UI panel",
			panelVersion, ActiveMinSUI())
	case CompatUntested:
		return fmt.Sprintf("S-UI %s has not been verified with this PSP build (last verified: %s) — it may work, but an unnoticed API change can silently break inbound sync or traffic poll",
			panelVersion, ActiveMaxTestedSUI())
	default:
		if ActiveMaxTestedSUI() == "" {
			return "S-UI compatibility data has not been published for this PSP build yet — the panel is managed by adapter capability negotiation, without a version-range gate"
		}
		return fmt.Sprintf("S-UI version unknown (reported %q) — PSP couldn't probe the panel or couldn't parse its reply", panelVersion)
	}
}

// activeSUIAdvisories mirrors activeAdvisories for S-UI, keyed by canonical
// major.minor.patch. Runtime-only (never persisted), same as the 3X-UI map.
var activeSUIAdvisories atomic.Value // map[string]XUIAdvisory

// SetActiveSUIAdvisories installs the S-UI advisory map; nil installs an empty
// map so LookupSUIAdvisory reports "no advisory" rather than panicking.
func SetActiveSUIAdvisories(m map[string]XUIAdvisory) {
	if m == nil {
		m = map[string]XUIAdvisory{}
	}
	activeSUIAdvisories.Store(m)
}

// LookupSUIAdvisory returns the pre-upgrade advisory for a specific S-UI
// version, matched by EXACT canonical major.minor.patch so one release's
// warning never leaks onto its neighbours.
func LookupSUIAdvisory(version string) (XUIAdvisory, bool) {
	key, ok := canonSemverKey(version)
	if !ok {
		return XUIAdvisory{}, false
	}
	m, _ := activeSUIAdvisories.Load().(map[string]XUIAdvisory)
	a, ok := m[key]
	return a, ok
}
