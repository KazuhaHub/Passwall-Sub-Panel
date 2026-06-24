package version

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// MinXUI is the compiled SAFETY BACKSTOP for the lower bound — the lowest
// 3X-UI version this binary's code can correctly drive. It's a code-level
// fact, not a tunable: PSP calls endpoints (3.2.0's /clients/* API; the
// inbound-scoped per-client endpoints are gone) that simply don't exist on
// older panels, so the binary literally can't manage a < MinXUI panel.
// v3.6.2 raised it 3.1.0 → 3.2.0 with that /clients/* migration.
//
// v3.9.0 raised it 3.2.0 → 3.3.0: the shared-client model attaches ONE client
// to many inbounds and drives lifecycle via /clients/update by email, and on
// 3X-UI 3.2.x that update path hits "UNIQUE constraint failed:
// client_inbounds.client_id, client_inbounds.inbound_id" (the client_inbounds
// upsert was fixed in the 3.3 line — full add→update→del round-trip is
// live-verified on 3.3.0/3.3.1). 3.2.x panels must upgrade to ≥ 3.3.0 before
// migrating to PSP v3.9.0. The compat JSON keeps a separate v3.6.2–v3.8.99 entry
// at the old 3.2.0 floor so the per-node-era PSP builds aren't wrongly tightened.
//
// docs/compat/v3.json's per-entry min_xui describes the SAME floor and MUST
// stay equal to this const — TestMinXUIConstMatchesCompatJSON fails the build
// the moment they drift, so you can't edit the JSON and forget the const (the
// v3.6.2 footgun: JSON bumped, runtime floor not, 3.1.0 panels un-flagged).
// Edit BOTH together when the floor changes.
//
// At runtime ActiveMinXUI returns max(MinXUI, JSON min) — NOT to let the JSON
// widen the floor, but as a safety net: the test keeps them equal, so the max
// is a no-op in a correct release; if a drift ever slips past the test (or a
// stale cache serves an older min), the higher value wins and the floor is
// never wrongly LOWERED below what this binary's code can actually speak.
//
// MaxTestedXUI has NO const counterpart at all: the upper bound is fully
// dynamic, owned by the same JSON. A hardcoded ceiling would go stale the
// moment 3X-UI ships a verified patch release. Admin can also bypass the
// gate via the upgrade-panel "force" flag (see AdminServersHandler), so a
// panel beyond the published tested range isn't a hard wall.
//
// Versions compare numerically (major.minor.patch). A leading "v" is
// tolerated — 3X-UI's /server/status emits "3.1.0" while /getPanelUpdateInfo
// emits "v3.1.0" for the same release.
const MinXUI = "3.3.0"

// activeMaxTestedXUI holds the runtime-effective upper bound, loaded
// from docs/compat/xui-compat.json via RefreshRemoteCompat. Empty string
// means "remote JSON not loaded yet" — CheckXUI returns CompatUnknown for
// every panel until the first successful refresh lands. atomic.Value
// because RefreshRemoteCompat writes from a background goroutine while
// every CheckXUI caller reads concurrently.
var activeMaxTestedXUI atomic.Value // string

// activeMinXUI holds the operational lower bound loaded from the compat
// JSON's per-entry min_xui (RefreshRemoteCompat). ActiveMinXUI clamps it so
// it can only RAISE the floor above the compiled MinXUI backstop. Empty =
// not loaded yet (ActiveMinXUI then returns the compiled backstop).
var activeMinXUI atomic.Value // string

// ActiveMaxTestedXUI returns the upper bound currently in effect, or ""
// when the remote-compat JSON has never been successfully fetched. Empty
// string is a meaningful signal — callers (CheckXUI) treat it as
// "compatibility unknown" rather than picking an arbitrary default.
func ActiveMaxTestedXUI() string {
	if v, ok := activeMaxTestedXUI.Load().(string); ok {
		return v
	}
	return ""
}

// XUIUpgradeTarget reports the 3X-UI version a panel running `current` should be
// nudged to upgrade to — PSP's max_tested ceiling — returned only when `current`
// is STRICTLY below it. ("", false) when current is at/above the ceiling or when
// either version is unknown/unparseable. Unlike IsXUIUpdateAvailable (which chases
// the upstream LatestXUI tag), this only ever points at what PSP has verified, so
// admins are never nudged onto an untested 3X-UI release.
func XUIUpgradeTarget(current string) (string, bool) {
	max := ActiveMaxTestedXUI()
	if max == "" || current == "" {
		return "", false
	}
	cv, ok1 := parseSemver(current)
	mv, ok2 := parseSemver(max)
	if !ok1 || !ok2 {
		return "", false
	}
	if cmpSemver(cv, mv) >= 0 {
		return "", false
	}
	return max, true
}

// ActiveMinXUI returns the lower bound currently in effect: the HIGHER of the
// compiled MinXUI backstop and the operational min_xui loaded from the compat
// JSON (RefreshRemoteCompat). The JSON can only RAISE the floor above the
// backstop, never lower it below what this binary's code can speak — so
// whichever of the two is bumped the effective floor rises, and forgetting to
// sync the other is safe. An empty / unparseable dynamic value falls back to
// the backstop.
func ActiveMinXUI() string {
	floor := MinXUI
	dyn, _ := activeMinXUI.Load().(string)
	if dyn == "" {
		return floor
	}
	dv, ok := parseSemver(dyn)
	if !ok {
		return floor
	}
	fv, _ := parseSemver(floor)
	if cmpSemver(dv, fv) > 0 {
		return dyn
	}
	return floor
}

// SetActiveMaxTestedXUI installs the remote-loaded upper bound. Pass ""
// to clear (CheckXUI then returns Unknown for everything). The value
// isn't validated here — callers (RefreshRemoteCompat) parse + sanity-
// check before installing so a malformed remote JSON can never leave
// PSP in an unparseable state.
func SetActiveMaxTestedXUI(v string) {
	activeMaxTestedXUI.Store(v)
}

// SetActiveMinXUI installs the JSON-loaded operational floor. Pass "" to clear
// (ActiveMinXUI then returns the compiled MinXUI backstop). Like
// SetActiveMaxTestedXUI the value isn't validated here — RefreshRemoteCompat
// parse-checks before installing, and ActiveMinXUI defensively ignores an
// unparseable value.
func SetActiveMinXUI(v string) {
	activeMinXUI.Store(v)
}

// CompatStatus is the result of comparing a panel's reported 3X-UI version
// against this PSP build's tested range.
type CompatStatus int

const (
	// CompatUnknown — the panel hasn't been probed yet, or the probe failed,
	// or the reported version string couldn't be parsed.
	CompatUnknown CompatStatus = iota
	// CompatSupported — version >= MinXUI and <= MaxTestedXUI.
	CompatSupported
	// CompatTooOld — version < MinXUI; PSP likely can't talk to this panel.
	CompatTooOld
	// CompatUntested — version > MaxTestedXUI; PSP hasn't verified this
	// 3X-UI version, may work but may also have undetected schema breaks.
	CompatUntested
)

func (s CompatStatus) String() string {
	switch s {
	case CompatSupported:
		return "supported"
	case CompatTooOld:
		return "too_old"
	case CompatUntested:
		return "untested"
	default:
		return "unknown"
	}
}

// CheckXUI compares panelVersion against the currently-active
// compatibility range (hardcoded MinXUI + remote-loaded MaxTested).
// Returns CompatUnknown when EITHER the panel version is unparseable OR
// the remote-compat JSON hasn't been fetched yet (active max is empty).
// Distinguishing these two cases isn't useful at the call sites — both
// mean "we can't make a confident judgment, surface unknown" — but
// they're distinguished in CompatMessage so the admin UI / log line
// can hint at the right next step.
func CheckXUI(panelVersion string) CompatStatus {
	pv, ok := parseSemver(panelVersion)
	if !ok {
		return CompatUnknown
	}
	maxStr := ActiveMaxTestedXUI()
	if maxStr == "" {
		// Remote compat never loaded — refuse to invent a default
		// because picking either "Supported" or "Untested" would be
		// arbitrary and could lead admin into unsafe action.
		return CompatUnknown
	}
	minV, _ := parseSemver(ActiveMinXUI())
	maxV, ok := parseSemver(maxStr)
	if !ok {
		// Malformed active max (shouldn't happen — RefreshRemoteCompat
		// validates before installing — but defensive nonetheless).
		return CompatUnknown
	}
	if cmpSemver(pv, minV) < 0 {
		return CompatTooOld
	}
	if cmpSemver(pv, maxV) > 0 {
		return CompatUntested
	}
	return CompatSupported
}

// parseSemver accepts "3.1.0", "v3.1.0", "3.1.0-beta.1" (build/pre-release
// suffix ignored), "3.1" (minor-only, patch defaults to 0). Returns false on
// anything else so probe paths can treat the panel as Unknown rather than
// crashing.
func parseSemver(s string) ([3]int, bool) {
	var zero [3]int
	s = strings.TrimSpace(s)
	if s == "" {
		return zero, false
	}
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	// Drop pre-release / build suffix (e.g. "3.1.0-beta.1" → "3.1.0").
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return zero, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return zero, false
		}
		out[i] = n
	}
	return out, true
}

// cmpSemver returns -1 / 0 / +1.
func cmpSemver(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// CompatMessage returns a human-readable explanation of a compat status,
// suitable for log lines or admin UI tooltips. Reads through the Active*
// accessors so the message always reflects the currently-loaded range
// (or "compat data not loaded" when the remote JSON hasn't landed yet).
func CompatMessage(panelVersion string, status CompatStatus) string {
	switch status {
	case CompatSupported:
		return fmt.Sprintf("3X-UI %s is within PSP's supported range [%s, %s]",
			panelVersion, ActiveMinXUI(), ActiveMaxTestedXUI())
	case CompatTooOld:
		return fmt.Sprintf("3X-UI %s is older than PSP's minimum required version %s — traffic poll and reconcile may fail; please upgrade the 3X-UI panel",
			panelVersion, ActiveMinXUI())
	case CompatUntested:
		return fmt.Sprintf("3X-UI %s has not been tested with this PSP build (last verified: %s) — features may work but unexpected schema changes can silently break traffic poll; verify before upgrading more panels",
			panelVersion, ActiveMaxTestedXUI())
	default:
		max := ActiveMaxTestedXUI()
		if max == "" {
			return fmt.Sprintf("3X-UI compatibility data not loaded yet (PSP min is %s; remote-compat JSON fetch pending) — open the Servers page or click Test to trigger a refresh",
				ActiveMinXUI())
		}
		return fmt.Sprintf("3X-UI version unknown (reported %q) — PSP couldn't probe the panel or couldn't parse its reply", panelVersion)
	}
}
