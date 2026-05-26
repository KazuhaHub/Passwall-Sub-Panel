package version

import (
	"fmt"
	"strconv"
	"strings"
)

// XUI compatibility range for this build of PSP. Both ends are inclusive.
//
// Updating these is a deliberate act: when PSP starts supporting a newer 3X-UI
// version, bump MaxTestedXUI; when PSP drops support for an older one (because
// it relies on an API only present in newer 3X-UI), bump MinXUI. The values
// are checked into source so the constraint is part of the release artifact —
// admins cannot relax it from a settings table, which is the point: a newer
// 3X-UI may have schema-breaking changes (see docs/3xui-compat.md "2026-05-23
// / 3X-UI 3.1.0" event for a real example).
//
// Versions are compared numerically (semver-style major.minor.patch). Any
// leading "v" is tolerated when parsing — 3X-UI's /server/status emits
// "3.1.0" while /getPanelUpdateInfo emits "v3.1.0" for the same release.
const (
	MinXUI       = "3.1.0"
	MaxTestedXUI = "3.1.0"
)

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

// CheckXUI compares panelVersion against this build's compatibility range.
// Empty / unparseable panelVersion returns CompatUnknown without an error so
// boot probes can degrade gracefully on unreachable panels.
func CheckXUI(panelVersion string) CompatStatus {
	pv, ok := parseSemver(panelVersion)
	if !ok {
		return CompatUnknown
	}
	minV, _ := parseSemver(MinXUI)
	maxV, _ := parseSemver(MaxTestedXUI)
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
// suitable for log lines or admin UI tooltips.
func CompatMessage(panelVersion string, status CompatStatus) string {
	switch status {
	case CompatSupported:
		return fmt.Sprintf("3X-UI %s is within PSP's supported range [%s, %s]",
			panelVersion, MinXUI, MaxTestedXUI)
	case CompatTooOld:
		return fmt.Sprintf("3X-UI %s is older than PSP's minimum required version %s — traffic poll and reconcile may fail; please upgrade the 3X-UI panel",
			panelVersion, MinXUI)
	case CompatUntested:
		return fmt.Sprintf("3X-UI %s has not been tested with this PSP build (last verified: %s) — features may work but unexpected schema changes can silently break traffic poll; verify before upgrading more panels",
			panelVersion, MaxTestedXUI)
	default:
		return fmt.Sprintf("3X-UI version unknown (reported %q) — PSP couldn't probe the panel or couldn't parse its reply", panelVersion)
	}
}
