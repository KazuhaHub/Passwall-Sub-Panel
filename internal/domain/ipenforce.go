package domain

// Fail2banStatus is the raw answer from 3X-UI's GET /server/fail2banStatus
// (added upstream in 3.7.0). Four independent booleans rather than one verdict,
// because the panel reports the gates and PSP decides what they mean.
//
// Enabled   the XUI_ENABLE_FAIL2BAN environment variable is unset or equals
//
//	the literal "true". Anything else — including "1" — turns the
//	whole enforcement path off. See ClassifyIPLimit.
//
// Installed the fail2ban binary answered `fail2ban-client -h`. Upstream only
//
//	probes this when Enabled, so a false here does NOT mean "absent"
//	on its own.
//
// Usable    upstream's own composite: Enabled && Installed.
// Windows   the node runs Windows, where the firewall half is skipped by
//
//	design and the panel disconnects the client itself.
type Fail2banStatus struct {
	Enabled   bool
	Installed bool
	Usable    bool
	Windows   bool
}

// IPLimitEnforcement is what PSP concluded about a panel's ability to act on
// the per-client concurrent-IP cap it is being sent.
//
// This is deliberately NOT a capability. A capability answers "can the panel
// store this field", and 3X-UI can store limitIp on every supported version —
// it accepts the write, returns success, and shows the value back. Whether
// anything then happens is a separate fact about the NODE, invisible to every
// read PSP already makes. Collapsing the two would either stop PSP healing
// drift on a panel that stores the value fine, or claim enforcement it cannot
// see; see docs/connection-limits.md §5.
type IPLimitEnforcement string

const (
	// IPLimitEnforcementUnknown means PSP has no answer yet from a panel that
	// should be able to give one: never probed, or every probe so far failed.
	// It is neither reassuring nor accusing, and nothing should treat it as
	// either.
	IPLimitEnforcementUnknown IPLimitEnforcement = "unknown"

	// IPLimitEnforcementUnsupported is a panel older than 3.7.0, where the
	// route does not exist. Held apart from Unknown because the two ask
	// different things of an operator: Unknown says the probe is broken and is
	// worth chasing, Unsupported says this panel version simply cannot answer
	// and there is nothing to chase. Collapsing them would put a permanent,
	// un-actionable warning on every older panel, which is how a warning stops
	// being read.
	IPLimitEnforcementUnsupported IPLimitEnforcement = "unsupported"

	// IPLimitEnforcementEnforced is the full path: the panel disconnects the
	// offending client through the xray API AND fail2ban bans the source IP at
	// the firewall.
	IPLimitEnforcementEnforced IPLimitEnforcement = "enforced"

	// IPLimitEnforcementDisconnectOnly is Windows: upstream skips the fail2ban
	// requirement there, so the cap still disconnects the client but no IP ban
	// follows and it can reconnect immediately.
	IPLimitEnforcementDisconnectOnly IPLimitEnforcement = "disconnect_only"

	// IPLimitEnforcementDisabled means XUI_ENABLE_FAIL2BAN is set to something
	// other than "true". The operator most likely believes they enabled it.
	IPLimitEnforcementDisabled IPLimitEnforcement = "disabled"

	// IPLimitEnforcementNotInstalled means the gate is open but fail2ban is
	// not there, so the job gives up before touching anyone.
	IPLimitEnforcementNotInstalled IPLimitEnforcement = "not_installed"
)

// Enforcing reports whether a limitIp pushed to this panel does SOMETHING.
// Neither Unknown nor Unsupported is enforcing: an unread precondition must
// never render as a working one, which is how a silently dead limit stays
// invisible.
func (e IPLimitEnforcement) Enforcing() bool {
	return e == IPLimitEnforcementEnforced || e == IPLimitEnforcementDisconnectOnly
}

// Actionable reports whether an operator has something to fix on the node.
// Neither Unknown nor Unsupported is actionable — telling someone to install
// fail2ban because we could not reach the endpoint is how a warning gets
// ignored.
func (e IPLimitEnforcement) Actionable() bool {
	return e == IPLimitEnforcementDisabled || e == IPLimitEnforcementNotInstalled
}

// Valid guards values read back from storage, where an older or hand-edited
// row can hold anything.
func (e IPLimitEnforcement) Valid() bool {
	switch e {
	case IPLimitEnforcementUnknown, IPLimitEnforcementUnsupported,
		IPLimitEnforcementEnforced, IPLimitEnforcementDisconnectOnly,
		IPLimitEnforcementDisabled, IPLimitEnforcementNotInstalled:
		return true
	}
	return false
}

// ClassifyIPLimit turns the panel's four gates into the one thing an operator
// needs to know.
//
// Order matters, and each branch is a real node:
//
//  1. Not enabled wins over everything, INCLUDING Windows. Upstream's job
//     returns before it looks at the platform, so a Windows node with
//     XUI_ENABLE_FAIL2BAN=1 enforces nothing either — reading Windows first
//     would report it as working.
//  2. Usable is taken from upstream rather than recomputed as
//     Enabled && Installed. It is the same thing today, but it is upstream's
//     composite: if a future release adds a third gate, deferring to it stops
//     PSP claiming enforcement that has quietly stopped. Recomputing would
//     keep saying "enforced" forever.
//  3. Windows without fail2ban is a real, supported configuration and must not
//     read as broken — the cap disconnects, it just cannot ban.
//  4. Everything left is a Linux node with the gate open and no fail2ban,
//     which is the case this whole probe exists to surface.
func ClassifyIPLimit(s Fail2banStatus) IPLimitEnforcement {
	switch {
	case !s.Enabled:
		return IPLimitEnforcementDisabled
	case s.Usable:
		return IPLimitEnforcementEnforced
	case s.Windows:
		return IPLimitEnforcementDisconnectOnly
	default:
		return IPLimitEnforcementNotInstalled
	}
}
