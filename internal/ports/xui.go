package ports

import (
	"context"
	"errors"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ErrXUIEndpointUnsupported is returned when a 3X-UI endpoint a client calls
// doesn't exist on the target panel's version (the route 404s) — e.g. a
// version-gated endpoint like getWebCertFiles (3.2.7+) against an older panel.
// Callers use errors.Is to degrade gracefully instead of surfacing a generic
// validation failure.
var ErrXUIEndpointUnsupported = errors.New("3X-UI endpoint unsupported on this panel version")

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
	// ListClientInbounds returns every client on the panel keyed by email, valued by
	// the inbound IDs it is attached to (one /list call). The shared-client orphan
	// reconcile uses it to find clients PSP no longer tracks in psp_client.
	ListClientInbounds(ctx context.Context) (map[string][]int, error)

	// BulkDelByEmail deletes many clients by their panel-wide email key in a
	// single /panel/api/clients/bulkDel call (one Xray restart instead of N).
	// keepTraffic is false — the xray traffic rows are dropped, matching
	// DelClientByEmail; PSP keeps its own accounting. Emails already absent
	// upstream are no-ops. Returns the count the panel reports as deleted.
	BulkDelByEmail(ctx context.Context, emails []string) (int, error)

	// --- v3.9.0 multi-inbound client surface (one client ↔ many inbounds) ---
	//
	// 3X-UI stores a client as a first-class row and projects it into the
	// settings.clients[] of every inbound it is attached to. These methods
	// expose that many-to-many directly so PSP can move from one-client-per-node
	// to one-client-per-(user,panel). All are LIVE-VERIFIED on 3.3.1 and present
	// since 3.1.0, so they are safe across PSP's whole supported range (≥3.2.0).
	// See docs/v3.9.0-client-multi-inbound.md.

	// AddClientToInbounds creates one first-class client and attaches it to
	// every id in inboundIDs in a single POST /panel/api/clients/add (one Xray
	// restart). The single-inbound AddClient is a thin wrapper over this. An
	// empty inboundIDs slice is an error — the panel needs at least one target.
	AddClientToInbounds(ctx context.Context, inboundIDs []int, spec ClientSpec) error

	// AttachClient attaches an EXISTING client (keyed by its panel-wide email)
	// to additional inbounds via POST /panel/api/clients/{email}/attach, body
	// {inboundIds}. Inbounds the client is already on are no-ops on the panel
	// side. An empty inboundIDs slice is a no-op (no request sent).
	AttachClient(ctx context.Context, email string, inboundIDs []int) error

	// DetachClient removes an existing client from the given inbounds via POST
	// /panel/api/clients/{email}/detach WITHOUT deleting the client record (it
	// survives even at zero inbounds — use DelClientByEmail for full removal).
	// (email, inbound) pairs where the client is not attached are silent no-ops.
	// An empty inboundIDs slice is a no-op.
	DetachClient(ctx context.Context, email string, inboundIDs []int) error

	// BulkAttach attaches many existing clients to many inbounds in one POST
	// /panel/api/clients/bulkAttach (single Xray restart). Returns per-email
	// done / skipped (already attached) / error lists. Empty emails or
	// inboundIDs is a no-op.
	BulkAttach(ctx context.Context, emails []string, inboundIDs []int) (BulkAttachResult, error)

	// BulkDetach detaches many clients from many inbounds in one POST
	// /panel/api/clients/bulkDetach (single Xray restart). Mirror of BulkAttach;
	// client records are kept even if orphaned. Empty inputs are a no-op.
	BulkDetach(ctx context.Context, emails []string, inboundIDs []int) (BulkAttachResult, error)

	// BulkCreateClients creates many NEW clients in one POST
	// /panel/api/clients/bulkCreate — body is a JSON array of {client, inboundIds},
	// the same per-item shape AddClientToInbounds accepts, processed server-side
	// with a SINGLE Xray restart regardless of fan-out. Use it to collapse a
	// per-user create loop (node provisioning / outage heal) into one call. Items
	// whose email already exists are skipped server-side (Created counts only the
	// real creates); pair with BulkAttach for the already-present ones. Empty
	// input is a no-op.
	BulkCreateClients(ctx context.Context, items []BulkCreateClientItem) (BulkCreateResult, error)

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

	// GetWebCertFiles hits /panel/api/server/getWebCertFiles — the panel's own
	// web TLS cert/key file PATHS (never the PEM bytes). 3X-UI 3.2.7+ only;
	// older panels have no such route and 404, which the adapter surfaces as
	// ErrXUIEndpointUnsupported. Backs the cert_source=from_panel flow: fill a
	// node-assigned inbound with file-mode paths that exist on the node.
	GetWebCertFiles(ctx context.Context) (*WebCertFiles, error)
}

// RealityScanner is the version-gated 3X-UI REALITY target discovery surface.
// It intentionally stays separate from XUIClient so historical test doubles and
// any out-of-tree adapters don't have to implement a capability that only exists
// on 3X-UI >= 3.4.2. node.Service type-asserts this interface after resolving the
// selected panel from XUIPool; the production xui.Client implements it.
type RealityScanner interface {
	ScanRealityTargets(ctx context.Context, targets string) ([]RealityScanResult, error)
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

// WebCertFiles is the obj of /panel/api/server/getWebCertFiles — filesystem
// PATHS on the panel host (e.g. /opt/1panel/secret/server.crt), never the
// certificate bytes. The cert must already exist on the node; PSP only learns
// where it lives so a node-assigned inbound can reference it in file mode.
type WebCertFiles struct {
	CertFile string // webCertFile
	KeyFile  string // webKeyFile
}

// RealityScanResult mirrors one item returned by
// POST /panel/api/server/scanRealityTargets. The probe is executed by the
// selected 3X-UI host (the same network vantage point as its Xray process), not
// by PSP. JSON tags deliberately follow 3X-UI's camelCase response contract.
type RealityScanResult struct {
	Target      string   `json:"target"`
	Host        string   `json:"host"`
	IP          string   `json:"ip"`
	Port        int      `json:"port"`
	Feasible    bool     `json:"feasible"`
	TLS13       bool     `json:"tls13"`
	TLSVersion  string   `json:"tlsVersion"`
	H2          bool     `json:"h2"`
	ALPN        string   `json:"alpn"`
	X25519      bool     `json:"x25519"`
	CurveID     string   `json:"curveID"`
	CertValid   bool     `json:"certValid"`
	CertSubject string   `json:"certSubject"`
	CertIssuer  string   `json:"certIssuer"`
	NotAfter    string   `json:"notAfter"`
	ServerNames []string `json:"serverNames"`
	LatencyMs   int      `json:"latencyMs"`
	Reason      string   `json:"reason"`
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
	// InboundIDs is the set of inbounds this client is currently attached to
	// (3X-UI's client_inbounds junction). For the legacy one-client-per-node
	// model this is a single id; the v3.9.0 shared-client model and reconcile's
	// attach/detach delta both read it to compare desired vs actual attachment.
	InboundIDs []int
}

// BulkAttachResult is the parsed obj of /panel/api/clients/bulkAttach and
// /bulkDetach. Done holds the emails the panel attached (bulkAttach) or
// detached (bulkDetach); Skipped lists emails already in the target state
// (already attached / not attached); Errors lists emails the panel failed on.
// The three together account for every requested email.
type BulkAttachResult struct {
	Done    []string
	Skipped []string
	Errors  []string
}

// BulkCreateClientItem is one entry in a BulkCreateClients call: a client spec
// plus the inbound IDs to attach it to (the same pairing AddClientToInbounds
// takes for a single client).
type BulkCreateClientItem struct {
	Spec       ClientSpec
	InboundIDs []int
}

// BulkCreateResult is the parsed obj of /panel/api/clients/bulkCreate. Created
// is the number of clients the panel actually created. (Callers partition
// create-vs-attach themselves from a prior client list, so the per-item skip
// reasons aren't surfaced here — anything missed is healed by the per-user
// resync backstop.)
type BulkCreateResult struct {
	Created int
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
