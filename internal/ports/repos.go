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
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ---- Common filter types ----

// Pagination is the common slice + sort + keyword parameters carried by
// every list endpoint. Embedded into per-resource Filter structs so each
// repo can layer its own typed predicates (Role, Status, etc.) on top
// without redefining the slice/sort fields. Empty values mean "no
// constraint" — SortBy="" defers to each repo's default order
// (typically id ASC or captured_at DESC). SortDir is lower-case
// "asc"/"desc"; any other value is treated as "asc".
//
// PageSize <= 0 means "no slice; return everything within the keyword
// + sort scope" — used by internal callers (reconcile, traffic poll)
// that want every row. Admin API handlers always clamp to a finite
// page_size before constructing the filter, so the unbounded path is
// never reachable through HTTP.
type Pagination struct {
	Page     int
	PageSize int
	// Keyword is a case-insensitive substring matched per repo. Each
	// repo's implementation documents which columns participate. Empty
	// = no filter.
	Keyword string
	// SortBy names a column or virtual sort key the repo recognizes.
	// Unrecognized values fall back to the repo's default order so a
	// stale frontend can't force an "ORDER BY <admin input>" injection.
	SortBy  string
	SortDir string // "asc" | "desc"
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

// AuthEventFilter scopes a query over the authentication-event log.
type AuthEventFilter struct {
	Pagination
	UserID  *int64
	Method  string // "local" / "saml" / "oidc"; empty = any
	Outcome string // "success" / "failure"; empty = any
	Since   *time.Time
	Until   *time.Time
	// Search is a case-insensitive substring matched across upn / ip / ua / reason.
	Search string
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

// UserStatusCounts holds the admin-dashboard summary counters, computed via
// COUNT queries instead of materialising the whole users table.
type UserStatusCounts struct {
	Total     int64
	Enabled   int64
	Disabled  int64
	Emergency int64 // users with an emergency window still active at the query time
}

type UserRepo interface {
	Create(ctx context.Context, u *domain.User) error
	Update(ctx context.Context, u *domain.User) error
	// CountEnabledAdmins returns how many ENABLED admin accounts exist. Used to
	// block demoting / removing the last admin (an enabled admin is the only
	// one who can manage the panel, so the count gates self-lockout).
	CountEnabledAdmins(ctx context.Context) (int64, error)
	// CountByStatus returns total / enabled / disabled / active-emergency user
	// counts via COUNT queries — the admin dashboard's summary tiles, which used
	// to page the entire users table just to tally these.
	CountByStatus(ctx context.Context, now time.Time) (UserStatusCounts, error)
	// ListExpiringBetween returns up to limit users whose ExpireAt falls in
	// [from, to], soonest first — the dashboard's "expiring soon" list, replacing
	// a full-table scan + in-memory sort/slice.
	ListExpiringBetween(ctx context.Context, from, to time.Time, limit int) ([]*domain.User, error)
	// UpdateTrafficState persists ONLY the traffic-poll-owned columns
	// (lifetime counters, period baseline/start). The traffic poll loads every
	// user at cycle start and writes them back many seconds later; a full-row
	// Update would clobber any concurrent admin / self-service edit (password,
	// group, role, expiry, sub_token) made in that window. This narrow write
	// touches only the columns the poll owns.
	//
	// Deliberately does NOT write the emergency-access columns: those are owned
	// by the emergency subsystem (UseEmergencyAccess grants, ClearEmergencyAccess
	// clears). Writing them here from the poll's stale snapshot would silently
	// revoke an emergency window granted concurrently mid-cycle.
	UpdateTrafficState(ctx context.Context, u *domain.User) error
	// AdvanceBlockViolation atomically advances the blocked-client violation
	// count in ONE gated UPDATE: it increments block_violation_count and
	// stamps last_block_violation_at / disable_detail only when the row's
	// last_block_violation_at is null or older than notBefore (the dedup
	// window). Returns the resulting count and advanced=true iff a row was
	// updated. Putting the dedup window inside the WHERE makes concurrent /sub
	// fetches safe — only one advances per window, so no lost increment and no
	// double-fire of auto-disable. Narrow-column write (same write-amplification
	// concern as UpdateTrafficState / UpdateHealth — /sub is the hottest public
	// write path, and concurrent admin edits must not be clobbered).
	AdvanceBlockViolation(ctx context.Context, userID int64, notBefore, at time.Time, detail string) (count int, advanced bool, err error)
	// ClearBlockViolation resets the blocked-client tracking columns
	// (count, last-at, disable-detail) — called when admin re-enables
	// a user, to prevent the auto-disable threshold from re-triggering
	// instantly on the next /sub fetch (the count would otherwise still
	// be at the threshold value when re-enabled).
	ClearBlockViolation(ctx context.Context, userID int64) error
	// BatchUpdateTrafficState runs N UpdateTrafficState writes in one
	// transaction. The traffic poll calls it ONCE at end-of-cycle instead
	// of issuing N inline UPDATEs while it walks the user list. On SQLite
	// each row write is its own ~5–10ms commit (WAL fsync) so collapsing
	// N commits into one is what cuts manual "Poll Now" from ~10s to
	// sub-second at modest scale. MySQL/Postgres get the smaller win of
	// fewer round-trips. Same column scope and emergency-column skip as
	// the single-row UpdateTrafficState — see that doc.
	BatchUpdateTrafficState(ctx context.Context, users []*domain.User) error
	// BatchUpdateLastOnline writes per-user last_online_at via a single
	// transaction (same batching rationale as BatchUpdateTrafficState).
	// Called by the traffic poll once per cycle with the per-user max of
	// 3X-UI clientStats.lastOnline. Drives the admin Users list "最近活跃"
	// column. Map keying mirrors how the poll aggregates the values —
	// callers don't need a slice rebuild.
	BatchUpdateLastOnline(ctx context.Context, lastOnline map[int64]time.Time) error
	// ClearEmergencyAccess nulls emergency_until and zeroes
	// emergency_baseline_bytes for one user via a targeted write. The traffic
	// poll calls it (under user.Service's emergency lock) when a period
	// rollover or quota-exhaustion ends the window, so it doesn't have to go
	// through UpdateTrafficState (which no longer owns those columns).
	ClearEmergencyAccess(ctx context.Context, userID int64) error
	// GrantEmergencyAccess writes emergency_until / emergency_used_count /
	// emergency_baseline_bytes for one user via a targeted write — the grant
	// counterpart of ClearEmergencyAccess. UseEmergencyAccess calls it (under
	// the emergency lock) so the broad Update, which omits the emergency
	// columns, can never revert a just-granted window from a concurrent admin
	// edit's stale snapshot.
	GrantEmergencyAccess(ctx context.Context, userID int64, until time.Time, usedCount int, baselineBytes int64) error
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

	// ---- 2FA / TOTP (column-scoped; secret encrypted at rest, codes hashed) ----
	// SetTOTP writes the secret + enabled flag + recovery-code hashes.
	SetTOTP(ctx context.Context, userID int64, secret string, enabled bool, recoveryHashes []string) error
	// GetTOTP returns the decrypted secret, enabled flag, and recovery hashes.
	GetTOTP(ctx context.Context, userID int64) (secret string, enabled bool, recoveryHashes []string, err error)
	// SetRecoveryCodes replaces the stored recovery-code hashes.
	SetRecoveryCodes(ctx context.Context, userID int64, recoveryHashes []string) error
	// ConsumeRecoveryCode atomically replaces the stored recovery-code hashes
	// only if they still equal prevHashes (compare-and-swap), returning true when
	// THIS call won the swap. It makes recovery-code redemption single-use even
	// under concurrency: two requests redeeming the same one-time code read the
	// same list, but only the first's CAS matches — the loser sees prevHashes
	// already changed (RowsAffected==0) and must refuse the code.
	ConsumeRecoveryCode(ctx context.Context, userID int64, prevHashes, nextHashes []string) (bool, error)
	// ClearTOTP disables 2FA and wipes the secret + recovery codes.
	ClearTOTP(ctx context.Context, userID int64) error
}

type GroupRepo interface {
	Create(ctx context.Context, g *domain.Group) error
	Update(ctx context.Context, g *domain.Group) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.Group, error)
	GetBySlug(ctx context.Context, slug string) (*domain.Group, error)
	// List returns every group unordered-but-stable (typically id ASC).
	// Kept for the many internal callers that need the full set —
	// render, group-aware reconcile, etc. Admin API uses ListPaged.
	List(ctx context.Context) ([]*domain.Group, error)
	// ListPaged is the admin-API entry point: paged + keyword + sort.
	// Keyword matches slug / name (case-insensitive substring).
	// SortBy recognizes "id" / "name" / "slug" / "created_at"; anything
	// else falls back to "id".
	ListPaged(ctx context.Context, p Pagination) (items []*domain.Group, total int64, err error)
	CountMembers(ctx context.Context, id int64) (int64, error)
	// CountMembersByGroups returns counts for many groups in one query.
	// Eliminates the per-row Count round-trip the admin /groups list
	// would otherwise issue on every render. Missing IDs in the result
	// map mean zero (no rows reference that group_id).
	CountMembersByGroups(ctx context.Context, ids []int64) (map[int64]int64, error)
}

type NodeRepo interface {
	Create(ctx context.Context, n *domain.Node) error
	Update(ctx context.Context, n *domain.Node) error
	// UpdateMetadata is the column-scoped writer for the admin-editable
	// identity fields (display_name, server_address, flow, region, tags,
	// sort_order). Use it instead of Update for the node-edit path so a
	// full-row Save can't roll back poll-owned columns (traffic / health /
	// inbound-config snapshot) to the dialog's stale snapshot.
	UpdateMetadata(ctx context.Context, n *domain.Node) error
	// UpdateTrafficCounters / UpdateHealth are column-scoped writes used by
	// the traffic poll and the health checker respectively. They run on
	// separate goroutines against the same row, so each must touch only its
	// own columns instead of a full-row Save that would clobber the other.
	UpdateTrafficCounters(ctx context.Context, n *domain.Node) error
	// BatchUpdateTrafficCounters runs N UpdateTrafficCounters writes in one
	// transaction. The traffic poll's per-inbound node accounting used to issue
	// one inline counter UPDATE per inbound mid-cycle, bypassing the pollSink
	// batch-flush design (each its own WAL commit / round-trip). The poll now
	// buffers the touched node rows and flushes them here once at end-of-cycle,
	// mirroring BatchUpdateTrafficState on the user side. Same column-scoped
	// write as UpdateTrafficCounters, so it never clobbers the health pass.
	BatchUpdateTrafficCounters(ctx context.Context, nodes []*domain.Node) error
	UpdateHealth(ctx context.Context, n *domain.Node) error
	// UpdateInboundConfig writes only the v3.5 inbound-config snapshot
	// columns (and the cached port/protocol the snapshot also bears). Same
	// column-scoped rationale as UpdateHealth / UpdateTrafficCounters:
	// snapshot writers (admin create/update, reconcile backfill, post-push
	// capture) must not clobber the health probe's port/HealthState/
	// HealthCheckedAt columns it may be writing concurrently.
	UpdateInboundConfig(ctx context.Context, n *domain.Node) error
	// UpdateEnabled writes only the `enabled` column. Same column-scoped
	// rationale as the writers above: SetEnabled / DeleteAndSync / reconcile's
	// disappeared-inbound branch flip enabled on a snapshot loaded at cycle
	// start, and a full-row Save there would revert the health/traffic/config
	// columns the concurrent loops are writing.
	UpdateEnabled(ctx context.Context, id int64, enabled bool) error
	// UpdateCertBinding writes only the managed-cert binding columns
	// (cert_source, cert_id). Column-scoped like UpdateHealth so the node-edit
	// cert selection never rolls back poll/health/config columns.
	UpdateCertBinding(ctx context.Context, nodeID int64, source domain.CertSource, certID int64) error
	// ListByCertID returns the psp_managed nodes bound to the given certificate
	// — the reverse lookup the renewal worker uses to re-deploy a renewed cert.
	ListByCertID(ctx context.Context, certID int64) ([]*domain.Node, error)
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
	// ListPaged is the admin-API entry point. Keyword matches
	// display_name / server_address / region / one of the tags (case-
	// insensitive substring). SortBy recognizes "id" / "display_name" /
	// "sort_order" / "created_at" / "panel_id"; default "sort_order".
	ListPaged(ctx context.Context, p Pagination) (items []*domain.Node, total int64, err error)
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
	// ListPaged is the admin-API entry point. Keyword matches
	// display_name; SortBy recognizes "id" / "display_name" /
	// "sort_order" / "created_at"; default "sort_order".
	ListPaged(ctx context.Context, p Pagination) (items []*domain.SeparatorEntry, total int64, err error)
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
	// ListByUsers is the batched form of ListByUser. PollOnce calls it once
	// at the top of each cycle to bucket every user's ownership rows in a
	// single SQL roundtrip instead of N. Mirrors TrafficRepo.LatestForUsers's
	// shape (input slice, output map keyed by lookup key) — users absent from
	// the result map have no ownership rows. Empty input returns an empty
	// non-nil map so callers don't need a nil guard.
	ListByUsers(ctx context.Context, userIDs []int64) (map[int64][]*domain.XUIClientEntry, error)
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
	// BatchUpdateCounters runs N UpdateCounters writes in one transaction
	// for the traffic poll's end-of-cycle flush. Same SQLite-commit
	// collapsing rationale as UserRepo.BatchUpdateTrafficState; same
	// per-row column scope as the single-row UpdateCounters.
	BatchUpdateCounters(ctx context.Context, items []*domain.XUIClientEntry) error
}

type TrafficRepo interface {
	Insert(ctx context.Context, s *domain.TrafficSnapshot) error
	LatestForUser(ctx context.Context, userID int64) (*domain.TrafficSnapshot, error)
	// LatestForUsers is the batched form of LatestForUser. Pre-fetched
	// ONCE at the top of PollOnce so the per-user inner loop's prev-
	// snapshot lookup is a map[int64] read instead of N SELECTs. The
	// returned map omits users with no prior snapshot (the caller treats
	// absence the same as LatestForUser's ErrNotFound). Single SQL
	// statement; relies on the (user_id, captured_at) composite index.
	LatestForUsers(ctx context.Context, userIDs []int64) (map[int64]*domain.TrafficSnapshot, error)
	LastBefore(ctx context.Context, userID int64, before time.Time) (*domain.TrafficSnapshot, error)
	// LastBeforeForUsers is the batched form of LastBefore. The admin
	// /traffic/top dashboard previously issued one LastBefore per user
	// (for the today-baseline lookup) on top of one LatestForUser per
	// user — at page_size=100 that was 200+ round-trips per /top click.
	// Single SQL query with the same MAX(id) pattern LatestForUsers uses.
	LastBeforeForUsers(ctx context.Context, userIDs []int64, before time.Time) (map[int64]*domain.TrafficSnapshot, error)
	ListByUser(ctx context.Context, userID int64, since, until time.Time) ([]*domain.TrafficSnapshot, error)
	// ListHourlyByUser returns the user's rolled-up hourly delta buckets in
	// [since, until). This is the long-window source for the traffic chart —
	// raw is kept only ~7 days, the hourly table out to TrafficHistoryDays.
	ListHourlyByUser(ctx context.Context, userID int64, since, until time.Time) ([]domain.HourlyTraffic, error)
	// SumHourlyAllUsers returns the per-bucket SUM of EVERY user's hourly rows in
	// [since, until), one row per bucket_start. Backs the all-scope traffic chart
	// in a single GROUP BY query instead of a per-user ListHourlyByUser N+1.
	SumHourlyAllUsers(ctx context.Context, since, until time.Time) ([]domain.HourlyTraffic, error)
	// LastBeforeForUserClients returns, for one user, the most recent client
	// snapshot strictly before `before` per (panel, inbound, email) client —
	// keyed by domain.ClientMatchKey. Backs the per-node usage view's "today"
	// column (before = local start-of-day). Clients with no prior snapshot are
	// absent from the map. Single SQL query over idx_client_time.
	LastBeforeForUserClients(ctx context.Context, userID int64, before time.Time) (map[string]*domain.ClientTrafficSnapshot, error)
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
	// LatestForNodes / LastBeforeForNodes mirror TrafficRepo's batched
	// user-side forms; the admin /traffic/nodes/top dashboard uses them
	// to avoid the per-node N+1 the loop did pre-fix.
	LatestForNodes(ctx context.Context, nodeIDs []int64) (map[int64]*domain.NodeTrafficSnapshot, error)
	LastBefore(ctx context.Context, nodeID int64, before time.Time) (*domain.NodeTrafficSnapshot, error)
	LastBeforeForNodes(ctx context.Context, nodeIDs []int64, before time.Time) (map[int64]*domain.NodeTrafficSnapshot, error)
	ListByNode(ctx context.Context, nodeID int64, since, until time.Time) ([]*domain.NodeTrafficSnapshot, error)
	// ListHourlyByNode returns the node's rolled-up hourly delta buckets in
	// [since, until) — the long-window source for NodeHistoryFor.
	ListHourlyByNode(ctx context.Context, nodeID int64, since, until time.Time) ([]domain.HourlyTraffic, error)
	// SumHourlyAllNodes returns the per-bucket SUM of EVERY node's hourly rows in
	// [since, until), one row per bucket_start. Backs the all-scope node traffic
	// chart in a single GROUP BY query instead of a per-node ListHourlyByNode N+1.
	SumHourlyAllNodes(ctx context.Context, since, until time.Time) ([]domain.HourlyTraffic, error)
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

// AuthEventRepo stores the first-class authentication-event log (logins across
// every method, success + failure). DeleteBefore drives retention.
type AuthEventRepo interface {
	Insert(ctx context.Context, e *domain.AuthEvent) error
	List(ctx context.Context, filter AuthEventFilter) (items []*domain.AuthEvent, total int64, err error)
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
	// RecentAuthFailures counts genuine credential failures
	// (reason=invalid_credentials) for a scope since `since`, and returns the
	// timestamp of the most recent one (zero when count==0). A non-empty ip
	// and/or upn narrows the scope; pass "" for a dimension to ignore it
	// (ip+upn = the ip_upn lockout scope; ip alone = the ip scope). Drives the
	// login guard's captcha-trigger and account-lockout decisions; deliberately
	// excludes locked_out / disabled / server-error failures so a locked-out
	// source's retries can't keep pushing the lock window forward.
	RecentAuthFailures(ctx context.Context, ip, upn string, since time.Time) (count int64, lastAt time.Time, err error)
	// RecentUserFailures counts failure events for ONE user with the given reason
	// since `since` (across all source IPs), and returns the most recent matching
	// timestamp (zero when count==0). Backs the per-account 2FA verification
	// lockout: the threat is a distributed attacker who already has the password
	// grinding TOTP from many IPs, which RecentAuthFailures' IP scope can't catch.
	RecentUserFailures(ctx context.Context, userID int64, reason string, since time.Time) (count int64, lastAt time.Time, err error)
	// CountByReasonSince counts events with the given failure reason since
	// `since`. Drives the notification center's login_security alert (e.g. how
	// many locked_out rejections happened recently = an active brute-force).
	CountByReasonSince(ctx context.Context, reason string, since time.Time) (int64, error)
}

// AuthTokenRepo stores one-time hashed credentials for self-service auth flows
// (password recovery, email verification). Tokens are single-use: Consume*
// atomically marks the row used so it can't be replayed.
type AuthTokenRepo interface {
	Create(ctx context.Context, t *domain.AuthToken) error
	// ConsumeByTokenHash matches a still-valid link token by (purpose,
	// token_hash), marks it used, and returns it. domain.ErrNotFound if none /
	// already used / expired.
	ConsumeByTokenHash(ctx context.Context, purpose, tokenHash string, now time.Time) (*domain.AuthToken, error)
	// ConsumeByUserCode is the OTP equivalent, scoped to a user so a short code
	// can only be guessed against one account.
	ConsumeByUserCode(ctx context.Context, purpose string, userID int64, codeHash string, now time.Time) (*domain.AuthToken, error)
	// DeleteByUserPurpose drops a user's outstanding tokens for a purpose so a
	// freshly-issued one invalidates earlier ones.
	DeleteByUserPurpose(ctx context.Context, userID int64, purpose string) error
	// DeleteExpired prunes expired or already-used rows (retention).
	DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error)
}

// WebAuthnCredentialRepo stores registered passkeys (WebAuthn credentials).
type WebAuthnCredentialRepo interface {
	// Save persists a newly-registered credential (sets ID/CreatedAt back).
	Save(ctx context.Context, c *domain.PasskeyCredential) error
	// FindByUserID lists a user's credentials (profile management, and the
	// WebAuthnCredentials() the library needs for that user).
	FindByUserID(ctx context.Context, userID int64) ([]*domain.PasskeyCredential, error)
	// FindByCredentialID resolves a credential globally by its CredentialID —
	// the credential→user lookup for usernameless (discoverable) login.
	FindByCredentialID(ctx context.Context, credentialID string) (*domain.PasskeyCredential, error)
	// UpdateAfterLogin writes back the post-assertion credential record + sign
	// count, GATED on the new count not regressing (WHERE sign_count <= newCount):
	// a decreasing/equal count signals a cloned authenticator, so the gated write
	// is the anti-clone seam. Returns whether the row advanced.
	UpdateAfterLogin(ctx context.Context, id int64, credential []byte, newSignCount int64, lastUsed time.Time) (bool, error)
	// Rename / Delete are user-scoped (WHERE id=? AND user_id=?) so a caller can
	// only mutate their own credentials.
	Rename(ctx context.Context, id, userID int64, name string) error
	Delete(ctx context.Context, id, userID int64) error
	// DeleteAllByUserID drops every credential for a user — the admin "revoke
	// all passkeys" break-glass. Returns the number of credentials removed.
	DeleteAllByUserID(ctx context.Context, userID int64) (int, error)
	// CountByUserIDs returns the credential count per user for the given ids in one
	// grouped query (admin user-list enrichment without an N+1). Users with none
	// are absent from the map.
	CountByUserIDs(ctx context.Context, userIDs []int64) (map[int64]int, error)
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
	// MarkRunning atomically claims a Pending task (Pending -> Running).
	// claimed is false when no row matched — the task was Canceled by an admin
	// or already claimed by another runner between ListDue and here — so the
	// caller MUST skip executing its (often irreversible) side effect.
	MarkRunning(ctx context.Context, id int64) (claimed bool, err error)
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
	// ListPaged is the admin-API entry point. Keyword matches slug /
	// name; SortBy recognizes "slug" / "name" / "sort"; default "sort".
	ListPaged(ctx context.Context, p Pagination) (items []*domain.RuleSet, total int64, err error)
	GetBySlug(ctx context.Context, slug string) (*domain.RuleSet, error)
	Save(ctx context.Context, r *domain.RuleSet) error
	Delete(ctx context.Context, slug string) error
}

type TemplateRepo interface {
	List(ctx context.Context) ([]*domain.Template, error)
	// ListPaged is the admin-API entry point. Keyword matches slug /
	// name / client_type; SortBy recognizes "slug" / "name" /
	// "client_type"; default "slug".
	ListPaged(ctx context.Context, p Pagination) (items []*domain.Template, total int64, err error)
	GetBySlug(ctx context.Context, slug string) (*domain.Template, error)
	GetDefault(ctx context.Context, clientType domain.ClientType) (*domain.Template, error)
	Save(ctx context.Context, t *domain.Template) error
	Delete(ctx context.Context, slug string) error
}

type XUIPanelRepo interface {
	List(ctx context.Context) ([]*domain.XUIPanel, error)
	// ListPaged is the admin-API entry point. Keyword matches name /
	// url / remark / username; SortBy recognizes "id" / "name" /
	// "url" / "panel_version" / "created_at"; default "id".
	ListPaged(ctx context.Context, p Pagination) (items []*domain.XUIPanel, total int64, err error)
	GetByID(ctx context.Context, id int64) (*domain.XUIPanel, error)
	GetByName(ctx context.Context, name string) (*domain.XUIPanel, error)
	Save(ctx context.Context, panel *domain.XUIPanel) error
	Delete(ctx context.Context, id int64) error

	// UpdateVersion writes only the version-identity snapshot columns,
	// not the full row — so a boot-time or admin-triggered version probe
	// doesn't race with an admin editing credentials in another tab and
	// silently revert their save. Mirrors nodes.UpdateHealth in
	// scope-by-column style. checkedAt = nil clears the timestamp
	// (treat as "never probed"); empty version strings carry through
	// untouched so callers can record a failed probe.
	UpdateVersion(ctx context.Context, panelID int64, panelVersion, xrayVersion string, checkedAt *time.Time) error
	// UpdateVersionCheckedAt updates ONLY the timestamp column, leaving
	// panel_version / xray_version untouched. Used by the probe paths
	// when the probe fails: we want to record "we tried at <time>" for
	// the UI's freshness indicator, WITHOUT clobbering a previously-
	// observed valid version with empty strings. Fixes the v3.6.0-beta.6
	// data-loss bug where a transient panel outage during a piggyback
	// probe would wipe the cached version snapshot and downgrade admin
	// UI to "never probed" until the next successful probe.
	UpdateVersionCheckedAt(ctx context.Context, panelID int64, checkedAt time.Time) error
}

// CertificateRepo persists PSP-managed ACME certificates (cert_source=psp_managed).
type CertificateRepo interface {
	Create(ctx context.Context, c *domain.TLSCertificate) error
	// Update writes the full row — admin-owned fields (name / domains /
	// auto_renew / binding). Do NOT call it from the issuance/renewal worker;
	// use UpdateIssued so a concurrent admin edit isn't reverted.
	Update(ctx context.Context, c *domain.TLSCertificate) error
	// UpdateIssued writes ONLY the issuance-owned columns (cert_pem / key_pem /
	// status / not_before / not_after / fingerprint / last_error). Column-scoped
	// like nodes.UpdateHealth and xui_panels.UpdateVersion so the worker never
	// rolls back an admin edit that landed after it loaded its snapshot.
	UpdateIssued(ctx context.Context, c *domain.TLSCertificate) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.TLSCertificate, error)
	List(ctx context.Context) ([]*domain.TLSCertificate, error)
	ListByStatus(ctx context.Context, status domain.CertStatus) ([]*domain.TLSCertificate, error)
}

// DNSCredentialRepo persists DNS-provider credentials for ACME DNS-01.
type DNSCredentialRepo interface {
	Create(ctx context.Context, c *domain.DNSCredential) error
	Update(ctx context.Context, c *domain.DNSCredential) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.DNSCredential, error)
	List(ctx context.Context) ([]*domain.DNSCredential, error)
}

// ACMEAccountRepo persists admin-managed ACME CA account profiles. (email,
// directory) is unique — the same contact on the same CA is one ACME account.
type ACMEAccountRepo interface {
	Create(ctx context.Context, a *domain.ACMEAccount) error
	// Update saves the admin-editable config (name/email/directory/EAB/keytype).
	// It does NOT touch the lazily-filled AccountKey/Registration — the service
	// clears those separately when the registered identity (email/directory/EAB)
	// changes so the next issuance re-registers.
	Update(ctx context.Context, a *domain.ACMEAccount) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.ACMEAccount, error)
	List(ctx context.Context) ([]*domain.ACMEAccount, error)
	// UpdateRegistration writes back the account key + registration after a
	// successful first registration (the lazy machine fields), leaving config
	// untouched. Used by the issuer write-back path.
	UpdateRegistration(ctx context.Context, id int64, accountKeyPEM, registrationJSON string) error
	// ClearRegistration drops the account key + registration so the next issuance
	// re-registers — used when the admin changes the account's identity.
	ClearRegistration(ctx context.Context, id int64) error
}

// CertEventRepo is the append-only cert issuance/renewal activity log surfaced
// on the Logs page.
type CertEventRepo interface {
	Create(ctx context.Context, e *domain.CertEvent) error
	// ListPaged returns events newest-first plus the total count.
	ListPaged(ctx context.Context, limit, offset int) ([]*domain.CertEvent, int64, error)
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
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
	// (BrandName, below, derives the human-facing brand string from these.)
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
	// AND the user/node hourly rollup tables' retention (the chart's SOLE
	// source — HistoryFor/NodeHistoryFor read the *_hourly tables; the raw
	// 5-min tables are kept only ~7 days, feeding the rollup). Range queries
	// beyond this window return empty buckets, so it must comfortably exceed
	// the UI's longest range (365 days). It is coerced to a default of 730
	// (2y) when <= 0 — there is no "keep forever" option; 0 is NOT honored.
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

	// ---- Login protection: CAPTCHA on the public local-login form (v3.6.5) ----
	// Defense-in-depth on top of the per-IP login rate limit, aimed at
	// automated / distributed brute force + credential stuffing. CaptchaEnabled
	// gates the whole thing (default off). CaptchaProvider selects the
	// implementation: "image" (self-hosted base64 character captcha — the
	// CN-safe default, zero external calls), "turnstile" (Cloudflare),
	// "recaptcha" (Google v2), "hcaptcha". CaptchaTrigger is "always" or
	// "after_failures" (only require it once an IP/account has accumulated
	// failed logins — counted over LockoutWindowMinutes — reaching
	// CaptchaFailThreshold). SiteKey is public (embedded in the login page);
	// SecretKey is the provider's server-side verify secret, encrypted at rest
	// and masked in the admin GET (has_captcha_secret_key). Image mode needs no
	// keys. All hot-reloadable (read per login attempt).
	CaptchaEnabled       bool   `json:"captcha_enabled"`
	CaptchaProvider      string `json:"captcha_provider"`
	CaptchaTrigger       string `json:"captcha_trigger"`
	CaptchaFailThreshold int    `json:"captcha_fail_threshold"`
	CaptchaSiteKey       string `json:"captcha_site_key"`
	CaptchaSecretKey     string `json:"captcha_secret_key,omitempty"`
	// Per-context captcha (v3.7.0): CaptchaEnabled gates the LOGIN form (with the
	// CaptchaTrigger always/after_failures semantics). These two extend the same
	// provider/site-key/secret to the public self-service forms, always-on when
	// true (no per-account failure history exists pre-account). Both default off
	// → existing installs keep login-only captcha unchanged.
	CaptchaRegisterEnabled bool `json:"captcha_register_enabled"`
	CaptchaForgotEnabled   bool `json:"captcha_forgot_enabled"`

	// ---- Login protection: account lockout / failed-attempt backoff (v3.6.5) ----
	// Complements the captcha (which stops automation): a temporary lock stops
	// targeted slow guessing. When enabled, a source that accumulates
	// LockoutThreshold failed local logins within LockoutWindowMinutes is refused
	// for LockoutDurationMinutes — independent of, and stricter than, the per-IP
	// rate limit. LockoutScope picks what is locked: "ip" (the client IP) or
	// "ip_upn" (the IP+username pair — recommended, so knowing a username can't
	// be used to lock a victim out by spamming failures from elsewhere). Reuses
	// the auth_events failure log (no new table). LockoutWindowMinutes also
	// defines the recent-failure window the CaptchaTrigger="after_failures" mode
	// counts over.
	LockoutEnabled         bool   `json:"lockout_enabled"`
	LockoutThreshold       int    `json:"lockout_threshold"`
	LockoutWindowMinutes   int    `json:"lockout_window_minutes"`
	LockoutDurationMinutes int    `json:"lockout_duration_minutes"`
	LockoutScope           string `json:"lockout_scope"`

	// ---- Self-service local-password recovery (v3.7.0) ----
	// PasswordRecoveryEnabled gates the forgot-password / reset-password flow.
	// Default off; needs SMTP configured to actually deliver. Only ever applies
	// to accounts with a local password AND an email on file (SSO-only accounts
	// are unaffected). PasswordRecoveryDelivery picks the email shape: "link" (a
	// one-time reset URL) or "otp" (a short numeric code the user types on the
	// reset page).
	PasswordRecoveryEnabled  bool   `json:"password_recovery_enabled"`
	PasswordRecoveryDelivery string `json:"password_recovery_delivery"`

	// ---- Self-service registration (v3.7.0) ----
	// RegistrationEnabled gates the public /auth/register flow. Default off.
	// Registrants log in with their email as the username (UPN), get role=user,
	// join RegistrationDefaultGroupID, and inherit the default quota/expiry below
	// (Group itself carries no quota — only node selection). With
	// RegistrationRequireEmailVerification on (recommended), the account is
	// created disabled + unprovisioned until the email is confirmed.
	RegistrationEnabled bool `json:"registration_enabled"`
	// RegistrationAllowUnverified is the INVERSE of "require email verification".
	// Stored inverted so the zero value (false) means the safe default —
	// verification required — without the KV layer needing to distinguish unset
	// from explicit-false for a bool. The admin API + public methods present the
	// positive "registration_require_email_verification" (= !AllowUnverified).
	RegistrationAllowUnverified bool `json:"registration_allow_unverified"`
	// RegistrationEmailDomains is a comma-separated allow-list of email domains
	// (e.g. "example.com, corp.org"). Empty = any domain. Server-side only —
	// never exposed to the public methods endpoint.
	RegistrationEmailDomains  string  `json:"registration_email_domains"`
	RegistrationDefaultGroupID int64  `json:"registration_default_group_id"`
	// RegistrationDelivery picks the email-verify shape: "link" or "otp".
	RegistrationDelivery       string  `json:"registration_delivery"`
	// Quota/expiry a registrant inherits (Group has none). 0 = unlimited / no expiry.
	RegistrationDefaultTrafficGB  float64 `json:"registration_default_traffic_gb"`
	RegistrationDefaultExpireDays int     `json:"registration_default_expire_days"`

	// ---- Two-factor auth / TOTP (v3.7.0) ----
	// TOTPEnabled is the panel-wide master toggle for letting local users enroll
	// an authenticator-app second factor on their profile page. It does NOT
	// affect users who already enabled 2FA — their login still requires it even
	// if the admin later turns this off (you can't bypass an active factor).
	TOTPEnabled bool `json:"totp_enabled"`

	// PasskeyEnabled lets local-password accounts register WebAuthn passkeys on
	// their profile page (a possession factor). PasskeyPasswordless additionally
	// allows a usernameless passkey to mint a session directly from the login
	// page (discoverable login); with it off, passkeys only serve as a second
	// factor. Both default off; SSO-only accounts can't enroll (no local password).
	PasskeyEnabled      bool `json:"passkey_enabled"`
	PasskeyPasswordless bool `json:"passkey_passwordless"`

	// ---- Alternative 2FA verification methods (v3.7.0) ----
	// At the login 2FA challenge the account is offered the factors it actually
	// has: TOTP (if enrolled), an enrolled passkey (a passkey IS a second factor —
	// enrolling one opts the account in; gated only on PasskeyEnabled + the first
	// factor being a password), and one-time recovery codes. TwoFAAllowEmail adds
	// an opt-in weaker fallback (default off): a one-time code by email — whoever
	// holds the password+inbox passes — rate-limited, single-use, short TTL.
	TwoFAAllowEmail bool `json:"twofa_allow_email"`
	// TwoFAEmailResendCooldownSec throttles how often a login email code may be
	// re-sent to one account (anti mail-bombing + drives the login page's resend
	// countdown). 0 → defaulted to 60s.
	TwoFAEmailResendCooldownSec int `json:"twofa_email_resend_cooldown_sec"`

	// Require2FAForStaff forces every admin/operator account (with a local
	// password) to enroll a second factor before using the panel — a panel-wide
	// lever on top of the per-group and per-user require flags. High-privilege
	// accounts are the obvious thing to harden first.
	Require2FAForStaff bool `json:"require_2fa_for_staff"`

	// ---- IP geolocation (access-log region display, offline .mmdb) ----
	// Resolution is fully offline against a local .mmdb in <ConfigDir>/geoip/;
	// no per-IP external calls. GeoIPEnabled gates the whole feature (default
	// false — admin opts in after placing/downloading a database).
	GeoIPEnabled bool `json:"geo_ip_enabled"`
	// GeoIPDBFile selects which .mmdb is the ACTIVE source when several are
	// present (filename only, within the geoip dir). Empty = first by name.
	// Only one database is ever active — sources are never merged, so two
	// databases can't conflict.
	GeoIPDBFile string `json:"geo_ip_db_file"`
	// GeoIPAutoUpdate enables periodic re-download of the database (the panel
	// fetches a public DB — no user IPs involved).
	GeoIPAutoUpdate bool `json:"geo_ip_auto_update"`
	// GeoIPUpdateSource picks the updater: "ipinfo" (ipinfo Lite), "maxmind"
	// (GeoLite2, .tar.gz), or "custom" (direct URL).
	GeoIPUpdateSource string `json:"geo_ip_update_source"`
	// GeoIPUpdateToken is the ipinfo token / MaxMind license key / custom
	// bearer. Encrypted at rest, masked in the admin GET (has_geo_ip_update_token).
	GeoIPUpdateToken string `json:"geo_ip_update_token,omitempty"`
	// GeoIPUpdateURL is the direct download URL for the "custom" source.
	GeoIPUpdateURL string `json:"geo_ip_update_url"`
	// GeoIPUpdateEdition is the database identifier for the chosen source:
	// the MaxMind edition id (e.g. GeoLite2-City / paid GeoIP2-City) or the
	// IPinfo database filename stem (e.g. ipinfo_lite / a paid product). The
	// frontend clears it when the source changes so each source's default
	// applies; candidateURLs interprets it per-source.
	GeoIPUpdateEdition string `json:"geo_ip_update_edition"`
	// GeoIPUpdateIntervalHours is the auto-update cadence in hours. Default 12,
	// floored at 1 so the panel can't hammer the upstream DB hosts. The update
	// loop re-reads this each cycle, so changes take effect without a restart.
	GeoIPUpdateIntervalHours int `json:"geo_ip_update_interval_hours"`

	// cert --- PSP-managed ACME certificate automation (v3.6.4).
	// CertRenewBeforeDays is the renewal threshold (hybrid: the renewal scan
	// falls back to a lifetime fraction for short-lived certs). The renewal loop
	// re-reads CertRenewCheckIntervalHours each cycle, so cadence changes take
	// effect without a restart.
	CertRenewBeforeDays         int `json:"cert_renew_before_days"`
	CertRenewCheckIntervalHours int `json:"cert_renew_check_interval_hours"`

	// AuthEventRetentionDays bounds how long the authentication-event log is
	// kept — separate from AuditRetentionDays so logins can be retained on their
	// own (e.g. longer for compliance) schedule. Freely editable like
	// traffic_history_days: default 90 (applied only when the key was never set,
	// by key-presence in Load); an explicit 0 = keep forever and any positive
	// value is honored as-is (not floored).
	AuthEventRetentionDays int `json:"auth_event_retention_days"`

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

// BrandName returns the human-facing brand name used on surfaces that show a
// product identity outside the panel chrome: the TOTP issuer in authenticator
// apps, the WebAuthn RP display name, and email headers. It prefers SiteTitle
// (the full brand, e.g. "Kazuha Hub Passwall"), falls back to AppTitle (the
// short product name), and finally to "Passwall". Centralising this keeps the
// three call sites from drifting.
func (s UISettings) BrandName() string {
	if v := strings.TrimSpace(s.SiteTitle); v != "" {
		return v
	}
	if v := strings.TrimSpace(s.AppTitle); v != "" {
		return v
	}
	return "Passwall"
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

// QuickLink defines a user-facing shortcut on the self-service page.
type QuickLink struct {
	Label string `yaml:"label" json:"label"`
	URL   string `yaml:"url" json:"url"`
	// Icon is rendered by the portal with auto-detected source: an
	// "http(s)://" value is shown as an <img>; a "mui:Name" value picks a
	// built-in icon from the curated allowlist; anything else is treated as
	// literal text (emoji). Empty = no icon.
	Icon string `yaml:"icon" json:"icon"`
	// Description is an optional one/two-line subtitle under the label.
	Description string `yaml:"description" json:"description"`
	// Group is an optional section name. Links sharing a group render under a
	// section header; links with no group render ungrouped. When NO link has a
	// group the portal shows a single flat grid (no headers).
	Group string `yaml:"group" json:"group"`
	// Highlight visually emphasizes the card (e.g. featured tutorial).
	Highlight bool `yaml:"highlight" json:"highlight"`
	NewWindow bool `yaml:"new_window" json:"new_window"`
	Enabled   bool `yaml:"enabled" json:"enabled"`
	Sort      int  `yaml:"sort" json:"sort"`
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

// SettingsReader is the read-only Load slice of SettingsRepo / ScopedSettings,
// for helpers (paneltz timezone, sub-path/base resolution) that only need the
// global value and must accept either the plain repo or the scoped resolver.
type SettingsReader interface {
	Load(ctx context.Context, defaults UISettings) (UISettings, error)
}

type SettingsRepo interface {
	Load(ctx context.Context, defaults UISettings) (UISettings, error)
	Save(ctx context.Context, s UISettings) error
}

// ScopeOverride is one per-scope setting override — the same (Type, Name, Value)
// cell as a global setting row, scoped to a Group. Name must be a live setting
// key (KnownSettingNames). Encrypted-at-rest keys are connection secrets, are
// global-only, and are rejected at the repo boundary (see the global/group
// partition in docs/v3.8.0-group-scoped-admin.md §3 / §10-3) — so Encrypted is
// always false on a stored override.
type ScopeOverride struct {
	Type      string
	Name      string
	Value     string
	Encrypted bool
}

// ScopeSettingsRepo persists sparse per-scope setting overrides that layer over
// the global SettingsRepo. A missing (scope, key) row means "inherit the global
// value", so an empty table = every scope fully inherits (the zero-migration
// story). v3.8.0 only writes ScopeType "group"; the string scope type reserves
// room for a future "user" scope without a schema change (no promise it ships).
type ScopeSettingsRepo interface {
	// ListOverrides returns one scope's sparse override set (typically 0–10
	// rows); order is not significant.
	ListOverrides(ctx context.Context, scopeType string, scopeID int64) ([]ScopeOverride, error)
	// SetOverride upserts one override. Rejects unknown keys and encrypted keys.
	SetOverride(ctx context.Context, scopeType string, scopeID int64, o ScopeOverride) error
	// DeleteOverride removes one override = restore inheritance for that key.
	DeleteOverride(ctx context.Context, scopeType string, scopeID int64, typ, name string) error
	// DeleteScope removes every override for a scope (e.g. on group delete).
	DeleteScope(ctx context.Context, scopeType string, scopeID int64) error
}

// OverridableScopeKeys is the SINGLE source of which settings ("type.name") a
// scope may override: post-identity, genuinely group-scoped settings. The admin
// handler gates writes on it, the resolver (applyScopeOverrides) skips anything
// outside it (so a stray row can't take effect), and the frontend renders from
// the API's `overridable` echo of this set. Settings consumed BEFORE the
// user/group is known stay global (lockout / captcha / LoginMode /
// passkey_passwordless / twofa_email_resend_cooldown_sec — §10-1); role-based
// require_2fa_for_staff stays global too (Group.Require2FA covers per-group
// enrollment). Grow this set one category at a time as consumers migrate.
var OverridableScopeKeys = map[string]bool{
	// 2FA methods (login / enroll) — auth_local / twofa / passkey / login2fa.
	"security.totp_enabled":      true,
	"security.passkey_enabled":   true,
	"security.twofa_allow_email": true,
	// Notification thresholds — mailer.processUser.
	"notify.expire_before_days":     true,
	"notify.traffic_remain_percent": true,
	// Emergency access (break-glass over-limit grant) — user.emergencyFloor /
	// UseEmergencyAccess / EmergencyAccessStatusForUser.
	"security.emergency_access_enabled":   true,
	"security.emergency_access_hours":     true,
	"security.emergency_access_max_count": true,
	"security.emergency_access_quota_gb":  true,
	// Login policy (post-identity portal locks) — user_me ChangePassword /
	// PutRules / profile. DisallowLocalLogin / LoginMode stay GLOBAL (decided
	// pre-identity, §10-1), so they are deliberately NOT in this set.
	"auth.disallow_user_password_change": true,
	"runtime.allow_user_personal_rules":  true,
}

// ScopedSettings resolves the EFFECTIVE settings for a scope: the global
// SettingsRepo value overlaid with a group's sparse overrides (global ⊕ group).
// Load returns the global value unchanged (equivalent to SettingsRepo.Load);
// LoadForGroup / LoadForUser apply the group's overrides on top of the
// already-defaulted global base. GroupID 0 resolves to pure global. v3.8.0 is
// two-level (global + group) only — there is no per-user scope.
type ScopedSettings interface {
	Load(ctx context.Context, defaults UISettings) (UISettings, error)
	LoadForGroup(ctx context.Context, groupID int64, defaults UISettings) (UISettings, error)
	LoadForUser(ctx context.Context, u *domain.User, defaults UISettings) (UISettings, error)
}

type MailRepo interface {
	LoadSettings(ctx context.Context, defaults domain.MailSettings) (domain.MailSettings, error)
	SaveSettings(ctx context.Context, s domain.MailSettings) error
	ListTemplates(ctx context.Context) ([]*domain.MailTemplate, error)
	GetTemplate(ctx context.Context, kind domain.MailReminderKind) (*domain.MailTemplate, error)
	SaveTemplate(ctx context.Context, t *domain.MailTemplate) error
	HasSent(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKey string) (bool, error)
	RecordSent(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKey, toEmail string) error
	// ReserveSentSlot atomically inserts the (user, kind, windowKey) row and
	// reports whether THIS call won the insert (true) or it already existed
	// (false). Lets capped soft notifications reserve a per-day slot before
	// sending so concurrent senders can't both clear the same cap. Like
	// RecordSent it relies on OnConflict DoNothing; the only difference is the
	// boolean return.
	ReserveSentSlot(ctx context.Context, userID int64, kind domain.MailReminderKind, windowKey, toEmail string) (bool, error)
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
	AuthEvent   AuthEventRepo
	AuthToken   AuthTokenRepo
	WebAuthn    WebAuthnCredentialRepo
	SubLog      SubLogRepo
	SyncTask    SyncTaskRepo
	RuleSet     RuleSetRepo
	Template    TemplateRepo
	XUIPanel      XUIPanelRepo
	Settings       SettingsRepo
	ScopeSettings  ScopeSettingsRepo
	ScopedSettings ScopedSettings
	Mail           MailRepo
	SAMLConfig  SAMLConfigRepo
	OIDCConfig  OIDCConfigRepo

	Certificate   CertificateRepo
	DNSCredential DNSCredentialRepo
	ACMEAccount   ACMEAccountRepo
	CertEvent     CertEventRepo
}
