package domain

import (
	"fmt"
	"time"
)

// EmailRules captures the runtime-configurable email domain used when
// constructing a 3X-UI client.email. A single suffix is shared by every
// user regardless of how they sign in — local password, SAML, OIDC. Loaded
// from UISettings.
type EmailRules struct {
	Domain string // e.g. "passwall.kazuhahub.com"
}

// SSO connection identifiers. Format is "<protocol>:<connection_name>" so
// the panel can grow into multiple SAML / OIDC tenants without a schema
// change — when that happens, connection_name comes from the saml/oidc
// config row and these constants stay only as defaults / local marker.
const (
	SSOProviderLocal      = "local"
	SSOProviderSAMLPrefix = "saml:"
	SSOProviderOIDCPrefix = "oidc:"
	SSOConnectionDefault  = "default"
)

// SSOProviderSAML / SSOProviderOIDC are the current well-known values for
// the single-connection deployment we ship today. EnsureSSO callers should
// build the string through these so changing the format later (e.g. swapping
// "default" for an admin-named connection) only touches the producer side.
const (
	SSOProviderSAML = SSOProviderSAMLPrefix + SSOConnectionDefault
	SSOProviderOIDC = SSOProviderOIDCPrefix + SSOConnectionDefault
)

// User is the panel-side logical user. One User maps to multiple 3X-UI clients
// (one per authorized inbound) via the ownership table.
type User struct {
	ID                 int64
	UPN                string // display + local-login username; not used as SSO lookup key anymore
	Email              string // notification recipient; SSO uses the Email claim, not UPN
	PasswordHash       string // bcrypt; present when the account has local-password login
	// SSOProvider is the SSO connection this account is bound to. "local"
	// for local-password accounts; "saml:<name>" / "oidc:<name>" once a
	// first-time SSO login has linked the row to an external identity.
	// Paired with SSOSubject as a composite uniqueness key so a UPN
	// rename in the IdP never re-maps a user to a different panel row.
	SSOProvider string
	// SSOSubject is the IdP-side stable identifier: SAML <NameID> for
	// SAML, the `sub` claim for OIDC. For local accounts we store the
	// UPN here so the (provider, subject) composite remains unique
	// across local rows too — no NULL handling needed.
	SSOSubject string
	Role               Role
	SubToken           string // 32-byte base64url, subscription URL credential
	UUID               string // v4, used as the derivation seed for proxy passwords
	GroupID            int64
	EnabledRuleSets    []string
	PersonalRules      string
	ExpireAt           *time.Time
	TrafficLimitBytes  int64 // 0 = unlimited
	TrafficResetPeriod ResetPeriod
	TrafficPeriodStart *time.Time
	// LifetimeUpBytes / LifetimeDownBytes / LifetimeTotalBytes accumulate
	// monotonically across 3X-UI restarts. The poll worker computes per-cycle
	// deltas against the previous snapshot and treats negative deltas (counter
	// reset on Xray restart) as "delta = current value", so these counters
	// never go backwards.
	LifetimeUpBytes    int64
	LifetimeDownBytes  int64
	LifetimeTotalBytes int64
	// PeriodBaselineBytes is LifetimeTotalBytes at the moment TrafficPeriodStart
	// last advanced. Subtracting it from current LifetimeTotalBytes gives the
	// bytes used this period — O(1) memory math, no DB lookup. Pre-v3 this
	// was computed via LastBefore(period_start) on traffic_snapshots on every
	// query AND every poll cycle (one random-point read per user), and was
	// duplicated in both traffic.Service and mailer.Service. Now lifetime
	// counters + this baseline are the single source of truth.
	PeriodBaselineBytes int64
	// LifetimeBaselineAt marks when the poll worker last updated the lifetime
	// counters. It's the cutoff the bootstrap-delta logic uses: ownerships
	// created AFTER this point are genuinely new traffic (count their first
	// cumulative read); ownerships created BEFORE were already accounted for
	// in lifetime (skip their bootstrap to avoid double-counting). Decoupled
	// from UpdatedAt because that field gets touched by many unrelated edits.
	LifetimeBaselineAt *time.Time
	// DisplayName is the friendly name shown in panel UI (avatar label,
	// header, lists). Independent of UPN, which is the stable identifier.
	// SSO users get it from the SAML displayname claim on every login; for
	// local accounts the admin enters it on create/edit. UI falls back to UPN
	// when empty.
	DisplayName        string
	Remark             string
	Enabled            bool
	AutoDisabledReason AutoDisabledReason
	// DisableDetail stores additional context for the disable reason (e.g., admin note, blocked client info).
	DisableDetail string
	// BlockViolationCount tracks how many times the user attempted to use a blocked subscription client.
	BlockViolationCount int
	EmergencyUsedCount  int
	EmergencyUntil      *time.Time
	// EmergencyBaselineBytes snapshots LifetimeTotalBytes at the moment an
	// emergency-access window was granted. The traffic poll uses it to compute
	// "bytes consumed during this emergency window" and ends the window early
	// when the admin-configured EmergencyAccessQuotaGB is exhausted. Reset to
	// zero implicitly when EmergencyUntil is cleared (rollover, admin reset,
	// natural expiry).
	EmergencyBaselineBytes int64
	// TokenVersion is a monotonic counter the JWT issuer embeds in every
	// access/refresh token. Increment it to revoke every JWT issued
	// before "now" — used when admin disables the account, demotes the
	// role, or the user changes password. Middleware.RequireAuth compares
	// the claim against the live row and 401s on mismatch.
	TokenVersion int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// IsExpired reports whether ExpireAt is non-nil and earlier than t.
// PeriodUsed returns bytes consumed since the start of u's current traffic
// period. Pure O(1) memory math — derived from the monotonic lifetime
// counter and the baseline snapshotted at the last period rollover. Both
// fields are maintained by the traffic poll, so anyone holding a fresh
// User from the repo can call this without touching the DB. Pre-v3 this
// was duplicated between traffic.Service and mailer.Service, each running
// its own LastBefore(period_start) random-point query per user per call.
func (u *User) PeriodUsed() int64 {
	used := u.LifetimeTotalBytes - u.PeriodBaselineBytes
	if used < 0 {
		// Baseline > lifetime should be impossible in normal operation —
		// guard anyway so a single bad row can't make the dashboard show a
		// nonsense negative number.
		return 0
	}
	return used
}

func (u *User) IsExpired(t time.Time) bool {
	return u.ExpireAt != nil && t.After(*u.ExpireAt)
}

// EffectiveEnabled is the value the panel should publish to 3X-UI for the
// `enable` field. Distinct from the raw u.Enabled toggle because expiry
// gates effective access independently of the admin's enable intent:
//
//   - admin disabled                    → false (admin overrides everything)
//   - permanent user (no ExpireAt)      → u.Enabled
//   - ExpireAt in future                → u.Enabled
//   - ExpireAt in past, no emergency    → false (expired + no extension)
//   - ExpireAt in past, emergency live  → u.Enabled (emergency extends)
//
// Without this gate, an expired-but-Enabled user would push enable=true,
// 3X-UI's own cron would re-disable on the past expiry timestamp, and the
// reconcile loop would keep "fixing" the same enable_mismatch every cycle
// (see the bug log on the v3.0.0-beta.20 release notes).
func (u *User) EffectiveEnabled(t time.Time) bool {
	if u == nil || !u.Enabled {
		return false
	}
	if !u.IsExpired(t) {
		return true
	}
	// Expired — honor emergency-access extension if still live.
	if u.EmergencyUntil != nil && u.EmergencyUntil.After(t) {
		return true
	}
	return false
}

// HasLocalPassword reports whether the user can authenticate through the
// panel's local UPN/password flow. SSO-linked pre-created accounts keep
// their password hash, so this deliberately does not mean "not SSO".
func (u *User) HasLocalPassword() bool {
	return u != nil && u.PasswordHash != ""
}

// ClientEmail builds the 3X-UI client.email for one (user, node) pair.
// Format: "u<userID>-n<nodeID>@<domain>", e.g. "u42-n5@psp.local".
//
// The node ID disambiguates per inbound. Although 3X-UI's own unique
// index is (inbound, email), several forks enforce email uniqueness
// across the whole panel — so the same user on two different inbounds
// of the same server would otherwise collide. Including the node ID
// guarantees collision-free emails regardless of fork.
//
// Using the panel-side user ID for the user part guarantees:
//   - uniqueness across local and SSO accounts regardless of the UPN value;
//   - stability — renaming a user does NOT change their 3X-UI emails,
//     so reconciliation never has to re-create the client;
//   - that Entra ID's opaque persistent NameID, however garbled, never
//     leaks into the 3X-UI client list.
//
// Cross-reference "u42-n5" with the admin user/node lists to find the
// human-readable names. Historically-imported clients keep their original
// email in the ownership table and do NOT go through this helper.
func (u *User) ClientEmail(nodeID int64, r EmailRules) string {
	suffix := r.Domain
	if suffix == "" {
		suffix = "psp.local"
	}
	return fmt.Sprintf("u%d-n%d@%s", u.ID, nodeID, suffix)
}

// Group is a user grouping that defines accessible nodes and render layout.
type Group struct {
	ID        int64
	Slug      string
	Name      string
	TagFilter TagFilter
	Layout    Layout
	Remark    string
	CreatedAt time.Time
}

// TagFilter expresses a node selection rule.
//   - All=true matches every node (Mode + Tags ignored)
//   - otherwise Tags is a list of conditions like "region:TW", "tag:reality",
//     "server:tw-hinet" combined under Mode semantics:
//       Mode == "" or "all" → AND (every condition must match)
//       Mode == "any"       → OR  (at least one condition must match)
type TagFilter struct {
	All  bool
	Tags []string
	// Mode is the conjunction over Tags. Empty defaults to "all" for
	// backwards compatibility with rows persisted before OR was added.
	Mode string
}

// Layout is the group-level render layout that controls node ordering and
// visual separator placement.
type Layout struct {
	Separators          []Separator
	Sort                []SortEntry
	DefaultSortStrategy string // e.g. "by_region_then_id"
}

// Separator is a visual separator row (rendered as a 127.0.0.1:1 dummy proxy)
// inserted at a specific position in the node list.
type Separator struct {
	Position int    // 0-indexed; inserted before this position
	Name     string // display text, e.g. "🇹🇼 Premium" or "----- TW -----"
}

// SortEntry assigns an explicit weight to one node. Nodes not listed here
// follow the group's DefaultSortStrategy.
type SortEntry struct {
	NodeID int64
	Weight int
}

// Node is the panel-side metadata for a 3X-UI inbound (1:1 mapping).
// Protocol parameters (addr/port/TLS/Reality) are NOT stored here —
// those live in 3X-UI and are fetched on demand. Flow is the exception:
// the panel owns it so managed VLESS clients can be kept consistent.
//
// ServerAddress is the public hostname that clients dial. 3X-UI inbounds
// don't carry this on their own (their `listen` is a bind interface), so
// the panel records it explicitly here. Required for subscription rendering.
// NodeKind discriminates "real" 3X-UI-backed nodes from "separator"
// decoration entries the admin adds to visually group the subscription
// list (e.g. an entry titled "---- Taiwan HiNet ----"). Separators
// render as DIRECT proxies in client configs and never participate in
// traffic accounting or health probing — they exist purely for layout.
// Empty value is treated as NodeKindReal so existing rows in the DB
// stay valid without a backfill.
type NodeKind string

const (
	NodeKindReal      NodeKind = "real"
	NodeKindSeparator NodeKind = "separator"
)

// IsSeparator reports whether the Node is the decoration variant. Calls
// that need to skip layout-only rows (traffic poll, health probe,
// 3X-UI client sync) should gate on this rather than string-compare
// Kind directly.
func (n *Node) IsSeparator() bool {
	return n != nil && n.Kind == NodeKindSeparator
}

// PushExpireTime returns the 3X-UI expire_time (ms since epoch) that
// should be transmitted for u — i.e. MAX(ExpireAt, EmergencyUntil) so
// an active emergency-access window can extend the wall-clock expiry
// without panel having to mutate u.ExpireAt itself. Returns 0 ("no
// expiry") only when BOTH ExpireAt and EmergencyUntil are nil, which
// preserves the "permanent user" contract that 3X-UI uses for clients
// that never expire.
//
// Defined on *User in the domain package so every push path
// (user.pushClientConfigToAll, reconcile.checkOne, sync recovery
// flows) shares one source of truth — the v2.2.4 / v2.2.5 history
// taught that splitting this calculation across packages drifts and
// causes reconcile to fight traffic poll over the same field.
func (u *User) PushExpireTime() int64 {
	if u == nil {
		return 0
	}
	var effective time.Time
	has := false
	if u.ExpireAt != nil {
		effective = *u.ExpireAt
		has = true
	}
	if u.EmergencyUntil != nil && u.EmergencyUntil.After(effective) {
		effective = *u.EmergencyUntil
		has = true
	}
	if !has {
		return 0
	}
	return effective.UnixMilli()
}

type Node struct {
	ID            int64
	PanelID       int64
	InboundID     int
	DisplayName   string
	ServerAddress string
	Flow          string
	// Protocol caches the upstream inbound's protocol (vless / vmess /
	// trojan / shadowsocks / hysteria2, lowercased) so the UI can gate
	// protocol-specific fields (e.g. Flow is VLESS-only) without a live
	// 3X-UI fetch. Populated on import / create / inbound edit; empty for
	// rows written before this column existed (treated as "unknown").
	Protocol      string
	Region        string
	Tags          []string
	SortOrder     int
	Enabled       bool
	Kind          NodeKind
	CreatedAt     time.Time
	// LifetimeUpBytes / LifetimeDownBytes / LifetimeTotalBytes accumulate
	// monotonically across 3X-UI counter resets, mirroring the user-level
	// fields. Updated by the traffic poll worker.
	LifetimeUpBytes       int64
	LifetimeDownBytes     int64
	LifetimeTotalBytes    int64
	LastTrafficUpBytes    int64
	LastTrafficDownBytes  int64
	LastTrafficTotalBytes int64
	// Health is updated by the periodic health-check worker. Empty (zero
	// value) until the first probe runs. Lets the admin Nodes view show a
	// green/red dot without polling each 3X-UI live.
	HealthState     NodeHealthState
	HealthCheckedAt *time.Time
	// HealthDetail carries the panel/inbound error string for the most
	// recent failed probe; empty when healthy.
	HealthDetail string
}

// SeparatorMode controls how a SeparatorEntry decides whether to appear
// in a given group's subscription. Two values:
//
//   - SeparatorModeGlobal:    visible in every group, position by SortOrder.
//   - SeparatorModeNodeBound: visible only when the group includes at least
//     one node from NodeIDs. Position is still SortOrder — NodeIDs only
//     gates visibility, not where it lands in the list.
type SeparatorMode string

const (
	SeparatorModeGlobal    SeparatorMode = "global"
	SeparatorModeNodeBound SeparatorMode = "node_bound"
)

// SeparatorEntry is a decoration row rendered as a DIRECT proxy in
// subscription documents, used to visually group nodes (e.g. an entry
// titled "----- Taiwan -----"). Lives in its own table (nodes_separator)
// separate from real 3X-UI-backed nodes so traffic / health / reconcile
// loops can iterate the Node list without ever needing a runtime
// IsSeparator() filter — replaces the v3.0.0-beta.6 design where a
// separator was a row in `nodes` with kind='separator' and a synthetic
// negative inbound_id.
//
// Visibility / position model (v3.0.0-rc.4):
//   - Mode=global:     always visible; position by SortOrder.
//   - Mode=node_bound: visible only when the rendered group's node list
//     contains at least one ID in NodeIDs. Position is still SortOrder.
//     NodeIDs only gates visibility, not placement.
//
// SortOrder shares the same integer scale as Node.SortOrder so admins can
// drag a separator into place between two real nodes in NodesView.
type SeparatorEntry struct {
	ID          int64
	DisplayName string
	SortOrder   int
	Enabled     bool
	Mode        SeparatorMode
	// NodeIDs is the relevant set of node IDs when Mode=node_bound. Empty
	// (with Mode=node_bound) means "never visible" — the explicit
	// hidden state, parallel to a node that's disabled.
	NodeIDs   []int64
	CreatedAt time.Time
}

// VisibleForNodes reports whether the separator should appear when the
// group being rendered contains the supplied node IDs. Encapsulates the
// global / node_bound precedence so callers don't reimplement it.
func (s *SeparatorEntry) VisibleForNodes(groupNodeIDs []int64) bool {
	if s == nil || !s.Enabled {
		return false
	}
	if s.Mode == SeparatorModeGlobal {
		return true
	}
	// node_bound: any intersection between NodeIDs and groupNodeIDs.
	if len(s.NodeIDs) == 0 || len(groupNodeIDs) == 0 {
		return false
	}
	wanted := make(map[int64]struct{}, len(s.NodeIDs))
	for _, id := range s.NodeIDs {
		wanted[id] = struct{}{}
	}
	for _, id := range groupNodeIDs {
		if _, ok := wanted[id]; ok {
			return true
		}
	}
	return false
}

// NodeHealthState classifies the outcome of the most recent health probe.
type NodeHealthState string

const (
	// NodeHealthUnknown is the initial value before any probe has run, and
	// also the value used when the node is disabled (we don't waste calls
	// on disabled nodes).
	NodeHealthUnknown NodeHealthState = ""
	// NodeHealthOK means the 3X-UI panel responded and the inbound exists
	// and is enabled.
	NodeHealthOK NodeHealthState = "ok"
	// NodeHealthPanelUnreachable means the 3X-UI panel itself didn't
	// respond (network, auth, or server-side error). The inbound's actual
	// state is unknown.
	NodeHealthPanelUnreachable NodeHealthState = "panel_unreachable"
	// NodeHealthInboundMissing means the panel responded but the inbound
	// ID is no longer present (deleted on the 3X-UI side).
	NodeHealthInboundMissing NodeHealthState = "inbound_missing"
	// NodeHealthInboundDisabled means the panel returned the inbound but
	// it's flagged off in 3X-UI — subscriptions will render it as a dead
	// proxy.
	NodeHealthInboundDisabled NodeHealthState = "inbound_disabled"
)

// NodeTrafficSnapshot is the per-node analogue of TrafficSnapshot: a
// monotonic lifetime value at one point in time. Raw inbound counters are kept
// on Node.LastTraffic* only as the baseline for the next delta calculation.
type NodeTrafficSnapshot struct {
	ID         int64
	NodeID     int64
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	CapturedAt time.Time
}

// HasTag reports whether the node carries an exact-match tag.
func (n *Node) HasTag(tag string) bool {
	for _, t := range n.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// XUIClientEntry is one row of the ownership table: it records which 3X-UI
// client a panel user owns. SyncSvc's write guard rejects any client write
// whose (PanelID, InboundID, ClientEmail) tuple does NOT appear here.
type XUIClientEntry struct {
	ID          int64
	UserID      int64
	PanelID     int64
	InboundID   int
	ClientEmail string
	ClientUUID  string
	CreatedAt   time.Time
	// Lifetime counters accumulate per-cycle deltas across 3X-UI counter
	// resets, mirroring the same fields on User / Node. Updated by the
	// traffic poll. Makes "top clients by all-time usage" a direct SQL
	// ORDER BY rather than a snapshot scan.
	LifetimeUpBytes    int64
	LifetimeDownBytes  int64
	LifetimeTotalBytes int64
	// LastRawXxx is the most recently observed raw 3X-UI cumulative counter,
	// used as the baseline for the next poll's monotonicDelta computation.
	// Zero on a fresh ownership row → the first poll treats the current
	// cumulative as the initial delta.
	LastRawUpBytes    int64
	LastRawDownBytes  int64
	LastRawTotalBytes int64
}

// TrafficSnapshot captures the monotonic lifetime traffic of a panel user at
// one point in time. The poll worker derives these values from per-client raw
// counter deltas so a reset on one inbound cannot hide growth on another.
type TrafficSnapshot struct {
	ID         int64
	UserID     int64
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	CapturedAt time.Time
}

// ClientTrafficSnapshot captures the raw cumulative counters for one managed
// 3X-UI client. User-level lifetime snapshots are derived from per-client
// deltas so a reset on one inbound cannot hide traffic on another inbound.
type ClientTrafficSnapshot struct {
	ID          int64
	UserID      int64
	PanelID     int64
	InboundID   int
	ClientEmail string
	UpBytes     int64
	DownBytes   int64
	TotalBytes  int64
	CapturedAt  time.Time
}

// SubLog records one subscription URL fetch for diagnostics.
type SubLog struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	UserUPN     string    `json:"user_upn,omitempty"`
	UserDisplay string    `json:"user_display,omitempty"`
	UserGroupID int64     `json:"user_group_id,omitempty"`
	IP          string    `json:"ip"`
	UA          string    `json:"ua"`
	ClientType  string    `json:"client_type"`
	AccessedAt  time.Time `json:"accessed_at"`
}

// AuditEntry is one immutable line in the admin audit log.
type AuditEntry struct {
	ID         int64     `json:"id"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	Target     string    `json:"target"`
	BeforeJSON string    `json:"before_json"`
	AfterJSON  string    `json:"after_json"`
	IP         string    `json:"ip"`
	At         time.Time `json:"at"`
}

// SyncTask is a persistent retryable operation that must change a 3X-UI
// panel before the panel-side state can be considered complete.
type SyncTask struct {
	ID         int64          `json:"id"`
	Type       SyncTaskType   `json:"type"`
	Status     SyncTaskStatus `json:"status"`
	TargetType string         `json:"target_type"`
	TargetID   int64          `json:"target_id"`
	Summary    string         `json:"summary"`
	Payload    string         `json:"payload"`
	LastError  string         `json:"last_error"`
	Attempts   int            `json:"attempts"`
	NextRunAt  time.Time      `json:"next_run_at"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	FinishedAt *time.Time     `json:"finished_at"`
}

// RuleSet is one rules shard stored in the DB.
type RuleSet struct {
	Slug            string
	Name            string
	Sort            int
	Enabled         bool
	ProxyGroupOrder []string
	Content         string // raw YAML rules fragment
}

// Template is one Clash/Sing-box config template stored under config/templates/.
type Template struct {
	Slug            string
	Name            string
	ClientType      ClientType
	IsDefault       bool
	RuleSets        []string
	ProxyGroupOrder []string
	Content         string // contains placeholders such as {{ proxies }}, {{ proxy_groups }}, {{ rules_common }}
}

// XUIPanel holds the connection credentials for one 3X-UI panel.
type XUIPanel struct {
	ID       int64
	Name     string
	URL      string
	APIToken string // preferred: Bearer token auth
	Username string // fallback: 3X-UI panel username/password cookie session
	Password string
	Remark   string
}

type MailReminderKind string

const (
	MailReminderExpireBefore     MailReminderKind = "expire_before"
	MailReminderExpired          MailReminderKind = "expired"
	MailReminderTrafficLow       MailReminderKind = "traffic_low"
	MailReminderTrafficExhausted MailReminderKind = "traffic_exhausted"
	MailReminderAccountDisable   MailReminderKind = "account_disabled"
	MailReminderAccountEnable    MailReminderKind = "account_enabled"
	MailReminderAnnouncement     MailReminderKind = "announcement"
	MailReminderBlockedClient    MailReminderKind = "blocked_client"
)

type MailSettings struct {
	Enabled      bool
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	FromEmail    string
	FromName     string
	Encryption   string // none | starttls | tls
}

type MailTemplate struct {
	Kind      MailReminderKind
	Subject   string
	Body      string
	Enabled   bool
	UpdatedAt time.Time
}

// EmailLog is a row from the mail_sent table joined with the recipient
// user — surfaced to admin's Logs → Email tab so an outgoing reminder
// has a verifiable audit trail (matches the SubLog / AuditEntry pattern).
// The (user_id, kind, window_key) trio comes from mail_sent's unique
// index so the same notification window only ever produces one log row.
type EmailLog struct {
	ID          int64            `json:"id"`
	UserID      int64            `json:"user_id"`
	UserUPN     string           `json:"user_upn,omitempty"`
	UserDisplay string           `json:"user_display,omitempty"`
	ToEmail     string           `json:"to_email"`
	Kind        MailReminderKind `json:"kind"`
	WindowKey   string           `json:"window_key"`
	SentAt      time.Time        `json:"sent_at"`
}
