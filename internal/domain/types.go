package domain

import "time"

// User is the panel-side logical user. One User maps to multiple 3X-UI clients
// (one per authorized inbound) via the ownership table.
type User struct {
	ID                 int64
	Username           string // unique for local accounts
	UPN                string // unique for SSO users (Entra ID UPN)
	Source             UserSource
	PasswordHash       string // bcrypt; only when Source == local
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
	Remark             string
	Enabled            bool
	AutoDisabledReason AutoDisabledReason
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// IsExpired reports whether ExpireAt is non-nil and earlier than t.
func (u *User) IsExpired(t time.Time) bool {
	return u.ExpireAt != nil && t.After(*u.ExpireAt)
}

// EmailForXUI returns the value used for 3X-UI client.email.
// SSO users use their UPN; local accounts use local_{username}@psp.local.
// Historically-imported clients keep their original email in the ownership
// table and do NOT go through this helper.
func (u *User) EmailForXUI() string {
	if u.Source == UserSourceSSO {
		return u.UPN
	}
	return "local_" + u.Username + "@psp.local"
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
// those live in 3X-UI and are fetched on demand.
//
// ServerAddress is the public hostname that clients dial. 3X-UI inbounds
// don't carry this on their own (their `listen` is a bind interface), so
// the panel records it explicitly here. Required for subscription rendering.
type Node struct {
	ID            int64
	PanelName     string
	InboundID     int
	DisplayName   string
	ServerAddress string
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
// whose (PanelName, InboundID, ClientEmail) tuple does NOT appear here.
type XUIClientEntry struct {
	ID          int64
	UserID      int64
	PanelName   string
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
	ID         int64
	UserID     int64
	IP         string
	UA         string
	ClientType string
	AccessedAt time.Time
}

// AuditEntry is one immutable line in the admin audit log.
type AuditEntry struct {
	ID         int64
	Actor      string
	Action     string
	Target     string
	BeforeJSON string
	AfterJSON  string
	IP         string
	At         time.Time
}

// RuleSet is one rules shard stored as a YAML file under config/rule_sets/.
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
	Content    string // contains placeholders such as {{ proxies }} and {{ rules_common }}
}

// XUIPanel holds the connection credentials for one 3X-UI panel.
type XUIPanel struct {
	Name     string
	URL      string
	APIToken string // preferred: Bearer token auth
	Username string // fallback: username/password + cookie session
	Password string
	Remark   string
}
