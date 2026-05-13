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
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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
		protocol domain.Protocol, userUUID, email, flow string, expireTime int64) error
	DelOwnedClient(ctx context.Context, panelID int64, inboundID int, email string) error
	SetOwnedClientEnable(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, userUUID, flow string, enable bool, expireTime int64) error
	DelAllOwnedForUser(ctx context.Context, userID int64) error
	RotateClientUUID(ctx context.Context, panelID int64, inboundID int, email string,
		protocol domain.Protocol, oldUUID, newUUID, flow string, enable bool, expireTime int64) error
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

	emergencyMu sync.Mutex
}

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
	Username           string
	DisplayName        string // friendly name shown in panel UI (optional)
	InitialPassword    string // if empty, a random one is generated
	GroupID            int64
	ExpireAt           *time.Time
	TrafficLimitBytes  int64
	TrafficResetPeriod domain.ResetPeriod
	Remark             string
}

// CreateLocalResult conveys the generated initial password (shown to admin
// once) plus the persisted user (with uuid + sub_token).
type CreateLocalResult struct {
	User            *domain.User
	InitialPassword string
}

// dropOrphanUser deletes a stale panel user along with any 3X-UI clients
// recorded under their ownership. Used when EnsureSSO needs to reclaim a
// username or clean up a "pending approval" stub from earlier policies —
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
	if in.Username == "" {
		return nil, fmt.Errorf("%w: username required", domain.ErrValidation)
	}
	if _, err := s.users.GetByUsername(ctx, in.Username); err == nil {
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
		Username:           in.Username,
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
		Enabled:            true,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return &CreateLocalResult{User: u, InitialPassword: pwd}, nil
}

// EnsureSSOInput carries the SAML-derived facts a successful SSO login
// brings back, plus the defaults the panel should apply when auto-
// provisioning a new SSO user.
type EnsureSSOInput struct {
	UPN                string
	Email              string
	DisplayName        string
	Groups             []string
	IsAdmin            bool
	DefaultGroupSlug   string
	DefaultExpireDays  int
	DefaultLimitBytes  int64
	DefaultResetPeriod domain.ResetPeriod
}

// EnsureSSO returns the user matching the given UPN; if absent, creates
// one with the supplied defaults. Role is re-evaluated on every call so
// admin group changes in the IdP take effect at the next login.
//
// On first creation the user is automatically resynced to push their
// client into every authorised inbound — they can use the subscription
// URL immediately.
func (s *Service) EnsureSSO(ctx context.Context, in EnsureSSOInput) (*domain.User, error) {
	if in.UPN == "" {
		return nil, fmt.Errorf("%w: upn required", domain.ErrValidation)
	}
	desiredRole := domain.RoleUser
	if in.IsAdmin {
		desiredRole = domain.RoleAdmin
	}

	u, err := s.users.GetByUPN(ctx, in.UPN)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	// Stale auto-creations from the previous "pending approval" policy are
	// treated as if no account exists: drop their 3X-UI clients and the
	// panel row so the caller falls through to the fresh-login path.
	if u != nil && u.AutoDisabledReason == domain.DisabledPendingApproval {
		s.dropOrphanUser(ctx, u.ID)
		u = nil
	}
	if u != nil {
		// Existing SSO user. Reconcile role + display name in case they
		// changed in the IdP.
		dirty := false
		if u.Role != desiredRole {
			u.Role = desiredRole
			dirty = true
		}
		if in.DisplayName != "" && u.DisplayName != in.DisplayName {
			u.DisplayName = in.DisplayName
			dirty = true
		}
		// Fix garbled usernames from earlier versions that stored the opaque
		// UPN/NameID. Prefer email, then display name, then keep current.
		// If the target name is held by a stale passwordless SSO orphan
		// (different user), delete the orphan so the rename can proceed.
		// Password-enabled accounts are NEVER auto-deleted — admin-created
		// identities stay sacred.
		betterName := ssoDisplayName(in)
		if betterName != "" && betterName != in.UPN && u.Username == in.UPN {
			existing, nameErr := s.users.GetByUsername(ctx, betterName)
			switch {
			case nameErr != nil:
				// Name is free.
				u.Username = betterName
				dirty = true
			case existing.ID != u.ID && !existing.HasLocalPassword():
				// Stale SSO record from an earlier login cycle blocking the
				// rename. Drop it (and any 3X-UI clients it still owns) so
				// the rename can proceed.
				s.dropOrphanUser(ctx, existing.ID)
				u.Username = betterName
				dirty = true
			}
			// Otherwise: a local user owns this name — leave both alone.
		}
		if dirty {
			if err := s.users.Update(ctx, u); err != nil {
				return nil, fmt.Errorf("update sso user: %w", err)
			}
		}
		return u, nil
	}

	// No UPN match. Try to link to a pre-created account whose username equals
	// the SSO email — supports the "admin batch-creates local users who then
	// log in via SSO" workflow. Strictly limited to accounts that have NOT
	// already claimed an SSO identity (UPN == ""); otherwise this would
	// silently rebind a stranger's login to someone else's account.
	if in.Email != "" {
		if linked, lerr := s.users.GetByUsername(ctx, in.Email); lerr == nil {
			switch {
			case linked.AutoDisabledReason == domain.DisabledPendingApproval:
				// Stale stub from the old auto-creation policy — drop both
				// the panel row and any 3X-UI clients it still owns.
				s.dropOrphanUser(ctx, linked.ID)
			case linked.UPN == "":
				// Genuine pre-created password account → claim it for this SSO id.
				linked.UPN = in.UPN
				if linked.Role != desiredRole {
					linked.Role = desiredRole
				}
				if in.DisplayName != "" && linked.DisplayName != in.DisplayName {
					linked.DisplayName = in.DisplayName
				}
				if err := s.users.Update(ctx, linked); err != nil {
					return nil, fmt.Errorf("link sso upn: %w", err)
				}
				return linked, nil
			}
			// Otherwise the email collides with someone else's SSO-bound
			// account — fall through to ErrSSONoAccount instead of hijacking.
		}
	}

	// No account, no email match. Regular users are NOT auto-provisioned —
	// the caller redirects them to a "contact your administrator" page.
	// Admins are auto-provisioned so the IdP-side admin group is enough to
	// bootstrap a fresh panel.
	if !in.IsAdmin {
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
		t := time.Now().AddDate(0, 0, in.DefaultExpireDays)
		expire = &t
	}
	resetPeriod := in.DefaultResetPeriod
	if resetPeriod == "" {
		resetPeriod = domain.ResetMonthly
	}
	desiredUsername := ssoDisplayName(in)
	if desiredUsername != in.UPN {
		if _, nameErr := s.users.GetByUsername(ctx, desiredUsername); nameErr == nil {
			desiredUsername = in.UPN
		}
	}
	now := time.Now()
	u = &domain.User{
		Username:           desiredUsername,
		UPN:                in.UPN,
		Role:               domain.RoleAdmin,
		SubToken:           subToken,
		UUID:               idgen.NewUUID(),
		GroupID:            groupID,
		ExpireAt:           expire,
		TrafficLimitBytes:  in.DefaultLimitBytes,
		TrafficResetPeriod: resetPeriod,
		TrafficPeriodStart: &now,
		DisplayName:        in.DisplayName,
		Enabled:            true,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("create sso admin: %w", err)
	}
	return u, nil
}

// ssoDisplayName picks the most human-readable name available from an SSO
// login: email → display name → UPN (which may be an opaque identifier).
func ssoDisplayName(in EnsureSSOInput) string {
	if in.Email != "" {
		return in.Email
	}
	if in.DisplayName != "" {
		return in.DisplayName
	}
	return in.UPN
}

// VerifyLocalPassword returns the user if username/password match a password-enabled account.
func (s *Service) VerifyLocalPassword(ctx context.Context, username, password string) (*domain.User, error) {
	u, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if !u.HasLocalPassword() {
		return nil, domain.ErrUnauthorized
	}
	if !u.Enabled {
		return nil, domain.ErrForbidden
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, domain.ErrUnauthorized
	}
	return u, nil
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
	var expireTime int64
	if u.ExpireAt != nil {
		expireTime = u.ExpireAt.UnixMilli()
	}
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
			info.protocol, oldUUID, newUUID, info.flow, u.Enabled, expireTime); err != nil {
			needsRetry = true
		}
	}
	if needsRetry {
		if err := s.enqueueUserTask(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync credentials for user %s", u.Username)); err != nil {
			return nil, err
		}
	} else {
		s.recordUserTaskSucceeded(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync credentials for user %s", u.Username))
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
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	return s.users.Update(ctx, u)
}

// Get returns one user or ErrNotFound.
func (s *Service) Get(ctx context.Context, id int64) (*domain.User, error) {
	return s.users.GetByID(ctx, id)
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
		var expireTime int64
		if u.ExpireAt != nil {
			expireTime = u.ExpireAt.UnixMilli()
		}
		if err := s.syncer.AddClientToInbound(ctx, u.ID, n.PanelID, n.InboundID,
			info.protocol, u.UUID, email, info.flow, expireTime); err != nil {
			needsRetry = true
			continue
		}
		synced++
	}
	if needsRetry {
		if err := s.enqueueUserTask(ctx, domain.SyncTaskUserResync, u.ID, fmt.Sprintf("sync node membership for user %s", u.Username)); err != nil {
			return nil, err
		}
	} else {
		s.recordUserTaskSucceeded(ctx, domain.SyncTaskUserResync, u.ID, fmt.Sprintf("sync node membership for user %s", u.Username))
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
			s.recordUserTaskSucceeded(ctx, domain.SyncTaskUserDelete, userID, fmt.Sprintf("delete user %s", u.Username))
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
		Summary:    fmt.Sprintf("delete user %s", u.Username),
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
		switch *in.Role {
		case domain.RoleAdmin, domain.RoleUser:
			u.Role = *in.Role
		default:
			return fmt.Errorf("%w: invalid role", domain.ErrValidation)
		}
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
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	if groupChanged {
		if err := s.ResyncMembership(ctx, userID); err != nil {
			return s.enqueueUserTask(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync node membership for user %s", u.Username))
		}
		s.recordUserTaskSucceeded(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync node membership for user %s", u.Username))
	}
	// Note: ResyncMembership skips updating existing clients, so if expireChanged we
	// must explicitly push it. We do this by calling a sync loop similar to SetEnabledAndSync.
	if expireChanged {
		if err := s.pushClientConfigToAll(ctx, u); err != nil {
			return s.enqueueUserTask(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync enabled/expiry config for user %s", u.Username))
		}
		s.recordUserTaskSucceeded(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync enabled/expiry config for user %s", u.Username))
	}
	return nil
}

func (s *Service) UseEmergencyAccess(ctx context.Context, userID int64) (*EmergencyAccessResult, error) {
	s.emergencyMu.Lock()
	defer s.emergencyMu.Unlock()

	settings, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return nil, err
	}
	if !settings.EmergencyAccessEnabled {
		return nil, fmt.Errorf("%w: emergency access is disabled", domain.ErrForbidden)
	}
	if settings.EmergencyAccessHours <= 0 || settings.EmergencyAccessMaxCount <= 0 {
		return nil, fmt.Errorf("%w: emergency access settings are invalid", domain.ErrValidation)
	}

	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.ExpireAt == nil {
		return nil, fmt.Errorf("%w: permanent accounts do not need emergency access", domain.ErrValidation)
	}
	if u.EmergencyUsedCount >= settings.EmergencyAccessMaxCount {
		return nil, fmt.Errorf("%w: emergency access limit reached", domain.ErrForbidden)
	}

	now := time.Now()
	from := now
	if u.ExpireAt.After(now) {
		from = *u.ExpireAt
	}
	until := from.Add(time.Duration(settings.EmergencyAccessHours) * time.Hour)
	u.ExpireAt = &until
	u.EmergencyUsedCount++
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	if err := s.pushClientConfigToAll(ctx, u); err != nil {
		if taskErr := s.enqueueUserTask(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync emergency access for user %s", u.Username)); taskErr != nil {
			return nil, taskErr
		}
	} else {
		s.recordUserTaskSucceeded(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync emergency access for user %s", u.Username))
	}
	return &EmergencyAccessResult{
		User:          u,
		ExtendedFrom:  from,
		ExtendedUntil: until,
		UsedCount:     u.EmergencyUsedCount,
		MaxCount:      settings.EmergencyAccessMaxCount,
	}, nil
}

func (s *Service) ResetEmergencyUsage(ctx context.Context, userID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.EmergencyUsedCount == 0 {
		return nil
	}
	u.EmergencyUsedCount = 0
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
	if err := s.ResyncMembership(ctx, userID); err != nil {
		return s.enqueueUserTask(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync node membership for user %s", u.Username))
	}
	s.recordUserTaskSucceeded(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync node membership for user %s", u.Username))
	return nil
}

// ResyncMembership recomputes a user's 3X-UI client memberships against
// the CURRENT group definition (after potential changes) and applies the
// diff via SyncSvc.
//
// Algorithm:
//  1. desired = NodesFor(user's group) — set of (panel, inbound) tuples
//  2. current = ownership.ListByUser — set of (panel, inbound, email)
//  3. ADD = desired - current  → AddClientToInbound for each
//  4. DEL = current - desired  → DelOwnedClient for each
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
	var firstErr error

	// ADD: desired but not currently owned
	for k, n := range desired {
		if _, ok := have[k]; ok {
			continue
		}
		info, err := s.inspectInbound(ctx, n)
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
		var expireTime int64
		if u.ExpireAt != nil {
			expireTime = u.ExpireAt.UnixMilli()
		}
		if err := s.syncer.AddClientToInbound(ctx, u.ID, k.panelID, k.inboundID,
			info.protocol, u.UUID, email, info.flow, expireTime); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("add to %d/%d: %w", k.panelID, k.inboundID, err)
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
func (s *Service) SetEnabledAndSync(ctx context.Context, userID int64, enabled bool, reason domain.AutoDisabledReason) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	u.Enabled = enabled
	u.AutoDisabledReason = reason
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	if err := s.pushClientConfigToAll(ctx, u); err != nil {
		return s.enqueueUserTask(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync enabled/expiry config for user %s", u.Username))
	}
	s.recordUserTaskSucceeded(ctx, domain.SyncTaskUserPushConfig, userID, fmt.Sprintf("sync enabled/expiry config for user %s", u.Username))
	return nil
}

// pushClientConfigToAll iterates through all owned clients of the user and pushes
// their Enable flag and ExpireAt to 3X-UI.
func (s *Service) pushClientConfigToAll(ctx context.Context, u *domain.User) error {
	entries, err := s.ownership.ListByUser(ctx, u.ID)
	if err != nil {
		return err
	}
	var expireTime int64
	if u.ExpireAt != nil {
		expireTime = u.ExpireAt.UnixMilli()
	}
	var firstErr error
	for _, e := range entries {
		info, err := s.inspectInboundByPanel(ctx, e.PanelID, e.InboundID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if info.protocol == "" {
			continue
		}
		if err := s.syncer.SetOwnedClientEnable(ctx, e.PanelID, e.InboundID, e.ClientEmail,
			info.protocol, u.UUID, info.flow, u.Enabled, expireTime); err != nil && firstErr == nil {
			firstErr = err
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
		if err := s.tasks.MarkRunning(ctx, task.ID); err != nil {
			return err
		}
		if err := s.runUserTask(ctx, task); err != nil {
			next := time.Now().Add(deleteTaskBackoff(task.Attempts + 1))
			if markErr := s.tasks.MarkRetry(ctx, task.ID, err.Error(), next); markErr != nil {
				return markErr
			}
			continue
		}
		if err := s.tasks.MarkSucceeded(ctx, task.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) runUserTask(ctx context.Context, task *domain.SyncTask) error {
	switch task.Type {
	case domain.SyncTaskUserDelete:
		return s.runUserDeleteTask(ctx, task)
	case domain.SyncTaskUserResync:
		return s.ResyncMembership(ctx, task.TargetID)
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

func (s *Service) recordUserTaskSucceeded(ctx context.Context, typ domain.SyncTaskType, userID int64, summary string) {
	if s.tasks == nil {
		return
	}
	now := time.Now()
	_ = s.tasks.Create(ctx, &domain.SyncTask{
		Type:       typ,
		Status:     domain.SyncTaskSucceeded,
		TargetType: "user",
		TargetID:   userID,
		Summary:    summary,
		NextRunAt:  now,
		FinishedAt: &now,
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
	var expireTime int64
	if u.ExpireAt != nil {
		expireTime = u.ExpireAt.UnixMilli()
	}
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
			info.protocol, oldUUID, newUUID, info.flow, u.Enabled, expireTime); err != nil {
			needsRetry = true
		}
	}
	if needsRetry {
		_ = s.enqueueUserTask(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync credentials for user %s", u.Username))
	} else {
		s.recordUserTaskSucceeded(ctx, domain.SyncTaskUserResync, userID, fmt.Sprintf("sync credentials for user %s", u.Username))
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
	info := &inboundInfo{
		ssMethod: extractSSMethod(inb.Settings),
		flow:     extractDefaultFlow(inb.Settings),
	}
	info.protocol = crypto.DetectProtocol(inb.Protocol, info.ssMethod)
	if info.flow == "" {
		info.flow = n.Flow
	}
	return info, nil
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
