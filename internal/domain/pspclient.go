package domain

import (
	"fmt"
	"time"
)

// PSPClient is the v3.9.0 first-class client record — PSP's mirror of 3X-UI's
// `clients` table (one row, projected by the panel into every inbound it is
// attached to). Its database-minted ID is the durable identity; partition
// properties such as Email and CredClass may change while the row and its
// monotonic traffic baselines survive.
//
// It supersedes the per-(user,node) [XUIClientEntry] ownership model: where the
// old model created N clients (distinct email per node) for a user on a panel,
// PSPClient is ONE client attached to many inbounds via [PSPClientInbound]. The
// two coexist during the v3.9.0 migration window; XUIClientEntry is retired once
// every install has migrated.
type PSPClient struct {
	// ID is minted once by PSP and is the only stable client-row identity. It
	// must never be derived from email, credential class, partition position,
	// or any other rendered/configurable value.
	ID      int64
	UserID  int64
	PanelID int64
	// CredClass describes the current password-compatibility partition on one
	// panel. It is a mutable planner output, not identity. It is almost always 0
	// (a single shared client covers
	// VLESS/VMess/Trojan/SS/SS-2022-256/Hysteria2 — they use disjoint fields, or
	// the same UUID-derived value). A second class (1, 2, …) is minted ONLY when
	// the user has SS-2022 inbounds of DIFFERENT key lengths on the same panel:
	// a 16-byte and a 32-byte PSK cannot share the one `password` field.
	CredClass int

	// Email is the current 3X-UI client email. It is unique in the upstream
	// panel's live model but deliberately NOT a local database identity: changing
	// rules.Domain or crossing the one/two-partition boundary rewrites it in the
	// same stable row. Built by PSPClientEmail.
	Email string

	// Credentials mirrored into the upstream client. Render still derives the
	// byte-identical values from the user's UUID; UUID is the VLESS/VMess `id`
	// and Hysteria2 `auth`, while Password serves the password protocols.
	UUID     string
	Password string

	// DesiredEnable/Expiry are backend-neutral intent. Panel* fields are the
	// compatibility projection for third-party panels; native quota/IP policy
	// belongs to directives and never enters the roster ETag. Adapters consume
	// these persisted values rather than rebuilding intent from User or live
	// upstream state.
	DesiredEnable     bool
	DesiredExpiryTime int64
	// PanelQuotaHeadroom is the legacy-panel safety-net encoding (including
	// its exhausted=1 sentinel). It is explicitly not native directive state.
	PanelQuotaHeadroom int64
	PanelIPLimit       int
	PanelDeviceLimit   int
	DesiredMinted      bool

	CreatedAt time.Time

	// Lifetime / LastRaw / PeriodBaseline counters carry the EXACT semantics of
	// the same fields on [XUIClientEntry], but keyed per (user,panel,credClass)
	// instead of per (user,node). Because a shared client reports ONE aggregate
	// traffic row in 3X-UI (LIVE-VERIFIED: every attached inbound echoes the same
	// counter), the poll reads it once by email and folds the monotonic delta
	// here — no per-inbound summation (which would double-count a shared client).
	LifetimeUpBytes    int64
	LifetimeDownBytes  int64
	LifetimeTotalBytes int64

	LastRawUpBytes    int64
	LastRawDownBytes  int64
	LastRawTotalBytes int64
	// LastCounterEpoch disambiguates a native agent counter reset from an
	// ordinary monotonic increment. Zero is reserved for legacy panels, whose
	// adapters cannot report epochs and retain the decrease-detection fallback.
	LastCounterEpoch uint64

	PeriodBaselineUpBytes    int64
	PeriodBaselineDownBytes  int64
	PeriodBaselineTotalBytes int64
}

// SetDesiredLifecycle updates only the desired intent carried by this client.
func (c *PSPClient) SetDesiredLifecycle(lifecycle UserLifecycle) {
	if c == nil {
		return
	}
	c.DesiredEnable = lifecycle.Enable
	c.DesiredExpiryTime = lifecycle.ExpiryTime
	c.PanelQuotaHeadroom = lifecycle.QuotaHeadroom
	c.PanelIPLimit = lifecycle.IPLimit
	c.PanelDeviceLimit = lifecycle.DeviceLimit
	c.DesiredMinted = true
}

// DesiredLifecycle returns the persisted intent. It is the sole input panel
// compatibility adapters and the native roster/directive minter consume.
func (c *PSPClient) DesiredLifecycle() UserLifecycle {
	if c == nil {
		return UserLifecycle{}
	}
	return UserLifecycle{
		Enable: c.DesiredEnable, ExpiryTime: c.DesiredExpiryTime,
		QuotaHeadroom: c.PanelQuotaHeadroom,
		IPLimit:       c.PanelIPLimit, DeviceLimit: c.PanelDeviceLimit,
	}
}

// PSPClientInbound is the v3.9.0 attachment junction — PSP's mirror of 3X-UI's
// `client_inbounds` table. It records which inbounds (PSP nodes) a [PSPClient]
// is attached to, i.e. PSP's DESIRED attachment set; reconcile diffs it against
// the panel's live `GetClient().InboundIDs` to compute attach/detach deltas.
//
// NodeID identifies the PSP node (which fixes the panel + inbound). FlowOverride
// carries a per-attachment VLESS flow when it must differ across the inbounds a
// single client spans (3X-UI stores flow per (client,inbound), not per client),
// empty when the node's default flow applies.
type PSPClientInbound struct {
	ClientID     int64
	NodeID       int64
	FlowOverride string
	// State is the observed convergence state for this attachment. Unlike the
	// former Provisioned boolean it distinguishes retryable work, permanent
	// rejection and dependency blocking, and FirstFailedAt gives the failure a
	// length so pending/rejected can become operator-visible timeouts.
	State          ClientApplyState
	AppliedVersion uint64
	// Applied* is the credential snapshot confirmed by the backend at
	// AppliedVersion. Desired PSPClient credentials may already have advanced;
	// subscription rendering must keep using this snapshot until the newer
	// roster is confirmed, otherwise credential rotation has no happens-before.
	AppliedEmail    string
	AppliedUUID     string
	AppliedPassword string
	FirstFailedAt   *time.Time
}

// ClientApplyState is the closed set reported by a node agent for one desired
// client attachment. Keep these values byte-identical to the wire contract;
// the PSP does not import that module until the native adapter lands in C1.
type ClientApplyState string

const (
	ClientApplyApplied  ClientApplyState = "applied"
	ClientApplyPending  ClientApplyState = "pending"
	ClientApplyRejected ClientApplyState = "rejected"
	ClientApplyBlocked  ClientApplyState = "blocked"
)

// Applied reports whether render/traffic may trust this attachment. Keeping
// the gate on the domain type prevents callers from re-inventing truthiness for
// pending/rejected/blocked states.
func (a PSPClientInbound) Applied() bool { return a.State == ClientApplyApplied }

// ClientApplyIssue is the operator-visible escalation produced when a
// retryable or permanently rejected object remains unresolved past its
// deadline. Blocked objects are intentionally excluded: their owning
// dependency is the object that must be diagnosed.
type ClientApplyIssue struct {
	Code      string
	ClientID  int64
	NodeID    int64
	State     ClientApplyState
	StartedAt time.Time
}

const (
	IssueObjectPendingTimeout  = "object_pending_timeout"
	IssueObjectRejectedTimeout = "object_rejected_timeout"
)

// TimeoutIssue promotes a long-lived pending/rejected attachment to an Issue.
// A non-positive timeout disables escalation. The function is pure so the
// exact timeout policy can be shared by background processing and tests.
func (a PSPClientInbound) TimeoutIssue(now time.Time, timeout time.Duration) *ClientApplyIssue {
	if timeout <= 0 || a.FirstFailedAt == nil || now.Before(a.FirstFailedAt.Add(timeout)) {
		return nil
	}
	code := ""
	switch a.State {
	case ClientApplyPending:
		code = IssueObjectPendingTimeout
	case ClientApplyRejected:
		code = IssueObjectRejectedTimeout
	default:
		return nil
	}
	return &ClientApplyIssue{
		Code: code, ClientID: a.ClientID, NodeID: a.NodeID,
		State: a.State, StartedAt: *a.FirstFailedAt,
	}
}

// PeriodUsedTotal returns this client's usage in the current period:
// LifetimeTotalBytes minus the baseline captured at the last rollover. Mirrors
// XUIClientEntry/User period math; never negative in practice (baseline ≤
// lifetime) but callers should still floor at zero for defensiveness.
func (c *PSPClient) PeriodUsedTotal() int64 {
	used := c.LifetimeTotalBytes - c.PeriodBaselineTotalBytes
	if used < 0 {
		return 0
	}
	return used
}

// PSPClientEmail builds the panel-wide unique client email for the v3.9.0
// shared-client model: "u{userID}{suffix}@{domain}". The suffix is precomputed
// by the partition (clientplan.partKey.emailSuffix): "" for a lone/default
// client or "-k{8hex}" where multiple current partitions need distinct upstream
// emails. This is a projection, not identity: the same durable row may receive
// a different email after a partition-layout or domain change. Using the
// panel-side user ID (not the UPN) keeps the email stable across renames and free
// of any SSO identifier, exactly as the legacy User.ClientEmail did — only the
// per-node suffix is gone because one client now spans the user's inbounds.
func PSPClientEmail(userID int64, suffix string, rules EmailRules) string {
	d := rules.Domain
	if d == "" {
		d = "psp.local"
	}
	return fmt.Sprintf("u%d%s@%s", userID, suffix, d)
}
