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

func TestBuildClientSpecSUIModernProtocols(t *testing.T) {
	for _, protocol := range []domain.Protocol{domain.ProtoAnyTLS, domain.ProtoTUIC, domain.ProtoNaive} {
		spec := buildClientSpec(protocol, "", "uuid-xyz", "u@example.test", "", 0, 0)
		if spec.ID != "uuid-xyz" || spec.Password != "uuid-xyz" {
			t.Fatalf("%s spec = %+v, want UUID in id and password", protocol, spec)
		}
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
func (r *fakeOwnership) DistinctUserIDs(ctx context.Context) ([]int64, error) { return nil, nil }
func (r *fakeOwnership) DropIfMigrated(ctx context.Context) (bool, error)     { return true, nil }
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
	deletedByEmail string
	addClientErr   error // when set, AddClient fails with this

	// bulk-delete knobs
	bulkDeleted []string // emails passed to BulkDelByEmail
	bulkDelErr  error    // when set, BulkDelByEmail fails with this
}

func (c *fakeXUIClient) AddClient(ctx context.Context, inboundID int, spec ports.ClientSpec) error {
	return c.addClientErr
}

func (c *fakeXUIClient) GetClient(ctx context.Context, email string) (*ports.ClientDetail, error) {
	return nil, nil
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

var _ ports.OwnershipRepo = (*fakeOwnership)(nil)
var _ ports.XUIPool = (*fakePool)(nil)
var _ ports.XUIClient = (*fakeXUIClient)(nil)
