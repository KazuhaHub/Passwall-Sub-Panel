package alert

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- narrow-interface stubs ----

type stubNodes struct {
	nodes []*domain.Node
	err   error
}

func (s stubNodes) List(context.Context) ([]*domain.Node, error) { return s.nodes, s.err }

type stubPanels struct{ panels []*domain.XUIPanel }

func (s stubPanels) List(context.Context) ([]*domain.XUIPanel, error) { return s.panels, nil }

type stubCerts struct {
	failed []*domain.TLSCertificate
	active []*domain.TLSCertificate
}

func (s stubCerts) ListByStatus(_ context.Context, status domain.CertStatus) ([]*domain.TLSCertificate, error) {
	switch status {
	case domain.CertStatusFailed:
		return s.failed, nil
	case domain.CertStatusActive:
		return s.active, nil
	}
	return nil, nil
}

type stubEvents struct{ count int64 }

func (s stubEvents) CountByReasonSince(context.Context, string, time.Time) (int64, error) {
	return s.count, nil
}

type stubSettings struct{ s ports.UISettings }

func (s stubSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return s.s, nil
}

func tPtr(t time.Time) *time.Time { return &t }

func newSvc(d Deps, now time.Time) *Service {
	if d.Now == nil {
		d.Now = func() time.Time { return now }
	}
	return New(d)
}

func byType(alerts []Alert, typ Type) []Alert {
	var out []Alert
	for _, a := range alerts {
		if a.Type == typ {
			out = append(out, a)
		}
	}
	return out
}

func TestNodeHealthAlerts(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	nodes := []*domain.Node{
		{ID: 1, DisplayName: "ok-node", PanelID: 1, Enabled: true, HealthState: domain.NodeHealthOK},
		{ID: 2, DisplayName: "down-node", PanelID: 1, Enabled: true, HealthState: domain.NodeHealthPanelUnreachable, HealthCheckedAt: tPtr(now.Add(-1 * time.Minute))},
		{ID: 3, DisplayName: "disabled-bad", PanelID: 1, Enabled: false, HealthState: domain.NodeHealthInboundMissing},
		{ID: 4, DisplayName: "missing-node", PanelID: 2, Enabled: true, HealthState: domain.NodeHealthInboundMissing, HealthCheckedAt: tPtr(now.Add(-5 * time.Minute))},
	}
	svc := newSvc(Deps{
		Nodes:    stubNodes{nodes: nodes},
		Panels:   stubPanels{panels: []*domain.XUIPanel{{ID: 1, Name: "panel-A"}, {ID: 2, Name: "panel-B"}}},
		Settings: stubSettings{},
	}, now)

	alerts, _ := svc.List(context.Background())
	nh := byType(alerts, TypeNodeHealth)
	if len(nh) != 2 {
		t.Fatalf("want 2 node_health alerts (enabled+unhealthy only), got %d", len(nh))
	}
	// Sorted most-recently-checked first → node 2 (-1m) before node 4 (-5m).
	if nh[0].TargetID != 2 || nh[1].TargetID != 4 {
		t.Fatalf("node_health order wrong: %d then %d", nh[0].TargetID, nh[1].TargetID)
	}
	if nh[0].Severity != SeverityError || nh[0].PanelName != "panel-A" || nh[0].HealthState != string(domain.NodeHealthPanelUnreachable) {
		t.Fatalf("node_health alert fields wrong: %+v", nh[0])
	}
	if nh[0].Key != "node_health:2" {
		t.Fatalf("key = %q, want node_health:2", nh[0].Key)
	}
}

func TestPanelUpgradeAlerts(t *testing.T) {
	now := time.Now()
	svc := newSvc(Deps{
		Panels:   stubPanels{panels: []*domain.XUIPanel{{ID: 5, Name: "p5", PanelVersion: "3.2.6"}, {ID: 6, Name: "p6", PanelVersion: "3.2.8"}}},
		Settings: stubSettings{},
		// p5 has a tested-supported upgrade to 3.2.8; p6 is up to date.
		UpgradeFor: func(current string) (string, bool) {
			if current == "3.2.6" {
				return "3.2.8", true
			}
			return "", false
		},
	}, now)
	alerts, _ := svc.List(context.Background())
	up := byType(alerts, TypePanelUpgrade)
	if len(up) != 1 {
		t.Fatalf("want 1 panel_upgrade alert, got %d", len(up))
	}
	a := up[0]
	if a.Severity != SeverityInfo || a.TargetID != 5 || a.CurrentVersion != "3.2.6" || a.LatestVersion != "3.2.8" {
		t.Fatalf("panel_upgrade fields wrong: %+v", a)
	}
}

func TestPanelUpgradeAlertsSkipOtherProducts(t *testing.T) {
	for _, tt := range []struct {
		name    string
		kind    domain.PanelKind
		version string
	}{
		// The beta7 screenshot compared this Passwall Node build to 3X-UI 3.7.0.
		{"passwall_node_beta7_screenshot", domain.PanelKindPSP, "v0.0.1-beta4 (4b40af2)"},
		// Conversion can temporarily retain the previous upstream panel version.
		{"passwall_node_with_stale_xui_version", domain.PanelKindPSP, "3.4.2"},
		{"s_ui", domain.PanelKindSUI, "1.3.0"},
		{"unknown_kind_with_xui_like_version", domain.PanelKind("future-panel"), "3.4.2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			svc := New(Deps{
				Panels: stubPanels{panels: []*domain.XUIPanel{{
					ID: 1, Kind: tt.kind, Name: "Canada BC Danika Home - Telus", PanelVersion: tt.version,
				}}},
				UpgradeFor: func(string) (string, bool) {
					calls++
					return "3.7.0", true
				},
			})
			alerts, counts := svc.List(context.Background())
			if calls != 0 {
				t.Fatalf("3X-UI upgrade callback called %d times for kind %q", calls, tt.kind)
			}
			if len(alerts) != 0 || counts != (Counts{}) {
				t.Fatalf("another product generated 3X-UI alerts or badge counts: alerts=%+v counts=%+v", alerts, counts)
			}
		})
	}
}

func TestPanelUpgradeAlertsMixedKindsPreserve3XUIAndLegacy(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	var compared []string
	svc := newSvc(Deps{
		Panels: stubPanels{panels: []*domain.XUIPanel{
			{ID: 1, Kind: domain.PanelKindPSP, Name: "Canada BC Danika Home - Telus", PanelVersion: "v0.0.1-beta4 (4b40af2)"},
			{ID: 2, Kind: domain.PanelKindSUI, Name: "S-UI", PanelVersion: "1.3.0"},
			{ID: 3, Kind: domain.PanelKind("future-panel"), Name: "Unknown", PanelVersion: "3.4.2"},
			{ID: 4, Kind: domain.PanelKind3XUI, Name: "China Shanghai - Aliyun", PanelVersion: "3.4.2"},
			{ID: 5, Name: "Legacy 3X-UI", PanelVersion: "3.4.2"},
			{ID: 6, Kind: domain.PanelKind3XUI, Name: "Current 3X-UI", PanelVersion: "3.7.0"},
		}},
		UpgradeFor: func(current string) (string, bool) {
			compared = append(compared, current)
			return "3.7.0", current == "3.4.2"
		},
		Nodes:    stubNodes{nodes: []*domain.Node{{ID: 7, Enabled: true, HealthState: domain.NodeHealthUnreachable}}},
		Certs:    stubCerts{active: []*domain.TLSCertificate{{ID: 8, Name: "expiring", Status: domain.CertStatusActive, NotAfter: tPtr(now.Add(3 * 24 * time.Hour))}}},
		Settings: stubSettings{s: ports.UISettings{CertRenewBeforeDays: 14}},
	}, now)
	alerts, counts := svc.List(context.Background())
	if len(compared) != 3 || compared[0] != "3.4.2" || compared[1] != "3.4.2" || compared[2] != "3.7.0" {
		t.Fatalf("only normalized 3X-UI versions should be compared, got %q", compared)
	}
	up := byType(alerts, TypePanelUpgrade)
	if len(up) != 2 {
		t.Fatalf("want 2 upgrades for explicit and legacy 3X-UI rows, got %+v", up)
	}
	for i, panel := range []struct {
		id   int64
		name string
	}{{4, "China Shanghai - Aliyun"}, {5, "Legacy 3X-UI"}} {
		a := up[i]
		if a.Key != "panel_upgrade:"+strconv.FormatInt(panel.id, 10)+":3.7.0" || a.TargetID != panel.id || a.TargetName != panel.name ||
			a.Type != TypePanelUpgrade || a.Severity != SeverityInfo || a.CurrentVersion != "3.4.2" || a.LatestVersion != "3.7.0" {
			t.Fatalf("3X-UI upgrade fields changed: %+v", a)
		}
	}
	if counts != (Counts{Error: 1, Warning: 1, Info: 2}) {
		t.Fatalf("badge counts must exclude false upgrades and retain other categories, got %+v", counts)
	}
}

func TestCertAlerts(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	svc := newSvc(Deps{
		Settings: stubSettings{s: ports.UISettings{CertRenewBeforeDays: 14}},
		Certs: stubCerts{
			failed: []*domain.TLSCertificate{{ID: 1, Name: "fail.example", Status: domain.CertStatusFailed, LastError: "dns timeout"}},
			active: []*domain.TLSCertificate{
				{ID: 2, Name: "fresh.example", Status: domain.CertStatusActive, NotAfter: tPtr(now.Add(60 * 24 * time.Hour))}, // far off → no alert
				{ID: 3, Name: "soon.example", Status: domain.CertStatusActive, NotAfter: tPtr(now.Add(5 * 24 * time.Hour))},   // within 14d → warning
				{ID: 4, Name: "gone.example", Status: domain.CertStatusActive, NotAfter: tPtr(now.Add(-2 * 24 * time.Hour))},  // already expired → error
			},
		},
	}, now)
	alerts, _ := svc.List(context.Background())

	failed := byType(alerts, TypeCertFailed)
	if len(failed) != 1 || failed[0].Severity != SeverityError || failed[0].LastError != "dns timeout" {
		t.Fatalf("cert_failed wrong: %+v", failed)
	}
	expiring := byType(alerts, TypeCertExpiring)
	if len(expiring) != 2 {
		t.Fatalf("want 2 cert_expiring (soon+gone, not fresh), got %d", len(expiring))
	}
	// Map id→severity to assert expired=error, soon=warning.
	sev := map[int64]Severity{}
	for _, a := range expiring {
		sev[a.TargetID] = a.Severity
	}
	if sev[3] != SeverityWarning || sev[4] != SeverityError {
		t.Fatalf("cert_expiring severities wrong: %+v", sev)
	}
}

func TestPSPUpgradeAlert(t *testing.T) {
	now := time.Now()
	// A newer stable available → one admin-only info alert carrying both versions.
	svc := newSvc(Deps{
		Settings:   stubSettings{},
		PSPUpgrade: func() (string, string, bool) { return "v3.7.0-beta.16", "v3.7.0", true },
	}, now)
	up := byType(mustList(t, svc), TypePSPUpgrade)
	if len(up) != 1 || up[0].Severity != SeverityInfo || up[0].CurrentVersion != "v3.7.0-beta.16" || up[0].LatestVersion != "v3.7.0" {
		t.Fatalf("psp_upgrade wrong: %+v", up)
	}
	if !TypePSPUpgrade.AdminOnly() {
		t.Fatal("psp_upgrade must be admin-only (operators don't manage PSP updates)")
	}
	// Up to date → silent.
	none := newSvc(Deps{Settings: stubSettings{}, PSPUpgrade: func() (string, string, bool) { return "v3.7.0", "v3.7.0", false }}, now)
	if len(byType(mustList(t, none), TypePSPUpgrade)) != 0 {
		t.Fatal("psp_upgrade must be silent when up to date")
	}
}

func TestLoginSecurityAlert(t *testing.T) {
	now := time.Now()
	// Lockout off → no alert even if events exist.
	off := newSvc(Deps{Settings: stubSettings{}, Events: stubEvents{count: 9}}, now)
	if a, _ := off.List(context.Background()); len(byType(a, TypeLoginSecurity)) != 0 {
		t.Fatal("login_security must be silent when lockout is disabled")
	}
	// Lockout on + recent locked_out events → one aggregate alert.
	on := newSvc(Deps{
		Settings: stubSettings{s: ports.UISettings{LockoutEnabled: true, LockoutWindowMinutes: 15}},
		Events:   stubEvents{count: 9},
	}, now)
	ls := byType(mustList(t, on), TypeLoginSecurity)
	if len(ls) != 1 || ls[0].Severity != SeverityWarning || ls[0].Count != 9 {
		t.Fatalf("login_security wrong: %+v", ls)
	}
}

func TestCountsAggregate(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	svc := newSvc(Deps{
		Nodes:      stubNodes{nodes: []*domain.Node{{ID: 1, Enabled: true, HealthState: domain.NodeHealthUnreachable}}},                                           // 1 error
		Certs:      stubCerts{active: []*domain.TLSCertificate{{ID: 2, Name: "s", Status: domain.CertStatusActive, NotAfter: tPtr(now.Add(3 * 24 * time.Hour))}}}, // 1 warning
		Panels:     stubPanels{panels: []*domain.XUIPanel{{ID: 3, Name: "p", PanelVersion: "3.2.6"}}},
		Settings:   stubSettings{s: ports.UISettings{CertRenewBeforeDays: 14}},
		UpgradeFor: func(string) (string, bool) { return "3.2.8", true }, // 1 info
		// Two geo singletons: one warning each, however many users are
		// behind them — the badge counts things to look at, not accounts.
		GeoFlags:     &stubGeoFlags{n: 4},
		ServiceHolds: &stubServiceHolds{byReason: map[domain.AutoDisabledReason]int64{domain.DisabledGeoAutoSuspend: 2}},
	}, now)
	_, counts := svc.List(context.Background())
	if counts.Error != 1 || counts.Warning != 3 || counts.Info != 1 {
		t.Fatalf("counts wrong: %+v, want error 1, warning 3 (cert + two geo entries), info 1", counts)
	}
}

func mustList(t *testing.T, s *Service) []Alert {
	t.Helper()
	a, _ := s.List(context.Background())
	return a
}

// ---- location detector (geo.go) ----

type stubGeoFlags struct {
	n     int64
	err   error
	since time.Time
	calls int
}

func (s *stubGeoFlags) CountFlagged(_ context.Context, since time.Time) (int64, error) {
	s.calls++
	s.since = since
	return s.n, s.err
}

type stubServiceHolds struct {
	byReason map[domain.AutoDisabledReason]int64
	err      error
	asked    []domain.AutoDisabledReason
}

func (s *stubServiceHolds) CountByServiceDisabledReason(_ context.Context, r domain.AutoDisabledReason) (int64, error) {
	s.asked = append(s.asked, r)
	return s.byReason[r], s.err
}

// One entry for however many accounts are flagged, counted from the latch
// and bounded to rows the detector judged in the last day. The bound is
// passed through, not reinvented: the bell must stop lighting for a user the
// poll stopped judging (a deleted client, a dead poll) rather than keep a
// week-old latch on screen forever.
func TestGeoAnomalyAlert_CountsFlaggedUsers(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	flags := &stubGeoFlags{n: 3}
	got := byType(mustList(t, newSvc(Deps{GeoFlags: flags}, now)), TypeGeoAnomaly)
	if len(got) != 1 {
		t.Fatalf("geo_anomaly alerts = %+v, want exactly one singleton", got)
	}
	a := got[0]
	if a.Key != "geo_anomaly" || a.Severity != SeverityWarning || a.Count != 3 {
		t.Fatalf("geo_anomaly = %+v, want key geo_anomaly, severity warning, count 3", a)
	}
	if want := now.Add(-24 * time.Hour); !flags.since.Equal(want) {
		t.Fatalf("CountFlagged since = %v, want %v (24 hours before now)", flags.since, want)
	}
}

// No flagged account, no entry — and a failing count is skipped rather than
// blanking the feed, like every other source here.
func TestGeoAnomalyAlert_SilentWhenNoneFlagged(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if got := byType(mustList(t, newSvc(Deps{GeoFlags: &stubGeoFlags{}}, now)), TypeGeoAnomaly); len(got) != 0 {
		t.Fatalf("geo_anomaly with nobody flagged = %+v, want none", got)
	}
	failing := newSvc(Deps{
		GeoFlags: &stubGeoFlags{n: 5, err: errors.New("db down")},
		Nodes:    stubNodes{nodes: []*domain.Node{{ID: 1, Enabled: true, HealthState: domain.NodeHealthUnreachable}}},
	}, now)
	all := mustList(t, failing)
	if got := byType(all, TypeGeoAnomaly); len(got) != 0 {
		t.Fatalf("geo_anomaly on a count error = %+v, want none", got)
	}
	if len(byType(all, TypeNodeHealth)) != 1 {
		t.Fatal("a failing geo count blanked the rest of the feed")
	}
}

// The second entry counts only geo_auto — the detector's own time-boxed
// suspension. geo_anomaly is a person's decision and service_manual is an
// admin pause; neither is news the bell should repeat.
func TestGeoAutoSuspendedAlert_CountsGeoAutoOnly(t *testing.T) {
	holds := &stubServiceHolds{byReason: map[domain.AutoDisabledReason]int64{
		domain.DisabledGeoAutoSuspend: 2,
		domain.DisabledGeoAnomaly:     5,
		domain.DisabledServiceManual:  7,
	}}
	got := byType(mustList(t, newSvc(Deps{ServiceHolds: holds}, time.Now())), TypeGeoAutoSuspended)
	if len(got) != 1 {
		t.Fatalf("geo_auto_suspended alerts = %+v, want exactly one singleton", got)
	}
	a := got[0]
	if a.Key != "geo_auto_suspended" || a.Severity != SeverityWarning || a.Count != 2 {
		t.Fatalf("geo_auto_suspended = %+v, want key geo_auto_suspended, severity warning, count 2", a)
	}
	for _, r := range holds.asked {
		if r != domain.DisabledGeoAutoSuspend {
			t.Fatalf("asked for reason %q; the entry is about geo_auto only", r)
		}
	}

	none := &stubServiceHolds{byReason: map[domain.AutoDisabledReason]int64{domain.DisabledGeoAnomaly: 5}}
	if got := byType(mustList(t, newSvc(Deps{ServiceHolds: none}, time.Now())), TypeGeoAutoSuspended); len(got) != 0 {
		t.Fatalf("geo_auto_suspended with no geo_auto rows = %+v, want none", got)
	}
	failing := &stubServiceHolds{byReason: map[domain.AutoDisabledReason]int64{domain.DisabledGeoAutoSuspend: 2}, err: errors.New("db down")}
	if got := byType(mustList(t, newSvc(Deps{ServiceHolds: failing}, time.Now())), TypeGeoAutoSuspended); len(got) != 0 {
		t.Fatalf("geo_auto_suspended on a count error = %+v, want none", got)
	}
}

// Both entries lead to the Geo tab, which is admin-only because it names
// people on a signal rather than proof. The feed route is staff-visible, so
// AdminOnly is what keeps an operator from being handed that link.
func TestGeoAlertsAreAdminOnly(t *testing.T) {
	for _, typ := range []Type{TypeGeoAnomaly, TypeGeoAutoSuspended} {
		if !typ.AdminOnly() {
			t.Errorf("%s must be admin-only (the Geo tab is the owner's call, not an operator's)", typ)
		}
	}
}
