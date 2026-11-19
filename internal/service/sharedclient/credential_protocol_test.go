package sharedclient

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// staleXUI models what production 3X-UI 3.7.0 does to a VLESS client after the
// 4.3 per-line split: UpdateClient takes the new password but the read-back
// keeps the old shared one, because VLESS authenticates by id and the stored
// password field is inert for it. Everything else round-trips.
type staleXUI struct {
	fakeXUI
	stored *ports.ClientDetail
	stale  string // what GetClient reports as the password
	writes int
}

func (c *staleXUI) Capabilities() []ports.PanelCapability {
	return []ports.PanelCapability{ports.CapabilityClientWrite, ports.CapabilityClientIPLimit, ports.CapabilityClientDeviceLimit}
}

func (c *staleXUI) GetClient(context.Context, string) (*ports.ClientDetail, error) {
	if c.stored == nil {
		return nil, nil
	}
	d := *c.stored
	d.Password = c.stale
	return &d, nil
}

func (c *staleXUI) UpdateClient(_ context.Context, spec ports.ClientSpec) error {
	c.writes++
	c.stored = &ports.ClientDetail{
		ID: spec.ID, Email: spec.Email, Enable: spec.Enable, Flow: spec.Flow,
		Password: spec.Password, Auth: spec.Auth, ExpiryTime: spec.ExpiryTime,
		TotalGB: spec.TotalGB, LimitIP: spec.LimitIP, LimitHwid: spec.LimitHwid,
	}
	return nil
}

// allCreds compares every credential field, which is what the skip did before
// it became protocol-aware.
var allCreds = domain.CredentialFieldsFor()

func credNodes(protos map[int64]string) fakeNodes {
	by := map[int64]*domain.Node{}
	for id, p := range protos {
		n := enabledNode(id, 10, int(id))
		n.DesiredProtocol = p
		by[id] = n
	}
	return fakeNodes{byID: by}
}

func credClient() *domain.PSPClient {
	return &domain.PSPClient{ID: 1, PanelID: 10, Email: "u15@psp.local", UUID: "11111111-2222-3333-4444-555555555555", Password: "11111111-2222-3333-4444-555555555555"}
}

func credAttachments(nodeIDs ...int64) []domain.PSPClientInbound {
	var out []domain.PSPClientInbound
	for _, id := range nodeIDs {
		out = append(out, domain.PSPClientInbound{ClientID: 1, NodeID: id, State: domain.ClientApplyApplied})
	}
	return out
}

func TestStaleVLESSPasswordDoesNotFailConfirmOrDefeatSkip(t *testing.T) {
	metrics.Reset()
	xui := &staleXUI{stale: "STALE-44-CHAR-SHARED-PASSWORD-FROM-BEFORE-SPLIT=="}
	svc := New(&fakeClients{attachments: credAttachments(12)}, fakePool{c: xui}, credNodes(map[int64]string{12: "vless"}))
	c := credClient()
	push := func() error {
		return syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true})
	}
	if err := push(); err != nil {
		t.Fatalf("first push: a VLESS-only client's stale password must not fail the confirm: %v", err)
	}
	if xui.writes != 1 {
		t.Fatalf("writes = %d, want 1", xui.writes)
	}
	if err := push(); err != nil {
		t.Fatalf("second push: %v", err)
	}
	if xui.writes != 1 {
		t.Fatalf("writes = %d after second push, want 1: the skip must fire despite the stale password", xui.writes)
	}
}

func TestPasswordProtocolStillFailsConfirmOnWrongReadBack(t *testing.T) {
	metrics.Reset()
	xui := &staleXUI{stale: "wrong"}
	svc := New(&fakeClients{attachments: credAttachments(17)}, fakePool{c: xui}, credNodes(map[int64]string{17: "shadowsocks"}))
	c := credClient()
	c.Password = "ss2022-psk-wanted"
	if err := syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true}); err == nil {
		t.Fatal("an SS client whose password does not read back must still fail the confirm")
	}
}

func TestMixedAttachmentsCompareThePassword(t *testing.T) {
	metrics.Reset()
	xui := &staleXUI{stale: "wrong"}
	svc := New(&fakeClients{attachments: credAttachments(12, 17)}, fakePool{c: xui}, credNodes(map[int64]string{12: "vless", 17: "shadowsocks"}))
	c := credClient()
	c.Password = "ss2022-psk-wanted"
	if err := syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true}); err == nil {
		t.Fatal("a legacy client with a password-protocol attachment must have its password compared")
	}
}

func TestUnresolvableNodeFailsClosed(t *testing.T) {
	metrics.Reset()
	xui := &staleXUI{stale: "wrong"}
	svc := New(&fakeClients{attachments: credAttachments(12)}, fakePool{c: xui}, fakeNodes{})
	if err := syncLifecycle(svc, context.Background(), credClient(), domain.UserLifecycle{Enable: true}); err == nil {
		t.Fatal("with no protocol known every credential field must still be compared")
	}
}

type idDriftXUI struct{ staleXUI }

func (c *idDriftXUI) GetClient(ctx context.Context, e string) (*ports.ClientDetail, error) {
	d, err := c.staleXUI.GetClient(ctx, e)
	if d != nil {
		d.ID = "some-other-uuid"
	}
	return d, err
}

func TestVLESSIDMismatchStillFailsConfirm(t *testing.T) {
	metrics.Reset()
	xui := &idDriftXUI{staleXUI: staleXUI{stale: "x"}}
	svc := New(&fakeClients{attachments: credAttachments(12)}, fakePool{c: xui}, credNodes(map[int64]string{12: "vless"}))
	if err := syncLifecycle(svc, context.Background(), credClient(), domain.UserLifecycle{Enable: true}); err == nil {
		t.Fatal("a VLESS client whose id does not read back must still fail")
	}
}
