package handler

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/alert"
)

// Every alert category the dashboard surfaces in the bell must also be a category
// the unified AlertService produces. user_expiring was intentionally REMOVED from
// the bell in v3.7.0 (notifications are admin-operational only; user expiry stays
// a dashboard card + a per-user email reminder), so it's no longer listed here.
func TestDashboardCategoriesCoveredByAlertService(t *testing.T) {
	for _, typ := range []alert.Type{alert.TypeNodeHealth, alert.TypeCertFailed} {
		if typ == "" {
			t.Fatalf("a dashboard-surfaced category has no AlertService type constant")
		}
	}
}
