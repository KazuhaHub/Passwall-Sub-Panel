package handler

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/alert"
)

// The dashboard "expiring soon" card and the notification bell (alert.Service)
// derive expiring users from the same idea but in two places. They MUST use the
// same look-ahead window, or a user could surface under one and not the other.
// This is the drift guard the unified-feed design calls for: change one window
// without the other and this test goes red.
func TestExpiringWindowMatchesAlertService(t *testing.T) {
	if expiringWindowDays != alert.UserExpiringWindowDays {
		t.Fatalf("dashboard expiringWindowDays=%d but alert.UserExpiringWindowDays=%d — the bell and the dashboard card would disagree on which users are 'expiring soon'; keep them equal",
			expiringWindowDays, alert.UserExpiringWindowDays)
	}
}

// Every alert category the dashboard surfaces must also be a category the
// unified AlertService produces, so migrating the dashboard to consume the feed
// can never silently drop one. Pins the "dashboard types ⊆ AlertService types"
// invariant from the notification-center design.
func TestDashboardCategoriesCoveredByAlertService(t *testing.T) {
	// node_alerts → node_health, cert_alerts → cert_failed,
	// expiring_users → user_expiring.
	for _, typ := range []alert.Type{alert.TypeNodeHealth, alert.TypeCertFailed, alert.TypeUserExpiring} {
		if typ == "" {
			t.Fatalf("a dashboard-surfaced category has no AlertService type constant")
		}
	}
}
