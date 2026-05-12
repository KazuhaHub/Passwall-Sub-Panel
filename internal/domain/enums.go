package domain

type UserSource string

const (
	UserSourceLocal UserSource = "local"
	UserSourceSSO   UserSource = "sso"
)

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
	ClientClash     ClientType = "clash"
	ClientClashMeta ClientType = "clash-meta"
	ClientSingBox   ClientType = "sing-box"
)
