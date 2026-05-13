package domain

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type ResetPeriod string

const (
	ResetNever     ResetPeriod = "never"
	ResetMonthly   ResetPeriod = "monthly"
	ResetQuarterly ResetPeriod = "quarterly"
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

// Protocol identifies a 3X-UI inbound's protocol family.
// Used by pkg/crypto.DeriveProxyPassword to pick the right derivation rule.
type Protocol string

const (
	ProtoVLESS  Protocol = "vless"
	ProtoVMess  Protocol = "vmess"
	ProtoTrojan Protocol = "trojan"
	ProtoSS     Protocol = "shadowsocks"
	ProtoSS2022 Protocol = "ss2022"
)

type ClientType string

const (
	ClientMihomo  ClientType = "mihomo"
	ClientSingBox ClientType = "sing-box"
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
)

type SyncTaskStatus string

const (
	SyncTaskPending   SyncTaskStatus = "pending"
	SyncTaskRunning   SyncTaskStatus = "running"
	SyncTaskSucceeded SyncTaskStatus = "succeeded"
	SyncTaskCanceled  SyncTaskStatus = "canceled"
)
