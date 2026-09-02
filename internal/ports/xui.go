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

// ErrPanelCapabilityUnsupported means the selected adapter does not implement
// an optional operation. It is distinct from a version-gated endpoint missing
// on an otherwise capable backend.
var ErrPanelCapabilityUnsupported = errors.New("panel capability unsupported")

// PanelCapability is a stable, serialisable feature identifier. The admin API
// exposes these values so clients can hide actions a backend cannot perform.
type PanelCapability string

const (
	CapabilityInboundRead PanelCapability = "inbound.read"
	// CapabilityInboundWrite is the legacy aggregate retained for API
	// compatibility. New consumers should check the granular capabilities.
	CapabilityInboundWrite  PanelCapability = "inbound.write"
	CapabilityInboundCreate PanelCapability = "inbound.create"
	CapabilityInboundUpdate PanelCapability = "inbound.update"
	CapabilityInboundDelete PanelCapability = "inbound.delete"
	CapabilityInboundEnable PanelCapability = "inbound.enable"
	CapabilityClientRead    PanelCapability = "client.read"
	CapabilityClientWrite   PanelCapability = "client.write"
	CapabilityTrafficRead   PanelCapability = "traffic.read"
	CapabilityStatusRead    PanelCapability = "status.read"
	CapabilityPanelUpgrade  PanelCapability = "panel.upgrade"
	CapabilityCoreUpgrade   PanelCapability = "core.upgrade"
	CapabilityWebCertRead   PanelCapability = "webcert.read"
	CapabilityRealityScan   PanelCapability = "reality.scan"
	// CapabilityClientIPLimit — the panel enforces a per-client concurrent
	// source-IP cap (ClientSpec.LimitIP).
	CapabilityClientIPLimit PanelCapability = "client.iplimit"
	// CapabilityClientDeviceLimit — the panel enforces a per-client device
	// cap (ClientSpec.LimitHwid). Declared by an adapter whose protocol
	// carries the field; whether the PANEL BUILD honours it is a version
	// question (3X-UI 3.7.0+), tracked in docs/compat/v3.json, not here —
	// capabilities are static per adapter and cannot see a panel's version.
	CapabilityClientDeviceLimit PanelCapability = "client.devicelimit"
)

// CapabilityProvider is implemented by production adapters. It avoids
// guessing support merely because a compatibility method exists and returns
// ErrPanelCapabilityUnsupported.
type CapabilityProvider interface {
	Capabilities() []PanelCapability
}

// SupportsCapability returns true for legacy/test implementations that do not
// expose a capability list, preserving source compatibility. Production
// adapters implement CapabilityProvider and are checked exactly.
func SupportsCapability(client any, capability PanelCapability) bool {
	provider, ok := client.(CapabilityProvider)
	if !ok {
		return true
	}
	for _, item := range provider.Capabilities() {
		if item == capability {
			return true
		}
	}
	return false
}

// PanelClient is the data-plane contract for one upstream panel. The service
// layer never instantiates an adapter directly — it routes through PanelPool
// by panel id.
type PanelClient interface {
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
	// keys clients by their panel-wide unique email. AddClient still needs an
	// inbound because a create has to land somewhere; every other operation is
	// keyed by spec.Email alone. See docs/3xui-3.2-clients-migration.md.
	AddClient(ctx context.Context, inboundID int, spec ClientSpec) error
	UpdateClient(ctx context.Context, spec ClientSpec) error
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
	// BulkSetEnabled flips the enable flag for many clients in ONE call, so a
	// fan-out that used to cost N /clients/update writes (and N xray reloads on
	// the same panel) costs one. It ONLY moves the enable flag — unlike
	// UpdateClient it does not re-push credentials — so use it for pure state
	// transitions (quota suspend/resume) and keep the full write for paths that
	// actually change configuration.
	BulkSetEnabled(ctx context.Context, emails []string, enable bool) (BulkSetEnabledResult, error)

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

	// UpdatePanel triggers /panel/api/server/updatePanel — 3X-UI self-
	// updates to the latest GitHub release and restarts. The HTTP
	// connection drops mid-call as the panel binary exits; that is
	// normal, not an error. Callers should expect a network-side EOF /
	// reset and treat it as "upgrade initiated, verify reachability
	// after grace period". No version parameter — 3X-UI only knows how
	// to pull latest.

	// InstallXray triggers /panel/api/server/installXray/:version. Pass
	// "latest" for the newest published xray-core release, or a specific
	// tag like "v25.10.31". 3X-UI restarts xray after install but does
	// NOT restart the panel itself, so unlike UpdatePanel this call
	// returns normally with the panel still running.

	// GetXrayVersionList hits /panel/api/server/getXrayVersion and returns
	// the xray-core tags the panel knows it can install (e.g. ["v25.10.31",
	// "v25.9.15", ...] — typically the recent N releases plus "latest").
	// Lets the admin Upgrade-Xray dialog populate a version dropdown so
	// admin can pin a specific tag instead of always taking "latest".

	// GetWebCertFiles hits /panel/api/server/getWebCertFiles — the panel's own
	// web TLS cert/key file PATHS (never the PEM bytes). 3X-UI 3.2.7+ only;
	// older panels have no such route and 404, which the adapter surfaces as
	// ErrXUIEndpointUnsupported. Backs the cert_source=from_panel flow: fill a
	// node-assigned inbound with file-mode paths that exist on the node.
}

// XUIClient is a source-compatibility alias. New services should depend on
// PanelClient/PanelPool and optional capabilities instead.
type XUIClient = PanelClient

// PanelUpdater is the optional panel self-update capability.
type PanelUpdater interface {
	GetPanelUpdateInfo(ctx context.Context) (*PanelUpdateInfo, error)
	UpdatePanel(ctx context.Context) error
}

// CoreUpdater is the vendor-neutral optional core upgrade capability. A
// 3X-UI adapter maps this to xray-core; another adapter may map it to sing-box.
type CoreUpdater interface {
	GetCoreVersionList(ctx context.Context) ([]string, error)
	InstallCore(ctx context.Context, version string) error
}

// WebCertProvider exposes certificate file paths from the upstream host.
type WebCertProvider interface {
	GetWebCertFiles(ctx context.Context) (*WebCertFiles, error)
}

// LiveIPReader is the optional "who is connected right now" capability.
//
// It answers a question no single panel can: a user's credentials are split
// across panels, and each panel counts only its own. PSP holds the
// email→user mapping, so it is the only place the per-USER total exists.
// The panel-side caps are per client email per panel and are multiplied by
// both — see docs/connection-limits.md §11.
//
// Source IPs, not devices. The data plane sees connections and source
// addresses; it has no device concept. One household behind one NAT reads
// as 1, one phone moving between wifi and cellular reads as 2. A device
// identity exists only at the subscription layer. Anything built on this
// must say "IP", never "device", or it will promise something it cannot
// deliver — the exact mistake that let the device cap look enforced.
type LiveIPReader interface {
	// ListLiveClientIPs returns each client email's currently-live source
	// IPs on this panel. Upstream applies its own staleness window, so an
	// email with no live connections is absent rather than empty.
	ListLiveClientIPs(ctx context.Context) (map[string][]string, error)
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
	// LimitIP / LimitHwid mirror ClientSpec's caps. Read back so the push
	// path's compare-then-write can tell a drifted cap from an unchanged one
	// and heal it, the same way it does for enable/expiry/quota.
	LimitIP   int
	LimitHwid int
	// Comment is the operator's free-text note on the client, set in the 3X-UI
	// UI. PSP has no concept of it and never sets one — it is read back solely
	// so an update can hand it straight back instead of blanking it. See
	// ClientSpec.Comment.
	Comment string
	// Group is the operator's 3X-UI client-list label, same story as Comment.
	Group string
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
// BulkSetEnabledSkip is one email the panel declined to flip, with its reason.
type BulkSetEnabledSkip struct {
	Email  string
	Reason string
}

// BulkSetEnabledResult reports what a bulk enable/disable actually did.
// Skipped is load-bearing: the panel answers success while silently declining
// individual emails (a client that no longer exists), so a caller that read
// "no error" as "every email flipped" would mark users synced that the panel
// never touched.
type BulkSetEnabledResult struct {
	Changed int
	Skipped []BulkSetEnabledSkip
}

type BulkCreateResult struct {
	Created int
}

// Inbound is the normalized panel DTO. Settings, StreamSettings, Sniffing and
// Allocate retain the historical Xray-shaped JSON strings at the API boundary
// so existing clients remain compatible; adapters translate as needed.
type Inbound struct {
	ID         int
	Up         int64
	Down       int64
	Total      int64
	Remark     string
	Enable     bool
	ExpiryTime int64
	// SubSortIndex orders this inbound's links in the PANEL's own subscription
	// output (1-based, lower first, ties by id). PSP renders its own
	// subscriptions and never consumes it — it is decoded solely so
	// UpdateInbound can echo the operator's value back instead of flattening
	// every PSP-touched inbound to rank 1. Zero on a panel that predates it.
	SubSortIndex   int
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
	ID     string // UUID (VLESS/VMess)
	Email  string
	Enable bool
	Flow   string // e.g. "xtls-rprx-vision"
	// LimitIP caps the number of distinct source IPs the client may use
	// concurrently; 0 is unlimited. PSP owns the value — see
	// docs/connection-limits.md.
	LimitIP int
	// LimitHwid is 3X-UI's per-subscription device cap; 0 is unlimited.
	//
	// IT DOES NOT ENFORCE ANYTHING IN PSP'S ARCHITECTURE, on any panel
	// version. Upstream reads limit_hwid for a decision in exactly one
	// place — effectiveHwidLimitForSubID, reached only from
	// EnforceHwidForSubID, called only from 3X-UI's OWN subscription
	// controller. Devices register when a client app fetches the PANEL's
	// subscription URL. PSP serves its own subscriptions, so that endpoint
	// is never hit by a PSP user, client_hwids stays empty for
	// PSP-managed clients, and the cap is stored and then ignored.
	//
	// Keep sending it anyway. It costs one integer, it is correct if a
	// deployment ever exposes the panel's own subscription, and omitting
	// the key is actively harmful: upstream binds a missing key to 0, so
	// every push would silently clear a cap an operator set by hand.
	//
	// Not an upstream defect — 3X-UI enforces at the point it serves. The
	// mismatch is ours: PSP took over the subscription and expected
	// enforcement to stay behind. A cap that is per USER can only live at
	// PSP's own subscription endpoint, which is the one place that sees
	// every fetch by one user across every credential partition and panel.
	// See docs/connection-limits.md §4.1.
	//
	// PSP sends its OWN intended value, never an echo. An update that
	// echoes back a value read moments earlier feeds a stale limit into
	// the panel's trimClientHwidsForSubID, which DELETES device
	// registrations beyond it — so a concurrent cap change or registration
	// inside the read-write window destroyed rows permanently. Sending an
	// intent has no read-modify-write window. That hazard is real and the
	// stance is right; it is simply moot until something populates the
	// registry this trims.
	LimitHwid  int
	TotalGB    int64 // bytes; panel manages traffic, keep this at 0
	ExpiryTime int64 // ms epoch; panel manages expiry, keep this at 0
	SubID      string
	TgID       string
	Reset      int

	// Comment carries the operator's 3X-UI note back through an update.
	// PSP never authors one: upstream writes clients.comment unconditionally
	// from the request body, so an update that omits the key blanks whatever
	// the operator typed. Callers that already read the client (they get it
	// from ClientDetail.Comment) set this so the value survives; callers that
	// do not leave it empty, the key is omitted, and the pre-existing blanking
	// stands — no path is made worse by not knowing it.
	Comment string
	// Group is the operator's 3X-UI client-list grouping label. Same contract as
	// Comment, and wiped by the same mechanism for a subtler reason: upstream's
	// merge DOES guard it, but ClientService.Update then writes group_name
	// unconditionally in a separate statement, deliberately, because the 3X-UI
	// client editor always round-trips the field and clearing it had to work.
	// PSP does not round-trip it, so the guard never protects PSP's updates.
	// It is a label for the panel's own client list — no job, enforcement, xray
	// config or subscription behaviour reads it.
	Group string

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

// PanelPool routes calls to the appropriate adapter by stable panel id.
// Multi-panel deployments require service code to go through Pool.Get rather
// than holding a concrete adapter directly.
//
// Add / Remove are used by AdminServersHandler so the pool stays in lockstep
// with the persisted server list — adding a server immediately becomes
// usable without a panel restart.
type PanelPool interface {
	Get(panelID int64) (PanelClient, error)
	List() []*domain.Panel
	Add(panel *domain.Panel) error
	Remove(panelID int64) error
}

// PanelKindValidator is an optional pool capability used by the admin API to
// reject unknown adapter kinds before persisting a panel row.
type PanelKindValidator interface {
	SupportsKind(kind domain.PanelKind) bool
}

// XUIPool is a source-compatibility alias for the generic pool contract.
type XUIPool = PanelPool
