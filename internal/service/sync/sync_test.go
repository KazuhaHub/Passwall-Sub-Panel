package sync

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestAddClientToInbound_AdoptsOrphanOnDuplicate pins the M5 fix: when 3X-UI
// already has the client (duplicate email) but PSP has no ownership row, the
// add must ADOPT it (create the ownership row) instead of failing forever.
func TestAddClientToInbound_AdoptsOrphanOnDuplicate(t *testing.T) {
	no := false
	xui := &fakeXUIClient{addClientErr: fmt.Errorf("xui addClient: duplicate email")}
	own := &fakeOwnership{existsVal: &no} // orphan: in 3X-UI, no ownership row
	s := New(&fakePool{xui: xui}, own)

	if err := s.AddClientToInbound(context.Background(), 1, 1, 100, domain.ProtoVLESS, "", "uuid-x", "u1-n1@x", "", 0, 0); err != nil {
		t.Fatalf("duplicate client should be adopted, got: %v", err)
	}
	if !own.addCalled {
		t.Error("expected the ownership row to be created (adopt), Add was never called")
	}
}

// A non-duplicate AddClient failure must still propagate (and NOT create an
// ownership row that doesn't match a real 3X-UI client).
func TestAddClientToInbound_NonDuplicateErrorPropagates(t *testing.T) {
	no := false
	xui := &fakeXUIClient{addClientErr: fmt.Errorf("connection refused")}
	own := &fakeOwnership{existsVal: &no}
	s := New(&fakePool{xui: xui}, own)

	if err := s.AddClientToInbound(context.Background(), 1, 1, 100, domain.ProtoVLESS, "", "uuid-x", "u1-n1@x", "", 0, 0); err == nil {
		t.Fatal("non-duplicate AddClient error must propagate")
	}
	if own.addCalled {
		t.Error("ownership must NOT be created when the add genuinely failed")
	}
}

func TestDelOwnedClientDeletesSSClientWithEmptyIDByEmail(t *testing.T) {
	xui := &fakeXUIClient{clients: []ports.ClientDetail{{Email: "u1@example.test"}}}
	own := newFakeOwnership("u1@example.test", "")
	s := New(&fakePool{xui: xui}, own)

	if err := s.DelOwnedClient(context.Background(), 1, 100, "u1@example.test"); err != nil {
		t.Fatal(err)
	}
	if xui.deletedByEmail != "u1@example.test" {
		t.Fatalf("deletedByEmail = %q", xui.deletedByEmail)
	}
	if !own.removed {
		t.Fatalf("ownership was not removed")
	}
}

// TestDelOwnedClientDeletesByEmailNotByStoredID pins that deletion always
// goes through delClientByEmail and never delClient-by-id, so it works
// regardless of which per-protocol key 3X-UI's delClient/:id expects
// (Shadowsocks, confirmed in production, rejects delete by the stored id).
func TestDelOwnedClientDeletesByEmailNotByStoredID(t *testing.T) {
	xui := &fakeXUIClient{clients: []ports.ClientDetail{{ID: "some-uuid", Email: "u1@example.test"}}}
	own := newFakeOwnership("u1@example.test", "stale-id")
	s := New(&fakePool{xui: xui}, own)

	if err := s.DelOwnedClient(context.Background(), 1, 100, "u1@example.test"); err != nil {
		t.Fatal(err)
	}
	if xui.deletedByEmail != "u1@example.test" {
		t.Fatalf("deletedByEmail = %q, want u1@example.test", xui.deletedByEmail)
	}
	if xui.deletedID != "" {
		t.Fatalf("must not use the by-id delete path, deletedID = %q", xui.deletedID)
	}
	if !own.removed {
		t.Fatalf("ownership was not removed")
	}
}

// TestBuildClientSpecHysteria2SetsAuth pins that Hysteria2 clients carry the
// per-user credential in the `auth` field (3X-UI's client id for HY2), not id
// or password — otherwise 3X-UI rejects the client as "empty client ID". The
// value equals the user's UUID, matching what the subscription renderer emits.
func TestBuildClientSpecHysteria2SetsAuth(t *testing.T) {
	spec := buildClientSpec(domain.ProtoHysteria2, "", "uuid-xyz", "u@example.test", "", 0, 0)
	if spec.Auth != "uuid-xyz" {
		t.Fatalf("Auth = %q, want uuid-xyz", spec.Auth)
	}
	if spec.ID != "" || spec.Password != "" {
		t.Fatalf("HY2 should set only Auth; got ID=%q Password=%q", spec.ID, spec.Password)
	}
}

// TestBuildClientSpecSS2022KeyLength confirms the ssMethod threaded into
// buildClientSpec reaches the derived PSK, so the credential pushed to 3X-UI
// matches the inbound cipher's required key length (16 bytes for aes-128-gcm,
// 32 for aes-256-gcm). A length mismatch makes Xray reject the client.
func TestBuildClientSpecSS2022KeyLength(t *testing.T) {
	cases := []struct {
		method    string
		wantBytes int
	}{
		{"2022-blake3-aes-128-gcm", 16},
		{"2022-blake3-aes-256-gcm", 32},
	}
	for _, tc := range cases {
		spec := buildClientSpec(domain.ProtoSS2022, tc.method, "uuid-xyz", "u@example.test", "", 0, 0)
		raw, err := base64.StdEncoding.DecodeString(spec.Password)
		if err != nil {
			t.Fatalf("method %q: PSK %q not base64: %v", tc.method, spec.Password, err)
		}
		if len(raw) != tc.wantBytes {
			t.Fatalf("method %q: PSK %d bytes, want %d", tc.method, len(raw), tc.wantBytes)
		}
	}
}

// --- 3.2.x bulk paths ---

// DelAllOwnedForInbound must collapse N per-client deletes into ONE bulkDel
// and then drop every ownership row — never the per-client DelClientByEmail.
func TestDelAllOwnedForInboundUsesBulkDel(t *testing.T) {
	xui := &fakeXUIClient{}
	own := &fakeOwnership{listEntries: []*domain.XUIClientEntry{
		{PanelID: 1, InboundID: 100, ClientEmail: "a@x"},
		{PanelID: 1, InboundID: 100, ClientEmail: "b@x"},
	}}
	s := New(&fakePool{xui: xui}, own)
	if err := s.DelAllOwnedForInbound(context.Background(), 1, 100); err != nil {
		t.Fatal(err)
	}
	if len(xui.bulkDeleted) != 2 {
		t.Fatalf("want 2 emails in one bulkDel, got %v", xui.bulkDeleted)
	}
	if xui.deletedByEmail != "" {
		t.Fatalf("must NOT fall back to per-client DelClientByEmail, got %q", xui.deletedByEmail)
	}
	if len(own.removedEmails) != 2 {
		t.Fatalf("want 2 ownership rows removed, got %v", own.removedEmails)
	}
}

// A bulkDel failure must surface an error and leave EVERY ownership row intact
// so the node-delete task retries the whole batch (and aborts before deleting
// the inbound).
func TestDelAllOwnedForInboundBulkDelErrorKeepsOwnership(t *testing.T) {
	xui := &fakeXUIClient{bulkDelErr: fmt.Errorf("network down")}
	own := &fakeOwnership{listEntries: []*domain.XUIClientEntry{
		{PanelID: 1, InboundID: 100, ClientEmail: "a@x"},
	}}
	s := New(&fakePool{xui: xui}, own)
	if err := s.DelAllOwnedForInbound(context.Background(), 1, 100); err == nil {
		t.Fatal("bulkDel failure must surface an error")
	}
	if len(own.removedEmails) != 0 {
		t.Fatalf("no ownership rows may be removed on bulk failure, got %v", own.removedEmails)
	}
}

func TestDelAllOwnedForUserBulkDels(t *testing.T) {
	xui := &fakeXUIClient{}
	own := &fakeOwnership{listEntries: []*domain.XUIClientEntry{
		{PanelID: 1, InboundID: 100, ClientEmail: "u1-n1@x"},
		{PanelID: 1, InboundID: 101, ClientEmail: "u1-n2@x"},
	}}
	s := New(&fakePool{xui: xui}, own)
	if err := s.DelAllOwnedForUser(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(xui.bulkDeleted) != 2 {
		t.Fatalf("want both emails bulk-deleted, got %v", xui.bulkDeleted)
	}
	if len(own.removedEmails) != 2 {
		t.Fatalf("want 2 ownership rows removed, got %v", own.removedEmails)
	}
}

// BulkAddClientsToInbound: created clients and duplicates (adopt) get an
// ownership row; any OTHER skip reason means the client wasn't created, so it
// is NOT owned and the error surfaces. All specs go out in one bulkCreate.
func TestBulkAddClientsToInboundCreatesAdoptsSkips(t *testing.T) {
	no := false
	xui := &fakeXUIClient{bulkAddResult: ports.BulkAddResult{
		Created: 1,
		Skipped: []ports.BulkSkip{
			{Email: "dup@x", Reason: "email already in use: dup@x"},
			{Email: "bad@x", Reason: "invalid spec"},
		},
	}}
	own := &fakeOwnership{existsVal: &no} // nothing owned yet → created/adopted go through Add
	s := New(&fakePool{xui: xui}, own)
	reqs := []ports.BulkClientAdd{
		{UserID: 1, Protocol: domain.ProtoVLESS, UserUUID: "uuid-new", Email: "new@x"},
		{UserID: 2, Protocol: domain.ProtoVLESS, UserUUID: "uuid-dup", Email: "dup@x"},
		{UserID: 3, Protocol: domain.ProtoVLESS, UserUUID: "uuid-bad", Email: "bad@x"},
	}
	owned, err := s.BulkAddClientsToInbound(context.Background(), 1, 100, reqs)
	if err == nil {
		t.Fatal("the non-duplicate skip (bad@x) must surface as an error")
	}
	if owned != 2 {
		t.Fatalf("owned = %d, want 2 (created new@x + adopted dup@x)", owned)
	}
	if len(xui.bulkAddSpecs) != 3 {
		t.Fatalf("all 3 specs must go in one bulkCreate, got %d", len(xui.bulkAddSpecs))
	}
	if !containsStr(own.addedEmails, "new@x") || !containsStr(own.addedEmails, "dup@x") {
		t.Fatalf("created + adopted must be owned, addedEmails = %v", own.addedEmails)
	}
	if containsStr(own.addedEmails, "bad@x") {
		t.Fatal("bad@x was not created upstream — it must NOT be owned")
	}
}

// Adopting a duplicate that ALREADY has an ownership row refreshes its uuid
// (upsert), never a second Add.
func TestBulkAddClientsToInboundAdoptRefreshesUUIDWhenOwned(t *testing.T) {
	yes := true
	xui := &fakeXUIClient{bulkAddResult: ports.BulkAddResult{
		Skipped: []ports.BulkSkip{{Email: "dup@x", Reason: "email already in use"}},
	}}
	own := &fakeOwnership{existsVal: &yes}
	s := New(&fakePool{xui: xui}, own)
	owned, err := s.BulkAddClientsToInbound(context.Background(), 1, 100,
		[]ports.BulkClientAdd{{UserID: 1, Protocol: domain.ProtoVLESS, UserUUID: "u", Email: "dup@x"}})
	if err != nil {
		t.Fatal(err)
	}
	if owned != 1 {
		t.Fatalf("owned = %d, want 1 (adopted)", owned)
	}
	if len(own.updatedUUIDs) != 1 {
		t.Fatalf("adopt of an already-owned client must refresh uuid, updatedUUIDs = %v", own.updatedUUIDs)
	}
	if own.addCalled {
		t.Fatal("must not Add when the row already exists (upsert refreshes uuid)")
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

type fakePool struct {
	xui ports.XUIClient
}

func (p *fakePool) Get(panelID int64) (ports.XUIClient, error) { return p.xui, nil }
func (p *fakePool) List() []*domain.XUIPanel                   { return nil }
func (p *fakePool) Add(panel *domain.XUIPanel) error           { return nil }
func (p *fakePool) Remove(panelID int64) error                 { return nil }

type fakeOwnership struct {
	entry     *domain.XUIClientEntry
	removed   bool
	addCalled bool
	// existsVal overrides Exists when non-nil (default behaviour stays "true").
	existsVal *bool

	// list/track knobs for bulk tests
	listEntries   []*domain.XUIClientEntry // returned by ListByUser / ListByInbound
	removedEmails []string                 // emails passed to RemoveByMatch
	addedEmails   []string                 // emails passed to Add
	updatedUUIDs  []string                 // emails passed to UpdateUUID
}

func newFakeOwnership(email, uuid string) *fakeOwnership {
	return &fakeOwnership{entry: &domain.XUIClientEntry{
		UserID:      1,
		PanelID:     1,
		InboundID:   100,
		ClientEmail: email,
		ClientUUID:  uuid,
	}}
}

func (r *fakeOwnership) Add(ctx context.Context, e *domain.XUIClientEntry) error {
	r.addCalled = true
	r.addedEmails = append(r.addedEmails, e.ClientEmail)
	return nil
}
func (r *fakeOwnership) Remove(ctx context.Context, id int64) error { return nil }
func (r *fakeOwnership) RemoveByMatch(ctx context.Context, panelID int64, inboundID int, email string) error {
	r.removed = true
	r.removedEmails = append(r.removedEmails, email)
	return nil
}
func (r *fakeOwnership) GetByMatch(ctx context.Context, panelID int64, inboundID int, email string) (*domain.XUIClientEntry, error) {
	return r.entry, nil
}
func (r *fakeOwnership) ListByUsers(ctx context.Context, userIDs []int64) (map[int64][]*domain.XUIClientEntry, error) {
	return map[int64][]*domain.XUIClientEntry{}, nil
}
func (r *fakeOwnership) ListByUser(ctx context.Context, userID int64) ([]*domain.XUIClientEntry, error) {
	return r.listEntries, nil
}
func (r *fakeOwnership) ListByInbound(ctx context.Context, panelID int64, inboundID int) ([]*domain.XUIClientEntry, error) {
	return r.listEntries, nil
}
func (r *fakeOwnership) Exists(ctx context.Context, panelID int64, inboundID int, email string) (bool, error) {
	if r.existsVal != nil {
		return *r.existsVal, nil
	}
	return true, nil
}
func (r *fakeOwnership) UpdateUUID(ctx context.Context, panelID int64, inboundID int, email, newUUID string) error {
	r.updatedUUIDs = append(r.updatedUUIDs, email)
	return nil
}
func (r *fakeOwnership) UpdateCounters(ctx context.Context, e *domain.XUIClientEntry) error {
	return nil
}
func (r *fakeOwnership) BatchUpdateCounters(ctx context.Context, items []*domain.XUIClientEntry) error {
	return nil
}

type fakeXUIClient struct {
	ports.XUIClient
	clients        []ports.ClientDetail
	deletedID      string
	deletedByEmail string
	delClientErr   error // when set, DelClient (by id) fails with this
	addClientErr   error // when set, AddClient fails with this

	// bulk knobs
	bulkDeleted    []string           // emails passed to BulkDelByEmail
	bulkDelErr     error              // when set, BulkDelByEmail fails with this
	bulkAddSpecs   []ports.ClientSpec // specs passed to BulkAddToInbound
	bulkAddResult  ports.BulkAddResult
	bulkAddErr     error
}

func (c *fakeXUIClient) AddClient(ctx context.Context, inboundID int, spec ports.ClientSpec) error {
	return c.addClientErr
}

func (c *fakeXUIClient) GetClient(ctx context.Context, email string) (*ports.ClientDetail, error) {
	for i := range c.clients {
		if c.clients[i].Email == email {
			cd := c.clients[i]
			return &cd, nil
		}
	}
	return nil, nil
}
func (c *fakeXUIClient) DelClient(ctx context.Context, inboundID int, clientUUID string) error {
	c.deletedID = clientUUID
	return c.delClientErr
}
func (c *fakeXUIClient) DelClientByEmail(ctx context.Context, inboundID int, email string) error {
	c.deletedByEmail = email
	return nil
}
func (c *fakeXUIClient) BulkDelByEmail(ctx context.Context, emails []string) (int, error) {
	if c.bulkDelErr != nil {
		return 0, c.bulkDelErr
	}
	c.bulkDeleted = append(c.bulkDeleted, emails...)
	return len(emails), nil
}
func (c *fakeXUIClient) BulkAddToInbound(ctx context.Context, inboundID int, specs []ports.ClientSpec) (ports.BulkAddResult, error) {
	if c.bulkAddErr != nil {
		return ports.BulkAddResult{}, c.bulkAddErr
	}
	c.bulkAddSpecs = append(c.bulkAddSpecs, specs...)
	return c.bulkAddResult, nil
}

var _ ports.OwnershipRepo = (*fakeOwnership)(nil)
var _ ports.XUIPool = (*fakePool)(nil)
var _ ports.XUIClient = (*fakeXUIClient)(nil)
