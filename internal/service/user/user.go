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
	AddClientToInbound(ctx context.Context, userID int64, panelID int64, inboundID int,
		protocol domain.Protocol, ssMethod, userUUID, email, flow string, expireTime, totalGB int64) error
	DelOwnedClient(ctx context.Context, panelID int64, inboundID int, email string) error
	SetOwnedClientEnable(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, ssMethod, userUUID, flow string, enable bool, expireTime, totalGB int64) error
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
	settings  ports.SettingsRepo
	// trafficUsage is set lazily via SetTrafficUsage after traffic.Service
	// is constructed (traffic depends on user, so user must exist first).
	// May be nil during early-start; trafficFloor degrades to 0 in that case.
	trafficUsage TrafficUsageReader

	emergencyMu sync.Mutex
}

const maxPersonalRulesBytes = 64 * 1024

func New(users ports.UserRepo, groups ports.GroupRepo, ownership ports.OwnershipRepo,
	tasks ports.SyncTaskRepo, selector NodeSelector, syncer ClientSyncer, pool ports.XUIPool, settings ports.SettingsRepo) *Service {
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
	st, err := s.settings.Load(ctx, ports.UISettings{})
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
	_ = s.users.Delete(ctx, userID)
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

	now := time.Now()
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
	if err := s.users.Update(ctx, u); err != nil {
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
	now := time.Now()
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
		if err := s.users.Update(ctx, u); err != nil {
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
	if !u.Enabled && !emergencySelfServiceAllowedReason(u.AutoDisabledReason) {
		return u, domain.ErrForbidden
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, domain.ErrUnauthorized
	}
	return u, nil
}

func emergencySelfServiceAllowedReason(reason domain.AutoDisabledReason) bool {
	// Delegate to the domain helper so the login path and the token-refresh
	// path share one definition (see domain.SelfServiceDisableReason).
	return domain.SelfServiceDisableReason(reason)
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
	if err := s.users.Update(ctx, u); err != nil {
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
	if err := s.users.Update(ctx, u); err != nil {
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
	if err := s.users.Update(ctx, u); err != nil {
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
	if needsRetry {
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
	return s.users.Update(ctx, u)
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
	if err := s.users.Update(ctx, u); err != nil {
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
	return s.users.Update(ctx, u)
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
		_ = s.users.Delete(context.Background(), u.ID)
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

	rules := s.emailRules(ctx)
	floor := s.trafficFloor(ctx, u)
	synced := 0
	needsRetry := false
	for _, n := range nodes {
		info, err := s.inspectInbound(ctx, n)
		if err != nil {
			needsRetry = true
			continue
		}
		if info.protocol == "" {
			continue // unrecognised protocol — skip rather than fail the whole create
		}
		email := u.ClientEmail(n.ID, rules)
		expireTime := u.PushExpireTime()
		if err := s.syncer.AddClientToInbound(ctx, u.ID, n.PanelID, n.InboundID,
			info.protocol, info.ssMethod, u.UUID, email, info.flow, expireTime, floor); err != nil {
			needsRetry = true
			continue
		}
		synced++
	}
	if needsRetry {
		if err := s.enqueueUserTask(ctx, domain.SyncTaskUserResync, u.ID, fmt.Sprintf("sync node membership for user %s", u.UPN)); err != nil {
			return nil, err
		}
	}

	return &CreateLocalSyncedResult{
		User:            u,
		InitialPassword: base.InitialPassword,
		SyncedInbounds:  synced,
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
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}

	// Synchronous fast path: when 3X-UI is reachable, delete every owned
	// client and the panel row right here. This is the SetEnabledAndSync
	// pattern applied to deletion.
	if err := s.syncer.DelAllOwnedForUser(ctx, u.ID); err == nil {
		if err := s.users.Delete(ctx, u.ID); err == nil {
			return nil
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
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	if groupChanged {
		if err := s.ResyncMembershipOrEnqueue(ctx, userID, fmt.Sprintf("sync node membership for user %s", u.UPN)); err != nil {
			log.Warn("enqueue user membership resync failed", "user_id", userID, "err", err)
		}
		return nil
	}
	if expireChanged {
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

		settings, err := s.settings.Load(ctx, ports.UISettings{})
		if err != nil {
			return err
		}
		if !settings.EmergencyAccessEnabled {
			return fmt.Errorf("%w: emergency access is disabled", domain.ErrForbidden)
		}
		if settings.EmergencyAccessHours <= 0 || settings.EmergencyAccessMaxCount <= 0 {
			return fmt.Errorf("%w: emergency access settings are invalid", domain.ErrValidation)
		}

		u, err := s.users.GetByID(ctx, userID)
		if err != nil {
			return err
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
				u.AutoDisabledReason = domain.DisabledTrafficExceeded
				u.DisableDetail = "emergency access active"
			} else {
				u.AutoDisabledReason = domain.DisabledNone
				u.DisableDetail = ""
			}
		}
		if trafficLimitExceeded && u.Enabled {
			u.AutoDisabledReason = domain.DisabledTrafficExceeded
			u.DisableDetail = "emergency access active"
		}
		u.EmergencyUntil = &until
		u.EmergencyUsedCount++
		// Snapshot the lifetime counter so the traffic poll can compute how much
		// the user consumes during this emergency window and end it early once
		// EmergencyAccessQuotaGB is exhausted. Captured even when quota is 0 so
		// admins can flip the cap on later without retroactively breaking the
		// window's accounting.
		u.EmergencyBaselineBytes = u.LifetimeTotalBytes
		if err := s.users.Update(ctx, u); err != nil {
			return err
		}
		// The broad Update above omits the emergency columns (pollOwnedColumns)
		// so a concurrent admin edit can't revert this grant; write them through
		// the targeted writer under the same lock.
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
	expiredEligible := expired && (u.Enabled || u.AutoDisabledReason == domain.DisabledExpired)
	trafficExceeded := (u.AutoDisabledReason == domain.DisabledTrafficExceeded && (!u.Enabled || u.EmergencyUntil != nil)) ||
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
	return s.users.Update(ctx, u)
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
	if err := s.users.Update(ctx, u); err != nil {
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
	safego.Go("user.resync-group-members", func() {
		ctx := context.Background()
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
	})
}

// ResyncMembership recomputes a user's 3X-UI client memberships against
// the CURRENT group definition (after potential changes) and applies the
// diff via SyncSvc.
//
// Algorithm:
//  1. desired = NodesFor(user's group) — set of (panel, inbound) tuples
//  2. current = ownership.ListByUser — set of (panel, inbound, email)
//  3. ADD = desired - current  → AddClientToInbound for each
//  4. UPDATE = desired ∩ current → SetOwnedClientEnable for current config
//  5. DEL = current - desired  → DelOwnedClient for each
//
// Errors during individual sync calls are returned as a single wrapped error
// after the loop so partial progress is preserved. Drift left behind is
// healed by the next reconciliation pass.
func (s *Service) ResyncMembership(ctx context.Context, userID int64) error {
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
	current, err := s.ownership.ListByUser(ctx, userID)
	if err != nil {
		return err
	}

	type key struct {
		panelID   int64
		inboundID int
	}
	desired := make(map[key]*domain.Node, len(desiredNodes))
	for _, n := range desiredNodes {
		desired[key{n.PanelID, n.InboundID}] = n
	}
	have := make(map[key]*domain.XUIClientEntry, len(current))
	for _, e := range current {
		have[key{e.PanelID, e.InboundID}] = e
	}

	rules := s.emailRules(ctx)
	floor := s.trafficFloor(ctx, u)
	var firstErr error

	// Prefetch ListInbounds once per unique panel referenced by the desired
	// nodes (parallel, capped by the shared fan-out concurrency), instead of a
	// serial per-inbound GetInbound inside each ADD/UPDATE iteration. inboundInfo
	// is then resolved from this in-memory index. Mirrors pushClientConfigToAll's
	// Phase-1 prefetch.
	cfg, _ := s.settings.Load(ctx, ports.UISettings{})
	concurrency := paneltz.ResolveMaxPanelConcurrency(cfg.MaxPanelConcurrency)
	// Fetch ONLY the inbounds the desired nodes reference (via GetInbound), not
	// every inbound on each panel. resolveInfo below reads just the inbound-level
	// Protocol + Settings (method/flow) of those specific inbounds; the old
	// ListInbounds pulled the whole panel's clients[] for every inbound and
	// discarded it. Group members on one panel share the same few inbounds, so
	// this also shrinks the per-member fan-out during a group re-tag.
	panelInbounds := make(map[int64]map[int]struct{})
	for k := range desired {
		ids := panelInbounds[k.panelID]
		if ids == nil {
			ids = make(map[int]struct{})
			panelInbounds[k.panelID] = ids
		}
		ids[k.inboundID] = struct{}{}
	}
	type panelData struct {
		byInbound map[int]*ports.Inbound
		err       error
	}
	panelMap := make(map[int64]panelData, len(panelInbounds))
	var prefetchMu sync.Mutex
	var prefetchWG sync.WaitGroup
	prefetchSem := make(chan struct{}, concurrency)
	for pid, ids := range panelInbounds {
		prefetchWG.Add(1)
		go func(p int64, want map[int]struct{}) {
			defer safego.Recover("user.ResyncMembership.prefetch")
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
					continue // missing → resolveInfo returns "inbound not found", as before
				}
				idx[inb.ID] = inb
			}
			prefetchMu.Lock()
			panelMap[p] = panelData{byInbound: idx}
			prefetchMu.Unlock()
		}(pid, ids)
	}
	prefetchWG.Wait()

	// resolveInfo reads the prefetched inbound and builds inboundInfo, mirroring
	// inspectInbound's failure modes (panel unreachable / inbound gone).
	resolveInfo := func(n *domain.Node) (*inboundInfo, error) {
		pd, ok := panelMap[n.PanelID]
		if !ok || pd.err != nil {
			if ok && pd.err != nil {
				return nil, pd.err
			}
			return nil, fmt.Errorf("panel %d not reachable", n.PanelID)
		}
		inb, found := pd.byInbound[n.InboundID]
		if !found {
			return nil, fmt.Errorf("inbound %d not found on panel %d", n.InboundID, n.PanelID)
		}
		return inboundInfoFromInbound(inb, n.Flow), nil
	}

	// ADD: desired but not currently owned
	for k, n := range desired {
		if _, ok := have[k]; ok {
			continue
		}
		info, err := resolveInfo(n)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("inspect %d/%d: %w", k.panelID, k.inboundID, err)
			}
			continue
		}
		if info.protocol == "" {
			continue
		}
		email := u.ClientEmail(n.ID, rules)
		expireTime := u.PushExpireTime()
		if err := s.syncer.AddClientToInbound(ctx, u.ID, k.panelID, k.inboundID,
			info.protocol, info.ssMethod, u.UUID, email, info.flow, expireTime, floor); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("add to %d/%d: %w", k.panelID, k.inboundID, err)
			}
		}
	}

	// UPDATE: currently owned and still desired. Keep remote client fields in
	// lockstep with the local user record. This makes queued user_resync tasks
	// sufficient after credential reset, expiry changes, or enable flips.
	for k, e := range have {
		n, ok := desired[k]
		if !ok {
			continue
		}
		info, err := resolveInfo(n)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("inspect %d/%d: %w", k.panelID, k.inboundID, err)
			}
			continue
		}
		if info.protocol == "" {
			continue
		}
		expireTime := u.PushExpireTime()
		if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			info.protocol, info.ssMethod, u.UUID, info.flow, u.EffectiveEnabled(time.Now()), expireTime, floor); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("update %d/%d: %w", k.panelID, k.inboundID, err)
			}
		}
	}

	// DEL: currently owned but no longer desired
	for k, e := range have {
		if _, ok := desired[k]; ok {
			continue
		}
		if err := s.syncer.DelOwnedClient(ctx, e.PanelID, e.InboundID, e.ClientEmail); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("del from %d/%d: %w", k.panelID, k.inboundID, err)
			}
		}
	}

	return firstErr
}

// SetEnabledAndSync flips the enabled flag and propagates it to every owned
// 3X-UI client. Used both by the admin UI and by traffic-limit enforcement.
//
// Iterates over the ownership table (rather than re-deriving from the
// user's group) so imported clients with their recorded email are still
// updated correctly.
func (s *Service) SetEnabledAndSync(ctx context.Context, userID int64, enabled bool, reason domain.AutoDisabledReason, detail string) error {
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
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	// On re-enable, clear the blocked-client tracking columns. Without
	// this, a user who was auto-disabled at SubBlockAutoDisableCount
	// (say, 5 violations) keeps block_violation_count=5 across the
	// admin's manual re-enable, and the very next /sub fetch with a
	// blocked client increments past the threshold and re-disables
	// instantly — admin has no way to break the loop without an SQL
	// edit. Column-scoped write because pollOwnedColumns omits these
	// columns from the regular Update path above. Best-effort: log
	// instead of failing the whole re-enable.
	if enabled {
		if err := s.users.ClearBlockViolation(ctx, userID); err != nil {
			log.Warn("SetEnabledAndSync: ClearBlockViolation failed; user re-enabled but violation counter not reset",
				"user_id", userID, "err", err)
		}
	}
	if err := s.pushClientConfigToAll(ctx, u); err != nil {
		if taskErr := s.enqueueUserTask(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync enabled/expiry config for user %s", u.UPN)); taskErr != nil {
			log.Warn("enqueue user config push failed", "user_id", userID, "err", taskErr)
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
			task.Type != domain.SyncTaskUserPushConfig {
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
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	if err := s.syncer.DelAllOwnedForUser(ctx, u.ID); err != nil {
		return fmt.Errorf("sync delete: %w", err)
	}
	return s.users.Delete(ctx, u.ID)
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
	if err := s.users.Update(ctx, u); err != nil {
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
	if needsRetry {
		_ = s.enqueueUserTask(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync credentials for user %s", u.UPN))
	}
	return newUUID, nil
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
