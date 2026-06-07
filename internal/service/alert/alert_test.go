package alert

import (
	"context"
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

func TestCertAlerts(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	svc := newSvc(Deps{
		Settings: stubSettings{s: ports.UISettings{CertRenewBeforeDays: 14}},
		Certs: stubCerts{
			failed: []*domain.TLSCertificate{{ID: 1, Name: "fail.example", Status: domain.CertStatusFailed, LastError: "dns timeout"}},
			active: []*domain.TLSCertificate{
				{ID: 2, Name: "fresh.example", Status: domain.CertStatusActive, NotAfter: tPtr(now.Add(60 * 24 * time.Hour))},  // far off → no alert
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
		Nodes:    stubNodes{nodes: []*domain.Node{{ID: 1, Enabled: true, HealthState: domain.NodeHealthUnreachable}}}, // 1 error
		Certs:    stubCerts{active: []*domain.TLSCertificate{{ID: 2, Name: "s", Status: domain.CertStatusActive, NotAfter: tPtr(now.Add(3 * 24 * time.Hour))}}}, // 1 warning
		Panels:   stubPanels{panels: []*domain.XUIPanel{{ID: 3, Name: "p", PanelVersion: "3.2.6"}}},
		Settings: stubSettings{s: ports.UISettings{CertRenewBeforeDays: 14}},
		UpgradeFor: func(string) (string, bool) { return "3.2.8", true }, // 1 info
	}, now)
	_, counts := svc.List(context.Background())
	if counts.Error != 1 || counts.Warning != 1 || counts.Info != 1 {
		t.Fatalf("counts wrong: %+v", counts)
	}
}

func mustList(t *testing.T, s *Service) []Alert {
	t.Helper()
	a, _ := s.List(context.Background())
	return a
}
