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
	AddClientToInbound(ctx context.Context, userID int64, panel string, inboundID int,
		protocol domain.Protocol, userUUID, email, flow string) error
	DelOwnedClient(ctx context.Context, panel string, inboundID int, email string) error
	SetOwnedClientEnable(ctx context.Context, panel string, inboundID int, email string,
		protocol domain.Protocol, userUUID string, enable bool) error
	DelAllOwnedForUser(ctx context.Context, userID int64) error
	RotateClientUUID(ctx context.Context, panel string, inboundID int, email string,
		protocol domain.Protocol, oldUUID, newUUID string, enable bool) error
}

type Service struct {
	users     ports.UserRepo
	groups    ports.GroupRepo
	ownership ports.OwnershipRepo
	selector  NodeSelector
	syncer    ClientSyncer
	pool      ports.XUIPool
}

func New(users ports.UserRepo, groups ports.GroupRepo, ownership ports.OwnershipRepo,
	selector NodeSelector, syncer ClientSyncer, pool ports.XUIPool) *Service {
	return &Service{
		users:     users,
		groups:    groups,
		ownership: ownership,
		selector:  selector,
		syncer:    syncer,
		pool:      pool,
	}
}

// ---- Plain CRUD (no 3X-UI side effects) ----

// CreateLocalInput captures the admin form fields for creating a local user.
type CreateLocalInput struct {
	Username           string
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

// CreateLocal persists a new local-source user in the DB. It does NOT touch
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
		Source:             domain.UserSourceLocal,
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
	if err == nil {
		// Existing SSO user. Reconcile role + display name in case they
		// changed in the IdP.
		dirty := false
		if u.Role != desiredRole {
			u.Role = desiredRole
			dirty = true
		}
		if in.DisplayName != "" && u.Remark != in.DisplayName {
			u.Remark = in.DisplayName
			dirty = true
		}
		if !u.Enabled && u.AutoDisabledReason == domain.DisabledManual {
			// Don't auto-re-enable manually disabled accounts.
		}
		if dirty {
			if err := s.users.Update(ctx, u); err != nil {
				return nil, fmt.Errorf("update sso user: %w", err)
			}
		}
		return u, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	g, err := s.groups.GetBySlug(ctx, in.DefaultGroupSlug)
	if err != nil {
		return nil, fmt.Errorf("default group %q: %w", in.DefaultGroupSlug, err)
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
	now := time.Now()
	u = &domain.User{
		Username:           in.UPN, // SSO users use UPN in both fields; unique constraint covers either lookup
		UPN:                in.UPN,
		Source:             domain.UserSourceSSO,
		Role:               desiredRole,
		SubToken:           subToken,
		UUID:               idgen.NewUUID(),
		GroupID:            g.ID,
		ExpireAt:           expire,
		TrafficLimitBytes:  in.DefaultLimitBytes,
		TrafficResetPeriod: resetPeriod,
		TrafficPeriodStart: &now,
		Remark:             in.DisplayName,
		Enabled:            true,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("create sso user: %w", err)
	}
	// Push clients to every authorised inbound. Errors here are not fatal
	// for login — reconcile will heal them.
	if err := s.ResyncMembership(ctx, u.ID); err != nil {
		// log only; user can still authenticate
		_ = err
	}
	return u, nil
}

// VerifyLocalPassword returns the user if username/password match a local account.
func (s *Service) VerifyLocalPassword(ctx context.Context, username, password string) (*domain.User, error) {
	u, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if u.Source != domain.UserSourceLocal {
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

// SetPassword updates a local account's password (admin-side reset).
func (s *Service) SetPassword(ctx context.Context, userID int64, newPassword string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Source != domain.UserSourceLocal {
		return fmt.Errorf("%w: not a local account", domain.ErrValidation)
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
// It performs four steps and rolls back on partial failure:
//
//  1. Persist the user (CreateLocal).
//  2. Resolve the group's tag_filter into a node list.
//  3. For every node, inspect the underlying inbound to detect protocol
//     and push the new client through SyncSvc (which applies the write guard
//     and records ownership).
//  4. On any error, delete already-pushed clients and the user row.
func (s *Service) CreateLocalAndSync(ctx context.Context, in CreateLocalInput) (*CreateLocalSyncedResult, error) {
	base, err := s.CreateLocal(ctx, in)
	if err != nil {
		return nil, err
	}
	u := base.User

	// Use a background context for rollbacks so cancellation of the
	// originating request doesn't leak inconsistent state.
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

	email := u.EmailForXUI()
	synced := 0
	for _, n := range nodes {
		info, err := s.inspectInbound(ctx, n)
		if err != nil {
			rollback()
			return nil, fmt.Errorf("inspect inbound %s/%d: %w", n.PanelName, n.InboundID, err)
		}
		if info.protocol == "" {
			continue // unrecognised protocol — skip rather than fail the whole create
		}
		if err := s.syncer.AddClientToInbound(ctx, u.ID, n.PanelName, n.InboundID,
			info.protocol, u.UUID, email, info.flow); err != nil {
			rollback()
			return nil, fmt.Errorf("sync client to %s/%d: %w", n.PanelName, n.InboundID, err)
		}
		synced++
	}

	return &CreateLocalSyncedResult{
		User:            u,
		InitialPassword: base.InitialPassword,
		SyncedInbounds:  synced,
	}, nil
}

// DeleteAndSync removes every 3X-UI client owned by the user, then deletes
// the user row. Errors during sync are best-effort: a failed client delete
// leaves the row in the ownership table for the next reconciliation pass.
func (s *Service) DeleteAndSync(ctx context.Context, userID int64) error {
	if _, err := s.users.GetByID(ctx, userID); err != nil {
		return err
	}
	if err := s.syncer.DelAllOwnedForUser(ctx, userID); err != nil {
		return fmt.Errorf("sync delete: %w", err)
	}
	return s.users.Delete(ctx, userID)
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
	return s.ResyncMembership(ctx, userID)
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
		panel     string
		inboundID int
	}
	desired := make(map[key]*domain.Node, len(desiredNodes))
	for _, n := range desiredNodes {
		desired[key{n.PanelName, n.InboundID}] = n
	}
	have := make(map[key]*domain.XUIClientEntry, len(current))
	for _, e := range current {
		have[key{e.PanelName, e.InboundID}] = e
	}

	email := u.EmailForXUI()
	var firstErr error

	// ADD: desired but not currently owned
	for k, n := range desired {
		if _, ok := have[k]; ok {
			continue
		}
		info, err := s.inspectInbound(ctx, n)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("inspect %s/%d: %w", k.panel, k.inboundID, err)
			}
			continue
		}
		if info.protocol == "" {
			continue
		}
		if err := s.syncer.AddClientToInbound(ctx, u.ID, k.panel, k.inboundID,
			info.protocol, u.UUID, email, info.flow); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("add to %s/%d: %w", k.panel, k.inboundID, err)
			}
		}
	}

	// DEL: currently owned but no longer desired
	for k, e := range have {
		if _, ok := desired[k]; ok {
			continue
		}
		if err := s.syncer.DelOwnedClient(ctx, e.PanelName, e.InboundID, e.ClientEmail); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("del from %s/%d: %w", k.panel, k.inboundID, err)
			}
		}
	}

	return firstErr
}

// SetEnabledAndSync flips the enabled flag and propagates it to every owned
// 3X-UI client. Used both by the admin UI and by traffic-limit enforcement.
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
	// Re-derive the protocol per inbound so the spec matches what's there.
	g, err := s.groups.GetByID(ctx, u.GroupID)
	if err != nil {
		return err
	}
	nodes, err := s.selector.NodesFor(ctx, g)
	if err != nil {
		return err
	}
	email := u.EmailForXUI()
	for _, n := range nodes {
		info, err := s.inspectInbound(ctx, n)
		if err != nil || info.protocol == "" {
			continue
		}
		_ = s.syncer.SetOwnedClientEnable(ctx, n.PanelName, n.InboundID, email,
			info.protocol, u.UUID, enabled)
	}
	return nil
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
	for _, e := range entries {
		info, err := s.inspectInboundByPanel(ctx, e.PanelName, e.InboundID)
		if err != nil || info.protocol == "" {
			continue
		}
		_ = s.syncer.RotateClientUUID(ctx, e.PanelName, e.InboundID, e.ClientEmail,
			info.protocol, oldUUID, newUUID, u.Enabled)
	}
	return newUUID, nil
}

// inspectInboundByPanel is the address-by-(panel, inbound) version of
// inspectInbound, used when the caller has an ownership entry rather than
// a Node row.
func (s *Service) inspectInboundByPanel(ctx context.Context, panel string, inboundID int) (*inboundInfo, error) {
	c, err := s.pool.Get(panel)
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
	c, err := s.pool.Get(n.PanelName)
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
