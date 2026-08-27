package sharedclient

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// lifecycleWriteReason replaced an inline seven-field comparison. It is the
// no-op skip's condition now, so a field silently dropped from it makes the
// skip fire when it must not — a disabled user staying enabled in Xray, or a
// rotated UUID never reaching the panel. This table pins every field.
func TestLifecycleWriteReason(t *testing.T) {
	base := ports.ClientSpec{
		Enable: true, ExpiryTime: 1893456000000, TotalGB: 5 << 30,
		ID: "uuid-x", Password: "pw-x", Flow: "xtls-rprx-vision", Auth: "uuid-x",
	}
	match := func() *ports.ClientDetail {
		return &ports.ClientDetail{
			Enable: base.Enable, ExpiryTime: base.ExpiryTime, TotalGB: base.TotalGB,
			ID: base.ID, Password: base.Password, Flow: base.Flow, Auth: base.Auth,
		}
	}

	if got := lifecycleWriteReason(match(), base); got != "" {
		t.Fatalf("identical state must skip, got reason %q", got)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*ports.ClientDetail)
		want   string
	}{
		{"quota floor shrank", func(d *ports.ClientDetail) { d.TotalGB = 4 << 30 }, "total_gb"},
		{"user disabled", func(d *ports.ClientDetail) { d.Enable = false }, "enable"},
		{"expiry moved", func(d *ports.ClientDetail) { d.ExpiryTime = 1 }, "expiry"},
		{"uuid rotated", func(d *ports.ClientDetail) { d.ID = "uuid-y" }, "id"},
		{"password reset", func(d *ports.ClientDetail) { d.Password = "pw-y" }, "password"},
		{"flow changed", func(d *ports.ClientDetail) { d.Flow = "" }, "flow"},
		{"auth changed", func(d *ports.ClientDetail) { d.Auth = "other" }, "auth"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := match()
			tc.mutate(d)
			if got := lifecycleWriteReason(d, base); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

// total_gb is checked first on purpose: it is the field the plan predicts
// defeats the skip on every cycle, and a reason breakdown cannot confirm
// that if some other field can mask it.
func TestLifecycleWriteReason_QuotaWinsWhenSeveralFieldsDiffer(t *testing.T) {
	spec := ports.ClientSpec{Enable: true, TotalGB: 5 << 30, ID: "a"}
	cur := &ports.ClientDetail{Enable: false, TotalGB: 4 << 30, ID: "b"}
	if got := lifecycleWriteReason(cur, spec); got != "total_gb" {
		t.Fatalf("reason = %q, want total_gb", got)
	}
}

func counterByName(t *testing.T, name string) int64 {
	t.Helper()
	for _, c := range metrics.Take().Counters {
		if c.Name == name {
			return c.Value
		}
	}
	return 0
}

// The whole point of the Phase 0 counters is that skip rate is a ratio and
// not an estimate, which only holds if every call lands in exactly one
// bucket. Assert both arms end-to-end through SyncLifecycle.
func TestSyncLifecycle_SkipAndWriteAreCountedExactlyOnce(t *testing.T) {
	spec := func() *ports.ClientDetail {
		return &ports.ClientDetail{
			Enable: false, ExpiryTime: 1893456000000, TotalGB: 5 << 30,
			ID: "uuid-x", Password: "pw-x", Flow: "xtls-rprx-vision", Auth: "uuid-x",
		}
	}
	newSvc := func(detail *ports.ClientDetail) (*Service, *fakeXUI) {
		clients := &fakeClients{attachments: []domain.PSPClientInbound{
			{ClientID: 1, NodeID: 11, FlowOverride: "xtls-rprx-vision", Provisioned: true},
		}}
		xui := &fakeXUI{getDetail: detail}
		return New(clients, fakePool{c: xui}, fakeNodes{}), xui
	}
	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x", Password: "pw-x"}

	t.Run("panel already matches", func(t *testing.T) {
		metrics.Reset()
		svc, xui := newSvc(spec())
		if err := svc.SyncLifecycle(context.Background(), c, false, 1893456000000, 5<<30); err != nil {
			t.Fatal(err)
		}
		if xui.updateCalls != 0 {
			t.Errorf("update calls = %d, want 0 (the skip should have fired)", xui.updateCalls)
		}
		if got := counterByName(t, "psp_lifecycle_sync_skipped_total"); got != 1 {
			t.Errorf("skipped = %d, want 1", got)
		}
		if got := counterByName(t, "psp_lifecycle_sync_write_total"); got != 0 {
			t.Errorf("write = %d, want 0", got)
		}
		if got := counterByName(t, "psp_lifecycle_sync_total"); got != 1 {
			t.Errorf("total = %d, want 1", got)
		}
	})

	t.Run("quota floor moved", func(t *testing.T) {
		metrics.Reset()
		stale := spec()
		stale.TotalGB = 6 << 30 // the floor shrank by 1 GiB since the last push
		svc, xui := newSvc(stale)
		if err := svc.SyncLifecycle(context.Background(), c, false, 1893456000000, 5<<30); err != nil {
			t.Fatal(err)
		}
		if xui.updateCalls != 1 {
			t.Errorf("update calls = %d, want 1", xui.updateCalls)
		}
		if got := counterByName(t, "psp_lifecycle_sync_skipped_total"); got != 0 {
			t.Errorf("skipped = %d, want 0", got)
		}
		if got := counterByName(t, "psp_lifecycle_sync_write_total"); got != 1 {
			t.Errorf("write = %d, want 1", got)
		}
		if got := counterByName(t, `psp_lifecycle_sync_write_reason_total{reason=total_gb}`); got != 1 {
			t.Errorf("total_gb reason = %d, want 1", got)
		}
	})

	// An un-provisioned client returns before the decision, so it must NOT
	// land in the denominator — otherwise a fleet mid-cutover reports a
	// flattering skip rate made of calls that never considered a push.
	t.Run("unprovisioned is outside the denominator", func(t *testing.T) {
		metrics.Reset()
		clients := &fakeClients{attachments: []domain.PSPClientInbound{{ClientID: 1, NodeID: 11}}}
		svc := New(clients, fakePool{c: &fakeXUI{}}, fakeNodes{})
		if err := svc.SyncLifecycle(context.Background(), c, false, 0, 0); err != nil {
			t.Fatal(err)
		}
		if got := counterByName(t, "psp_lifecycle_sync_total"); got != 0 {
			t.Errorf("total = %d, want 0", got)
		}
		if got := counterByName(t, "psp_lifecycle_sync_not_provisioned_total"); got != 1 {
			t.Errorf("not_provisioned = %d, want 1", got)
		}
	})
}
