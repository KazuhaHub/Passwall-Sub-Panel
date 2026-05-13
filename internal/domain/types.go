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

// User is the panel-side logical user. One User maps to multiple 3X-UI clients
// (one per authorized inbound) via the ownership table.
type User struct {
	ID                 int64
	UPN                string // unique account identifier for both local and SSO users
	Email              string // notification recipient; SSO uses the Email claim, not UPN
	PasswordHash       string // bcrypt; present when the account has local-password login
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
	DisableDetail      string
	// BlockViolationCount tracks how many times the user attempted to use a blocked subscription client.
	BlockViolationCount int
	EmergencyUsedCount int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// IsExpired reports whether ExpireAt is non-nil and earlier than t.
func (u *User) IsExpired(t time.Time) bool {
	return u.ExpireAt != nil && t.After(*u.ExpireAt)
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
//   - All=true matches every node
//   - otherwise Tags is an AND-combination of entries like
//     "region:TW", "tag:reality", "server:tw-hinet"
type TagFilter struct {
	All  bool
	Tags []string
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
type Node struct {
	ID            int64
	PanelID       int64
	PanelName     string // display/cache only; PanelID is the stable reference
	InboundID     int
	DisplayName   string
	ServerAddress string
	Flow          string
	Region        string
	Tags          []string
	SortOrder     int
	Enabled       bool
	CreatedAt     time.Time
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
	PanelName   string // display/cache only; PanelID is the stable reference
	InboundID   int
	ClientEmail string
	ClientUUID  string
	CreatedAt   time.Time
}

// TrafficSnapshot captures the cumulative traffic of a panel user at one
// point in time (aggregated across all owned clients via 3X-UI's
// getClientTraffics).
type TrafficSnapshot struct {
	ID         int64
	UserID     int64
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	CapturedAt time.Time
}

// SubLog records one subscription URL fetch for diagnostics.
type SubLog struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	UserUPN      string    `json:"user_upn,omitempty"`
	UserDisplay  string    `json:"user_display,omitempty"`
	UserGroupID  int64     `json:"user_group_id,omitempty"`
	IP           string    `json:"ip"`
	UA           string    `json:"ua"`
	ClientType   string    `json:"client_type"`
	AccessedAt   time.Time `json:"accessed_at"`
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
	Slug    string
	Name    string
	Sort    int
	Enabled bool
	Content string // raw YAML rules fragment
}

// Template is one Clash/Sing-box config template stored under config/templates/.
type Template struct {
	Slug       string
	Name       string
	ClientType ClientType
	IsDefault  bool
	RuleSets   []string
	Content    string // contains placeholders such as {{ proxies }}, {{ proxy_groups }}, {{ rules_common }}
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
	MailReminderExpireBefore   MailReminderKind = "expire_before"
	MailReminderExpired        MailReminderKind = "expired"
	MailReminderTrafficLow     MailReminderKind = "traffic_low"
	MailReminderAccountDisable MailReminderKind = "account_disabled"
	MailReminderAccountEnable  MailReminderKind = "account_enabled"
)

type MailSettings struct {
	Enabled              bool
	SMTPHost             string
	SMTPPort             int
	SMTPUsername         string
	SMTPPassword         string
	FromEmail            string
	FromName             string
	Encryption           string // none | starttls | tls
	ExpireBeforeDays     int
	TrafficRemainPercent int
}

type MailTemplate struct {
	Kind      MailReminderKind
	Subject   string
	Body      string
	Enabled   bool
	UpdatedAt time.Time
}
