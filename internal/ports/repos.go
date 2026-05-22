// Package ports defines the abstract interfaces that service-layer code
// depends on (the "ports" in hexagonal architecture).
//
// The service layer imports only this package; the concrete implementations
// live in adapters/{mysql,yaml,xui}. This separation keeps business logic
// decoupled from storage choices and external systems, and makes services
// trivially mockable in unit tests.
package ports

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ---- Common filter types ----

type Pagination struct {
	Page     int
	PageSize int
}

type UserFilter struct {
	Pagination
	Search  string
	GroupID *int64
	Role    *domain.Role
	Enabled *bool
}

type AuditFilter struct {
	Pagination
	Actor  string
	Action string
	// Search is a case-insensitive substring matched across actor / action /
	// target. When set it's the only text filter the UI sends (Actor/Action
	// stay for API back-compat / programmatic callers).
	Search string
	Since  *time.Time
	Until  *time.Time
}

type SubLogFilter struct {
	Pagination
	UserID *int64
	// Search: case-insensitive substring across ip / ua / client_type and the
	// joined user's upn / display_name.
	Search string
	Since  *time.Time
	Until  *time.Time
}

type SyncTaskFilter struct {
	Pagination
	Status *domain.SyncTaskStatus
	Type   *domain.SyncTaskType
}

// ---- Repository interfaces ----

type UserRepo interface {
	Create(ctx context.Context, u *domain.User) error
	Update(ctx context.Context, u *domain.User) error
	// UpdateTrafficState persists ONLY the traffic-poll-owned columns
	// (lifetime counters, period baseline/start, emergency window). The
	// traffic poll loads every user at cycle start and writes them back many
	// seconds later; a full-row Update would clobber any concurrent admin /
	// self-service edit (password, group, role, expiry, sub_token) made in
	// that window. This narrow write touches only the columns the poll owns.
	UpdateTrafficState(ctx context.Context, u *domain.User) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	GetByUPN(ctx context.Context, upn string) (*domain.User, error)
	// GetBySSO is the lookup the SSO login path uses — composite match on
	// (sso_provider, sso_subject). Decoupled from GetByUPN so an admin
	// changing a user's UPN in the IdP can never reroute the SSO
	// assertion to a different panel row. Returns domain.ErrNotFound
	// when no row carries the requested external identity.
	GetBySSO(ctx context.Context, provider, subject string) (*domain.User, error)
	GetBySubToken(ctx context.Context, token string) (*domain.User, error)
	List(ctx context.Context, filter UserFilter) (items []*domain.User, total int64, err error)
	ListByGroup(ctx context.Context, groupID int64) ([]*domain.User, error)
}

type GroupRepo interface {
	Create(ctx context.Context, g *domain.Group) error
	Update(ctx context.Context, g *domain.Group) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.Group, error)
	GetBySlug(ctx context.Context, slug string) (*domain.Group, error)
	List(ctx context.Context) ([]*domain.Group, error)
	CountMembers(ctx context.Context, id int64) (int64, error)
}

type NodeRepo interface {
	Create(ctx context.Context, n *domain.Node) error
	Update(ctx context.Context, n *domain.Node) error
	// UpdateTrafficCounters / UpdateHealth are column-scoped writes used by
	// the traffic poll and the health checker respectively. They run on
	// separate goroutines against the same row, so each must touch only its
	// own columns instead of a full-row Save that would clobber the other.
	UpdateTrafficCounters(ctx context.Context, n *domain.Node) error
	UpdateHealth(ctx context.Context, n *domain.Node) error
	// BatchUpdateSortOrder rewrites Node.SortOrder for every (id, sort_order)
	// pair in a single transaction. Driven by the drag-to-reorder UI in the
	// admin node list — a one-shot N-row update is cheaper than N round-trips
	// of Update and keeps the table consistent if the call is interrupted
	// mid-flight.
	BatchUpdateSortOrder(ctx context.Context, updates []NodeSortUpdate) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.Node, error)
	GetByPanelInbound(ctx context.Context, panelID int64, inboundID int) (*domain.Node, error)
	List(ctx context.Context) ([]*domain.Node, error)
	ListEnabled(ctx context.Context) ([]*domain.Node, error)
}

// NodeSortUpdate is one (node_id, sort_order) pair for BatchUpdateSortOrder.
type NodeSortUpdate struct {
	NodeID    int64
	SortOrder int
}

// SeparatorRepo backs the dedicated `nodes_separator` table introduced in
// v3.0.0-beta.7 (see docs/ARCHITECTURE.md §16.4 / domain.SeparatorEntry).
// Trivial CRUD — separators have no business state beyond the row itself,
// so the service layer keeps these methods as 1-to-1 pass-throughs onto
// node.Service rather than building a separate package.
type SeparatorRepo interface {
	Create(ctx context.Context, s *domain.SeparatorEntry) error
	Update(ctx context.Context, s *domain.SeparatorEntry) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.SeparatorEntry, error)
	List(ctx context.Context) ([]*domain.SeparatorEntry, error)
	// ListEnabled is the render-time hot path: returns rows with
	// enabled=true sorted by sort_order ascending. The render layer
	// filters per-group on top via SeparatorEntry.VisibleForNodes.
	ListEnabled(ctx context.Context) ([]*domain.SeparatorEntry, error)
	// BatchUpdateSortOrder rewrites sort_order for every listed
	// separator in one transaction. Powers the drag-to-reorder bar
	// (separator-only — node reorder is on NodeRepo).
	BatchUpdateSortOrder(ctx context.Context, updates []SeparatorSortUpdate) error
}

// SeparatorSortUpdate mirrors NodeSortUpdate for the separator side of
// the reorder API split.
type SeparatorSortUpdate struct {
	SeparatorID int64
	SortOrder   int
}

type OwnershipRepo interface {
	Add(ctx context.Context, e *domain.XUIClientEntry) error
	Remove(ctx context.Context, id int64) error
	RemoveByMatch(ctx context.Context, panelID int64, inboundID int, email string) error
	GetByMatch(ctx context.Context, panelID int64, inboundID int, email string) (*domain.XUIClientEntry, error)
	ListByUser(ctx context.Context, userID int64) ([]*domain.XUIClientEntry, error)
	ListByInbound(ctx context.Context, panelID int64, inboundID int) ([]*domain.XUIClientEntry, error)
	Exists(ctx context.Context, panelID int64, inboundID int, email string) (bool, error)
	// UpdateUUID rewrites client_uuid for the row identified by the unique
	// (panel_id, inbound, email) triple. Used by the UUID-rotation flow so the
	// ownership table tracks the same uuid that's now in 3X-UI.
	UpdateUUID(ctx context.Context, panelID int64, inboundID int, email, newUUID string) error
	// UpdateCounters persists the LifetimeXxx + LastRawXxx fields for one
	// ownership row. Called by the traffic poll once per cycle per client so
	// the next cycle's monotonicDelta has a fresh baseline. Updates only the
	// counter columns to keep the write narrow.
	UpdateCounters(ctx context.Context, e *domain.XUIClientEntry) error
}

type TrafficRepo interface {
	Insert(ctx context.Context, s *domain.TrafficSnapshot) error
	LatestForUser(ctx context.Context, userID int64) (*domain.TrafficSnapshot, error)
	LastBefore(ctx context.Context, userID int64, before time.Time) (*domain.TrafficSnapshot, error)
	ListByUser(ctx context.Context, userID int64, since, until time.Time) ([]*domain.TrafficSnapshot, error)
	InsertClient(ctx context.Context, s *domain.ClientTrafficSnapshot) error
	// InsertBatch / InsertClientBatch consolidate per-poll snapshot writes
	// into a single SQL roundtrip (GORM CreateInBatches). Used by
	// PollOnce's end-of-cycle flush; per-event callers stay on the
	// single-row Insert/InsertClient methods.
	InsertBatch(ctx context.Context, snaps []*domain.TrafficSnapshot) error
	InsertClientBatch(ctx context.Context, snaps []*domain.ClientTrafficSnapshot) error
	// PruneBefore deletes rows from both traffic_snapshots and
	// client_traffic_snapshots older than cutoff. Mirrors the AuditRepo /
	// SubLogRepo retention pattern. Returns deleted row count summed across
	// both tables.
	PruneBefore(ctx context.Context, cutoff time.Time) (int64, error)
	// PruneHourlyBefore deletes rows from traffic_snapshots_hourly and
	// client_traffic_snapshots_hourly older than cutoff. The raw and hourly
	// tables get different retentions (raw covers "today + buffer", hourly
	// covers the admin-tunable chart depth), so they prune through separate
	// methods.
	PruneHourlyBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

type NodeTrafficRepo interface {
	Insert(ctx context.Context, s *domain.NodeTrafficSnapshot) error
	LatestForNode(ctx context.Context, nodeID int64) (*domain.NodeTrafficSnapshot, error)
	LastBefore(ctx context.Context, nodeID int64, before time.Time) (*domain.NodeTrafficSnapshot, error)
	ListByNode(ctx context.Context, nodeID int64, since, until time.Time) ([]*domain.NodeTrafficSnapshot, error)
	// InsertBatch consolidates per-poll node snapshot writes; mirrors the
	// TrafficRepo equivalent.
	InsertBatch(ctx context.Context, snaps []*domain.NodeTrafficSnapshot) error
	// PruneBefore deletes node_traffic_snapshots rows older than cutoff.
	PruneBefore(ctx context.Context, cutoff time.Time) (int64, error)
	// PruneHourlyBefore deletes node_traffic_snapshots_hourly rows older
	// than cutoff. See TrafficRepo.PruneHourlyBefore for the rationale.
	PruneHourlyBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

type AuditRepo interface {
	Insert(ctx context.Context, e *domain.AuditEntry) error
	List(ctx context.Context, filter AuditFilter) (items []*domain.AuditEntry, total int64, err error)
	Clear(ctx context.Context) error
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

type SubLogRepo interface {
	Insert(ctx context.Context, log *domain.SubLog) error
	List(ctx context.Context, filter SubLogFilter) (items []*domain.SubLog, total int64, err error)
	Clear(ctx context.Context) error
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

type SyncTaskRepo interface {
	Create(ctx context.Context, task *domain.SyncTask) error
	GetByID(ctx context.Context, id int64) (*domain.SyncTask, error)
	GetActiveByTarget(ctx context.Context, typ domain.SyncTaskType, targetType string, targetID int64) (*domain.SyncTask, error)
	// HasActiveByTargetAny reports whether any task of the given types is
	// pending/running for the target. Single round-trip alternative to
	// looping GetActiveByTarget per type.
	HasActiveByTargetAny(ctx context.Context, types []domain.SyncTaskType, targetType string, targetID int64) (bool, error)
	List(ctx context.Context, filter SyncTaskFilter) (items []*domain.SyncTask, total int64, err error)
	ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.SyncTask, error)
	MarkRunning(ctx context.Context, id int64) error
	MarkSucceeded(ctx context.Context, id int64) error
	MarkRetry(ctx context.Context, id int64, lastError string, nextRunAt time.Time) error
	Cancel(ctx context.Context, id int64) error
	RetryNow(ctx context.Context, id int64) error
	ResetRunning(ctx context.Context) error
	// DeleteSucceededBefore prunes succeeded tasks finished before the cutoff.
	// Pending/running/canceled/retrying tasks are left alone — those are
	// either still working or worth keeping for forensic visibility.
	DeleteSucceededBefore(ctx context.Context, cutoff time.Time) (int64, error)
	// DeleteFinished wipes every non-active task (anything not pending or
	// running). Powers the admin's one-click "clear sync tasks" button.
	DeleteFinished(ctx context.Context) (int64, error)
}

type RuleSetRepo interface {
	List(ctx context.Context) ([]*domain.RuleSet, error)
	GetBySlug(ctx context.Context, slug string) (*domain.RuleSet, error)
	Save(ctx context.Context, r *domain.RuleSet) error
	Delete(ctx context.Context, slug string) error
}

type TemplateRepo interface {
	List(ctx context.Context) ([]*domain.Template, error)
	GetBySlug(ctx context.Context, slug string) (*domain.Template, error)
	GetDefault(ctx context.Context, clientType domain.ClientType) (*domain.Template, error)
	Save(ctx context.Context, t *domain.Template) error
	Delete(ctx context.Context, slug string) error
}

type XUIPanelRepo interface {
	List(ctx context.Context) ([]*domain.XUIPanel, error)
	GetByID(ctx context.Context, id int64) (*domain.XUIPanel, error)
	GetByName(ctx context.Context, name string) (*domain.XUIPanel, error)
	Save(ctx context.Context, panel *domain.XUIPanel) error
	Delete(ctx context.Context, id int64) error
}

// UISettings holds runtime-editable UI preferences. They live in the DB so
// admin edits don't touch infrastructure fields.
type UISettings struct {
	LoginMode string `yaml:"login_mode" json:"login_mode"`
	// SiteTitle is the brand name displayed in the sidebar, header, and
	// login page. Defaults to "Kazuha Hub Passwall".
	SiteTitle string `yaml:"site_title" json:"site_title"`
	// AppTitle is the application/product name shown next to the logo in
	// the top-left brand area and on login pages. Defaults to "Passwall".
	AppTitle string `yaml:"app_title" json:"app_title"`
	// IconURL is the browser tab / PWA icon URL. Empty falls back to the
	// bundled avatar-style icon.
	IconURL string `yaml:"icon_url" json:"icon_url"`
	// LogoURL is a URL to a custom logo image for light backgrounds.
	// When empty, the bundled default logo is used.
	LogoURL string `yaml:"logo_url" json:"logo_url"`
	// LogoURLDark is a URL to a custom logo for dark backgrounds.
	// Falls back to LogoURL if empty.
	LogoURLDark string `yaml:"logo_url_dark" json:"logo_url_dark"`
	// EmailDomain is the suffix used when building the 3X-UI client.email
	// for every panel user (local + SSO, no distinction). Format is
	// "u<userID>-n<nodeID>@<domain>". Defaults to "psp.local".
	EmailDomain string `yaml:"email_domain" json:"email_domain"`
	// AuditRetentionDays controls automatic audit cleanup. 0 means never
	// delete audit entries automatically.
	AuditRetentionDays int `yaml:"audit_retention_days" json:"audit_retention_days"`
	// SubBaseURL is the panel's public base URL used to render absolute
	// subscription URLs ("<base>/sub/<token>"). Empty falls back to relative
	// paths.
	SubBaseURL string `yaml:"sub_base_url" json:"sub_base_url"`
	// Timezone is the IANA name (e.g. "Asia/Shanghai", "America/Los_Angeles")
	// used for system-level time calculations: monthly/quarterly traffic
	// resets, user expire_at "X days from now" math, and as the default
	// timezone for the admin traffic chart. Empty falls back to the server
	// process's time.Local so existing installs keep working unchanged.
	// User-facing views (subscription page, /user/me) stay on the browser's
	// timezone — this knob is only for the system "calendar day" boundary.
	Timezone string `yaml:"timezone" json:"timezone"`

	// ---- Runtime tuning (restart required for changes to take effect) ----
	// Background cron intervals; minutes. 0 keeps the previous default.
	CronTrafficPullMinutes int `json:"cron_traffic_pull_minutes"`
	CronReconcileMinutes   int `json:"cron_reconcile_minutes"`

	// MaxPanelConcurrency caps the fan-out of concurrent ListInbounds
	// calls during traffic poll + reconcile (v2.2.5 perf path). 0 or
	// negative falls back to the built-in default (8) so existing
	// installs keep working with no settings touched. Values larger
	// than 64 are clamped down to avoid accidentally slamming 3X-UI
	// with simultaneous HTTP requests on a misconfigured admin. Bigger
	// only helps when the deployment has many panels (10+) AND 3X-UI
	// can handle the parallel load; smaller is useful when 3X-UI is
	// resource-constrained or shares a CPU with the panel itself.
	// Dynamic — picked up at the start of each poll/reconcile cycle.
	MaxPanelConcurrency int `json:"max_panel_concurrency"`

	// JWT signing parameters. AccessTTL/RefreshTTL in minutes; Issuer is the
	// "iss" claim.
	JWTAccessTTLMinutes  int    `json:"jwt_access_ttl_minutes"`
	JWTRefreshTTLMinutes int    `json:"jwt_refresh_ttl_minutes"`
	JWTIssuer            string `json:"jwt_issuer"`

	// Per-IP rate limits, requests per minute.
	SubPerIPPerMin   int `json:"sub_per_ip_per_min"`
	LoginPerIPPerMin int `json:"login_per_ip_per_min"`

	// Sync-task retention in days. Only "succeeded" tasks get auto-pruned;
	// pending/running stay (still doing work) and canceled/error stay
	// (worth keeping for diagnosis). 0 disables auto-cleanup.
	SyncTaskRetentionDays int `json:"sync_task_retention_days"`

	// TrafficHistoryDays controls how far back the traffic chart can render
	// AND the hourly rollup table's retention. raw 5-min snapshots are kept
	// for a fixed-internal window (covers "today" + a small buffer) and are
	// not configurable here. Range queries beyond this window return empty
	// buckets. 0 keeps everything.
	//
	// Replaces the v3.0.0-beta.5 setting `traffic_snapshot_retention_days`,
	// which was raw-only and tied to a single 5-min table; v3.0.0-beta.6+
	// stores aggregated history in `*_hourly` tables, so the user-visible
	// "history depth" knob lives here.
	TrafficHistoryDays int `json:"traffic_history_days"`

	// Hard policies that apply REGARDLESS of LoginMode. These knobs let admins
	// reject ordinary users' local-password login even when /login/local remains
	// reachable as an admin break-glass path, and add an independent
	// password-change lock.
	DisallowUserLocalLogin     bool `json:"disallow_user_local_login"`
	DisallowUserPasswordChange bool `json:"disallow_user_password_change"`
	// AllowUserPersonalRules controls the user-self portal's personal-rules
	// editor. When true users can read+write their own rule fragment; when
	// false they can still VIEW (read-only) but the save path is rejected
	// server-side and the dialog hides edit affordances. Admins always edit
	// (this knob is a global default; individual rule strings stay).
	AllowUserPersonalRules bool `json:"allow_user_personal_rules"`

	// Emergency access lets an enabled user extend a non-permanent account
	// without admin intervention. Hours is the extension length per use;
	// MaxCount is per-user until an admin resets that user's counter.
	EmergencyAccessEnabled  bool `json:"emergency_access_enabled"`
	EmergencyAccessHours    int  `json:"emergency_access_hours"`
	EmergencyAccessMaxCount int  `json:"emergency_access_max_count"`
	// EmergencyAccessQuotaGB caps how much traffic a single emergency window
	// can consume on top of the user's already-exceeded period. 0 = unlimited
	// (only the time/count limits apply). When the user crosses the quota, the
	// traffic poll ends the emergency window early and re-runs auto-disable.
	// Fractional GB allowed (e.g. 0.12). Stored as a float string in the KV.
	EmergencyAccessQuotaGB float64 `json:"emergency_access_quota_gb"`

	// ---- Subscription settings ----
	// SubPath is the URL path prefix for subscription endpoints.
	// Defaults to "sub". Dynamic, no restart required.
	SubPath string `yaml:"sub_path" json:"sub_path"`
	// Deprecated: superseded by SubClients (v3.3.0). Retained only so the KV
	// loader can read legacy rows and migrate them once; no longer exposed via
	// the admin API. Remove in the next major (v4.0.0) together with sub_clients_legacy.go.
	SubClientRules []SubClientRule `yaml:"sub_client_rules" json:"sub_client_rules"`
	// SubClients is the unified client registry (v3.3.0): detection families
	// (UA keywords + render format + enabled gate), each owning the import
	// "apps" shown in the user portal. An app is advertised to users iff its
	// family is enabled AND the app is enabled — so a blocked client can never
	// still be offered for import. Replaces SubClientRules + SubImportClients.
	SubClients []SubClientFamily `yaml:"sub_clients" json:"sub_clients"`
	// SubClientFilterMode controls how the client gate treats detection
	// families: "blacklist" (default) blocks only a matched-but-disabled
	// family and lets unknown clients through; "whitelist" blocks anything
	// that isn't a matched + enabled family (unknown clients included).
	SubClientFilterMode string `yaml:"sub_client_filter_mode" json:"sub_client_filter_mode"`
	// SubLogRetentionDays controls automatic subscription log cleanup.
	// 0 means never delete logs automatically. Default 7.
	SubLogRetentionDays int `yaml:"sub_log_retention_days" json:"sub_log_retention_days"`
	// MailSentRetentionDays controls automatic email-log (mail_sent) cleanup.
	// 0 means never delete. Default 30 — short enough that the audit trail
	// stays compact, long enough to investigate "did I get the renewal
	// reminder last month?".
	MailSentRetentionDays int `yaml:"mail_sent_retention_days" json:"mail_sent_retention_days"`
	// SubBlockAutoDisable enables automatic account disabling when user uses blocked clients.
	SubBlockAutoDisable bool `yaml:"sub_block_auto_disable" json:"sub_block_auto_disable"`
	// SubBlockAutoDisableCount is the number of violations before auto-disabling. Default 3.
	SubBlockAutoDisableCount int `yaml:"sub_block_auto_disable_count" json:"sub_block_auto_disable_count"`
	// SubBlockNotifyUser, when true, emails the user a "you used a blocked
	// client" warning on a blocked subscription fetch that hasn't yet hit the
	// auto-disable threshold. Off by default. Capped at SubBlockNotifyMaxPerDay
	// emails/day per user so a polling client can't trigger a mail storm.
	SubBlockNotifyUser      bool `yaml:"sub_block_notify_user" json:"sub_block_notify_user"`
	SubBlockNotifyMaxPerDay int  `yaml:"sub_block_notify_max_per_day" json:"sub_block_notify_max_per_day"`
	// SubUpdateIntervalHours is the subscription auto-update interval in hours.
	// Controls the Profile-Update-Interval header. Default 24.
	SubUpdateIntervalHours int `yaml:"sub_update_interval_hours" json:"sub_update_interval_hours"`
	// SubProfileNameTemplate is the template used to construct the profile
	// name surfaced in the subscription's Content-Disposition / Profile-Title
	// headers AND in one-click-import deep links (the &name= query param).
	// Supported placeholders (rendered server-side, identical on both surfaces):
	//   {{ site_title }}   — admin's panel SiteTitle
	//   {{ app_title }}    — short brand name
	//   {{ display_name }} — user's display name (may be empty)
	//   {{ upn }}          — user's UPN (always set for active users)
	//   {{ user }}         — display_name with UPN fallback (the most useful one)
	// Empty value falls back to the compiled default "{{ site_title }} - {{ user }}".
	SubProfileNameTemplate string `yaml:"sub_profile_name_template" json:"sub_profile_name_template"`
	// SubRegionFlagPrefix, when true, prepends the Unicode flag for a node's
	// Region (ISO 3166-1 alpha-2 code) to the rendered node name. Off by
	// default to avoid double-flagging existing display names that already
	// embed a flag manually.
	SubRegionFlagPrefix bool `yaml:"sub_region_flag_prefix" json:"sub_region_flag_prefix"`
	// Deprecated: superseded by SubClients (v3.3.0). See SubClientRules.
	SubImportClients []SubImportClient `yaml:"sub_import_clients" json:"sub_import_clients"`
	// SubImportTutorialURL is an optional documentation/tutorial link shown
	// next to the one-click import section on the user portal.
	SubImportTutorialURL string `yaml:"sub_import_tutorial_url" json:"sub_import_tutorial_url"`
	// QuickLinks defines shortcut buttons on the user self-service page.
	QuickLinks []QuickLink `yaml:"quick_links" json:"quick_links"`
	// GlobalAnnouncement is a single pinned notice shown to all users.
	GlobalAnnouncement GlobalAnnouncement `yaml:"global_announcement" json:"global_announcement"`
	// FooterText is the text displayed at the bottom of the login page.
	// Defaults to "© Kazuha Hub Passwall".
	FooterText string `yaml:"footer_text" json:"footer_text"`
	// ThemeColor is the M3 source color (HEX, e.g. "#0061A4") used as the
	// system-default theme for every user. Empty = fall back to the
	// frontend's compiled-in DEFAULT_PRESET_HEX. Individual users can still
	// override via the appearance menu (stored in localStorage).
	ThemeColor string `yaml:"theme_color" json:"theme_color"`

	// ---- Mail notification thresholds (type=notify in the settings KV) ----
	// Previously lived in domain.MailSettings (mail_settings table); moved
	// here so they sit alongside other runtime tuning instead of mixed in
	// with SMTP connection config. Drives the "remind me when X" mailer
	// triggers; SMTP credentials stay in domain.MailSettings.
	ExpireBeforeDays     int `yaml:"expire_before_days" json:"expire_before_days"`
	TrafficRemainPercent int `yaml:"traffic_remain_percent" json:"traffic_remain_percent"`
}

// SubClientRule defines a subscription client detection rule.
type SubClientRule struct {
	Name         string   `yaml:"name" json:"name"`                   // Display name
	Keywords     []string `yaml:"keywords" json:"keywords"`           // UA keywords to match
	RenderFormat string   `yaml:"render_format" json:"render_format"` // "mihomo" or "sing-box"
	Enabled      bool     `yaml:"enabled" json:"enabled"`             // Whether this client is allowed
}

// SubImportClient defines a user-facing one-click subscription import target.
type SubImportClient struct {
	Name              string   `yaml:"name" json:"name"`
	Platforms         []string `yaml:"platforms" json:"platforms"` // windows, macos, linux, ios, android, other
	RenderFormat      string   `yaml:"render_format" json:"render_format"`
	ImportURLTemplate string   `yaml:"import_url_template" json:"import_url_template"`
	InstallURL        string   `yaml:"install_url" json:"install_url"`
	Enabled           bool     `yaml:"enabled" json:"enabled"`
	Sort              int      `yaml:"sort" json:"sort"`
	// RecommendedFor lists the platforms for which this client should be
	// rendered as the highlighted "hero" pick on the user portal. The portal
	// detects the visitor's device (windows/macos/linux/ios/android) and
	// renders the first enabled client whose RecommendedFor contains that
	// platform. Empty list = never the hero (just listed under "更多客户端").
	// Lets admins pick a different recommended client per OS without
	// fiddling with priorities — e.g., Clash Verge Rev for desktops, Clash
	// Meta for Android, Stash for iOS.
	RecommendedFor []string `yaml:"recommended_for" json:"recommended_for"`
}

// SubClientFamily is one UA-detection group in the unified client registry
// (v3.3.0). Detection matches a request's User-Agent against Keywords to pick
// RenderFormat; Enabled gates both fetch access and whether this family's apps
// are advertised in the portal. Apps are the per-platform import targets that
// belong to the family — they inherit the family's render format (the server
// serves it by UA), so individual apps carry no format of their own.
type SubClientFamily struct {
	Name         string         `yaml:"name" json:"name"`
	Keywords     []string       `yaml:"keywords" json:"keywords"`
	RenderFormat string         `yaml:"render_format" json:"render_format"`
	Enabled      bool           `yaml:"enabled" json:"enabled"`
	Apps         []SubClientApp `yaml:"apps" json:"apps"`
}

// SubClientApp is a user-facing one-click import target nested under a
// SubClientFamily (v3.3.0). It carries no render format — the format is the
// family's, resolved by UA at fetch time.
type SubClientApp struct {
	Name              string   `yaml:"name" json:"name"`
	Platforms         []string `yaml:"platforms" json:"platforms"`
	ImportURLTemplate string   `yaml:"import_url_template" json:"import_url_template"`
	InstallURL        string   `yaml:"install_url" json:"install_url"`
	Enabled           bool     `yaml:"enabled" json:"enabled"`
	Sort              int      `yaml:"sort" json:"sort"`
	// RecommendedFor lists the platforms for which this app is the highlighted
	// "hero" pick on the portal — the portal detects the visitor's device and
	// renders the first enabled app whose RecommendedFor contains it. Empty =
	// never the hero (only listed under "more clients").
	RecommendedFor []string `yaml:"recommended_for" json:"recommended_for"`
}

// QuickLink defines a user-facing shortcut button on the self-service page.
type QuickLink struct {
	Label     string `yaml:"label" json:"label"`
	URL       string `yaml:"url" json:"url"`
	NewWindow bool   `yaml:"new_window" json:"new_window"`
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	Sort      int    `yaml:"sort" json:"sort"`
}

// GlobalAnnouncement is a single pinned notice shown to all users.
//
// Popup: when true the portal renders the notice as a modal on first visit
// after a content change. Dismissal state (don't-remind-again) lives in the
// browser's localStorage keyed by the announcement's UpdatedAt — editing
// the announcement bumps the timestamp and the popup reappears for
// everyone, which is the desired "you have a new notice" behaviour.
type GlobalAnnouncement struct {
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	Title     string `yaml:"title" json:"title"`
	Content   string `yaml:"content" json:"content"`
	Level     string `yaml:"level" json:"level"` // info, warning, danger
	Popup     bool   `yaml:"popup" json:"popup"`
	UpdatedAt string `yaml:"updated_at" json:"updated_at"`
}

type SettingsRepo interface {
	Load(ctx context.Context, defaults UISettings) (UISettings, error)
	Save(ctx context.Context, s UISettings) error
}

type MailRepo interface {
	LoadSettings(ctx context.Context, defaults domain.MailSettings) (domain.MailSettings, error)
	SaveSettings(ctx context.Context, s domain.MailSettings) error
	ListTemplates(ctx context.Context) ([]*domain.MailTemplate, error)
	GetTemplate(ctx context.Context, kind domain.MailReminderKind) (*domain.MailTemplate, error)
	SaveTemplate(ctx context.Context, t *domain.MailTemplate) error
	HasSent(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKey string) (bool, error)
	RecordSent(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKey, toEmail string) error
	// CountSentInWindow counts mail_sent rows for (user, kind) whose window_key
	// starts with the given prefix. Lets soft notifications (e.g. the
	// blocked-client warning) cap at N per day by passing the date as prefix.
	CountSentInWindow(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKeyPrefix string) (int64, error)
	// Audit-style readers over the same mail_sent table — surfaced to
	// admin's Logs → Email tab. ListSent / ClearSent / DeleteSentBefore
	// mirror the SubLogRepo verb names so the handler / cron code uses
	// the same shape across log tables.
	ListSent(ctx context.Context, filter EmailLogFilter) ([]*domain.EmailLog, int64, error)
	ClearSent(ctx context.Context) error
	DeleteSentBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// EmailLogFilter mirrors SubLogFilter — pagination + optional narrowing
// by user_id / time range. Used by the admin Email logs tab.
type EmailLogFilter struct {
	Pagination
	UserID *int64
	// Search: case-insensitive substring across to_email / kind and the joined
	// user's upn / display_name.
	Search string
	Since  *time.Time
	Until  *time.Time
}

// SAMLConfigRepo persists the panel's SAML/SSO configuration in the
// relational DB so admin can edit it from the panel without shell access.
type SAMLConfigRepo interface {
	// Load returns the current SAML config, or default disabled config when
	// no row has been persisted yet.
	Load(ctx context.Context) (*config.SAMLConfig, error)
	Save(ctx context.Context, c *config.SAMLConfig) error
}

// OIDCConfigRepo persists the panel's OIDC/OAuth2 SSO configuration.
// Same single-row pattern as SAMLConfigRepo.
type OIDCConfigRepo interface {
	Load(ctx context.Context) (*config.OIDCConfig, error)
	Save(ctx context.Context, c *config.OIDCConfig) error
}

// Repos aggregates all repository ports for dependency injection.
type Repos struct {
	User        UserRepo
	Group       GroupRepo
	Node        NodeRepo
	Separator   SeparatorRepo
	Ownership   OwnershipRepo
	Traffic     TrafficRepo
	NodeTraffic NodeTrafficRepo
	Audit       AuditRepo
	SubLog      SubLogRepo
	SyncTask    SyncTaskRepo
	RuleSet     RuleSetRepo
	Template    TemplateRepo
	XUIPanel    XUIPanelRepo
	Settings    SettingsRepo
	Mail        MailRepo
	SAMLConfig  SAMLConfigRepo
	OIDCConfig  OIDCConfigRepo
}
