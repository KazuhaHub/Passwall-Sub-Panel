// Package sync is the single chokepoint for every write that targets a
// 3X-UI panel. All add/update/delete calls to 3X-UI clients pass through
// here, where two write guards run before the actual API call:
//
//  1. Client guard (ensureClientOwned): the (panel, inbound, email) triple
//     must already exist in the ownership table.
//  2. Inbound delete guard (ensureInboundDeletable): inbound deletion is
//     allowed only when every client inside is owned by the panel.
//
// These guards make it physically impossible for sync code (or any caller
// who routes through this service) to disturb the operator's personal
// clients or unmanaged inbounds — even in the face of bugs elsewhere.
package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/xrayspec"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	pool      ports.XUIPool
	ownership ports.OwnershipRepo
	// pspClients is the v3.9.0 shared-client repo, late-bound via SetPSPClientRepo
	// (nil before wiring / in tests). The inbound-deletable guard consults it so a
	// migrated user's shared client — which has no ownership row — is recognised as
	// PSP-managed rather than an operator's hand-made client.
	pspClients ports.PSPClientRepo
}

func New(pool ports.XUIPool, ownership ports.OwnershipRepo) *Service {
	return &Service{pool: pool, ownership: ownership}
}

// SetPSPClientRepo late-binds the v3.9.0 shared-client repo. Until set, the
// inbound-deletable guard falls back to ownership-only (pre-migration behaviour).
func (s *Service) SetPSPClientRepo(r ports.PSPClientRepo) { s.pspClients = r }

// ensureClientOwned returns nil only when (panelID, inboundID, email) is
// recorded in the ownership table.
func (s *Service) ensureClientOwned(ctx context.Context, panelID int64, inboundID int, email string) error {
	exists, err := s.ownership.Exists(ctx, panelID, inboundID, email)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: panel_id=%d inbound=%d email=%s",
			domain.ErrClientNotOwnedByPanel, panelID, inboundID, email)
	}
	return nil
}

// ensureInboundDeletable verifies that every client inside the inbound is
// owned by the panel. Used by inbound deletion to avoid orphaning the
// operator's personal clients.
func (s *Service) ensureInboundDeletable(ctx context.Context, panelID int64, inboundID int) error {
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	in, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return err
	}
	for _, cs := range in.ClientStats {
		ok, err := s.ownership.Exists(ctx, panelID, inboundID, cs.Email)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		// MIGRATION(v3→v4): ownership-OR-psp fallback. When the legacy ownership path
		// is removed, drop the s.ownership.Exists branch above and keep only this
		// psp_client check.
		// v3.9.0: a migrated user's client is NOT in the ownership table — it's a
		// shared client (email u{uid}@, panel-unique). Recognise it as PSP-managed
		// via psp_client. Without this, post-migration (ownership empty/dropped)
		// EVERY populated inbound looks "unmanaged" and node deletion is permanently
		// blocked with a 409. PSPClientInbound is keyed by NodeID (not 3X-UI
		// inboundID), so the by-email lookup is the right primitive here.
		if s.pspClients != nil {
			if c2, perr := s.pspClients.GetByEmail(ctx, panelID, cs.Email); perr == nil && c2 != nil {
				continue
			}
		}
		return fmt.Errorf("%w: panel_id=%d inbound=%d unmanaged_client=%s",
			domain.ErrInboundHasUnmanagedClients, panelID, inboundID, cs.Email)
	}
	return nil
}

// EnsureInboundDeletable exposes the inbound delete guard for callers that
// need a preflight before doing any destructive cleanup.
func (s *Service) EnsureInboundDeletable(ctx context.Context, panelID int64, inboundID int) error {
	return s.ensureInboundDeletable(ctx, panelID, inboundID)
}

// AddClientToInbound creates a new client in 3X-UI and records ownership.
// The caller is responsible for choosing a unique email per user.
//
// Idempotent: if the ownership row already exists for (panel, inbound,
// email), the function refreshes its UUID instead of inserting a duplicate.
// This is the "missing client recovery" path — reconcile finds the client
// missing in 3X-UI but ownership still claims it, and re-creates the
// 3X-UI side while leaving the panel-side bookkeeping in place.
// isDuplicateClientErr reports whether a 3X-UI AddClient error means the client
// already exists (duplicate email). Substring match mirrors the adapter's
// isPermanentPanelMsg — fragile across 3X-UI versions/locales (see the L42
// backlog item), but it's the only signal the panel gives. A false negative
// just means we keep the old "fail + retry" behaviour; a false positive would
// adopt a client that wasn't really a duplicate, which the ownership upsert +
// next push still reconcile.
func isDuplicateClientErr(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "duplicate") || strings.Contains(m, "already exist")
}

func (s *Service) AddClientToInbound(ctx context.Context, userID int64, panelID int64,
	inboundID int, protocol domain.Protocol, ssMethod, userUUID, email, flow string, want domain.UserLifecycle, panelLifetime int64) error {

	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	spec := buildClientSpec(protocol, ssMethod, userUUID, email, flow, want, panelLifetime)
	if err := c.AddClient(ctx, inboundID, spec); err != nil {
		// A duplicate-email error means the client already exists in 3X-UI. If
		// we have no ownership row for it, that's an ORPHANED client — fall
		// through to the ownership upsert below and ADOPT it instead of failing
		// forever (reconcile would otherwise retry AddClient → duplicate → fail
		// every cycle, and the (user,node) pair would stay unmanageable). The
		// next config push aligns the adopted client's credentials. Any other
		// error is fatal.
		if !isDuplicateClientErr(err) {
			return fmt.Errorf("xui addClient: %w", err)
		}
	}

	exists, err := s.ownership.Exists(ctx, panelID, inboundID, email)
	if err != nil {
		return fmt.Errorf("ownership exists check: %w", err)
	}
	if exists {
		// Recovery: row was already there. Keep it but refresh the UUID
		// in case a credential reset happened while 3X-UI was missing.
		if err := s.ownership.UpdateUUID(ctx, panelID, inboundID, email, userUUID); err != nil {
			return fmt.Errorf("ownership update uuid: %w", err)
		}
		return nil
	}

	entry := &domain.XUIClientEntry{
		UserID:      userID,
		PanelID:     panelID,
		InboundID:   inboundID,
		ClientEmail: email,
		ClientUUID:  userUUID,
	}
	if err := s.ownership.Add(ctx, entry); err != nil {
		// best-effort rollback to keep panel and 3X-UI consistent
		_ = c.DelClientByEmail(ctx, inboundID, email)
		return fmt.Errorf("ownership add: %w", err)
	}
	return nil
}

// RotateClientUUID rewrites a panel-owned client's UUID. 3X-UI's
// updateClient endpoint requires the OLD uuid in the path while the body
// carries the new id and derived password, so the caller must pass both.
//
// On success the ownership table is updated so subsequent operations use
// the new uuid as the path key.
func (s *Service) RotateClientUUID(ctx context.Context, panelID int64, inboundID int,
	email string, protocol domain.Protocol, ssMethod, oldUUID, newUUID, flow string, want domain.UserLifecycle, panelLifetime int64) error {

	if err := s.ensureClientOwned(ctx, panelID, inboundID, email); err != nil {
		return err
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	spec := buildClientSpec(protocol, ssMethod, newUUID, email, flow, want, panelLifetime)
	if err := c.UpdateClient(ctx, spec); err != nil {
		return fmt.Errorf("xui rotate uuid: %w", err)
	}
	return s.ownership.UpdateUUID(ctx, panelID, inboundID, email, newUUID)
}

// SetOwnedClientEnable pushes a client's full panel-derived spec (uuid +
// derived password + enable) to 3X-UI by way of the updateClient endpoint.
// Despite the name, this is the primitive used to fix drift in any of the
// uuid/password/enable/extra-field categories — as long as the path uuid
// still matches what 3X-UI has. Uuid mismatch is handled by
// RotateClientUUID, which takes both old and new uuids.
func (s *Service) SetOwnedClientEnable(ctx context.Context, panelID int64, inboundID int,
	email string, protocol domain.Protocol, ssMethod, userUUID, flow string, want domain.UserLifecycle, panelLifetime int64) error {

	if err := s.ensureClientOwned(ctx, panelID, inboundID, email); err != nil {
		return err
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	spec := buildClientSpec(protocol, ssMethod, userUUID, email, flow, want, panelLifetime)
	return c.UpdateClient(ctx, spec)
}

// SetOwnedClientEnableWithInbound is SetOwnedClientEnable with a
// pre-fetched inbound supplied by the caller. The traffic poll's push
// phase batches ListInbounds once per panel up-front and already has
// the inbound in hand; calling SetOwnedClientEnable (which then calls
// UpdateClient which then calls GetInbound) duplicated that work. With
// this variant the entire write phase is one HTTP roundtrip per push
// instead of two.
func (s *Service) SetOwnedClientEnableWithInbound(ctx context.Context, panelID int64, inb *ports.Inbound,
	email string, protocol domain.Protocol, ssMethod, userUUID, flow string, want domain.UserLifecycle, panelLifetime int64) error {

	if inb == nil {
		return fmt.Errorf("SetOwnedClientEnableWithInbound: inb is nil")
	}
	if err := s.ensureClientOwned(ctx, panelID, inb.ID, email); err != nil {
		return err
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	spec := buildClientSpec(protocol, ssMethod, userUUID, email, flow, want, panelLifetime)
	// No-op skip: if the inbound we already hold shows this client matching the
	// spec on every PSP-controlled field, UpdateClient would push byte-identical
	// values and only cost an Xray restart. Skipping keeps a resync / push pass
	// O(changed clients), not O(all owned clients) — so a large fleet doesn't pay
	// one Xray reload per owned client every cycle (the "gets laggier at scale"
	// problem). Conservative: any uncertainty falls through to the update.
	if clientUnchanged(inb, spec, protocol) {
		return nil
	}
	return c.UpdateClient(ctx, spec)
}

// clientUnchanged reports whether inb already holds a client byte-identical to
// spec on every field PSP would push, so an UpdateClient would be a pure no-op
// (its only effect being an Xray restart). It returns false — i.e. "go ahead and
// update" — whenever it cannot FULLY verify a match: a nil/slim inbound, a
// missing client, a settings parse error, or a protocol whose S-UI credential
// is not represented in the historical Xray-shaped settings. That conservatism guarantees a skip
// never leaves the panel stale; the worst case is an unnecessary update, exactly
// today's behaviour.
func clientUnchanged(inb *ports.Inbound, spec ports.ClientSpec, protocol domain.Protocol) bool {
	if inb == nil || protocol == domain.ProtoHysteria2 || protocol == domain.ProtoAnyTLS ||
		protocol == domain.ProtoTUIC || protocol == domain.ProtoNaive {
		return false
	}
	settings, err := xrayspec.ParseSettings(inb.Settings)
	if err != nil {
		return false
	}
	cur := xrayspec.FindClient(settings.Clients, spec.Email)
	if cur == nil {
		return false
	}
	if cur.IsEnabled() != spec.Enable ||
		cur.Flow != spec.Flow ||
		cur.ExpiryTime != spec.ExpiryTime ||
		cur.TotalGB != spec.TotalGB ||
		cur.LimitIP != spec.LimitIP ||
		cur.ID != spec.ID {
		return false
	}
	// limitHwid is NOT in the inbound's settings.clients[] — the panel keeps it
	// on its own client row — so a pre-fetched inbound cannot prove a match.
	// Per this function's contract (any uncertainty means "go ahead and
	// update"), a non-zero device cap forces the write. Zero needs no write:
	// it is what the panel already holds for a client PSP has never capped.
	if spec.LimitHwid != 0 {
		return false
	}
	// Trojan/SS/SS-2022 also carry a password; VLESS/VMess leave spec.Password
	// empty (so it's not compared — the id check above covers them).
	if spec.Password != "" && cur.Password != spec.Password {
		return false
	}
	return true
}

// DelAllOwnedForUser removes every 3X-UI client recorded under userID,
// batched into one bulkDel per panel. A user's clients can span several
// panels; each panel's deletes (and Xray restarts) collapse into a single
// call. Returns the first error so the caller (admin user-delete) can surface
// it rather than orphaning 3X-UI clients; panels that error keep their
// ownership rows for the next retry.
func (s *Service) DelAllOwnedForUser(ctx context.Context, userID int64) error {
	entries, err := s.ownership.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	byPanel := make(map[int64][]*domain.XUIClientEntry)
	for _, e := range entries {
		byPanel[e.PanelID] = append(byPanel[e.PanelID], e)
	}
	var firstErr error
	for panelID, es := range byPanel {
		c, err := s.pool.Get(panelID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := s.bulkDelOwned(ctx, c, es); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// DelAllOwnedForInbound removes every panel-owned client living inside the
// given inbound in a single bulkDel call. Used by node deletion before the
// inbound itself can be removed (the inbound delete guard requires no
// unmanaged clients remain). Collapsing N per-client deletes into one means
// one Xray restart instead of N when tearing down a busy node. The node-delete
// task checks this error and aborts before DeleteInbound — so a transient
// failure can't leave the inbound removed while an ownership row survives
// pointing at a now-gone inbound (the task retries next tick and converges).
func (s *Service) DelAllOwnedForInbound(ctx context.Context, panelID int64, inboundID int) error {
	entries, err := s.ownership.ListByInbound(ctx, panelID, inboundID)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	return s.bulkDelOwned(ctx, c, entries)
}

// bulkDelOwned deletes the given owned clients on ONE panel via a single
// /clients/bulkDel call, then drops their ownership rows. Every entry here
// comes from the ownership table, so they are all panel-managed; emails
// already gone upstream are no-ops on the panel side. On a bulk-delete failure
// it removes no ownership rows and returns the error so the caller's task
// retries the whole batch (idempotent on retry). Otherwise it returns the
// first ownership-removal error, leaving the rest removed.
func (s *Service) bulkDelOwned(ctx context.Context, c ports.XUIClient, entries []*domain.XUIClientEntry) error {
	if len(entries) == 0 {
		return nil
	}
	emails := make([]string, len(entries))
	for i, e := range entries {
		emails[i] = e.ClientEmail
	}
	if _, err := c.BulkDelByEmail(ctx, emails); err != nil {
		return fmt.Errorf("xui bulkDel: %w", err)
	}
	var firstErr error
	for _, e := range entries {
		if err := s.ownership.RemoveByMatch(ctx, e.PanelID, e.InboundID, e.ClientEmail); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// UnclaimAllForInbound drops every ownership row for the given inbound
// without contacting 3X-UI. Used by node detach when the upstream panel
// may be offline — we forget the clients locally so leftover 3X-UI rows
// are no longer considered panel-managed.
func (s *Service) UnclaimAllForInbound(ctx context.Context, panelID int64, inboundID int) error {
	entries, err := s.ownership.ListByInbound(ctx, panelID, inboundID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range entries {
		if err := s.ownership.RemoveByMatch(ctx, e.PanelID, e.InboundID, e.ClientEmail); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ClaimClient records ownership of an existing 3X-UI client under a panel
// user without touching 3X-UI itself. Used by the "import existing client"
// admin flow — the friend keeps their original UUID and the panel just
// adopts the row.
//
// The caller is responsible for supplying a correct (email, uuid) pair as
// it appears in 3X-UI; the unique index on (panel, inbound, email) prevents
// double-claiming.
func (s *Service) ClaimClient(ctx context.Context, userID int64, panelID int64, inboundID int, email, clientUUID string) (string, error) {
	c, err := s.pool.Get(panelID)
	if err != nil {
		return "", err
	}
	current, err := c.GetClient(ctx, email)
	if err != nil {
		return "", fmt.Errorf("xui get client: %w", err)
	}
	if current == nil {
		return "", fmt.Errorf("%w: client email %s not found in panel_id=%d inbound=%d", domain.ErrNotFound, email, panelID, inboundID)
	}
	if clientUUID == "" {
		clientUUID = current.ID
	}
	entry := &domain.XUIClientEntry{
		UserID:      userID,
		PanelID:     panelID,
		InboundID:   inboundID,
		ClientEmail: email,
		ClientUUID:  clientUUID,
	}
	if err := s.ownership.Add(ctx, entry); err != nil {
		return "", err
	}
	return clientUUID, nil
}

// DeleteInbound deletes an inbound only when the guard passes. The caller
// must also remove the corresponding nodes row (done by NodeSvc).
func (s *Service) DeleteInbound(ctx context.Context, panelID int64, inboundID int) error {
	if err := s.ensureInboundDeletable(ctx, panelID, inboundID); err != nil {
		return err
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	return c.DelInbound(ctx, inboundID)
}

// IsOwnershipError reports whether err is a write-guard rejection. Useful
// for transport-layer code to map these to HTTP 403 / 409.
func IsOwnershipError(err error) bool {
	return errors.Is(err, domain.ErrClientNotOwnedByPanel) ||
		errors.Is(err, domain.ErrInboundHasUnmanagedClients)
}

// buildClientSpec composes a ClientSpec by applying the protocol-specific
// derivation rule. Caller fills in Enable as needed.
//
// totalGB is the per-client traffic floor pushed into 3X-UI (despite the
// name, the field is bytes). 0 means unlimited on the 3X-UI side; pass the
// output of user.TrafficFloorBytes for the safety-net behaviour.
// buildClientSpec maps PSP's intent onto a panel client spec. `want` is the
// enforcement contract (domain.UserLifecycle) and panelLifetime is the client's
// current panel-side cumulative counter, which is what turns the contract's
// period-relative headroom into the absolute cap the panel compares against.
func buildClientSpec(protocol domain.Protocol, ssMethod, userUUID, email, flow string,
	want domain.UserLifecycle, panelLifetime int64) ports.ClientSpec {
	password := crypto.DeriveProxyPassword(userUUID, protocol, ssMethod)
	spec := ports.ClientSpec{
		Email:      email,
		Enable:     want.Enable,
		Flow:       flow,
		ExpiryTime: want.ExpiryTime,
		TotalGB:    want.PanelQuota(panelLifetime),
		LimitIP:    want.IPLimit,
		LimitHwid:  want.DeviceLimit,
	}
	switch protocol {
	case domain.ProtoVLESS, domain.ProtoVMess:
		spec.ID = userUUID
	case domain.ProtoTrojan, domain.ProtoSS, domain.ProtoSS2022,
		domain.ProtoAnyTLS, domain.ProtoTUIC, domain.ProtoNaive:
		// Keep the stable UUID available to adapters that identify a client by
		// UUID (notably TUIC); password-based protocols consume Password.
		spec.ID = userUUID
		spec.Password = password
	case domain.ProtoHysteria2:
		// 3X-UI keys Hysteria2 clients by the "auth" field (it treats auth as
		// the client id and rejects an empty one). password == userUUID here,
		// matching what the subscription renderer emits as the HY2 password.
		spec.Auth = password
	}
	return spec
}
