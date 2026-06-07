package handler

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/alert"
)

// driftNodes always reports one unhealthy node so AlertService emits node_health.
type driftNodes struct{}

func (driftNodes) List(context.Context) ([]*domain.Node, error) {
	return []*domain.Node{{ID: 1, Enabled: true, HealthState: domain.NodeHealthUnreachable, DisplayName: "n1"}}, nil
}

// driftCerts reports one failed cert so AlertService emits cert_failed.
type driftCerts struct{}

func (driftCerts) ListByStatus(_ context.Context, status domain.CertStatus) ([]*domain.TLSCertificate, error) {
	if status == domain.CertStatusFailed {
		return []*domain.TLSCertificate{{ID: 1, Name: "c1", LastError: "boom"}}, nil
	}
	return nil, nil
}

// The dashboard surfaces node-health and cert-failure cards; the bell's unified
// AlertService must actually PRODUCE those same categories, or the two drift (a
// card shown in one place, silently missing in the other). This exercises the
// service with sources that trigger both and asserts the categories appear in
// its output — unlike a hardcoded constant check, it fails if AlertService ever
// stops emitting one of them.
//
// user_expiring was intentionally REMOVED from the bell in v3.7.0 (notifications
// are admin-operational only; user expiry stays a dashboard card + a per-user
// email reminder), so it's deliberately not asserted here.
func TestDashboardCategoriesCoveredByAlertService(t *testing.T) {
	svc := alert.New(alert.Deps{
		Nodes: driftNodes{},
		Certs: driftCerts{},
		Now:   func() time.Time { return time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC) },
	})
	alerts, _ := svc.List(context.Background())
	got := map[alert.Type]bool{}
	for _, a := range alerts {
		got[a.Type] = true
	}
	for _, want := range []alert.Type{alert.TypeNodeHealth, alert.TypeCertFailed} {
		if !got[want] {
			t.Fatalf("dashboard category %q is not produced by AlertService (drift between dashboard cards and the bell)", want)
		}
	}
}
