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

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	pool      ports.XUIPool
	ownership ports.OwnershipRepo
}

func New(pool ports.XUIPool, ownership ports.OwnershipRepo) *Service {
	return &Service{pool: pool, ownership: ownership}
}

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
		if !ok {
			return fmt.Errorf("%w: panel_id=%d inbound=%d unmanaged_client=%s",
				domain.ErrInboundHasUnmanagedClients, panelID, inboundID, cs.Email)
		}
	}
	return nil
}

// AddClientToInbound creates a new client in 3X-UI and records ownership.
// The caller is responsible for choosing a unique email per user.
//
// Idempotent: if the ownership row already exists for (panel, inbound,
// email), the function refreshes its UUID instead of inserting a duplicate.
// This is the "missing client recovery" path — reconcile finds the client
// missing in 3X-UI but ownership still claims it, and re-creates the
// 3X-UI side while leaving the panel-side bookkeeping in place.
func (s *Service) AddClientToInbound(ctx context.Context, userID int64, panelID int64,
	inboundID int, protocol domain.Protocol, userUUID, email, flow string, expireTime int64) error {

	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	spec := buildClientSpec(protocol, userUUID, email, flow, expireTime)
	if err := c.AddClient(ctx, inboundID, spec); err != nil {
		return fmt.Errorf("xui addClient: %w", err)
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
		PanelName:   s.panelName(panelID),
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

// UpdateOwnedClient updates fields of a client that the panel already owns.
// Returns ErrClientNotOwnedByPanel if the guard rejects the call.
func (s *Service) UpdateOwnedClient(ctx context.Context, panelID int64, inboundID int,
	email string, protocol domain.Protocol, userUUID, flow string, enable bool, expireTime int64) error {

	if err := s.ensureClientOwned(ctx, panelID, inboundID, email); err != nil {
		return err
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	spec := buildClientSpec(protocol, userUUID, email, flow, expireTime)
	spec.Enable = enable
	return c.UpdateClient(ctx, inboundID, userUUID, spec)
}

// DelOwnedClient removes a panel-owned client from 3X-UI and the ownership
// table. Refuses if not in the ownership table.
//
// The 3X-UI canonical delete endpoint is /delClient/{uuid}; the ownership
// row carries the uuid, so we look it up first. /delClientByEmail/{email}
// is a fork-specific variant that some panels don't ship — using it caused
// silent delete failures that left orphaned clients (and orphaned
// ownership rows since the loop in DelAllOwnedForUser swallows errors).
func (s *Service) DelOwnedClient(ctx context.Context, panelID int64, inboundID int, email string) error {
	entry, err := s.ownership.GetByMatch(ctx, panelID, inboundID, email)
	if err != nil {
		// No ownership row → nothing for us to manage. Treat as success;
		// caller's desired end-state ("this client is gone") is satisfied.
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		// Anything else is a real DB error; surface it as-is so the caller
		// doesn't mistake it for a legitimate ownership-guard refusal.
		return fmt.Errorf("ownership lookup: %w", err)
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	if err := c.DelClient(ctx, inboundID, entry.ClientUUID); err != nil {
		// 3X-UI rejected. If the client isn't actually there (out-of-sync
		// after a partial earlier run, manual cleanup in 3X-UI, etc.) the
		// desired end-state is already achieved — clean our ownership row
		// and return success. Only genuine "panel unreachable / inbound
		// errored" cases bubble up to trigger async retry.
		if missing, vErr := s.clientMissing(ctx, c, inboundID, entry.ClientUUID); vErr == nil && missing {
			return s.ownership.RemoveByMatch(ctx, panelID, inboundID, email)
		}
		return fmt.Errorf("xui delClient: %w", err)
	}
	return s.ownership.RemoveByMatch(ctx, panelID, inboundID, email)
}

// clientMissing returns (true, nil) when the inbound exists and does NOT
// contain a client with the given UUID. (false, nil) means the client is
// still there. A non-nil error means we couldn't verify (panel unreachable,
// inbound gone) — the caller should treat as "can't tell" and bubble the
// original delete error.
func (s *Service) clientMissing(ctx context.Context, c ports.XUIClient, inboundID int, uuid string) (bool, error) {
	clients, err := c.GetInboundClients(ctx, inboundID)
	if err != nil {
		return false, err
	}
	for _, cl := range clients {
		if cl.ID == uuid {
			return false, nil
		}
	}
	return true, nil
}

// RotateClientUUID rewrites a panel-owned client's UUID. 3X-UI's
// updateClient endpoint requires the OLD uuid in the path while the body
// carries the new id and derived password, so the caller must pass both.
//
// On success the ownership table is updated so subsequent operations use
// the new uuid as the path key.
func (s *Service) RotateClientUUID(ctx context.Context, panelID int64, inboundID int,
	email string, protocol domain.Protocol, oldUUID, newUUID, flow string, enable bool, expireTime int64) error {

	if err := s.ensureClientOwned(ctx, panelID, inboundID, email); err != nil {
		return err
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	spec := buildClientSpec(protocol, newUUID, email, flow, expireTime)
	spec.Enable = enable
	if err := c.UpdateClient(ctx, inboundID, oldUUID, spec); err != nil {
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
	email string, protocol domain.Protocol, userUUID, flow string, enable bool, expireTime int64) error {

	if err := s.ensureClientOwned(ctx, panelID, inboundID, email); err != nil {
		return err
	}
	c, err := s.pool.Get(panelID)
	if err != nil {
		return err
	}
	spec := buildClientSpec(protocol, userUUID, email, flow, expireTime)
	spec.Enable = enable
	return c.UpdateClient(ctx, inboundID, userUUID, spec)
}

// DelAllOwnedForUser removes every 3X-UI client recorded under userID.
// Returns the first per-client error so the caller (admin user-delete)
// can surface it rather than orphaning 3X-UI clients. Successful deletes
// up to the failure point are real — the ownership table reflects that.
func (s *Service) DelAllOwnedForUser(ctx context.Context, userID int64) error {
	entries, err := s.ownership.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range entries {
		if err := s.DelOwnedClient(ctx, e.PanelID, e.InboundID, e.ClientEmail); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%d/%d/%s: %w", e.PanelID, e.InboundID, e.ClientEmail, err)
			}
		}
	}
	return firstErr
}

// DelAllOwnedForInbound removes every panel-owned client living inside the
// given inbound. Used by node deletion before the inbound itself can be
// removed (the inbound delete guard requires no unmanaged clients remain).
func (s *Service) DelAllOwnedForInbound(ctx context.Context, panelID int64, inboundID int) error {
	entries, err := s.ownership.ListByInbound(ctx, panelID, inboundID)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = s.DelOwnedClient(ctx, e.PanelID, e.InboundID, e.ClientEmail)
	}
	return nil
}

// ClaimClient records ownership of an existing 3X-UI client under a panel
// user without touching 3X-UI itself. Used by the "import existing client"
// admin flow — the friend keeps their original UUID and the panel just
// adopts the row.
//
// The caller is responsible for supplying a correct (email, uuid) pair as
// it appears in 3X-UI; the unique index on (panel, inbound, email) prevents
// double-claiming.
func (s *Service) ClaimClient(ctx context.Context, userID int64, panelID int64, inboundID int, email, clientUUID string) error {
	entry := &domain.XUIClientEntry{
		UserID:      userID,
		PanelID:     panelID,
		PanelName:   s.panelName(panelID),
		InboundID:   inboundID,
		ClientEmail: email,
		ClientUUID:  clientUUID,
	}
	return s.ownership.Add(ctx, entry)
}

func (s *Service) panelName(panelID int64) string {
	for _, p := range s.pool.List() {
		if p.ID == panelID {
			return p.Name
		}
	}
	return ""
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
func buildClientSpec(protocol domain.Protocol, userUUID, email, flow string, expireTime int64) ports.ClientSpec {
	password := crypto.DeriveProxyPassword(userUUID, protocol)
	spec := ports.ClientSpec{
		Email:      email,
		Enable:     true,
		Flow:       flow,
		ExpiryTime: expireTime,
	}
	switch protocol {
	case domain.ProtoVLESS, domain.ProtoVMess:
		spec.ID = userUUID
	case domain.ProtoTrojan, domain.ProtoSS, domain.ProtoSS2022:
		spec.ID = userUUID // 3X-UI still expects an id field
		spec.Password = password
	}
	return spec
}
