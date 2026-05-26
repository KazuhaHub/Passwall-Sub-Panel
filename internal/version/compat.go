package version

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// MinXUI is the absolute lower bound this PSP build can work with. NOT a
// "fallback" — it's a code-level requirement: PSP calls endpoints
// (/server/getPanelUpdateInfo, the nested-JSON form of /inbounds/list,
// etc.) that don't exist or behave differently on older 3X-UI. Bumping
// MinXUI requires shipping a new PSP binary because the change is in
// the call sites, not just in a number. There's no "remote override
// can widen this" semantic — admin can't make PSP suddenly speak to a
// 3X-UI version whose API surface PSP wasn't compiled against.
//
// MaxTestedXUI deliberately has NO const counterpart: the upper bound is
// fully dynamic, owned by docs/compat/xui-compat.json in the repo. A
// hardcoded ceiling would go stale the moment 3X-UI ships a verified
// patch release, and forcing a PSP re-release for every such case
// defeats the purpose. Admin can also opt to bypass the gate entirely
// via the upgrade-panel "force" flag (see AdminServersHandler), so even
// a panel beyond the remote-published tested range isn't a hard wall.
//
// Versions are compared numerically (semver-style major.minor.patch).
// Any leading "v" is tolerated when parsing — 3X-UI's /server/status
// emits "3.1.0" while /getPanelUpdateInfo emits "v3.1.0" for the same
// release.
const MinXUI = "3.1.0"

// activeMaxTestedXUI holds the runtime-effective upper bound, loaded
// from docs/compat/xui-compat.json via RefreshRemoteCompat. Empty string
// means "remote JSON not loaded yet" — CheckXUI returns CompatUnknown for
// every panel until the first successful refresh lands. atomic.Value
// because RefreshRemoteCompat writes from a background goroutine while
// every CheckXUI caller reads concurrently.
var activeMaxTestedXUI atomic.Value // string

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

// ActiveMinXUI returns the lower bound currently in effect. Always the
// hardcoded MinXUI const — exposed as a function for symmetry with
// ActiveMaxTestedXUI so callers don't have to remember which bound is
// dynamic and which is fixed.
func ActiveMinXUI() string {
	return MinXUI
}

// SetActiveMaxTestedXUI installs the remote-loaded upper bound. Pass ""
// to clear (CheckXUI then returns Unknown for everything). The value
// isn't validated here — callers (RefreshRemoteCompat) parse + sanity-
// check before installing so a malformed remote JSON can never leave
// PSP in an unparseable state.
func SetActiveMaxTestedXUI(v string) {
	activeMaxTestedXUI.Store(v)
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
