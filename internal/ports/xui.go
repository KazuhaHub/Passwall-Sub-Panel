package ports

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// XUIClient is the abstract HTTP client for a single 3X-UI panel. The service
// layer never instantiates this directly — it routes through XUIPool by
// panel id.
type XUIClient interface {
	// Inbound CRUD
	ListInbounds(ctx context.Context) ([]Inbound, error)
	// ListInboundsSlim hits /panel/api/inbounds/list/slim — same per-inbound
	// shape and full clientStats (up/down/total/email/lastOnline/...) but with
	// settings.clients[] stripped to {email,enable} and clientStats not enriched
	// with uuid/subId. The traffic poll only consumes clientStats, so it uses
	// this to keep the response small on panels with thousands of clients. Do
	// NOT use it where settings.clients[] (uuid/flow/password) is needed —
	// ListInbounds returns the full payload for those callers.
	ListInboundsSlim(ctx context.Context) ([]Inbound, error)
	GetInbound(ctx context.Context, id int) (*Inbound, error)
	AddInbound(ctx context.Context, spec InboundSpec) (int, error)
	UpdateInbound(ctx context.Context, id int, spec InboundSpec) error
	DelInbound(ctx context.Context, id int) error
	SetInboundEnable(ctx context.Context, id int, enable bool) error

	// Client CRUD. Backed by 3X-UI 3.2.0's first-class /clients/* API, which
	// keys clients by their panel-wide unique email. inboundID / clientUUID
	// args are retained for source-compatibility but are vestigial — PSP's
	// per-node-unique email (u{userID}-n{nodeID}@domain) is the real key. See
	// docs/3xui-3.2-clients-migration.md.
	AddClient(ctx context.Context, inboundID int, spec ClientSpec) error
	UpdateClient(ctx context.Context, inboundID int, clientUUID string, spec ClientSpec) error
	// UpdateClientWithInbound predates 3.2.0, when it saved a GetInbound
	// round-trip for the read-modify-write update path. 3.2.0 updates clients
	// by email with no inbound read, so it now just delegates to UpdateClient;
	// the pre-fetched inb is unused. Kept so the traffic-poll push phase and
	// reconcile call sites don't churn. inb must NOT be nil.
	UpdateClientWithInbound(ctx context.Context, inb *Inbound, clientUUID string, spec ClientSpec) error
	DelClientByEmail(ctx context.Context, inboundID int, email string) error

	// GetClient fetches one client by its panel-wide unique email via
	// /panel/api/clients/get/{email}. Returns (nil, nil) when the panel has no
	// such client (3.2.x answers HTTP 200 + success:false + " (record not
	// found)"), so callers can treat absence as a normal end-state without an
	// error. ClientDetail.ID carries the client's uuid (the xray client id),
	// NOT the numeric DB row id. Replaces the old GetInboundClients +
	// scan-by-email: PSP's email is unique within a panel (it encodes the node),
	// so a by-email fetch is both sufficient and far cheaper than pulling a
	// whole inbound's client list to find one entry.
	GetClient(ctx context.Context, email string) (*ClientDetail, error)

	// BulkAddToInbound creates many clients on one inbound in a single
	// /panel/api/clients/bulkCreate call (one Xray restart instead of N). The
	// panel processes items sequentially and returns how many were created plus
	// per-email skip reasons — a duplicate email lands in Skipped with reason
	// "email already in use", which the caller adopts (mirrors the single-add
	// orphan-adoption path) rather than treating as a hard failure.
	BulkAddToInbound(ctx context.Context, inboundID int, specs []ClientSpec) (BulkAddResult, error)

	// BulkDelByEmail deletes many clients by their panel-wide email key in a
	// single /panel/api/clients/bulkDel call (one Xray restart instead of N).
	// keepTraffic is false — the xray traffic rows are dropped, matching
	// DelClientByEmail; PSP keeps its own accounting. Emails already absent
	// upstream are no-ops. Returns the count the panel reports as deleted.
	BulkDelByEmail(ctx context.Context, emails []string) (int, error)

	// GetServerStatus hits /panel/api/server/status. PSP only consumes the
	// version-identity subset (panel/xray) for compatibility checks; the rest
	// of the rich status payload (cpu/mem/etc.) is intentionally not surfaced
	// to keep the cross-process contract narrow.
	GetServerStatus(ctx context.Context) (*ServerStatus, error)

	// GetPanelUpdateInfo hits /panel/api/server/getPanelUpdateInfo —
	// returns the panel's current version + the latest 3X-UI release tag
	// reachable on GitHub + a "is there an update" flag. PSP uses
	// LatestVersion as the pre-flight gate before triggering UpdatePanel:
	// if the latest version exceeds PSP's MaxTestedXUI, the upgrade is
	// refused (admin needs to upgrade PSP first). 3X-UI's /updatePanel
	// has no version-selection knob — it always pulls latest — so this
	// is the only sane way to avoid auto-upgrading into a schema break
	// like the 2026-05-23 v3.1.0 inbound serialization change.
	GetPanelUpdateInfo(ctx context.Context) (*PanelUpdateInfo, error)

	// UpdatePanel triggers /panel/api/server/updatePanel — 3X-UI self-
	// updates to the latest GitHub release and restarts. The HTTP
	// connection drops mid-call as the panel binary exits; that is
	// normal, not an error. Callers should expect a network-side EOF /
	// reset and treat it as "upgrade initiated, verify reachability
	// after grace period". No version parameter — 3X-UI only knows how
	// to pull latest.
	UpdatePanel(ctx context.Context) error

	// InstallXray triggers /panel/api/server/installXray/:version. Pass
	// "latest" for the newest published xray-core release, or a specific
	// tag like "v25.10.31". 3X-UI restarts xray after install but does
	// NOT restart the panel itself, so unlike UpdatePanel this call
	// returns normally with the panel still running.
	InstallXray(ctx context.Context, version string) error

	// GetXrayVersionList hits /panel/api/server/getXrayVersion and returns
	// the xray-core tags the panel knows it can install (e.g. ["v25.10.31",
	// "v25.9.15", ...] — typically the recent N releases plus "latest").
	// Lets the admin Upgrade-Xray dialog populate a version dropdown so
	// admin can pin a specific tag instead of always taking "latest".
	GetXrayVersionList(ctx context.Context) ([]string, error)
}

// PanelUpdateInfo is the version pair returned by
// /panel/api/server/getPanelUpdateInfo. CurrentVersion is reported without a
// leading "v" ("3.1.0"); LatestVersion typically carries one ("v3.1.0"). Both
// go through version.parseSemver so the difference is normalized away.
type PanelUpdateInfo struct {
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
}

// ServerStatus is the version-identity subset of /panel/api/server/status.
// 3X-UI 3.1.0 status payload reports panelVersion as "3.1.0" (no leading "v")
// and xray.version as the bare semver of the xray-core binary.
type ServerStatus struct {
	PanelVersion string
	XrayVersion  string
	XrayState    string // "running" / "stop" / "error"
}

// ClientDetail is a normalised view of one client. ID carries the uuid (the
// xray client id used by VLESS/VMess and as the path key elsewhere), NOT the
// panel's numeric DB row id. Fields not applicable to the underlying protocol
// come back zero.
type ClientDetail struct {
	ID         string // uuid (VLESS / VMess) or empty for SS
	Email      string
	Enable     bool
	Flow       string
	Password   string // Trojan / SS / SS-2022 user PSK
	Auth       string // Hysteria2 per-client credential
	ExpiryTime int64
	TotalGB    int64
}

// BulkAddResult is the parsed obj of /panel/api/clients/bulkCreate. Created is
// the number of brand-new clients; Skipped lists the items the panel did not
// create, each with its reason (a duplicate email reports "email already in
// use"). The two together account for every requested item.
type BulkAddResult struct {
	Created int
	Skipped []BulkSkip
}

// BulkSkip is one entry the panel declined to create in a bulk call.
type BulkSkip struct {
	Email  string
	Reason string
}

// BulkClientAdd is one client to create-and-own in a batched enrollment (e.g.
// attaching a node and adding every eligible group member to its inbound). It
// lives in ports so the node service can describe a bulk request without
// importing the sync service. The protocol-specific secret is derived from
// UserUUID by the sync layer; callers only supply the universal fields.
type BulkClientAdd struct {
	UserID     int64
	Protocol   domain.Protocol
	SSMethod   string
	UserUUID   string
	Email      string
	Flow       string
	ExpireTime int64
	TotalGB    int64
}

// Inbound is the DTO returned by 3X-UI inbound endpoints. The Settings,
// StreamSettings, Sniffing and Allocate fields are JSON strings (not parsed
// here) because their shape varies by protocol.
type Inbound struct {
	ID             int
	Up             int64
	Down           int64
	Total          int64
	Remark         string
	Enable         bool
	ExpiryTime     int64
	Listen         string
	Port           int
	Protocol       string
	Settings       string
	StreamSettings string
	Tag            string
	Sniffing       string
	Allocate       string
	ClientStats    []ClientTraffic
}

// InboundSpec is the request payload for AddInbound / UpdateInbound.
type InboundSpec struct {
	Remark         string
	Enable         bool
	Listen         string
	Port           int
	Protocol       string
	Settings       string
	StreamSettings string
	Sniffing       string
	Allocate       string
	ExpiryTime     int64
}

// ClientSpec is the set of fields used when adding or updating a client.
// Field meaning depends on the inbound protocol:
//   - VLESS / VMess: ID holds the UUID (mapped to JSON "id" field)
//   - Trojan: Password holds the password
//   - Shadowsocks / SS-2022: Password holds the PSK
type ClientSpec struct {
	ID         string // UUID (VLESS/VMess)
	Email      string
	Enable     bool
	Flow       string // e.g. "xtls-rprx-vision"
	LimitIP    int
	TotalGB    int64 // bytes; panel manages traffic, keep this at 0
	ExpiryTime int64 // ms epoch; panel manages expiry, keep this at 0
	SubID      string
	TgID       string
	Reset      int

	// Protocol-specific
	Password string // Trojan / SS / SS-2022
	Method   string // SS / SS-2022 cipher
	Auth     string // Hysteria2 per-client credential (3X-UI's "auth" / client id)
}

// ClientTraffic is the per-client traffic entry returned by 3X-UI.
//
// LastOnline is unix-MILLISECONDS (3X-UI 3.1.0+ enrichment; zero on older
// panels). Kept as int64 so callers don't need to thread a time.Time
// through every aggregation pass — converted at display/storage sites only.
type ClientTraffic struct {
	ID         int
	InboundID  int
	Email      string
	Up         int64
	Down       int64
	Total      int64
	Enable     bool
	ExpiryTime int64
	Reset      int
	LastOnline int64
}

// XUIPool routes write/read calls to the appropriate 3X-UI client by stable
// panel id. Multi-panel deployments require all service code to go through Pool.Get
// rather than holding a XUIClient reference directly.
//
// Add / Remove are used by AdminServersHandler so the pool stays in lockstep
// with the persisted server list — adding a server immediately becomes
// usable without a panel restart.
type XUIPool interface {
	Get(panelID int64) (XUIClient, error)
	List() []*domain.XUIPanel
	Add(panel *domain.XUIPanel) error
	Remove(panelID int64) error
}
