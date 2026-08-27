package sharedclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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

// --- Phase 1b: the per-user fan-out runs concurrently ---

// concurrencyProbe records how many SyncLifecycle calls were in flight at
// once, so "parallel" is asserted rather than assumed.
type concurrencyProbe struct {
	mu      sync.Mutex
	cur     int
	peak    int
	release chan struct{}
}

func (p *concurrencyProbe) enter() {
	p.mu.Lock()
	p.cur++
	if p.cur > p.peak {
		p.peak = p.cur
	}
	p.mu.Unlock()
}

func (p *concurrencyProbe) leave() {
	p.mu.Lock()
	p.cur--
	p.mu.Unlock()
}

// probeXUI blocks inside GetClient until every goroutine has arrived, which
// deadlocks (and so fails the test by timeout) if the calls are serial.
type probeXUI struct {
	fakeXUI
	probe   *concurrencyProbe
	arrived chan struct{}
	want    int
}

func (c *probeXUI) GetClient(_ context.Context, _ string) (*ports.ClientDetail, error) {
	c.probe.enter()
	defer c.probe.leave()
	c.arrived <- struct{}{}
	<-c.probe.release
	// A nil detail means "not on the panel", which sends SyncLifecycle
	// straight to UpdateClient — the shape that exercises both round trips.
	return nil, nil
}

func TestSyncUserLifecycle_FansOutConcurrently(t *testing.T) {
	const n = 4
	probe := &concurrencyProbe{release: make(chan struct{})}
	xui := &probeXUI{probe: probe, arrived: make(chan struct{}, n), want: n}

	byUser := make([]*domain.PSPClient, n)
	atts := make([]domain.PSPClientInbound, n)
	for i := range byUser {
		// Distinct panels and emails — the shape the safety argument rests on.
		byUser[i] = &domain.PSPClient{
			ID: int64(i + 1), UserID: 7, PanelID: int64(10 + i),
			Email: domain.PSPClientEmail(7, "", domain.EmailRules{}), UUID: "uuid-7",
		}
		atts[i] = domain.PSPClientInbound{ClientID: int64(i + 1), NodeID: int64(100 + i), Provisioned: true}
	}
	clients := &fakeClients{byUser: byUser, attachments: atts}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})

	done := make(chan error, 1)
	go func() { done <- svc.SyncUserLifecycle(context.Background(), 7, true, 0, 0) }()

	// Every client must reach GetClient before ANY of them is allowed to
	// finish. Serial execution can never satisfy this.
	for i := 0; i < n; i++ {
		select {
		case <-xui.arrived:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d clients reached the panel — the fan-out is still serial", i, n)
		}
	}
	close(probe.release)

	if err := <-done; err != nil {
		t.Fatalf("SyncUserLifecycle: %v", err)
	}
	if probe.peak < n {
		t.Errorf("peak concurrency = %d, want %d", probe.peak, n)
	}
}

var errFakeGet = errors.New("fake panel failure")

// A parallel fan-out must still report the first error in CLIENT order, not
// whichever goroutine happened to fail first — the returned error reaches an
// admin, and making it depend on panel latency would make the same failure
// report differently on every run.
func TestSyncUserLifecycle_FirstErrorIsDeterministic(t *testing.T) {
	// Client 1 fails but is slow; client 2 fails fast. Client order must win.
	byUser := []*domain.PSPClient{
		{ID: 1, UserID: 7, PanelID: 10, Email: "slow@psp.local", UUID: "u"},
		{ID: 2, UserID: 7, PanelID: 11, Email: "fast@psp.local", UUID: "u"},
	}
	clients := &fakeClients{byUser: byUser, attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 101, Provisioned: true},
		{ClientID: 2, NodeID: 102, Provisioned: true},
	}}
	xui := &failByEmailXUI{fail: map[string]bool{"slow@psp.local": true, "fast@psp.local": true},
		delay: map[string]time.Duration{"slow@psp.local": 40 * time.Millisecond}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})

	err := svc.SyncUserLifecycle(context.Background(), 7, true, 0, 0)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "slow@psp.local") {
		t.Fatalf("error = %v, want the FIRST client in list order (slow@psp.local), not the first to fail", err)
	}
}

type failByEmailXUI struct {
	fakeXUI
	fail  map[string]bool
	delay map[string]time.Duration
}

func (c *failByEmailXUI) GetClient(_ context.Context, _ string) (*ports.ClientDetail, error) {
	return nil, nil
}

func (c *failByEmailXUI) UpdateClient(_ context.Context, _ int, _ string, spec ports.ClientSpec) error {
	time.Sleep(c.delay[spec.Email])
	if c.fail[spec.Email] {
		return fmt.Errorf("update %s: %w", spec.Email, errFakeGet)
	}
	return nil
}

// Every client must be attempted even when an earlier one fails — the serial
// loop this replaced guaranteed that, and losing it would leave a user
// half-synced whenever one panel is down.
func TestSyncUserLifecycle_AttemptsEveryClientDespiteFailures(t *testing.T) {
	byUser := []*domain.PSPClient{
		{ID: 1, UserID: 7, PanelID: 10, Email: "a@psp.local", UUID: "u"},
		{ID: 2, UserID: 7, PanelID: 11, Email: "b@psp.local", UUID: "u"},
		{ID: 3, UserID: 7, PanelID: 12, Email: "c@psp.local", UUID: "u"},
	}
	clients := &fakeClients{byUser: byUser, attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 101, Provisioned: true},
		{ClientID: 2, NodeID: 102, Provisioned: true},
		{ClientID: 3, NodeID: 103, Provisioned: true},
	}}
	xui := &countingUpdateXUI{fail: map[string]bool{"a@psp.local": true}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})

	if err := svc.SyncUserLifecycle(context.Background(), 7, true, 0, 0); err == nil {
		t.Fatal("want the failing client's error")
	}
	if got := xui.updates(); got != 3 {
		t.Fatalf("update attempts = %d, want 3 — a failure must not abandon the rest", got)
	}
}

type countingUpdateXUI struct {
	fakeXUI
	fail map[string]bool
	mu   sync.Mutex
	n    int
}

func (c *countingUpdateXUI) GetClient(context.Context, string) (*ports.ClientDetail, error) {
	return nil, nil
}

func (c *countingUpdateXUI) UpdateClient(_ context.Context, _ int, _ string, spec ports.ClientSpec) error {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	if c.fail[spec.Email] {
		return errFakeGet
	}
	return nil
}

func (c *countingUpdateXUI) updates() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// --- The quota floor is rebased onto the panel's own counter ---

// SyncLifecycle receives PERIOD-relative headroom but the panel enforces
// against a lifetime counter it never resets. Pushing the headroom raw
// disabled users at half their quota (docs/traffic-floor-defect.md); this
// pins the rebase at the service boundary, not just in the domain helper.
func TestSyncLifecycle_PushesTheQuotaCapRebasedOnPanelLifetime(t *testing.T) {
	const GB = int64(1) << 30
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11, Provisioned: true},
	}}
	xui := &fakeXUI{}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})

	c := &domain.PSPClient{
		ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x",
		LastRawTotalBytes: 60 * GB, // what the panel's counter already holds
	}
	// 40 GB left this period.
	if err := svc.SyncLifecycle(context.Background(), c, true, 0, 40*GB); err != nil {
		t.Fatal(err)
	}
	if got := xui.updatedSpec.TotalGB; got != 100*GB {
		t.Fatalf("pushed totalGB = %d, want %d (60GB already counted + 40GB headroom). "+
			"Pushing the bare headroom is the defect that disabled users at half quota.", got, 100*GB)
	}
}

// An unlimited user must keep an unlimited panel cap. Rebasing a zero
// headroom would invent a quota out of the client's own history — the exact
// inverse defect, and one that would cut off users who have no limit at all.
func TestSyncLifecycle_UnlimitedStaysUnlimited(t *testing.T) {
	const GB = int64(1) << 30
	clients := &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11, Provisioned: true},
	}}
	xui := &fakeXUI{}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})

	c := &domain.PSPClient{
		ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x",
		LastRawTotalBytes: 500 * GB,
	}
	if err := svc.SyncLifecycle(context.Background(), c, true, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := xui.updatedSpec.TotalGB; got != 0 {
		t.Fatalf("pushed totalGB = %d, want 0 (unlimited)", got)
	}
}
