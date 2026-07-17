// Package user owns panel-side User CRUD and orchestrates the corresponding
// 3X-UI synchronization. It depends on two narrow ports — NodeSelector and
// ClientSyncer — instead of importing the group or sync packages directly.
// That keeps the layering clean and lets us mock these dependencies in tests.
package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/keyedmutex"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
)

// NodeSelector resolves a group's tag_filter into a concrete node list.
// Implemented by group.Service.
type NodeSelector interface {
	NodesFor(ctx context.Context, g *domain.Group) ([]*domain.Node, error)
}

// ClientSyncer is the subset of sync.Service this package needs.
// Defined here (not imported) so the user package never imports sync.
type ClientSyncer interface {
	// SetOwnedClientEnableWithInbound is the pre-fetched-inbound form
	// used by pushClientConfigToAll to skip the redundant GetInbound
	// each per-client push otherwise incurs.
	SetOwnedClientEnableWithInbound(ctx context.Context, panelID int64, inb *ports.Inbound, email string,
		protocol domain.Protocol, ssMethod, userUUID, flow string, enable bool, expireTime, totalGB int64) error
	DelAllOwnedForUser(ctx context.Context, userID int64) error
	RotateClientUUID(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, ssMethod, oldUUID, newUUID, flow string, enable bool, expireTime, totalGB int64) error
}

// TrafficUsageReader yields the bytes a user has consumed in their current
// traffic period. user.Service needs this to compute the per-client floor
// it pushes into 3X-UI (TrafficFloorBytes = limit - period_used). Defined
// as an interface so user doesn't have to import traffic — the actual
// implementation lives in traffic.Service and is wired late in app.Build.
//
// nil-safe: when the reader is nil (early-start path), trafficFloor returns
// 0 (= unlimited on the 3X-UI side) — equivalent to the historical
// behaviour before the floor was added.
type TrafficUsageReader interface {
	CurrentPeriodUsage(ctx context.Context, u *domain.User) (int64, error)
}

type Service struct {
	users     ports.UserRepo
	groups    ports.GroupRepo
	ownership ports.OwnershipRepo
	tasks     ports.SyncTaskRepo
	selector  NodeSelector
	syncer    ClientSyncer
	pool      ports.XUIPool
	settings  ports.ScopedSettings
	// authInvalidator evicts the HTTP authorization snapshot after a user row
	// changes. It is wired once during router construction and is nil in tests
	// that do not exercise HTTP authentication.
	authInvalidator func(int64)
	// trafficUsage is set lazily via SetTrafficUsage after traffic.Service
	// is constructed (traffic depends on user, so user must exist first).
	// May be nil during early-start; trafficFloor degrades to 0 in that case.
	trafficUsage TrafficUsageReader

	// bg, when set via SetBackgroundRunner, routes fire-and-forget background
	// work (group-member resync) through the app's tracked async dispatcher so
	// App.Shutdown drains it and it runs under a cancellable background context.
	// nil in tests / before wiring, where ResyncGroupMembersInBackground falls
	// back to an untracked safego.Go with context.Background.
	bg func(name string, fn func(ctx context.Context))

	// psp shadow-writes the v3.9.0 psp_client model alongside each membership
	// resync. Late-bound via SetPSPProvisioner; nil before wiring / in tests
	// disables the dual-write entirely (the real ownership path is unaffected).
	psp PSPClientProvisioner

	// sharedLife pushes a user's enable/expiry onto their v3.9.0 shared clients
	// in 3X-UI (HOLE #1). Late-bound via SetSharedLifecycleSyncer; nil before
	// wiring / in tests. Only the CHANGE-driven paths (ResyncMembership,
	// SetEnabledAndSync) call it — never the per-poll floor refresh — so it
	// causes no steady-state churn. Best-effort: a failure (e.g. the shared
	// client not yet provisioned in 3X-UI) never affects the real ownership push.
	sharedLife SharedLifecycleSyncer

	// migrator runs the V3-transitional shared-client migration for one user
	// (user_migrate task). Late-bound via SetSharedMigrator; nil = task is a no-op.
	migrator SharedMigrator

	// mailer notifies the user on service-axis state changes (suspend / restore).
	// Late-bound via SetMailNotifier; nil-tolerant (no email before wiring / in
	// tests). Fired async (through s.bg) so the traffic poll's suspend path — which
	// funnels through SetServiceSuspendedAndSync — never blocks on SMTP.
	mailer MailNotifier

	emergencyMu sync.Mutex

	// resyncLocks serializes ResyncMembership PER USER. The same user can be resynced
	// concurrently from the heal sweep, the sync-task drain, and request threads; the
	// xui adapter's lock is per-(backend,email), not per-user, so two passes could
	// race a re-key against the orphan reconcile (one deletes a client the other just
	// made desired). A per-user mutex makes each user's rebuild→provision→reconcile
	// atomic w.r.t. other passes. Idle entries are pruned after the last waiter.
	resyncLocks keyedmutex.Map[int64]
}

// PSPClientProvisioner mirrors a user's desired nodes into the v3.9.0 psp_client
// model. Implemented by clientprov.Service; kept as a local interface so the
// user service stays decoupled and nil-tolerant. See SyncUser.
type PSPClientProvisioner interface {
	SyncUser(ctx context.Context, userID int64, userUUID string, rules domain.EmailRules, desiredNodes []*domain.Node) (pruned map[int64][]string, err error)
}

// SetPSPProvisioner late-binds the v3.9.0 shadow dual-write (mirrors the other
// SetXxx wiring). Until set, ResyncMembership skips the psp_client write.
func (s *Service) SetPSPProvisioner(p PSPClientProvisioner) { s.psp = p }

// SharedLifecycleSyncer pushes a user's enable/expiry/quota onto their v3.9.0
// shared clients in 3X-UI. Implemented by sharedclient.Service; a local interface
// so the user service stays decoupled and nil-tolerant.
type SharedLifecycleSyncer interface {
	SyncUserLifecycle(ctx context.Context, userID int64, enable bool, expiryTime, totalGB int64) error
}

// SetSharedLifecycleSyncer late-binds the v3.9.0 shared-client lifecycle push.
// Until set, the change-driven paths skip it.
func (s *Service) SetSharedLifecycleSyncer(p SharedLifecycleSyncer) { s.sharedLife = p }

// SharedMigrator migrates ONE user to the shared-client model in two phases so
// the caller can push the user's REAL lifecycle (enable/expiry/floor) onto the
// freshly-provisioned shared client BEFORE the legacy per-node clients (which
// held the correct disabled/expired state) are deleted. Implemented by
// sharedclient.Service (adapted in the composition root). V3-transitional.
type SharedMigrator interface {
	// ProvisionUser creates/reconciles the shared client(s) in 3X-UI + marks the
	// confirmed attachments provisioned. Does NOT touch lifecycle or legacy clients.
	ProvisionUser(ctx context.Context, userID int64) error
	// BulkProvisionNodeInbound front-loads many users' shared-client creates onto
	// ONE node's inbound in a single bulkCreate + bulkAttach (one Xray restart). A
	// best-effort warm-up; the per-user ProvisionUser pass remains authoritative.
	BulkProvisionNodeInbound(ctx context.Context, n *domain.Node, userIDs []int64) error
	// DeleteLegacyForUser removes the user's legacy per-node clients (only those a
	// provisioned shared client now serves) + their ownership rows.
	DeleteLegacyForUser(ctx context.Context, userID int64) error
	// ReconcileOrphans deletes the user's STALE shared clients — 3X-UI clients
	// matching PSP's shared-client email scheme that the current plan no longer
	// wants (e.g. pre-merge per-class clients whose psp_client rows were already
	// pruned but whose 3X-UI clients survived a skipped delete). Per-panel
	// coverage-gated and idempotent; a no-orphan user is a few reads.
	ReconcileOrphans(ctx context.Context, userID int64) error
	// DeleteSharedForUser removes ALL of a user's shared 3X-UI clients + their
	// psp_client rows. The user-delete path MUST call this before deleting the user
	// row, else (post-migration) the shared client stays live + enabled and the
	// deleted user keeps proxy access.
	DeleteSharedForUser(ctx context.Context, userID int64) error
}

// lockUser acquires the per-user resync mutex and returns its unlock func. The
// mutex is created on first use (LoadOrStore) so concurrent first-touchers share one.
func (s *Service) lockUser(userID int64) func() {
	return s.resyncLocks.Lock(userID)
}

// SetSharedMigrator late-binds the V3 shared-client migrator. Until set, a
// user_migrate task is a no-op (treated as done).
func (s *Service) SetSharedMigrator(m SharedMigrator) { s.migrator = m }

// syncSharedLifecycle pushes the user's current enable/expiry/quota-floor onto
// their shared clients and RETURNS the push error. The error matters for the
// migration: ProvisionClient creates the shared client at the spec default
// (Enable:true, no expiry/quota), and this push is the ONLY thing that corrects
// it to the user's real state — so a caller that is about to delete the legacy
// per-node fallback (ResyncMembership) MUST NOT proceed if this failed, or a
// disabled/expired/over-quota user would be left with a fully-enabled shared
// client and no fallback (the audit-#1 bypass). Callers that aren't deleting a
// fallback (per-poll refresh, the reconcile heal) may ignore the returned error;
// it is logged here either way.
//
// Push the quota floor (limit - period_used) too, parity with the per-node path:
// it is the Xray-side safety net that cuts the client off even while PSP is
// offline. This is the change-driven snapshot; the per-poll refresh keeps it
// current as traffic accrues.
func (s *Service) syncSharedLifecycle(ctx context.Context, u *domain.User) error {
	if s.sharedLife == nil || u == nil {
		return nil
	}
	floor := s.trafficFloor(ctx, u)
	if err := s.sharedLife.SyncUserLifecycle(ctx, u.ID, u.EffectiveEnabled(time.Now()), u.PushExpireTime(), floor); err != nil {
		log.Warn("shared-client lifecycle push failed", "user_id", u.ID, "err", err)
		return err
	}
	return nil
}

// BackfillResult summarizes a BackfillPSPClients pass.
type BackfillResult struct {
	Processed int // users whose psp_client set was (re)synced
	Skipped   int // users skipped (pending-delete, or no provisioner wired)
	Errors    int // users that errored (logged + skipped, not fatal)
}

// EnqueueSharedMigration is the V3-transitional boot trigger for the silent
// shared-client migration. It backfills the psp_client model, then enqueues one
// user_migrate sync task per user that still holds legacy ownership rows. The
// sync-task loop drains them with backoff (surviving a 3X-UI outage — the
// "兜底"). Self-regulating: once every user is migrated (no ownership rows) it
// enqueues nothing, so it is cheap to call on every boot until done; enqueue is
// deduped per (type, user). Returns the number of users enqueued. V3-ONLY.
// SharedMigrationComplete reports whether the v3.9.0 shared-client migration has
// nothing left to do — no user remains on the legacy per-node ownership model.
// Once true it stays true (the ownership table is emptied then dropped and never
// repopulated), so the reconcile loop can drop the heavy per-user heal from
// every-tick (converge fast while migrating) to a slow drift backstop. A nil
// ownership repo (fresh install / shared model not wired) means nothing to migrate.
//
// MIGRATION(v3→v4): when the legacy ownership path is removed, "incomplete" can no
// longer happen — collapse this to always-true or delete it with the legacy code.
func (s *Service) SharedMigrationComplete(ctx context.Context) (bool, error) {
	if s.ownership == nil {
		return true, nil
	}
	pending, err := s.ownership.DistinctUserIDs(ctx)
	if err != nil {
		return false, err
	}
	return len(pending) == 0, nil
}

// MIGRATION(v3→v4): one-time v3.8→v3.9 migration entry point — delete with the
// legacy ownership path (and its boot call in app.go).
func (s *Service) EnqueueSharedMigration(ctx context.Context) (int, error) {
	if s.ownership == nil || s.tasks == nil {
		return 0, nil
	}
	pending, err := s.ownership.DistinctUserIDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("list un-migrated users: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil // already fully migrated
	}
	// Build the psp_client model (idempotent, DB-only) so the per-user tasks have
	// something to provision. Best-effort: a failure here doesn't block enqueuing
	// — each task re-derives from current state on run.
	if _, err := s.BackfillPSPClients(ctx); err != nil {
		log.Warn("shared migration backfill failed (tasks will still run)", "err", err)
	}
	n := 0
	for _, uid := range pending {
		if err := s.enqueueUserTask(ctx, domain.SyncTaskUserMigrate, uid, "migrate to shared client"); err != nil {
			log.Warn("enqueue user migrate task", "user_id", uid, "err", err)
			continue
		}
		n++
	}
	log.Info("shared-client migration enqueued", "users", n)
	return n, nil
}

// BackfillPSPClients is the v3.9.0 cutover **Stage 0**: it populates the
// psp_client model for EVERY user. The shadow dual-write only fires on
// ResyncMembership, so users who haven't resynced since it shipped have no
// psp_client rows; this closes that gap before any reader (render/traffic) is
// ever pointed at the shared-client model.
//
// It is DB-only — it runs the SAME clientprov.SyncUser the dual-write uses,
// which makes NO 3X-UI calls — so it merely fills the dormant psp_client /
// psp_client_inbounds tables that nothing reads in production yet. Idempotent
// (SyncUser upserts identity/creds and preserves poll-owned counters), so it is
// safe to re-run. Per-user failures are logged and skipped, never fatal. A nil
// provisioner makes the whole pass a no-op.
func (s *Service) BackfillPSPClients(ctx context.Context) (BackfillResult, error) {
	var res BackfillResult
	if s.psp == nil {
		return res, nil
	}
	users, _, err := s.users.List(ctx, ports.UserFilter{}) // PageSize 0 → all users
	if err != nil {
		return res, fmt.Errorf("list users: %w", err)
	}
	rules := s.emailRules(ctx)
	for _, u := range users {
		if u.AutoDisabledReason == domain.DisabledPendingDelete {
			res.Skipped++
			continue
		}
		g, err := s.groups.GetByID(ctx, u.GroupID)
		if err != nil {
			log.Warn("backfill psp_client: load group", "user_id", u.ID, "err", err)
			res.Errors++
			continue
		}
		nodes, err := s.selector.NodesFor(ctx, g)
		if err != nil {
			log.Warn("backfill psp_client: nodes for group", "user_id", u.ID, "err", err)
			res.Errors++
			continue
		}
		// Backfill only builds the DB model for not-yet-migrated users (no existing
		// psp_clients), so there is nothing to prune here; the 3X-UI-orphan cleanup
		// for any prune happens in ResyncMembership (after the new client is up).
		if _, err := s.psp.SyncUser(ctx, u.ID, u.UUID, rules, nodes); err != nil {
			log.Warn("backfill psp_client: sync user", "user_id", u.ID, "err", err)
			res.Errors++
			continue
		}
		res.Processed++
	}
	log.Info("psp_client backfill complete",
		"processed", res.Processed, "skipped", res.Skipped, "errors", res.Errors)
	return res, nil
}

// SetBackgroundRunner late-binds the app's tracked async dispatcher (mirrors
// SetTrafficUsage's lazy wiring). Once set, background resync runs under the
// panel-wide WaitGroup + background context instead of an untracked goroutine.
func (s *Service) SetBackgroundRunner(run func(name string, fn func(ctx context.Context))) {
	s.bg = run
}

// SetAuthInvalidator wires the shared HTTP authorization cache eviction hook.
func (s *Service) SetAuthInvalidator(invalidate func(int64)) {
	s.authInvalidator = invalidate
}

func (s *Service) invalidateAuth(userID int64) {
	if s.authInvalidator != nil {
		s.authInvalidator(userID)
	}
}

func (s *Service) updateUser(ctx context.Context, u *domain.User) error {
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	s.invalidateAuth(u.ID)
	return nil
}

func (s *Service) updateServiceState(ctx context.Context, userID int64, reason domain.AutoDisabledReason, detail string, disabledAt *time.Time) error {
	if err := s.users.UpdateServiceState(ctx, userID, reason, detail, disabledAt); err != nil {
		return err
	}
	s.invalidateAuth(userID)
	return nil
}

func (s *Service) deleteUser(ctx context.Context, userID int64) error {
	if err := s.users.Delete(ctx, userID); err != nil {
		return err
	}
	s.invalidateAuth(userID)
	return nil
}

// runBackground routes fire-and-forget work through the app's tracked dispatcher
// when wired (drained by Shutdown, cancellable ctx), else an untracked safego.Go.
// Mirrors ResyncGroupMembersInBackground's bg-or-safego fallback.
func (s *Service) runBackground(name string, fn func(ctx context.Context)) {
	if s.bg != nil {
		s.bg(name, fn)
		return
	}
	safego.Go(name, func() { fn(context.Background()) })
}

// MailNotifier emails the user on service-axis (proxy/subscription) state changes.
// Implemented by mailer.Service; a local interface keeps user decoupled from the
// mailer package and nil-tolerant. Distinct from account disable/enable — a service
// suspension does NOT block panel login.
type MailNotifier interface {
	SendServiceSuspendedToUser(ctx context.Context, userID int64, reason, detail string) error
	SendServiceRestoredToUser(ctx context.Context, userID int64) error
}

// SetMailNotifier late-binds the mailer used for service suspend / restore emails
// (same late-binding rationale as SetBackgroundRunner). nil = no email.
func (s *Service) SetMailNotifier(m MailNotifier) { s.mailer = m }

// notifyServiceSuspended / notifyServiceRestored fire the service-axis email off
// the caller's hot path (the traffic poll suspends through this service). nil
// mailer is a no-op.
func (s *Service) notifyServiceSuspended(userID int64, reason domain.AutoDisabledReason, detail string) {
	if s.mailer == nil {
		return
	}
	s.runBackground("user.service-suspended-email", func(ctx context.Context) {
		if err := s.mailer.SendServiceSuspendedToUser(ctx, userID, string(reason), detail); err != nil {
			log.Warn("service-suspended email", "user_id", userID, "err", err)
		}
	})
}

func (s *Service) notifyServiceRestored(userID int64) {
	if s.mailer == nil {
		return
	}
	s.runBackground("user.service-restored-email", func(ctx context.Context) {
		if err := s.mailer.SendServiceRestoredToUser(ctx, userID); err != nil {
			log.Warn("service-restored email", "user_id", userID, "err", err)
		}
	})
}

const maxPersonalRulesBytes = 64 * 1024

func New(users ports.UserRepo, groups ports.GroupRepo, ownership ports.OwnershipRepo,
	tasks ports.SyncTaskRepo, selector NodeSelector, syncer ClientSyncer, pool ports.XUIPool, settings ports.ScopedSettings) *Service {
	return &Service{
		users:     users,
		groups:    groups,
		ownership: ownership,
		tasks:     tasks,
		selector:  selector,
		syncer:    syncer,
		pool:      pool,
		settings:  settings,
	}
}

// SetTrafficUsage wires the late-bound traffic-usage reader. traffic.Service
// implements TrafficUsageReader but is constructed after user.Service (it
// takes user.Service as its disabler), so we can't pass it through New().
// Calling SetTrafficUsage with nil disables floor computation, keeping the
// 3X-UI side at "unlimited" on every push (the historical behaviour).
func (s *Service) SetTrafficUsage(r TrafficUsageReader) {
	s.trafficUsage = r
}

// trafficFloor returns the bytes value to push into 3X-UI's per-client
// totalGB for u. 0 means "no cap on 3X-UI side" — used for unlimited
// users, when the reader isn't wired, or on any error reading usage. Any
// failure here MUST degrade gracefully: a poll-time hiccup must not stop
// the rest of pushClientConfigToAll.
//
// Emergency access takes precedence over the normal limit math: while
// EmergencyUntil is in the future, the panel has intentionally let the
// user keep going past their period limit, so pushing floor=1 (the
// "you're over, disable yourself" sentinel) to 3X-UI would silently
// undo the grant — 3X-UI's local counter would trip the disable on its
// next tick. Instead, when emergency is active, the floor reflects the
// per-window EmergencyAccessQuotaGB (or 0 for unlimited when admin has
// it set to 0). The poll loop in service/traffic already short-circuits
// the auto-disable check during emergency, so the two layers agree.
func (s *Service) trafficFloor(ctx context.Context, u *domain.User) int64 {
	if u == nil || u.TrafficLimitBytes <= 0 {
		return 0
	}
	// Emergency check sits BEFORE the trafficUsage nil-guard because
	// the emergency branch only consults settings + user fields and
	// stays useful even on the early-start path where the usage reader
	// hasn't been wired yet.
	if u.EmergencyUntil != nil && time.Now().Before(*u.EmergencyUntil) {
		return s.emergencyFloor(ctx, u)
	}
	if s.trafficUsage == nil {
		return 0
	}
	used, err := s.trafficUsage.CurrentPeriodUsage(ctx, u)
	if err != nil {
		log.Warn("traffic floor: usage read failed, defaulting to unlimited",
			"user_id", u.ID, "err", err)
		return 0
	}
	return TrafficFloorBytes(u.TrafficLimitBytes, used)
}

// emergencyFloor computes the 3X-UI floor for a user inside an active
// emergency window. Three cases:
//   - admin set EmergencyAccessQuotaGB == 0 → unlimited inside the
//     window (matches the "the time bound is enough" config choice)
//   - quota > 0, user hasn't burned it → remaining bytes
//   - quota > 0 and exhausted → 1 (matching the over-limit sentinel)
//
// settings load errors degrade to 0 (unlimited) because failing closed
// would silently re-disable a user the admin just granted access to —
// the worse of two errors. The traffic poll has its own quota check
// (traffic.go::recordAndEnforce) that runs every cycle, so the cap is
// re-enforced server-side regardless of what 3X-UI sees.
func (s *Service) emergencyFloor(ctx context.Context, u *domain.User) int64 {
	if s.settings == nil {
		return 0
	}
	st, err := s.settings.LoadForUser(ctx, u, ports.UISettings{})
	if err != nil {
		log.Warn("traffic floor: emergency settings load failed, defaulting to unlimited",
			"user_id", u.ID, "err", err)
		return 0
	}
	if st.EmergencyAccessQuotaGB <= 0 {
		return 0
	}
	quotaBytes := int64(st.EmergencyAccessQuotaGB * 1024 * 1024 * 1024)
	used := u.LifetimeTotalBytes - u.EmergencyBaselineBytes
	if used < 0 {
		used = 0
	}
	remaining := quotaBytes - used
	if remaining <= 0 {
		return 1
	}
	return remaining
}

// emailRules loads the runtime-configurable email domain. Falls back to
// "psp.local" if Settings is unreachable so 3X-UI sync never blocks on a
// missing config row.
func (s *Service) emailRules(ctx context.Context) domain.EmailRules {
	defaults := ports.UISettings{EmailDomain: "psp.local"}
	st, err := s.settings.Load(ctx, defaults)
	if err != nil {
		st = defaults
	}
	if st.EmailDomain == "" {
		st.EmailDomain = "psp.local"
	}
	return domain.EmailRules{Domain: st.EmailDomain}
}

// ---- Plain CRUD (no 3X-UI side effects) ----

// CreateLocalInput captures the admin form fields for creating a local user.
type CreateLocalInput struct {
	UPN                string
	Email              string
	DisplayName        string // friendly name shown in panel UI (optional)
	InitialPassword    string // if empty, a random one is generated
	GroupID            int64
	ExpireAt           *time.Time
	TrafficLimitBytes  int64
	TrafficResetPeriod domain.ResetPeriod
	Remark             string
	// PendingEmailVerify creates the account disabled + flagged
	// DisabledPendingEmailVerify (self-registration before the email is
	// confirmed). Such a user can't log in and gets no 3X-UI clients until
	// ActivateAfterVerification flips it on. Default false = enabled on create.
	PendingEmailVerify bool
	// SelfRegistered marks the account as created via public signup, so it's
	// excluded from silent first-time SSO linking (see User.SelfRegistered).
	SelfRegistered bool
}

// CreateLocalResult conveys the generated initial password (shown to admin
// once) plus the persisted user (with uuid + sub_token).
type CreateLocalResult struct {
	User            *domain.User
	InitialPassword string
}

// dropOrphanUser deletes a stale panel user along with any 3X-UI clients
// recorded under their ownership. Used when EnsureSSO needs to reclaim a
// UPN or clean up a "pending approval" stub from earlier policies —
// the panel row alone would leave orphan ownership rows + ghost 3X-UI
// clients. Best-effort: every step is allowed to fail without aborting
// caller flow (the SSO login path is more important than the cleanup).
func (s *Service) dropOrphanUser(ctx context.Context, userID int64) {
	if s.syncer != nil {
		_ = s.syncer.DelAllOwnedForUser(ctx, userID)
	}
	_ = s.deleteUser(ctx, userID)
}

// HasPendingSync reports whether any 3X-UI sync task is currently queued
// for this user. Handlers call this immediately after a "sync first, async
// fallback" service operation so the response carries a flag the SPA can
// surface as a toast ("partial — 3X-UI sync queued for retry"). It's
// allowed to be slightly imprecise (a task queued by an earlier action
// counts too) — the spirit of the indicator is "there's still 3X-UI work
// pending behind the scenes", which is the truth either way.
func (s *Service) HasPendingSync(ctx context.Context, userID int64) bool {
	if s.tasks == nil {
		return false
	}
	pending, err := s.tasks.HasActiveByTargetAny(ctx, []domain.SyncTaskType{
		domain.SyncTaskUserDelete,
		domain.SyncTaskUserResync,
		domain.SyncTaskUserPushConfig,
	}, "user", userID)
	if err != nil {
		return false
	}
	return pending
}

// CreateLocal persists a new local-password user in the DB. It does NOT touch
// 3X-UI — use CreateLocalAndSync for the full "user appears on every
// authorised inbound" flow.
func (s *Service) CreateLocal(ctx context.Context, in CreateLocalInput) (*CreateLocalResult, error) {
	upn := strings.TrimSpace(in.UPN)
	if upn == "" {
		return nil, fmt.Errorf("%w: upn required", domain.ErrValidation)
	}
	if _, err := s.users.GetByUPN(ctx, upn); err == nil {
		return nil, domain.ErrAlreadyExists
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if _, err := s.groups.GetByID(ctx, in.GroupID); err != nil {
		return nil, fmt.Errorf("group: %w", err)
	}

	pwd := in.InitialPassword
	if pwd == "" {
		var err error
		pwd, err = idgen.NewPassword()
		if err != nil {
			return nil, err
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	subToken, err := idgen.NewSubToken()
	if err != nil {
		return nil, err
	}
	resetPeriod := in.TrafficResetPeriod
	if resetPeriod == "" {
		resetPeriod = domain.ResetMonthly
	}

	// Panel-tz: TrafficPeriodStart is compared back in the panel zone by
	// shouldRollPeriod, and business-calendar math goes through paneltz. A
	// server-local instant could sit in a different calendar month than the panel
	// and trip a spurious first-poll period roll.
	now := paneltz.Now(ctx, s.settings)
	u := &domain.User{
		UPN:                upn,
		Email:              in.Email,
		DisplayName:        in.DisplayName,
		PasswordHash:       string(hash),
		Role:               domain.RoleUser,
		SubToken:           subToken,
		UUID:               idgen.NewUUID(),
		GroupID:            in.GroupID,
		ExpireAt:           in.ExpireAt,
		TrafficLimitBytes:  in.TrafficLimitBytes,
		TrafficResetPeriod: resetPeriod,
		TrafficPeriodStart: &now,
		Remark:             in.Remark,
		// Seed the SSO identity columns so a first-time SSO login can
		// later overwrite them (see EnsureSSO linking path). Pinning
		// sso_subject to UPN keeps the (provider, subject) tuple unique
		// within the local namespace without needing a separate uuid.
		SSOProvider:    domain.SSOProviderLocal,
		SSOSubject:     upn,
		Enabled:        true,
		SelfRegistered: in.SelfRegistered,
	}
	if in.PendingEmailVerify {
		// Self-registration before the email is confirmed: created disabled so it
		// can't log in, and (via the caller using CreateLocal not CreateLocalAndSync)
		// with no 3X-UI clients until ActivateAfterVerification.
		u.Enabled = false
		u.AutoDisabledReason = domain.DisabledPendingEmailVerify
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return &CreateLocalResult{User: u, InitialPassword: pwd}, nil
}

// ActivateAfterVerification flips a pending-email-verify account live: enabled,
// reason cleared, and its 3X-UI clients provisioned. Idempotent and guarded —
// it only acts on accounts currently in the DisabledPendingEmailVerify state,
// so a stale email_verify token can't re-enable an admin-disabled user.
func (s *Service) ActivateAfterVerification(ctx context.Context, userID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Enabled || u.AutoDisabledReason != domain.DisabledPendingEmailVerify {
		return nil // already activated, or never in the pending state — no-op
	}
	u.Enabled = true
	u.AutoDisabledReason = domain.DisabledNone
	u.DisableDetail = ""
	if err := s.updateUser(ctx, u); err != nil {
		return err
	}
	// Provision the proxy clients now that the account is real. Best-effort with
	// async fallback (same pattern as CreateLocalAndSync's retry path).
	return s.ResyncMembershipOrEnqueue(ctx, userID, "provision after email verification for "+u.UPN)
}

// EnsureSSOInput carries the SAML/OIDC-derived facts a successful SSO
// login brings back, plus the defaults the panel should apply when auto-
// provisioning a new SSO user.
type EnsureSSOInput struct {
	// Provider names the SSO connection this login came through. Format
	// is "<protocol>:<connection_name>" — currently "saml:default" or
	// "oidc:default"; the namespace leaves room for multiple SAML/OIDC
	// connections without a schema change.
	Provider string
	// Subject is the IdP-side stable identifier that survives UPN/email
	// renames (SAML <NameID>, OIDC sub). Together with Provider it's the
	// composite SSO identity used to look up the panel row.
	Subject     string
	UPN         string
	Email       string
	DisplayName string
	Groups      []string
	// Attributes is the full IdP attribute set (every <Attribute Name>
	// in SAML, or every claim in OIDC's ID token) flattened to
	// map[string][]string. Threaded through so the role matcher can
	// look up arbitrary attributes per RoleRule, not just groups.
	Attributes map[string][]string
	// Rules + GroupsAttrName are the SSO config snapshot used to map
	// IdP attribute values to panel roles. Threaded in so EnsureSSO
	// can apply the per-rule Keep policy against the user's current
	// role in one place instead of splitting it between handler and
	// service.
	Rules          []config.SSORoleRule
	GroupsAttrName string
	// AllowAutoCreate: when true, a non-admin first-time SSO login is
	// auto-provisioned with the panel's default group / quota. When
	// false (the closed-deployment default) only IdP-promoted users
	// (admin / operator output by a rule) get an account; every other
	// unknown UPN is bounced to /sso-no-account.
	AllowAutoCreate    bool
	DefaultGroupSlug   string
	DefaultExpireDays  int
	DefaultLimitBytes  int64
	DefaultResetPeriod domain.ResetPeriod
}

// privilegedRuleMatch reports whether the SSO rules fired and elevated
// this principal to admin or operator. Used by the AllowAutoCreate
// gate — privileged rule output bypasses the gate the same way the
// pre-v2.4 IsAdmin signal did, so an IdP admin can bootstrap a fresh
// panel without flipping auto-create on.
func privilegedRuleMatch(in EnsureSSOInput) bool {
	role, matched := auth.MatchFirstRule(in.Rules, in.GroupsAttrName, in.Attributes, in.Groups)
	if !matched {
		return false
	}
	return role == domain.RoleAdmin || role == domain.RoleOperator
}

// EnsureSSO resolves the panel user for a successful SSO assertion in
// three layered passes:
//
//  1. Composite SSO lookup — match on (provider, subject). This is the
//     steady-state path once an account is bound to an external identity.
//     UPN renames in the IdP don't reroute lookups: subject is immutable.
//
//  2. First-time linking — if (1) misses, look up by UPN. If a row exists
//     and is still on the local provider (i.e. hasn't been bound to any
//     SSO connection yet), upgrade it in place: write the new
//     (provider, subject), keep PasswordHash so local login still works.
//     This covers two cases without any one-off migration code:
//     a) admins that originally had only a local password and are
//     signing in via SSO for the first time;
//     b) every legacy SSO user from before v2.3.0 — their row was
//     seeded with sso_provider='local' on upgrade, and the first
//     SSO login after upgrade rebinds them to the real identity.
//
//  3. Strict conflict refusal — if (2) finds a row already bound to a
//     DIFFERENT SSO identity (linkable.sso_provider != 'local' and
//     (provider, subject) doesn't match the row), refuse the login with
//     ErrSSOAccountConflict. This is the GitLab / Mattermost / Sentry
//     policy: an IdP can't silently rebind a UPN to a new external
//     subject. Admin must clear sso_provider/sso_subject (DB-level for
//     now) before the new identity can take over.
//
// Falling off the end means no matching row, in which case provisioning
// runs:
//   - IdP admin                       -> always create (bootstrap path).
//   - non-admin + AllowAutoCreate ON  -> create with default group/quota.
//   - non-admin + AllowAutoCreate OFF -> ErrSSONoAccount.
//
// Role policy stays promote-only by default; see applyRoleFromSSO.
func (s *Service) EnsureSSO(ctx context.Context, in EnsureSSOInput) (*domain.User, error) {
	if in.Provider == "" {
		return nil, fmt.Errorf("%w: sso provider required", domain.ErrValidation)
	}
	if in.Subject == "" {
		return nil, fmt.Errorf("%w: sso subject required", domain.ErrValidation)
	}
	if in.UPN == "" {
		return nil, fmt.Errorf("%w: upn required", domain.ErrValidation)
	}

	// Pass 1: already-linked row.
	u, err := s.users.GetBySSO(ctx, in.Provider, in.Subject)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if u != nil && u.AutoDisabledReason == domain.DisabledPendingApproval {
		// Stale auto-creation from the old "pending approval" policy —
		// scrub the row + 3X-UI clients so the linking / create path
		// can produce a clean account.
		s.dropOrphanUser(ctx, u.ID)
		u = nil
	}
	if u != nil {
		return s.reconcileSSOUser(ctx, u, in)
	}

	// Pass 2: first-time linking by UPN.
	linkable, err := s.users.GetByUPN(ctx, in.UPN)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if linkable != nil && linkable.AutoDisabledReason == domain.DisabledPendingApproval {
		s.dropOrphanUser(ctx, linkable.ID)
		linkable = nil
	}
	if linkable != nil && linkable.SelfRegistered {
		// A self-service-registered row's UPN is just an email the registrant
		// typed — anyone can pre-register a victim's email. So this row is NOT a
		// trustworthy first-time SSO link target; silently rebinding the IdP's
		// identity onto it would let an attacker shadow / hijack the victim's
		// incoming SSO account.
		if linkable.AutoDisabledReason == domain.DisabledPendingEmailVerify {
			// Still unverified (no 3X-UI clients) → just a squat. Drop it and let
			// SSO provision a clean, IdP-owned account for the real user.
			s.dropOrphanUser(ctx, linkable.ID)
			linkable = nil
		} else {
			// A verified, active self-registered local account. Refuse the
			// implicit takeover; an admin must link it explicitly.
			return nil, domain.ErrSSOAccountConflict
		}
	}
	if linkable != nil {
		// Pass 3: strict conflict refusal — only local rows are
		// eligible for first-time linking. Anything already bound to a
		// different SSO identity stays bound.
		if linkable.SSOProvider != domain.SSOProviderLocal {
			return nil, domain.ErrSSOAccountConflict
		}
		// PasswordHash is intentionally left alone so a local-admin
		// row keeps its emergency password path after SSO is bound.
		return s.reconcileSSOUser(ctx, linkable, in)
	}

	// Brand new identity — provisioning gates. Privileged role-rule
	// output (admin / operator) bypasses AllowAutoCreate the same way
	// the old IsAdmin signal did: the IdP affirmatively elevated this
	// principal, so the panel trusts it enough to bootstrap an account.
	if !privilegedRuleMatch(in) && !in.AllowAutoCreate {
		return nil, domain.ErrSSONoAccount
	}

	var groupID int64
	if in.DefaultGroupSlug != "" {
		g, err := s.groups.GetBySlug(ctx, in.DefaultGroupSlug)
		if err != nil {
			return nil, fmt.Errorf("default group %q: %w", in.DefaultGroupSlug, err)
		}
		groupID = g.ID
	}
	subToken, err := idgen.NewSubToken()
	if err != nil {
		return nil, err
	}
	var expire *time.Time
	if in.DefaultExpireDays > 0 {
		// AddDate the "days from now" offset in the panel's timezone so the
		// expiry wall-clock day matches what the admin intended when picking
		// e.g. "30 days" — the resulting instant is the same UTC moment but
		// the calendar arithmetic is now consistent with the panel's day
		// boundary (relevant near DST transitions / off-by-one hour cases).
		t := paneltz.Now(ctx, s.settings).AddDate(0, 0, in.DefaultExpireDays)
		expire = &t
	}
	resetPeriod := in.DefaultResetPeriod
	if resetPeriod == "" {
		resetPeriod = domain.ResetMonthly
	}
	// Panel-tz for TrafficPeriodStart (see the admin-create path) — server-local
	// could trip a spurious first-poll roll under shouldRollPeriod.
	now := paneltz.Now(ctx, s.settings)
	// New row: there is no current role to preserve, so the brand-new
	// path just takes whichever role the first rule matched. RoleUser
	// is the default when no rule fires; that's harmless because we
	// already passed the AllowAutoCreate gate above.
	matchedRole, ruleMatched := auth.MatchFirstRule(in.Rules, in.GroupsAttrName, in.Attributes, in.Groups)
	newRole := domain.RoleUser
	if ruleMatched {
		newRole = matchedRole
	}
	u = &domain.User{
		UPN:                in.UPN,
		Email:              in.Email,
		Role:               newRole,
		SubToken:           subToken,
		UUID:               idgen.NewUUID(),
		GroupID:            groupID,
		ExpireAt:           expire,
		TrafficLimitBytes:  in.DefaultLimitBytes,
		TrafficResetPeriod: resetPeriod,
		TrafficPeriodStart: &now,
		DisplayName:        in.DisplayName,
		Enabled:            true,
		SSOProvider:        in.Provider,
		SSOSubject:         in.Subject,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("create sso user: %w", err)
	}
	return u, nil
}

// reconcileSSOUser folds the per-login mutable signals (role / display name /
// email + linking columns) into an existing row. Shared between the linked
// (pass 1) and first-time-linking (pass 2) code paths so the role-policy
// and dirty-tracking stay in one place.
func (s *Service) reconcileSSOUser(ctx context.Context, u *domain.User, in EnsureSSOInput) (*domain.User, error) {
	dirty := false
	if u.SSOProvider != in.Provider {
		u.SSOProvider = in.Provider
		dirty = true
	}
	if u.SSOSubject != in.Subject {
		u.SSOSubject = in.Subject
		dirty = true
	}
	// Role resolution: ResolveRoleForSSO encapsulates the per-rule
	// Keep policy plus the "panel-managed role" carve-out (when no
	// rule outputs the user's current role, SSO leaves it alone).
	if newRole, ssoAuthoritative := auth.ResolveRoleForSSO(in.Rules, u.Role, in.GroupsAttrName, in.Attributes, in.Groups); ssoAuthoritative && newRole != u.Role {
		u.Role = newRole
		dirty = true
	}
	if in.DisplayName != "" && u.DisplayName != in.DisplayName {
		u.DisplayName = in.DisplayName
		dirty = true
	}
	if in.Email != "" && u.Email != in.Email {
		u.Email = in.Email
		dirty = true
	}
	if dirty {
		if err := s.updateUser(ctx, u); err != nil {
			return nil, fmt.Errorf("update sso user: %w", err)
		}
	}
	return u, nil
}

// VerifyLocalPassword returns the user if UPN/password match a password-enabled
// account. On ErrForbidden the user pointer is still returned (non-nil) so the
// caller can surface a reason-specific error message — for any other error the
// pointer is nil.
// dummyBcryptHash is compared against on the user-not-found / no-local-
// password paths so those responses take roughly the same time as a real
// password check, closing a UPN-enumeration timing oracle. Generated once at
// the same cost real hashes use (bcrypt.DefaultCost).
var dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer"), bcrypt.DefaultCost)

func (s *Service) VerifyLocalPassword(ctx context.Context, upn, password string) (*domain.User, error) {
	u, err := s.users.GetByUPN(ctx, strings.TrimSpace(upn))
	if err != nil {
		// Burn a bcrypt comparison so an unknown UPN doesn't return
		// measurably faster than a wrong password on a real account.
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		return nil, err
	}
	if !u.HasLocalPassword() {
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		return nil, domain.ErrUnauthorized
	}
	if !domain.AccountLoginAllowed(u.Enabled, u.AutoDisabledReason) {
		return u, domain.ErrForbidden
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, domain.ErrUnauthorized
	}
	return u, nil
}

// UnlinkSSO clears the user's SSO binding, dropping them back to local.
// SSOProvider goes to 'local', SSOSubject is rewritten to UPN so the
// composite key stays in the local namespace. PasswordHash is preserved
// — an admin can immediately give the user a password (or the user can
// just SSO again to re-link). Returns ErrValidation when the row isn't
// SSO-bound, so the admin UI can disable the action.
func (s *Service) UnlinkSSO(ctx context.Context, userID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.SSOProvider == "" || u.SSOProvider == domain.SSOProviderLocal {
		return fmt.Errorf("%w: account is not bound to any SSO identity", domain.ErrValidation)
	}
	u.SSOProvider = domain.SSOProviderLocal
	u.SSOSubject = u.UPN
	if err := s.updateUser(ctx, u); err != nil {
		return fmt.Errorf("unlink sso: %w", err)
	}
	return nil
}

// ResetSubToken issues a new subscription token, invalidating the old URL.
func (s *Service) ResetSubToken(ctx context.Context, userID int64) (string, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	token, err := idgen.NewSubToken()
	if err != nil {
		return "", err
	}
	u.SubToken = token
	if err := s.updateUser(ctx, u); err != nil {
		return "", err
	}
	return token, nil
}

type ResetCredentialsResult struct {
	SubToken string
	UUID     string
}

// ResetCredentialsAndSync rotates both credential layers at once:
// subscription token for /sub access, and UUID for actual proxy clients.
func (s *Service) ResetCredentialsAndSync(ctx context.Context, userID int64) (*ResetCredentialsResult, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	entries, err := s.ownership.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	token, err := idgen.NewSubToken()
	if err != nil {
		return nil, err
	}
	oldUUID := u.UUID
	newUUID := idgen.NewUUID()
	u.SubToken = token
	u.UUID = newUUID
	if err := s.updateUser(ctx, u); err != nil {
		return nil, err
	}
	expireTime := u.PushExpireTime()
	// Compute the floor once; reuse for every client we push so a slow
	// CurrentPeriodUsage doesn't blow up to N round-trips against the
	// snapshots table.
	floor := s.trafficFloor(ctx, u)
	needsRetry := false
	for _, e := range entries {
		info, err := s.inspectInboundByPanel(ctx, e.PanelID, e.InboundID)
		if err != nil {
			needsRetry = true
			continue
		}
		if info.protocol == "" {
			continue
		}
		if err := s.syncer.RotateClientUUID(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			info.protocol, info.ssMethod, oldUUID, newUUID, info.flow, u.EffectiveEnabled(time.Now()), expireTime, floor); err != nil {
			needsRetry = true
		}
	}
	// Enqueue a resync after the rotation. On the error path it retries the failed
	// per-node pushes; on the happy path it STILL runs whenever the shared model is
	// wired, so the v3.9.0 dual-write advances the psp_client's UUID/Password and
	// syncSharedLifecycle pushes the rotated creds onto the shared client. Without
	// this, post-migration (the per-node loop above is a no-op, so needsRetry stays
	// false) a successful rotation would NOT reach the shared client until the next
	// periodic reconcile — /sub serves the new UUID while 3X-UI still holds the old,
	// breaking auth for up to a full reconcile interval. Mirrors ResetUUIDAndSync.
	if needsRetry || s.sharedLife != nil {
		if err := s.enqueueUserTask(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync credentials for user %s", u.UPN)); err != nil {
			log.Warn("enqueue user credential resync failed", "user_id", userID, "err", err)
		}
	}
	return &ResetCredentialsResult{SubToken: token, UUID: newUUID}, nil
}

// SetPassword updates a password-enabled account's password (admin-side reset).
func (s *Service) SetPassword(ctx context.Context, userID int64, newPassword string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.HasLocalPassword() {
		return fmt.Errorf("%w: account has no local password", domain.ErrValidation)
	}
	if !isMinimallyStrongPassword(newPassword) {
		return fmt.Errorf("%w: password too weak (need ≥8 chars with at least one letter and one digit)", domain.ErrValidation)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	// Bump TokenVersion to revoke any other live session for this user
	// (e.g. a stolen browser cookie). The caller will receive a fresh
	// JWT on their next 401 → refresh cycle.
	u.TokenVersion++
	return s.updateUser(ctx, u)
}

// AdminResetPassword sets the account's local credential and returns the
// plaintext for one-time display. When the requested password is empty a
// random one is generated; otherwise the caller-supplied value is used
// after a minimum-strength check.
//
// Unlike SetPassword, this works for SSO-only accounts too — promoting
// them to dual-mode (the admin needs a way to hand out a fallback
// password when the IdP is offline or the user is locked out of SSO).
func (s *Service) AdminResetPassword(ctx context.Context, userID int64, requested string) (string, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	pwd := requested
	if pwd == "" {
		pwd, err = idgen.NewPassword()
		if err != nil {
			return "", err
		}
	} else {
		// Same floor as the React validator: ≥8 chars, contains a letter
		// and a digit. Cheap server-side check so a bypass of the form
		// doesn't seed a 1-character password into the bcrypt store.
		if !isMinimallyStrongPassword(pwd) {
			return "", fmt.Errorf("%w: password too weak", domain.ErrValidation)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	u.PasswordHash = string(hash)
	// Bump TokenVersion so every JWT issued before the password reset
	// stops working immediately — otherwise a stolen access token
	// outlives the password change for the remainder of the access TTL.
	u.TokenVersion++
	if err := s.updateUser(ctx, u); err != nil {
		return "", err
	}
	return pwd, nil
}

// IsMinimallyStrongPassword reports whether pwd meets the panel's local-password
// floor (>=8 chars, at least one letter and one digit). Exported so sibling
// services (e.g. password recovery) enforce the SAME policy before mutating a
// password, instead of each re-deriving the rule.
func IsMinimallyStrongPassword(pwd string) bool { return isMinimallyStrongPassword(pwd) }

func isMinimallyStrongPassword(s string) bool {
	if len(s) < 8 {
		return false
	}
	var hasLetter, hasDigit bool
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

// Get returns one user or ErrNotFound.
func (s *Service) Get(ctx context.Context, id int64) (*domain.User, error) {
	return s.users.GetByID(ctx, id)
}

// GetByUPN looks a user up by login username. Thin wrapper so callers that only
// have the service (e.g. self-registration's OTP-verify path) don't need the
// repo directly.
func (s *Service) GetByUPN(ctx context.Context, upn string) (*domain.User, error) {
	return s.users.GetByUPN(ctx, upn)
}

// SetPersonalRules updates only the user's subscription-only personal rule
// fragment. It does not touch 3X-UI because the rules are rendered into the
// YAML subscription document at request time.
func (s *Service) SetPersonalRules(ctx context.Context, userID int64, rules string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	rules = strings.TrimSpace(rules)
	if len([]byte(rules)) > maxPersonalRulesBytes {
		return fmt.Errorf("%w: personal rules too large", domain.ErrValidation)
	}
	u.PersonalRules = rules
	return s.updateUser(ctx, u)
}

// GetBySubToken is used by the subscription handler.
func (s *Service) GetBySubToken(ctx context.Context, token string) (*domain.User, error) {
	return s.users.GetBySubToken(ctx, token)
}

// List delegates to the repo with a filter.
func (s *Service) List(ctx context.Context, filter ports.UserFilter) ([]*domain.User, int64, error) {
	return s.users.List(ctx, filter)
}

// ---- Orchestrated use cases that touch 3X-UI ----

// CreateLocalSyncedResult is the orchestrated equivalent of CreateLocalResult.
type CreateLocalSyncedResult struct {
	User            *domain.User
	InitialPassword string
	SyncedInbounds  int
}

// CreateLocalAndSync is the canonical "admin creates a new friend" use case.
// It performs four steps:
//
//  1. Persist the user (CreateLocal).
//  2. Resolve the group's tag_filter into a node list.
//  3. For every node, inspect the underlying inbound to detect protocol
//     and push the new client through SyncSvc (which applies the write guard
//     and records ownership).
//  4. If any 3X-UI write fails, leave the user row in place and enqueue a
//     durable resync task instead of rolling back panel-side state.
func (s *Service) CreateLocalAndSync(ctx context.Context, in CreateLocalInput) (*CreateLocalSyncedResult, error) {
	base, err := s.CreateLocal(ctx, in)
	if err != nil {
		return nil, err
	}
	u := base.User

	// Keep non-3X-UI setup errors transactional: the user row is only useful
	// once their group can be resolved.
	rollback := func() {
		_ = s.syncer.DelAllOwnedForUser(context.Background(), u.ID)
		_ = s.deleteUser(context.Background(), u.ID)
	}

	g, err := s.groups.GetByID(ctx, u.GroupID)
	if err != nil {
		rollback()
		return nil, fmt.Errorf("load group: %w", err)
	}
	nodes, err := s.selector.NodesFor(ctx, g)
	if err != nil {
		rollback()
		return nil, fmt.Errorf("resolve nodes: %w", err)
	}

	// v3.9.0 inverted enrollment: provision the new user's SHARED client.
	// ResyncMembership builds the psp_client set, reconciles it into 3X-UI, and
	// pushes lifecycle — NO per-node ownership rows are created. Any 3X-UI failure
	// is recorded as a durable resync task so the worker retries with backoff.
	if err := s.ResyncMembership(ctx, u.ID); err != nil {
		log.Warn("create-local shared provision failed (queued for retry)", "user_id", u.ID, "err", err)
		if terr := s.enqueueUserTask(ctx, domain.SyncTaskUserResync, u.ID, fmt.Sprintf("sync node membership for user %s", u.UPN)); terr != nil {
			return nil, terr
		}
	}

	return &CreateLocalSyncedResult{
		User:            u,
		InitialPassword: base.InitialPassword,
		SyncedInbounds:  len(nodes),
	}, nil
}

// DeleteAndSync disables the user immediately, then tries to clean up 3X-UI
// inline. If every owned client comes off cleanly the panel row is removed
// before this function returns — the common online case finishes in well
// under a second. If any 3X-UI call fails (panel offline, network blip), a
// durable task is queued so the background worker can retry with backoff.
func (s *Service) DeleteAndSync(ctx context.Context, userID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	// Last-admin lockout guard: deleting the only enabled admin would leave
	// nobody able to manage the panel (recoverable only via the out-of-band
	// reset-admin-password binary). Mirrors UpdateProfile's demotion guard.
	if u.Role == domain.RoleAdmin && u.Enabled {
		n, err := s.users.CountEnabledAdmins(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return fmt.Errorf("%w: cannot delete the last enabled admin", domain.ErrValidation)
		}
	}
	u.Enabled = false
	u.AutoDisabledReason = domain.DisabledPendingDelete
	if err := s.updateUser(ctx, u); err != nil {
		return err
	}

	// Synchronous fast path: when 3X-UI is reachable, delete every owned
	// client and the panel row right here. This is the SetEnabledAndSync
	// pattern applied to deletion. BOTH the legacy per-node clients AND the
	// v3.9.0 shared clients must go before the user row — otherwise (post-
	// migration, where DelAllOwnedForUser is a no-op against the empty ownership
	// table) the shared client u{uid}@ stays live + enabled and the deleted user
	// keeps proxy access. The shared teardown runs BEFORE users.Delete because
	// there is no FK cascade from users to psp_client.
	if err := s.syncer.DelAllOwnedForUser(ctx, u.ID); err == nil {
		if err := s.deleteSharedForUser(ctx, u.ID); err == nil {
			if err := s.deleteUser(ctx, u.ID); err == nil {
				return nil
			}
		}
	}

	// Fallback: at least one 3X-UI call failed, or the final row delete
	// failed. Queue a durable task — the background worker iterates the
	// ownership table (which already reflects successful deletes from the
	// loop above) and retries just what's left, on exponential backoff.
	if s.tasks == nil {
		return fmt.Errorf("sync task repo not configured")
	}
	if _, err := s.tasks.GetActiveByTarget(ctx, domain.SyncTaskUserDelete, "user", userID); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return s.tasks.Create(ctx, &domain.SyncTask{
		Type:       domain.SyncTaskUserDelete,
		Status:     domain.SyncTaskPending,
		TargetType: "user",
		TargetID:   userID,
		Summary:    fmt.Sprintf("delete user %s", u.UPN),
		NextRunAt:  time.Now(),
	})
}

// UpdateInput is the patch applied by UpdateProfile. Each pointer field is
// nil → no change; non-nil → set to the dereferenced value. ClearExpire is
// a separate bool because *time.Time cannot distinguish "no change" from
// "explicit clear to permanent".
type UpdateInput struct {
	GroupID            *int64
	Role               *domain.Role
	Email              *string
	ExpireAt           *time.Time
	ClearExpire        bool
	TrafficLimitBytes  *int64
	TrafficResetPeriod *domain.ResetPeriod
	Remark             *string
	DisplayName        *string
}

type EmergencyAccessResult struct {
	User          *domain.User
	ExtendedFrom  time.Time
	ExtendedUntil time.Time
	UsedCount     int
	MaxCount      int
	Remaining     int
}

type EmergencyAccessStatus struct {
	Enabled       bool
	DurationHours int
	MaxCount      int
	UsedCount     int
	Remaining     int
	Available     bool
	Status        string
	Reason        string
	Until         *time.Time
	// QuotaBytes is the per-window traffic cap (0 = unlimited). UsedBytes is
	// how much of the current active window has been consumed (only meaningful
	// when Until is in the future). When UsedBytes >= QuotaBytes > 0 the poll
	// will end the window early.
	QuotaBytes int64
	UsedBytes  int64
}

// UpdateProfile applies a partial update to one user. If the group
// changed, 3X-UI client memberships are reconciled afterwards via
// ResyncMembership. Other field changes are panel-side only: expire and
// traffic limit are enforced by the panel (TrafficSvc / sub handler), not
// pushed into 3X-UI's client.expiryTime / totalGB.
func (s *Service) UpdateProfile(ctx context.Context, userID int64, in UpdateInput) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	groupChanged := false
	expireChanged := false
	trafficLimitChanged := false
	if in.GroupID != nil && *in.GroupID != u.GroupID {
		if _, err := s.groups.GetByID(ctx, *in.GroupID); err != nil {
			return fmt.Errorf("group: %w", err)
		}
		u.GroupID = *in.GroupID
		groupChanged = true
	}
	if in.Role != nil && *in.Role != u.Role {
		if !u.HasLocalPassword() {
			return fmt.Errorf("%w: only local users can be assigned admin role here", domain.ErrValidation)
		}
		// Last-admin lockout guard: refuse to demote the only enabled admin, or
		// the panel would be left with nobody able to manage it.
		if u.Role == domain.RoleAdmin && *in.Role != domain.RoleAdmin {
			n, err := s.users.CountEnabledAdmins(ctx)
			if err != nil {
				return err
			}
			if n <= 1 {
				return fmt.Errorf("%w: cannot demote the last enabled admin", domain.ErrValidation)
			}
		}
		switch *in.Role {
		case domain.RoleAdmin, domain.RoleUser:
			u.Role = *in.Role
		default:
			return fmt.Errorf("%w: invalid role", domain.ErrValidation)
		}
		// Role change → bump TokenVersion so any previously-issued JWT
		// with the old role can't keep accessing routes guarded by
		// RequireRole on the new role boundary.
		u.TokenVersion++
	}
	if in.ClearExpire && u.ExpireAt != nil {
		u.ExpireAt = nil
		expireChanged = true
	} else if in.ExpireAt != nil && (u.ExpireAt == nil || !in.ExpireAt.Equal(*u.ExpireAt)) {
		u.ExpireAt = in.ExpireAt
		expireChanged = true
	}
	if in.TrafficLimitBytes != nil {
		if *in.TrafficLimitBytes != u.TrafficLimitBytes {
			trafficLimitChanged = true
		}
		u.TrafficLimitBytes = *in.TrafficLimitBytes
	}
	if in.TrafficResetPeriod != nil {
		u.TrafficResetPeriod = *in.TrafficResetPeriod
	}
	if in.Remark != nil {
		u.Remark = *in.Remark
	}
	if in.DisplayName != nil {
		u.DisplayName = *in.DisplayName
	}
	if in.Email != nil {
		u.Email = *in.Email
	}
	if err := s.updateUser(ctx, u); err != nil {
		return err
	}
	now := time.Now()
	serviceStateChanged := false
	if u.ServiceDisabledReason == domain.DisabledExpired && !u.IsExpired(now) {
		u.ServiceDisabledReason = domain.DisabledNone
		u.ServiceDisableDetail = ""
		u.ServiceDisabledAt = nil
		if err := s.updateServiceState(ctx, u.ID, domain.DisabledNone, "", nil); err != nil {
			return err
		}
		serviceStateChanged = true
	}
	if u.ServiceDisabledReason == domain.DisabledTrafficExceeded && (!u.TrafficExceeded() || u.EmergencyActive(now)) {
		u.ServiceDisabledReason = domain.DisabledNone
		u.ServiceDisableDetail = ""
		u.ServiceDisabledAt = nil
		if err := s.updateServiceState(ctx, u.ID, domain.DisabledNone, "", nil); err != nil {
			return err
		}
		serviceStateChanged = true
	}
	if groupChanged {
		if err := s.ResyncMembershipOrEnqueue(ctx, userID, fmt.Sprintf("sync node membership for user %s", u.UPN)); err != nil {
			log.Warn("enqueue user membership resync failed", "user_id", userID, "err", err)
		}
		return nil
	}
	if expireChanged || trafficLimitChanged || serviceStateChanged {
		if err := s.pushClientConfigToAll(ctx, u); err != nil {
			if taskErr := s.enqueueUserTask(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync enabled/expiry config for user %s", u.UPN)); taskErr != nil {
				log.Warn("enqueue user config push failed", "user_id", userID, "err", taskErr)
			}
			return nil
		}
	}
	return nil
}

// WithEmergencyLock runs fn while holding the emergency-access mutex.
// The traffic poll uses this when it clears EmergencyUntil /
// EmergencyBaselineBytes (period rollover, quota exhaustion) so the
// write doesn't race a concurrent UseEmergencyAccess on the same user.
// Caller-supplied fn typically loads the user, mutates the emergency
// fields, and calls users.Update — all under the lock.
func (s *Service) WithEmergencyLock(fn func()) {
	s.emergencyMu.Lock()
	defer s.emergencyMu.Unlock()
	fn()
}

func (s *Service) UseEmergencyAccess(ctx context.Context, userID int64, trafficLimitExceeded bool) (*EmergencyAccessResult, error) {
	var result *EmergencyAccessResult
	var pushUser *domain.User
	// Critical section: serialize the state mutation against the poll's
	// emergency-clear (WithEmergencyLock) and concurrent grants. The 3X-UI push
	// is deliberately done AFTER the lock is released — it's a slow per-panel
	// network fan-out (xui ~30s timeout each), and holding emergencyMu across it
	// would stall the traffic poll's emergency cleanup for that whole duration.
	if err := func() error {
		s.emergencyMu.Lock()
		defer s.emergencyMu.Unlock()

		u, err := s.users.GetByID(ctx, userID)
		if err != nil {
			return err
		}
		settings, err := s.settings.LoadForUser(ctx, u, ports.UISettings{})
		if err != nil {
			return err
		}
		if !settings.EmergencyAccessEnabled {
			return fmt.Errorf("%w: emergency access is disabled", domain.ErrForbidden)
		}
		if settings.EmergencyAccessHours <= 0 || settings.EmergencyAccessMaxCount <= 0 {
			return fmt.Errorf("%w: emergency access settings are invalid", domain.ErrValidation)
		}

		now := time.Now()
		status := EmergencyAccessStatusForUserWithTrafficLimit(u, settings, now, trafficLimitExceeded)
		if status.Remaining <= 0 {
			return fmt.Errorf("%w: emergency access limit reached", domain.ErrForbidden)
		}
		if !status.Available {
			return fmt.Errorf("%w: %s", domain.ErrValidation, status.Reason)
		}

		from := now
		until := from.Add(time.Duration(settings.EmergencyAccessHours) * time.Hour)
		// Do NOT mutate ExpireAt here. The effective expiry pushed to 3X-UI is
		// MAX(ExpireAt, EmergencyUntil) via User.PushExpireTime, so the window
		// below already extends access without touching the stored expiry.
		// Overwriting a past ExpireAt with `until` permanently lost the user's
		// real expiry date — after the window the poll clears EmergencyUntil and
		// they'd appear to expire at the (long-gone) window end.
		if !u.Enabled && (u.AutoDisabledReason == domain.DisabledTrafficExceeded || u.AutoDisabledReason == domain.DisabledExpired) {
			u.Enabled = true
			if u.AutoDisabledReason == domain.DisabledTrafficExceeded {
				u.ServiceDisabledReason = domain.DisabledTrafficExceeded
				u.ServiceDisableDetail = "emergency access active"
			} else {
				u.ServiceDisabledReason = domain.DisabledNone
				u.ServiceDisableDetail = ""
			}
			u.AutoDisabledReason = domain.DisabledNone
			u.DisableDetail = ""
		}
		if trafficLimitExceeded && u.Enabled {
			u.ServiceDisabledReason = domain.DisabledTrafficExceeded
			u.ServiceDisableDetail = "emergency access active"
			nowCopy := now
			u.ServiceDisabledAt = &nowCopy
		}
		u.EmergencyUntil = &until
		u.EmergencyUsedCount++
		// Snapshot the lifetime counter so the traffic poll can compute how much
		// the user consumes during this emergency window and end it early once
		// EmergencyAccessQuotaGB is exhausted. Captured even when quota is 0 so
		// admins can flip the cap on later without retroactively breaking the
		// window's accounting.
		u.EmergencyBaselineBytes = u.LifetimeTotalBytes
		if err := s.updateUser(ctx, u); err != nil {
			return err
		}
		// The broad Update above omits the emergency columns AND the
		// service-suspension columns (pollOwnedColumns) so a concurrent admin edit
		// can't revert this grant; write both through their targeted writers under
		// the same lock. The service-state write mirrors the emergency-window
		// branches above (traffic-exceeded keeps a service-suspended marker;
		// expired clears it) — without it, granting emergency access would no
		// longer persist the service_disabled_* transition.
		if err := s.updateServiceState(ctx, u.ID, u.ServiceDisabledReason, u.ServiceDisableDetail, u.ServiceDisabledAt); err != nil {
			return err
		}
		if err := s.users.GrantEmergencyAccess(ctx, u.ID, until, u.EmergencyUsedCount, u.EmergencyBaselineBytes); err != nil {
			return err
		}
		pushUser = u
		result = &EmergencyAccessResult{
			User:          u,
			ExtendedFrom:  from,
			ExtendedUntil: until,
			UsedCount:     u.EmergencyUsedCount,
			MaxCount:      settings.EmergencyAccessMaxCount,
			Remaining:     max(0, settings.EmergencyAccessMaxCount-u.EmergencyUsedCount),
		}
		return nil
	}(); err != nil {
		return nil, err
	}

	// Outside the lock: slow per-panel network push. On failure, enqueue the
	// retryable sync task so 3X-UI converges without blocking this call.
	if err := s.pushClientConfigToAll(ctx, pushUser); err != nil {
		if taskErr := s.enqueueUserTask(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync emergency access for user %s", pushUser.UPN)); taskErr != nil {
			log.Warn("enqueue emergency access sync failed", "user_id", userID, "err", taskErr)
		}
	}
	return result, nil
}

func EmergencyAccessStatusForUser(u *domain.User, settings ports.UISettings, now time.Time) EmergencyAccessStatus {
	return EmergencyAccessStatusForUserWithTrafficLimit(u, settings, now, false)
}

func EmergencyAccessStatusForUserWithTrafficLimit(u *domain.User, settings ports.UISettings, now time.Time, trafficLimitExceeded bool) EmergencyAccessStatus {
	st := EmergencyAccessStatus{
		Enabled:       settings.EmergencyAccessEnabled,
		DurationHours: settings.EmergencyAccessHours,
		MaxCount:      settings.EmergencyAccessMaxCount,
		QuotaBytes:    int64(settings.EmergencyAccessQuotaGB * 1024 * 1024 * 1024),
	}
	if u != nil {
		st.UsedCount = u.EmergencyUsedCount
		st.Until = u.EmergencyUntil
		if u.EmergencyUntil != nil && u.EmergencyUntil.After(now) {
			used := u.LifetimeTotalBytes - u.EmergencyBaselineBytes
			if used < 0 {
				used = 0
			}
			st.UsedBytes = used
		}
	}
	st.Remaining = st.MaxCount - st.UsedCount
	if st.Remaining < 0 {
		st.Remaining = 0
	}
	if !st.Enabled {
		st.Status = "disabled"
		st.Reason = "emergency access is disabled"
		return st
	}
	if st.DurationHours <= 0 || st.MaxCount <= 0 {
		st.Status = "invalid_settings"
		st.Reason = "emergency access settings are invalid"
		return st
	}
	if u == nil {
		st.Status = "user_not_found"
		st.Reason = "user not found"
		return st
	}
	// Check "active" BEFORE "remaining". A user mid-window has used_count >=
	// 1 already (used it to open the window), so for single-use configs
	// remaining is 0 — but they shouldn't see "次数已用完" while their
	// granted window is still ticking. The remaining check is for "can I
	// open ANOTHER window", which is moot when one is already open.
	emergencyActive := u.EmergencyUntil != nil && u.EmergencyUntil.After(now)
	if emergencyActive {
		st.Status = "active"
		st.Reason = "emergency access is already active"
		return st
	}
	if st.Remaining <= 0 {
		st.Status = "no_quota"
		st.Reason = "emergency access limit reached"
		return st
	}
	expired := u.ExpireAt != nil && !u.ExpireAt.After(now)
	expiredEligible := expired && u.Enabled
	trafficExceeded := (u.ServiceDisabledReason == domain.DisabledTrafficExceeded && u.Enabled) ||
		(u.AutoDisabledReason == domain.DisabledTrafficExceeded && (!u.Enabled || u.EmergencyUntil != nil)) ||
		(trafficLimitExceeded && u.Enabled)
	if !expiredEligible && !trafficExceeded {
		st.Status = "not_eligible"
		st.Reason = "emergency access is only available after expiry or traffic limit exceeded"
		return st
	}
	st.Available = true
	st.Status = "available"
	return st
}

func (s *Service) ResetEmergencyUsage(ctx context.Context, userID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.EmergencyUsedCount == 0 && u.EmergencyUntil == nil && u.EmergencyBaselineBytes == 0 {
		return nil
	}
	u.EmergencyUsedCount = 0
	// Clear the active window and quota baseline too — otherwise an admin
	// "reset" leaves the user mid-window with a stale baseline that would mis-
	// attribute future traffic the moment another window is granted.
	u.EmergencyUntil = nil
	u.EmergencyBaselineBytes = 0
	return s.updateUser(ctx, u)
}

// ChangeGroupAndSync moves a user to a different group and reconciles their
// 3X-UI client memberships against the new group's tag_filter.
//
// Wraps ResyncMembership.
func (s *Service) ChangeGroupAndSync(ctx context.Context, userID, newGroupID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if _, err := s.groups.GetByID(ctx, newGroupID); err != nil {
		return fmt.Errorf("group: %w", err)
	}
	if u.GroupID == newGroupID {
		return nil
	}
	u.GroupID = newGroupID
	if err := s.updateUser(ctx, u); err != nil {
		return err
	}
	if err := s.ResyncMembershipOrEnqueue(ctx, userID, fmt.Sprintf("sync node membership for user %s", u.UPN)); err != nil {
		log.Warn("enqueue user membership resync failed", "user_id", userID, "err", err)
	}
	return nil
}

// ResyncMembershipOrEnqueue tries ResyncMembership immediately and leaves a
// durable task when the remote panel is unavailable. Local state has already
// been committed by callers before this is invoked.
func (s *Service) ResyncMembershipOrEnqueue(ctx context.Context, userID int64, summary string) error {
	if err := s.ResyncMembership(ctx, userID); err != nil {
		return s.enqueueUserTask(ctx, domain.SyncTaskUserResync, userID, summary)
	}
	return nil
}

// ResyncGroupMembersInBackground recomputes every member's 3X-UI memberships
// after a group's tag_filter changed. It runs immediately (sync-first, falling
// back to the async task queue per member on failure — same as
// ResyncMembershipOrEnqueue) but OFF the request thread, so saving the group
// returns at once instead of blocking on N sequential per-member 3X-UI
// round-trips. Uses a fresh context because the caller's request context is
// cancelled once the save response is written; anything left unsynced if the
// process stops mid-run is healed by the next reconcile pass.
func (s *Service) ResyncGroupMembersInBackground(groupID int64) {
	work := func(ctx context.Context) {
		members, err := s.users.ListByGroup(ctx, groupID)
		if err != nil {
			log.Warn("resync group members: list", "group_id", groupID, "err", err)
			return
		}
		for _, m := range members {
			if err := s.ResyncMembershipOrEnqueue(ctx, m.ID, "sync node membership for user "+m.UPN); err != nil {
				log.Warn("resync group member", "group_id", groupID, "user_id", m.ID, "err", err)
			}
		}
	}
	// When the app's tracked dispatcher is wired, run under the panel-wide
	// WaitGroup + cancellable background context so Shutdown drains it. Before
	// wiring (tests / early-start) fall back to an untracked goroutine with its
	// own context — anything left unsynced is healed by the next reconcile pass.
	if s.bg != nil {
		s.bg("user.resync-group-members", work)
		return
	}
	safego.Go("user.resync-group-members", func() { work(context.Background()) })
}

// ResyncMembership recomputes a user's 3X-UI client memberships against the
// CURRENT group definition (after potential changes) and converges the shared
// client plus any remaining legacy fallback state.
//
// v3.9 makes the shared-client model primary: this builds the user's desired
// psp_client set, provisions it into 3X-UI, pushes lifecycle/quota state, and
// only then removes the legacy per-node fallback rows. Errors during individual
// phases are returned as a single wrapped error after partial progress is
// preserved. Drift left behind is healed by the next reconciliation pass.
// syncUserDesired runs ONLY the DB build phase of a resync: recompute the user's
// desired psp_client set from their group's nodes and write it (psp.SyncUser). It's
// the prerequisite for the bulk node warm-up — the freshly-added node must be in the
// user's attachment set before BulkProvisionNodeInbound can create/attach onto it.
// Mirrors ResyncMembership's build preamble; the full resync remains authoritative.
func (s *Service) syncUserDesired(ctx context.Context, userID int64) error {
	if s.psp == nil {
		return nil
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.AutoDisabledReason == domain.DisabledPendingDelete {
		return nil
	}
	g, err := s.groups.GetByID(ctx, u.GroupID)
	if err != nil {
		return err
	}
	desiredNodes, err := s.selector.NodesFor(ctx, g)
	if err != nil {
		return err
	}
	desiredNodes = s.resolveShadowsocksMethods(ctx, desiredNodes)
	_, err = s.psp.SyncUser(ctx, u.ID, u.UUID, s.emailRules(ctx), desiredNodes)
	return err
}

// BulkProvisionNodeMembers builds every member's desired psp_client set (so the new
// node is in their attachments) and then provisions them onto the node's inbound in
// ONE bulkCreate + ONE bulkAttach via the migrator — collapsing a node-add / outage-
// heal create fan-out from N Xray restarts to 1. Best-effort warm-up: the caller's
// per-member ResyncMembershipOrEnqueue still runs as the authoritative pass, so a
// miss or partial failure here is healed (this never marks provisioned or pushes
// lifecycle — that stays with the resync).
func (s *Service) BulkProvisionNodeMembers(ctx context.Context, n *domain.Node, userIDs []int64) error {
	if s.migrator == nil || n == nil || len(userIDs) == 0 {
		return nil
	}
	for _, uid := range userIDs {
		if err := s.syncUserDesired(ctx, uid); err != nil {
			return err
		}
	}
	return s.migrator.BulkProvisionNodeInbound(ctx, n, userIDs)
}

func (s *Service) ResyncMembership(ctx context.Context, userID int64) error {
	// Serialize concurrent resyncs of the SAME user (heal sweep + sync-task drain +
	// request threads) so a re-key in one pass can't race the orphan reconcile in
	// another. Per-user, so different users still resync in parallel.
	unlock := s.lockUser(userID)
	defer unlock()

	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.AutoDisabledReason == domain.DisabledPendingDelete {
		return nil
	}
	g, err := s.groups.GetByID(ctx, u.GroupID)
	if err != nil {
		return err
	}
	desiredNodes, err := s.selector.NodesFor(ctx, g)
	if err != nil {
		return err
	}
	// v3.9.0 silent-migration correctness: a Shadowsocks node that was never
	// captured has empty InboundSettings, so the plan builder can't tell SS from
	// SS-2022 and would drop it into the raw-UUID credential class — rendering an
	// unusable PSK. Resolve the live cipher method before the psp_client plan is
	// built (clientplan.NodeCredFromNode's documented contract).
	desiredNodes = s.resolveShadowsocksMethods(ctx, desiredNodes)
	rules := s.emailRules(ctx)
	var firstErr error

	// v3.9.0 INVERTED ENROLLMENT: the shared-client model is PRIMARY. Build the
	// psp_client set from the desired nodes (DB), then reconcile it into 3X-UI —
	// provision the shared client, attach the desired inbounds, detach removed
	// ones — and delete the user's legacy per-node clients. Render derives the
	// SAME credentials the shared client stores (silent), so this never disrupts a
	// live connection. (Replaces the old per-node ADD/UPDATE/DEL ownership diff.)
	var prunedClients map[int64][]string
	if s.psp != nil {
		pruned, err := s.psp.SyncUser(ctx, u.ID, u.UUID, rules, desiredNodes)
		prunedClients = pruned
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("psp dual-write: %w", err)
			}
		}
	}
	// Order is enforcement-critical: provision the shared client → push the user's
	// REAL enable/expiry/floor onto it → ONLY THEN delete the legacy per-node
	// clients (which held the correct disabled/expired state). Provisioning with a
	// hardcoded enable=true and deleting the per-node fallback BEFORE the lifecycle
	// push would leave a disabled/expired/over-quota user with a fully-enabled
	// shared client (an enforcement bypass). Delete is skipped if provision failed,
	// so the per-node fallback survives.
	provisioned := false
	if s.migrator != nil {
		if err := s.migrator.ProvisionUser(ctx, u.ID); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("shared provision: %w", err)
			}
		} else {
			provisioned = true
		}
	}
	// The lifecycle push corrects the shared client from its provision default
	// (Enable:true / no expiry / no quota) to the user's REAL state. If it FAILS,
	// the shared client is still at that default, so we must NOT delete the legacy
	// per-node fallback (which holds the correct disabled/expired state) — doing so
	// would leave a disabled/expired/over-quota user fully enabled with no fallback
	// (audit #1). Surface the error so the sync-task retries and re-pushes next run.
	lifeErr := s.syncSharedLifecycle(ctx, u)
	if lifeErr != nil && firstErr == nil {
		firstErr = fmt.Errorf("shared lifecycle: %w", lifeErr)
	}
	// Clean up the user's STALE shared clients (pre-merge per-class clients the merge
	// re-keyed, or any client whose psp_client row was pruned but whose 3X-UI client
	// survived a skipped delete and is now UNTRACKED). This is the self-healing net
	// for the original bug: it DISCOVERS orphans by listing each panel's live clients
	// rather than relying on the prune map (whose emails are lost once the DB row is
	// gone). It is gated PER PANEL on coverage inside ReconcileOrphans — so it cleans a
	// healthy panel even if another panel is down (the cross-panel coupling that
	// created the orphans), and never deletes a client whose inbounds the live
	// replacement doesn't cover. It only deletes shared-scheme emails (never the
	// legacy per-node fallback), and removing an unmanaged stale client only tightens
	// enforcement — so it needs no provisioned/lifeErr precondition.
	if s.migrator != nil {
		if err := s.migrator.ReconcileOrphans(ctx, u.ID); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("orphan reconcile: %w", err)
		}
	}
	if provisioned && lifeErr == nil && s.migrator != nil {
		// The merged/current shared client(s) are now live, so it's safe to remove
		// the 3X-UI clients the dual-write pruned (old per-class clients the v3.9.0
		// merge collapsed, or clients on a panel/node the user left). Same ordering
		// rationale as the legacy delete: new client up first, then drop the old.
		s.deletePrunedSharedClients(ctx, prunedClients)
		if err := s.migrator.DeleteLegacyForUser(ctx, u.ID); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("delete legacy: %w", err)
			}
		}
	}

	return firstErr
}

// deletePrunedSharedClients removes from 3X-UI the shared clients whose psp_client
// rows the dual-write pruned (clientprov.Sync returns their emails per panel). The
// DB row is already gone; this deletes the now-orphaned 3X-UI client so a merge or
// a node/panel removal leaves no stray client. Best-effort: a panel that's
// unreachable is retried on the next resync. Delete is by email (panel-wide).
func (s *Service) deletePrunedSharedClients(ctx context.Context, pruned map[int64][]string) {
	for panelID, emails := range pruned {
		cli, err := s.pool.Get(panelID)
		if err != nil {
			log.Warn("delete pruned shared client: pool get", "panel_id", panelID, "err", err)
			continue
		}
		for _, email := range emails {
			if err := cli.DelClientByEmail(ctx, 0, email); err != nil {
				log.Warn("delete pruned shared client", "panel_id", panelID, "email", email, "err", err)
			}
		}
	}
}

// HealSharedClients is the v3.9.0 shared-model drift heal. It walks every user and
// re-runs a FULL membership reconcile (ResyncMembership) per user: rebuild the
// desired psp_client set, provision it, push lifecycle, and delete now-orphaned
// old/legacy clients. This repairs every kind of drift — a 3X-UI client manually
// deleted/detached, a provision that exhausted its sync-task retries, AND a stale
// psp_client SHAPE (e.g. a user still split into pre-merge per-class clients that
// the current clientplan would consolidate). The full reconcile is what converges
// existing users onto the merged-client model with no separate one-shot trigger.
//
// Idempotent + cheap once converged: the no-op skips (provision skips when already
// attached to the desired set; lifecycle skips when 3X-UI already holds the exact
// state+creds) mean a no-drift user costs only GetClient READS — zero Xray restarts
// — plus a NodesFor + a no-change dual-write, so it is safe on the reconcile
// cadence. Best-effort + concurrency-capped: per-user failures are logged and
// skipped. Returns the count of users reconciled and the first error.
func (s *Service) HealSharedClients(ctx context.Context) (int, error) {
	if s.migrator == nil {
		return 0, nil // shared model not wired (tests / non-shared build)
	}
	cfg, _ := s.settings.Load(ctx, ports.UISettings{})
	concurrency := paneltz.ResolveMaxPanelConcurrency(cfg.MaxPanelConcurrency)

	const pageSize = 200
	var mu sync.Mutex
	var healed int
	var firstErr error
	note := func(err error) {
		mu.Lock()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			healed++
		}
		mu.Unlock()
	}
	for page := 1; ; page++ {
		users, total, err := s.users.List(ctx, ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			note(err)
			break
		}
		if len(users) == 0 {
			break
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, concurrency)
		for _, u := range users {
			if u == nil || u.AutoDisabledReason == domain.DisabledPendingDelete {
				continue
			}
			wg.Add(1)
			go func(u *domain.User) {
				defer safego.Recover("user.HealSharedClients")
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				// Full reconcile (not just provision): rebuilds the psp_client set so a
				// user migrated under an OLDER partition (pre-merge per-class clients) is
				// re-grouped into the merged client, provisions it, pushes lifecycle, and
				// deletes the orphaned old clients — then no-ops once converged.
				if err := s.ResyncMembership(ctx, u.ID); err != nil {
					log.Warn("shared heal: resync", "user_id", u.ID, "err", err)
					note(err)
					return
				}
				note(nil)
			}(u)
		}
		wg.Wait()
		if int64(page*pageSize) >= total {
			break
		}
	}
	return healed, firstErr
}

// SetEnabledAndSync flips the enabled flag and propagates it to every owned
// 3X-UI client. Used both by the admin UI and by traffic-limit enforcement.
//
// Iterates over the ownership table (rather than re-deriving from the
// user's group) so imported clients with their recorded email are still
// updated correctly.
func (s *Service) SetEnabledAndSync(ctx context.Context, userID int64, enabled bool, reason domain.AutoDisabledReason, detail string) error {
	if enabled && reason != domain.DisabledNone {
		return fmt.Errorf("%w: enabling an account cannot carry a disable reason", domain.ErrValidation)
	}
	if !enabled && !domain.AccountDisableReason(reason) {
		return fmt.Errorf("%w: invalid account disable reason %q", domain.ErrValidation, reason)
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	// Last-admin lockout guard: disabling the only enabled admin — whether by an
	// admin action or an auto-disable path (quota/expiry) — would lock everyone
	// out. Availability of panel management beats quota enforcement for the sole
	// admin (who normally has no quota anyway). Mirrors UpdateProfile/DeleteAndSync.
	if !enabled && u.Role == domain.RoleAdmin && u.Enabled {
		n, err := s.users.CountEnabledAdmins(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return fmt.Errorf("%w: cannot disable the last enabled admin", domain.ErrValidation)
		}
	}
	u.Enabled = enabled
	u.AutoDisabledReason = reason
	u.DisableDetail = detail
	// On disable, bump TokenVersion so any JWT in flight stops working
	// immediately for protected endpoints (self-service /api/user/me is
	// still allowed for quota/expired disables; see RequireAuth).
	if !enabled {
		u.TokenVersion++
	}
	if err := s.updateUser(ctx, u); err != nil {
		return err
	}
	// pushClientConfigToAll mirrors the enable/expiry flip onto the shared client
	// (HOLE #1) before its per-node work — this is THE path admin disable +
	// quota/expiry auto-disable funnel through, so it's what cuts a disabled user's
	// shared client off. (No separate syncSharedLifecycle call here: that would be
	// a redundant second UpdateClient — pushClientConfigToAll already does it.)
	pushErr := s.pushClientConfigToAll(ctx, u)
	if pushErr != nil {
		if taskErr := s.enqueueUserTask(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync enabled/expiry config for user %s", u.UPN)); taskErr != nil {
			log.Warn("enqueue user config push failed", "user_id", userID, "err", taskErr)
		}
		return nil
	}
	return nil
}

// SetServiceSuspendedAndSync pauses proxy/subscription service while preserving
// panel login. Used by blocked-client enforcement, quota enforcement, and the
// admin "pause service" action.
func (s *Service) SetServiceSuspendedAndSync(ctx context.Context, userID int64, reason domain.AutoDisabledReason, detail string) error {
	if !domain.ServiceSuspensionReason(reason) {
		return fmt.Errorf("%w: invalid service suspension reason %q", domain.ErrValidation, reason)
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := s.updateServiceState(ctx, userID, reason, detail, &now); err != nil {
		return err
	}
	u.ServiceDisabledReason = reason
	u.ServiceDisableDetail = detail
	u.ServiceDisabledAt = &now
	// Notify the user their SERVICE is suspended (with the reason). Async + nil-
	// tolerant; fired once here so EVERY suspend path (quota poll, blocked-client,
	// admin manual, manual usage override) emails uniformly from this chokepoint.
	// The state is already committed (UpdateServiceState above), so the email is
	// correct even if the 3X-UI push below fails and is enqueued.
	s.notifyServiceSuspended(userID, reason, detail)
	if err := s.pushClientConfigToAll(ctx, u); err != nil {
		if taskErr := s.enqueueUserTask(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync service status for user %s", u.UPN)); taskErr != nil {
			log.Warn("enqueue user service-status push failed", "user_id", userID, "err", taskErr)
		}
		return nil
	}
	return nil
}

// ResumeServiceAndSync clears a service-level suspension. If the suspension was
// caused by blocked-client violations, the violation counter is reset as part of
// the same operation so the next allowed restore does not instantly re-suspend.
func (s *Service) ResumeServiceAndSync(ctx context.Context, userID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	wasBlocked := u.ServiceDisabledReason == domain.DisabledBlockedClient
	if err := s.updateServiceState(ctx, userID, domain.DisabledNone, "", nil); err != nil {
		return err
	}
	u.ServiceDisabledReason = domain.DisabledNone
	u.ServiceDisableDetail = ""
	u.ServiceDisabledAt = nil
	// Tell the user their service is back (async, nil-tolerant). Mirrors the
	// suspend notification so a suspend mail always has a matching resume mail.
	s.notifyServiceRestored(userID)
	if wasBlocked {
		if err := s.users.ClearBlockViolation(ctx, userID); err != nil {
			log.Warn("ResumeServiceAndSync: ClearBlockViolation failed; service restored but violation counter not reset",
				"user_id", userID, "err", err)
		}
	}
	if err := s.pushClientConfigToAll(ctx, u); err != nil {
		if taskErr := s.enqueueUserTask(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync service resume for user %s", u.UPN)); taskErr != nil {
			log.Warn("enqueue user service-resume push failed", "user_id", userID, "err", taskErr)
		}
		return nil
	}
	return nil
}

// PushClientConfig is the public entry the traffic poll worker calls after
// each successful snapshot to refresh the per-client traffic floor in
// 3X-UI. Thin wrapper around pushClientConfigToAll so the worker doesn't
// need access to the internal helper.
func (s *Service) PushClientConfig(ctx context.Context, userID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	return s.pushClientConfigToAll(ctx, u)
}

// pushClientConfigToAll iterates through all owned clients of the user and
// pushes Enable + ExpireAt + the per-client traffic floor to 3X-UI. The
// floor is the safety-net cap (limit - period_used) that lets 3X-UI cut
// off the client when the panel is offline. Computed once per call since
// it depends on a snapshot read that can be slow on large traffic tables.
func (s *Service) pushClientConfigToAll(ctx context.Context, u *domain.User) error {
	// Refresh the SHARED client's enable/expiry/quota-floor too. The traffic poll
	// calls this for users who accrued traffic, so it is the per-poll refresh that
	// keeps the Xray-side totalGB safety net (cut the user off while PSP is offline)
	// current as the floor (limit - period_used) shrinks. Runs BEFORE the early
	// return below, so a fully-migrated user (zero ownership rows) still gets it.
	s.syncSharedLifecycle(ctx, u)

	entries, err := s.ownership.ListByUser(ctx, u.ID)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	expireTime := u.PushExpireTime()
	floor := s.trafficFloor(ctx, u)

	// Resolve concurrency cap once. The setting is shared with the
	// traffic poll and reconcile fan-out (v2.2.7 admin tunable) so an
	// admin moving one slider controls every 3X-UI fan-out in the panel.
	cfg, _ := s.settings.Load(ctx, ports.UISettings{})
	concurrency := paneltz.ResolveMaxPanelConcurrency(cfg.MaxPanelConcurrency)

	// Phase 1 — fetch ONLY the inbounds this user actually owns on each panel
	// (via GetInbound), not the panel's entire inbound+client roster. The push
	// path consumes just the owned inbound's Protocol + Settings (method/flow);
	// in 3.2.0 the write itself (UpdateClient by email) reads no clients[] at
	// all, so the panel-wide ListInbounds the old code ran was fetched and
	// discarded. Users typically own 1 inbound per panel, so this collapses a
	// whole-panel pull to 1–2 by-id gets.
	//
	// "Don't mass-drop ownership on a panel blip" guard preserved WITHOUT a full
	// list: a panel that resolves ZERO of its requested inbounds is treated as
	// down (skip, keep ownership); if it resolves at least one it's up, so a
	// still-missing inbound was genuinely deleted upstream and its stale
	// ownership is dropped — same outcome as the old empty-vs-non-empty check,
	// with no inbound-not-found error-string matching (which would risk dropping
	// a live inbound on a transient error).
	panelInbounds := make(map[int64]map[int]struct{})
	for _, e := range entries {
		ids := panelInbounds[e.PanelID]
		if ids == nil {
			ids = make(map[int]struct{})
			panelInbounds[e.PanelID] = ids
		}
		ids[e.InboundID] = struct{}{}
	}
	type panelData struct {
		byInbound   map[int]*ports.Inbound
		anyResolved bool // panel returned ≥1 requested inbound → it's reachable
		err         error
	}
	panelMap := make(map[int64]panelData, len(panelInbounds))
	var prefetchMu sync.Mutex
	var prefetchWG sync.WaitGroup
	prefetchSem := make(chan struct{}, concurrency)
	for pid, ids := range panelInbounds {
		prefetchWG.Add(1)
		go func(p int64, want map[int]struct{}) {
			defer safego.Recover("user.pushClientConfigToAll.prefetch")
			defer prefetchWG.Done()
			prefetchSem <- struct{}{}
			defer func() { <-prefetchSem }()
			c, err := s.pool.Get(p)
			if err != nil {
				prefetchMu.Lock()
				panelMap[p] = panelData{err: err}
				prefetchMu.Unlock()
				return
			}
			idx := make(map[int]*ports.Inbound, len(want))
			for id := range want {
				inb, gerr := c.GetInbound(ctx, id)
				if gerr != nil || inb == nil {
					continue // missing — left absent, classified in Phase 2
				}
				idx[inb.ID] = inb
			}
			prefetchMu.Lock()
			panelMap[p] = panelData{byInbound: idx, anyResolved: len(idx) > 0}
			prefetchMu.Unlock()
		}(pid, ids)
	}
	prefetchWG.Wait()

	// Phase 2 — concurrent SetOwnedClientEnable across entries, capped
	// by the same sema. Ownership-table writes (stale cleanup) and
	// firstErr collection happen sequentially after the fan-out so we
	// don't race the repo or get nondeterministic error reporting.
	type pushOutcome struct {
		entry        *domain.XUIClientEntry
		err          error
		staleInbound bool
		panelErr     error // separate so we can distinguish "panel down" from "per-entry error"
	}
	outcomes := make(chan pushOutcome, len(entries))
	var pushWG sync.WaitGroup
	pushSem := make(chan struct{}, concurrency)
	for _, e := range entries {
		pd, ok := panelMap[e.PanelID]
		if !ok || pd.err != nil {
			perr := fmt.Errorf("panel %d not reachable", e.PanelID)
			if ok && pd.err != nil {
				perr = pd.err
			}
			outcomes <- pushOutcome{entry: e, panelErr: perr}
			continue
		}
		inb, found := pd.byInbound[e.InboundID]
		if !found {
			// GetInbound didn't resolve this owned inbound. If the panel
			// resolved NONE of its requested inbounds, treat it as a transient
			// blip (3X-UI restart / momentary state) and skip this cycle WITHOUT
			// dropping ownership — the next reconcile confirms + cleans up for
			// real. If it resolved others, the panel is up and this inbound was
			// genuinely deleted upstream, so the stale ownership row is dropped.
			if !pd.anyResolved {
				log.Warn("user.pushClientConfigToAll: panel resolved no owned inbounds; skipping ownership without deletion",
					"panel_id", e.PanelID, "inbound_id", e.InboundID, "email", e.ClientEmail)
				outcomes <- pushOutcome{entry: e}
				continue
			}
			outcomes <- pushOutcome{entry: e, staleInbound: true}
			continue
		}
		info := inboundInfo{
			ssMethod: extractSSMethod(inb.Settings),
			flow:     extractDefaultFlow(inb.Settings),
		}
		info.protocol = crypto.DetectProtocol(inb.Protocol, info.ssMethod)
		if info.protocol == "" {
			outcomes <- pushOutcome{entry: e}
			continue
		}
		pushWG.Add(1)
		entry := e
		infoCopy := info
		inbCopy := inb
		go func() {
			defer safego.Recover("user.pushClientConfigToAll.push")
			defer pushWG.Done()
			pushSem <- struct{}{}
			defer func() { <-pushSem }()
			// Use the pre-fetched inbound — pre-fix this called
			// SetOwnedClientEnable which then ran GetInbound per push,
			// re-fetching what Phase 1 already had in hand.
			perr := s.syncer.SetOwnedClientEnableWithInbound(ctx, entry.PanelID, inbCopy, entry.ClientEmail,
				infoCopy.protocol, infoCopy.ssMethod, u.UUID, infoCopy.flow, u.EffectiveEnabled(time.Now()), expireTime, floor)
			outcomes <- pushOutcome{entry: entry, err: perr}
		}()
	}
	pushWG.Wait()
	close(outcomes)

	// Collect serially: ownership.RemoveByMatch isn't goroutine-safe to
	// race, and firstErr should be deterministic.
	var firstErr error
	for o := range outcomes {
		if o.staleInbound {
			if rmErr := s.ownership.RemoveByMatch(ctx, o.entry.PanelID, o.entry.InboundID, o.entry.ClientEmail); rmErr != nil {
				log.Warn("stale ownership cleanup failed",
					"panel_id", o.entry.PanelID, "inbound_id", o.entry.InboundID, "email", o.entry.ClientEmail, "err", rmErr)
			} else {
				log.Info("removed stale ownership (3X-UI inbound deleted)",
					"panel_id", o.entry.PanelID, "inbound_id", o.entry.InboundID, "email", o.entry.ClientEmail)
			}
			continue
		}
		if o.panelErr != nil && firstErr == nil {
			firstErr = fmt.Errorf("inspect %d/%d/%s: %w", o.entry.PanelID, o.entry.InboundID, o.entry.ClientEmail, o.panelErr)
			continue
		}
		if o.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("push config %d/%d/%s: %w", o.entry.PanelID, o.entry.InboundID, o.entry.ClientEmail, o.err)
		}
	}
	return firstErr
}

// ProcessDueTasks runs pending user-scoped sync tasks. It is safe to call
// from a periodic background loop; every failed remote write is persisted
// with a backoff and retried later.
func (s *Service) ProcessDueTasks(ctx context.Context, limit int) error {
	if s.tasks == nil {
		return nil
	}
	tasks, err := s.tasks.ListDue(ctx, time.Now(), limit)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Type != domain.SyncTaskUserDelete &&
			task.Type != domain.SyncTaskUserResync &&
			task.Type != domain.SyncTaskUserPushConfig &&
			task.Type != domain.SyncTaskUserMigrate {
			continue
		}
		claimed, err := s.tasks.MarkRunning(ctx, task.ID)
		if err != nil {
			// Per-task bookkeeping error: log and continue so one transient DB
			// blip doesn't strand the rest of this batch (the task stays Pending
			// and is retried next tick).
			log.Warn("user task mark-running", "task_id", task.ID, "err", err)
			continue
		}
		if !claimed {
			// Canceled by admin (or claimed by another runner) in the window
			// between ListDue and here — skip so the 3X-UI side effect the admin
			// just canceled never fires.
			continue
		}
		if err := s.runUserTask(ctx, task); err != nil {
			// Cap retries at maxUserTaskAttempts. At 1-minute backoff this
			// is ~1.5 hours of trying — long enough for any realistic
			// transient outage, short enough that a permanently broken
			// task (e.g. 3X-UI rejecting a stale inbound config that the
			// admin has since deleted) doesn't loop forever burning CPU +
			// 3X-UI quota. The task is cancelled with the last error
			// preserved so admin can see WHY it gave up in the Sync Tasks
			// view; manual "Retry" still works as the explicit override.
			if task.Attempts+1 >= maxUserTaskAttempts {
				log.Warn("user task gave up after max attempts",
					"task_id", task.ID, "type", task.Type,
					"target_id", task.TargetID, "attempts", task.Attempts+1,
					"last_err", err.Error())
				if markErr := s.tasks.Cancel(ctx, task.ID); markErr != nil {
					log.Warn("user task cancel", "task_id", task.ID, "err", markErr)
				}
				continue
			}
			next := time.Now().Add(deleteTaskBackoff(task.Attempts + 1))
			if markErr := s.tasks.MarkRetry(ctx, task.ID, err.Error(), next); markErr != nil {
				log.Warn("user task mark-retry", "task_id", task.ID, "err", markErr)
			}
			continue
		}
		if err := s.tasks.MarkSucceeded(ctx, task.ID); err != nil {
			log.Warn("user task mark-succeeded", "task_id", task.ID, "err", err)
		}
	}
	return nil
}

func (s *Service) runUserTask(ctx context.Context, task *domain.SyncTask) error {
	switch task.Type {
	case domain.SyncTaskUserDelete:
		return s.runUserDeleteTask(ctx, task)
	case domain.SyncTaskUserResync:
		if err := s.ResyncMembership(ctx, task.TargetID); err != nil {
			// User deleted between enqueue and run → nothing to resync, task is
			// done. Without this the task fails and retries ~100x. Mirrors the
			// SyncTaskUserPushConfig ErrNotFound handling below.
			if errors.Is(err, domain.ErrNotFound) {
				return nil
			}
			return err
		}
		return nil
	case domain.SyncTaskUserPushConfig:
		u, err := s.users.GetByID(ctx, task.TargetID)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.pushClientConfigToAll(ctx, u)
	case domain.SyncTaskUserMigrate:
		// Drive the migration through ResyncMembership: dual-write → provision →
		// LIFECYCLE → delete legacy, so an auto-migrated disabled/expired/over-quota
		// user's shared client gets the correct enable/expiry (no enforcement bypass).
		if err := s.ResyncMembership(ctx, task.TargetID); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil // user deleted between enqueue and run → done
			}
			return err // transient (e.g. 3X-UI down) → the queue retries with backoff
		}
		return nil
	default:
		return nil
	}
}

func (s *Service) runUserDeleteTask(ctx context.Context, task *domain.SyncTask) error {
	u, err := s.users.GetByID(ctx, task.TargetID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	u.Enabled = false
	u.AutoDisabledReason = domain.DisabledPendingDelete
	if err := s.updateUser(ctx, u); err != nil {
		return err
	}
	if err := s.syncer.DelAllOwnedForUser(ctx, u.ID); err != nil {
		return fmt.Errorf("sync delete: %w", err)
	}
	if err := s.deleteSharedForUser(ctx, u.ID); err != nil {
		return fmt.Errorf("shared delete: %w", err)
	}
	return s.deleteUser(ctx, u.ID)
}

// deleteSharedForUser tears down the user's v3.9.0 shared clients (3X-UI + their
// psp_client rows) via the migrator. Nil-tolerant: a no-op when the shared model
// isn't wired (matches the late-binding convention). MUST be called before the
// user row is deleted — psp_client rows are keyed by userID with no FK cascade.
func (s *Service) deleteSharedForUser(ctx context.Context, userID int64) error {
	if s.migrator == nil {
		return nil
	}
	return s.migrator.DeleteSharedForUser(ctx, userID)
}

// deleteTaskBackoff returns a flat 1-minute retry interval. The sync-first
// design means tasks only reach the queue when 3X-UI was unreachable; in
// that case we want a steady polling cadence rather than exponentially
// pushing the recovery further out.
func deleteTaskBackoff(_ int) time.Duration {
	return time.Minute
}

// maxUserTaskAttempts caps how many times a user-scoped sync task will
// retry before ProcessDueTasks cancels it. At deleteTaskBackoff's flat
// 1-minute cadence this gives a task ~1.5 hours of recovery window —
// well past any plausible transient 3X-UI outage but bounded so a
// permanently broken task (e.g. an inbound the admin has since deleted
// upstream, a credential change that ResyncMembership can't authenticate
// against) doesn't hammer the panel forever. Admin can still hit
// "Retry" in Sync Tasks for an explicit override.
const maxUserTaskAttempts = 100

func (s *Service) enqueueUserTask(ctx context.Context, typ domain.SyncTaskType, userID int64, summary string) error {
	if s.tasks == nil {
		return nil
	}
	if _, err := s.tasks.GetActiveByTarget(ctx, typ, "user", userID); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return s.tasks.Create(ctx, &domain.SyncTask{
		Type:       typ,
		Status:     domain.SyncTaskPending,
		TargetType: "user",
		TargetID:   userID,
		Summary:    summary,
		NextRunAt:  time.Now(),
	})
}

// ResetUUIDAndSync rotates the user UUID and pushes the change to every
// owned 3X-UI client via SyncSvc.RotateClientUUID.
//
// Per-client errors are collected but do not abort the loop — partial
// rotations are healed by the next reconciliation pass, which compares
// each 3X-UI client.id against user.UUID and runs another rotation.
func (s *Service) ResetUUIDAndSync(ctx context.Context, userID int64) (string, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	oldUUID := u.UUID
	newUUID := idgen.NewUUID()
	u.UUID = newUUID
	if err := s.updateUser(ctx, u); err != nil {
		return "", err
	}
	entries, err := s.ownership.ListByUser(ctx, userID)
	if err != nil {
		return newUUID, err
	}
	expireTime := u.PushExpireTime()
	floor := s.trafficFloor(ctx, u)
	needsRetry := false
	for _, e := range entries {
		info, err := s.inspectInboundByPanel(ctx, e.PanelID, e.InboundID)
		if err != nil {
			needsRetry = true
			continue
		}
		if info.protocol == "" {
			continue
		}
		if err := s.syncer.RotateClientUUID(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			info.protocol, info.ssMethod, oldUUID, newUUID, info.flow, u.EffectiveEnabled(time.Now()), expireTime, floor); err != nil {
			needsRetry = true
		}
	}
	// Enqueue a resync after a UUID rotation. On the error path it retries the
	// failed per-node pushes; on the happy path it still runs so the v3.9.0
	// dual-write advances the psp_client's UUID/Password and syncSharedLifecycle
	// pushes the rotated credentials onto the shared client — without this, a
	// successful rotation wouldn't reach the shared client until the next periodic
	// reconcile. The per-node re-push is a no-op (already rotated inline + the
	// no-op-skip), so the resync's real work is just the shared-model propagation.
	if needsRetry || s.sharedLife != nil {
		_ = s.enqueueUserTask(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync credentials for user %s", u.UPN))
	}
	return newUUID, nil
}

// resolveShadowsocksMethods returns desiredNodes with InboundSettings filled in
// for any Shadowsocks node missing it (never captured), so the plan builder can
// distinguish SS from SS-2022 (and the 16- vs 32-byte PSK) and pick the right
// credential class. Non-SS nodes and already-captured SS nodes pass through with
// NO extra 3X-UI call. Best-effort: a node whose inbound can't be fetched passes
// through unchanged (the next resync after capture self-corrects). Patches COPIES
// — never mutates the selector's cached *domain.Node rows.
func (s *Service) resolveShadowsocksMethods(ctx context.Context, nodes []*domain.Node) []*domain.Node {
	var out []*domain.Node // lazily cloned on first patch; nil → return input as-is
	for i, n := range nodes {
		if n == nil {
			continue
		}
		p := strings.ToLower(strings.TrimSpace(n.Protocol))
		if p != "shadowsocks" && p != "ss" {
			continue
		}
		if extractSSMethod(n.InboundSettings) != "" {
			continue // already captured → exact
		}
		info, err := s.inspectInbound(ctx, n)
		if err != nil || info == nil || info.ssMethod == "" {
			continue // best-effort; next resync after capture self-corrects
		}
		if out == nil {
			out = append([]*domain.Node(nil), nodes...)
		}
		patched := *n
		b, _ := json.Marshal(struct {
			Method string `json:"method"`
		}{info.ssMethod})
		patched.InboundSettings = string(b)
		out[i] = &patched
	}
	if out == nil {
		return nodes
	}
	return out
}

// inspectInboundByPanel is the address-by-(panel_id, inbound) version of
// inspectInbound, used when the caller has an ownership entry rather than
// a Node row.
func (s *Service) inspectInboundByPanel(ctx context.Context, panelID int64, inboundID int) (*inboundInfo, error) {
	c, err := s.pool.Get(panelID)
	if err != nil {
		return nil, err
	}
	inb, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return nil, err
	}
	info := &inboundInfo{
		ssMethod: extractSSMethod(inb.Settings),
		flow:     extractDefaultFlow(inb.Settings),
	}
	info.protocol = crypto.DetectProtocol(inb.Protocol, info.ssMethod)
	return info, nil
}

// ---- helpers ----

type inboundInfo struct {
	protocol domain.Protocol
	flow     string
	ssMethod string
}

// inspectInbound fetches the inbound from 3X-UI and extracts the bits we
// need to construct a ClientSpec: protocol (with SS / SS-2022 distinguished
// via the cipher method) and the default xtls flow.
func (s *Service) inspectInbound(ctx context.Context, n *domain.Node) (*inboundInfo, error) {
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return nil, err
	}
	inb, err := c.GetInbound(ctx, n.InboundID)
	if err != nil {
		return nil, err
	}
	return inboundInfoFromInbound(inb, n.Flow), nil
}

// inboundInfoFromInbound extracts protocol / flow / ss-method from an
// already-fetched inbound, applying the node's flow as a fallback (the shared
// core of inspectInbound and ResyncMembership's prefetched-inbound path).
func inboundInfoFromInbound(inb *ports.Inbound, nodeFlow string) *inboundInfo {
	info := &inboundInfo{
		ssMethod: extractSSMethod(inb.Settings),
		flow:     extractDefaultFlow(inb.Settings),
	}
	info.protocol = crypto.DetectProtocol(inb.Protocol, info.ssMethod)
	if info.flow == "" {
		info.flow = nodeFlow
	}
	return info
}

func extractSSMethod(settingsJSON string) string {
	var v struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal([]byte(settingsJSON), &v)
	return v.Method
}

func extractDefaultFlow(settingsJSON string) string {
	var v struct {
		Clients []struct {
			Flow string `json:"flow"`
		} `json:"clients"`
	}
	if json.Unmarshal([]byte(settingsJSON), &v) != nil {
		return ""
	}
	for _, c := range v.Clients {
		if c.Flow != "" {
			return c.Flow
		}
	}
	return ""
}
