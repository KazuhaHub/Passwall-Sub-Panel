package sharedclient

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// capPool hands a different adapter per panel, so one user can straddle a
// panel that carries the caps and one that does not — the situation the edit
// form has to describe and could not.
type capPool struct {
	ports.XUIPool
	byPanel map[int64]ports.XUIClient
}

func (p capPool) Get(id int64) (ports.XUIClient, error) {
	c, ok := p.byPanel[id]
	if !ok {
		return nil, errors.New("panel not registered")
	}
	return c, nil
}

// capClient declares exactly the capability set it is given.
type capClient struct {
	ports.XUIClient
	caps []ports.PanelCapability
}

func (c capClient) Capabilities() []ports.PanelCapability { return c.caps }

func bothCaps() capClient {
	return capClient{caps: []ports.PanelCapability{
		ports.CapabilityClientIPLimit, ports.CapabilityClientDeviceLimit,
	}}
}

// The connection caps are enforced per (panel, client email) while an admin
// types ONE number for the user, so the form's number and the enforced number
// are different quantities. This reports the facts that difference is made of.
func TestLimitEnforcement(t *testing.T) {
	clients := &fakeClients{byUser: []*domain.PSPClient{
		{ID: 1, UserID: 7, PanelID: 10},
		// Second credential class on the SAME panel: two panel-side rows, so
		// this panel alone enforces the typed cap twice.
		{ID: 2, UserID: 7, PanelID: 10, CredClass: 1},
		{ID: 3, UserID: 7, PanelID: 20},
		{ID: 4, UserID: 9, PanelID: 30}, // another user; must not leak in
	}}
	svc := New(clients, capPool{byPanel: map[int64]ports.XUIClient{
		10: bothCaps(),
		// S-UI's shape: carries neither cap. A user on this panel has no
		// enforced cap there at all.
		20: capClient{caps: []ports.PanelCapability{ports.CapabilityClientWrite}},
	}}, fakeNodes{})

	got, err := svc.LimitEnforcement(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("panels = %d, want 2 (one row per PANEL, not per client)", len(got))
	}

	if got[0].PanelID != 10 || got[0].Clients != 2 {
		t.Fatalf("panel 10 = %+v, want 2 clients", got[0])
	}
	if !got[0].CanStoreIP || !got[0].CanStoreDevice {
		t.Fatalf("panel 10 should carry both caps: %+v", got[0])
	}
	if got[1].PanelID != 20 || got[1].Clients != 1 {
		t.Fatalf("panel 20 = %+v, want 1 client", got[1])
	}
	if got[1].CanStoreIP || got[1].CanStoreDevice {
		t.Fatalf("panel 20 carries neither cap, so both must be false: %+v", got[1])
	}
}

// A panel the pool will not hand over is UNKNOWN, not "cannot store". The two
// read differently to an operator and only one of them is actionable, and an
// unread precondition must never render as a working cap — the same rule the
// fail2ban verdict is held to.
func TestLimitEnforcementSeparatesUnreachableFromUncapable(t *testing.T) {
	clients := &fakeClients{byUser: []*domain.PSPClient{{ID: 1, UserID: 7, PanelID: 99}}}
	svc := New(clients, capPool{byPanel: map[int64]ports.XUIClient{}}, fakeNodes{})

	got, err := svc.LimitEnforcement(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("panels = %d, want 1 — a panel we cannot read must be REPORTED, not dropped", len(got))
	}
	if !got[0].PanelUnreachable {
		t.Fatalf("want PanelUnreachable: %+v", got[0])
	}
	if got[0].CanStoreIP || got[0].CanStoreDevice {
		t.Fatalf("an unreachable panel must not claim a capability: %+v", got[0])
	}
}

// A user with no clients has nothing enforcing anything, and that is an empty
// list rather than an error: it is the ordinary state of a freshly created
// user, and an error here would put a red banner on the edit form.
func TestLimitEnforcementNoClients(t *testing.T) {
	svc := New(&fakeClients{}, capPool{}, fakeNodes{})
	got, err := svc.LimitEnforcement(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("panels = %d, want 0", len(got))
	}
}
