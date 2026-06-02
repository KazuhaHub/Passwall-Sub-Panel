package domain

type Role string

const (
	RoleAdmin Role = "admin"
	// RoleOperator can do day-to-day user management (CRUD users, reset
	// credentials, view/edit traffic, run sync tasks) but is locked out of
	// integration credentials and system-wide settings (3X-UI panels, SMTP,
	// SAML/OIDC, the KV settings table, rule sets, templates, audit log clear). The
	// rationale: bring in a paid-tier "助理" to manage tenants without
	// handing over the panel's break-glass keys.
	RoleOperator Role = "operator"
	RoleUser     Role = "user"
)

type ResetPeriod string

const (
	ResetNever     ResetPeriod = "never"
	ResetMonthly   ResetPeriod = "monthly"
	ResetQuarterly ResetPeriod = "quarterly"
	ResetYearly    ResetPeriod = "yearly"
)

type AutoDisabledReason string

const (
	DisabledNone            AutoDisabledReason = ""
	DisabledTrafficExceeded AutoDisabledReason = "traffic_exceeded"
	DisabledExpired         AutoDisabledReason = "expired"
	DisabledManual          AutoDisabledReason = "manual"
	DisabledPendingDelete   AutoDisabledReason = "pending_delete"
	DisabledPendingApproval AutoDisabledReason = "pending_approval"
	DisabledBlockedClient   AutoDisabledReason = "blocked_client"
)

// SelfServiceDisableReason reports whether an auto-disabled user with this
// reason may still authenticate (log in AND refresh tokens) so they can reach
// the self-service emergency-access page and rescue themselves. Only
// traffic-exceeded and expired qualify; admin-disabled / pending / blocked
// users stay locked out. Single source of truth for the login path
// (user.VerifyLocalPassword) and the token-refresh path (auth handler) so they
// can't drift — a refresh that's stricter than login bounces these users back
// to the login screen every access-TTL.
func SelfServiceDisableReason(r AutoDisabledReason) bool {
	return r == DisabledTrafficExceeded || r == DisabledExpired
}

// Protocol identifies a 3X-UI inbound's protocol family.
// Used by pkg/crypto.DeriveProxyPassword to pick the right derivation rule.
type Protocol string

const (
	ProtoVLESS     Protocol = "vless"
	ProtoVMess     Protocol = "vmess"
	ProtoTrojan    Protocol = "trojan"
	ProtoSS        Protocol = "shadowsocks"
	ProtoSS2022    Protocol = "ss2022"
	ProtoHysteria2 Protocol = "hysteria2"
)

type ClientType string

const (
	ClientMihomo  ClientType = "mihomo"
	ClientSingBox ClientType = "sing-box"
	// ClientURIList is the base64-encoded list of proxy URIs (one per line)
	// that V2rayN, OpenWrt Passwall, Shadowrocket and most other "classic"
	// V2Ray-family clients consume as their subscription format. Carries
	// nodes only — routing rules live in the client because this format has
	// no concept of a ruleset.
	ClientURIList ClientType = "uri-list"
)

type SyncTaskType string

const (
	SyncTaskUserDelete     SyncTaskType = "user_delete"
	SyncTaskUserResync     SyncTaskType = "user_resync"
	SyncTaskUserPushConfig SyncTaskType = "user_push_config"
	SyncTaskNodeCreate     SyncTaskType = "node_create"
	SyncTaskNodeDelete     SyncTaskType = "node_delete"
	SyncTaskNodeSetEnabled SyncTaskType = "node_set_enabled"
	SyncTaskNodeUpdate     SyncTaskType = "node_update"
	SyncTaskMailNotify     SyncTaskType = "mail_notify"
)

type SyncTaskStatus string

const (
	SyncTaskPending   SyncTaskStatus = "pending"
	SyncTaskRunning   SyncTaskStatus = "running"
	SyncTaskSucceeded SyncTaskStatus = "succeeded"
	SyncTaskCanceled  SyncTaskStatus = "canceled"
)
