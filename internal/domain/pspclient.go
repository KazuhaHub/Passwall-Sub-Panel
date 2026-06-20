package domain

import (
	"fmt"
	"time"
)

// PSPClient is the v3.9.0 first-class client record — PSP's mirror of 3X-UI's
// `clients` table (one row, projected by the panel into every inbound it is
// attached to). ONE PSPClient per (UserID, PanelID, CredClass) holds the
// credentials PSP both pushes to 3X-UI and renders into subscriptions (full
// symmetry — no on-the-fly derivation), plus the monotonic traffic counters.
//
// It supersedes the per-(user,node) [XUIClientEntry] ownership model: where the
// old model created N clients (distinct email per node) for a user on a panel,
// PSPClient is ONE client attached to many inbounds via [PSPClientInbound]. The
// two coexist during the v3.9.0 migration window; XUIClientEntry is retired once
// every install has migrated.
type PSPClient struct {
	ID      int64
	UserID  int64
	PanelID int64
	// CredClass partitions a user's clients on one panel by credential
	// compatibility. It is almost always 0 (a single shared client covers
	// VLESS/VMess/Trojan/SS/SS-2022-256/Hysteria2 — they use disjoint fields, or
	// the same UUID-derived value). A second class (1, 2, …) is minted ONLY when
	// the user has SS-2022 inbounds of DIFFERENT key lengths on the same panel:
	// a 16-byte and a 32-byte PSK cannot share the one `password` field.
	CredClass int

	// Email is the 3X-UI client email — the panel-wide unique key. v3.9.0 scheme
	// drops the per-node suffix (a client now spans inbounds): u{UserID}@{domain}
	// for CredClass 0, u{UserID}-c{CredClass}@{domain} otherwise. Built by
	// PSPClientEmail; stored so a rename of the rules domain never re-keys a live
	// client.
	Email string

	// Credentials — the single source of truth (no DeriveProxyPassword at render
	// time). UUID is the VLESS/VMess `id` and the Hysteria2 `auth`. Password
	// serves every password protocol (Trojan/SS/SS-2022); it is generated in
	// SS-2022-256 PSK format (base64 of 32 bytes) so it is simultaneously a valid
	// Trojan/SS password and a valid 32-byte PSK.
	UUID     string
	Password string

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

	PeriodBaselineUpBytes    int64
	PeriodBaselineDownBytes  int64
	PeriodBaselineTotalBytes int64
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
// shared-client model: "u{userID}@{domain}" for the default credential class,
// "u{userID}-c{class}@{domain}" for an additional class. Using the panel-side
// user ID (not the UPN) keeps the email stable across renames and free of any
// SSO identifier, exactly as the legacy User.ClientEmail did — only the per-node
// suffix is gone because one client now spans the user's inbounds on the panel.
func PSPClientEmail(userID int64, credClass int, rules EmailRules) string {
	domain := rules.Domain
	if domain == "" {
		domain = "psp.local"
	}
	if credClass == 0 {
		return fmt.Sprintf("u%d@%s", userID, domain)
	}
	return fmt.Sprintf("u%d-c%d@%s", userID, credClass, domain)
}
