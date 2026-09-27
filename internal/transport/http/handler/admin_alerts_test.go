package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/alert"
)

type alNodes struct{ n []*domain.Node }

func (a alNodes) List(context.Context) ([]*domain.Node, error) { return a.n, nil }

type alPanels struct{ p []*domain.XUIPanel }

func (a alPanels) List(context.Context) ([]*domain.XUIPanel, error) { return a.p, nil }

// alertsHandlerWith builds a real alert.Service producing one operator-visible
// alert (node_health) and one admin-only alert (panel_upgrade).
func alertsHandlerWith() *AdminAlertsHandler {
	svc := alert.New(alert.Deps{
		Nodes:      alNodes{n: []*domain.Node{{ID: 1, DisplayName: "n1", Enabled: true, HealthState: domain.NodeHealthUnreachable}}},
		Panels:     alPanels{p: []*domain.XUIPanel{{ID: 2, Name: "p2", PanelVersion: "3.2.6"}}},
		UpgradeFor: func(string) (string, bool) { return "3.2.8", true },
	})
	return NewAdminAlertsHandler(svc)
}

func TestAdminAlerts_OperatorHidesAdminOnly(t *testing.T) {
	h := alertsHandlerWith()
	c, rr := claimsCtx(domain.RoleOperator)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/alerts", nil)
	h.List(c)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"node_health"`) {
		t.Fatalf("operator must still see node_health: %s", body)
	}
	if strings.Contains(body, `"panel_upgrade"`) {
		t.Fatalf("operator must NOT see admin-only panel_upgrade (dead link): %s", body)
	}
	// Counts must match the filtered list (info dropped with the panel_upgrade).
	if !strings.Contains(body, `"info":0`) {
		t.Fatalf("counts must be recomputed after filtering: %s", body)
	}
}

func TestAdminAlerts_AdminSeesAll(t *testing.T) {
	h := alertsHandlerWith()
	c, rr := claimsCtx(domain.RoleAdmin)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/alerts", nil)
	h.List(c)

	body := rr.Body.String()
	if !strings.Contains(body, `"node_health"`) || !strings.Contains(body, `"panel_upgrade"`) {
		t.Fatalf("admin must see both node_health and panel_upgrade: %s", body)
	}
}

func TestAdminAlerts_MixedBackendsKeepUpgradeTargetsAndCountsIsolated(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleOperator} {
		t.Run(string(role), func(t *testing.T) {
			svc := alert.New(alert.Deps{
				Nodes: alNodes{n: []*domain.Node{{
					ID: 3, PanelID: 1, DisplayName: "native-health", Enabled: true,
					HealthState: domain.NodeHealthUnreachable,
				}}},
				Panels: alPanels{p: []*domain.XUIPanel{
					{ID: 1, Kind: domain.PanelKindPSP, Name: "Canada BC Danika Home - Telus", PanelVersion: "v0.0.1-beta4 (4b40af2)"},
					{ID: 2, Kind: domain.PanelKind3XUI, Name: "China Shanghai - Aliyun", PanelVersion: "3.4.2"},
					{ID: 4, Kind: domain.PanelKindSUI, Name: "S-UI server", PanelVersion: "1.5.0"},
				}},
				// Every version looks older to an XUI-only callback. The service
				// must first establish that this is actually an XUI server.
				UpgradeFor: func(string) (string, bool) { return "3.7.0", true },
			})
			c, rr := claimsCtx(role)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/alerts", nil)
			NewAdminAlertsHandler(svc).List(c)
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d, want 200: %s", rr.Code, rr.Body.String())
			}
			var response struct {
				Alerts []alert.Alert `json:"alerts"`
				Counts alert.Counts  `json:"counts"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			wantCounts := alert.Counts{Error: 1}
			if role == domain.RoleAdmin {
				wantCount, wantCounts.Info = 2, 1
			}
			if len(response.Alerts) != wantCount || response.Counts != wantCounts {
				t.Fatalf("alerts=%+v counts=%+v, want %d alerts and %+v", response.Alerts, response.Counts, wantCount, wantCounts)
			}
			for _, item := range response.Alerts {
				switch item.Type {
				case alert.TypeNodeHealth:
					if item.TargetID != 3 || item.PanelName != "Canada BC Danika Home - Telus" {
						t.Fatalf("native health alert must remain intact: %+v", item)
					}
				case alert.TypePanelUpgrade:
					if item.TargetID != 2 || item.CurrentVersion != "3.4.2" || item.LatestVersion != "3.7.0" {
						t.Fatalf("only the real XUI server may be offered XUI 3.7.0: %+v", item)
					}
				default:
					t.Fatalf("unexpected alert: %+v", item)
				}
			}
		})
	}
}

type alGeoFlags struct{ n int64 }

func (a alGeoFlags) CountFlagged(context.Context, time.Time) (int64, error) { return a.n, nil }

type alServiceHolds struct{ n int64 }

func (a alServiceHolds) CountByServiceDisabledReason(_ context.Context, r domain.AutoDisabledReason) (int64, error) {
	if r == domain.DisabledGeoAutoSuspend {
		return a.n, nil
	}
	return 0, nil
}

// The feed route is staff-visible; the Geo tab both entries lead to is not,
// because it names people on a signal rather than proof. An operator must
// get neither entry nor a badge count that includes them — the admin, on the
// same service, gets both.
func TestAdminAlerts_OperatorNeverSeesGeoAlerts(t *testing.T) {
	svc := alert.New(alert.Deps{
		Nodes:        alNodes{n: []*domain.Node{{ID: 1, DisplayName: "n1", Enabled: true, HealthState: domain.NodeHealthUnreachable}}},
		GeoFlags:     alGeoFlags{n: 2},
		ServiceHolds: alServiceHolds{n: 1},
	})
	h := NewAdminAlertsHandler(svc)

	c, rr := claimsCtx(domain.RoleAdmin)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/alerts", nil)
	h.List(c)
	admin := rr.Body.String()
	if !strings.Contains(admin, `"geo_anomaly"`) || !strings.Contains(admin, `"geo_auto_suspended"`) {
		t.Fatalf("admin must see both geo entries: %s", admin)
	}

	c, rr = claimsCtx(domain.RoleOperator)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/alerts", nil)
	h.List(c)
	op := rr.Body.String()
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rr.Code)
	}
	if !strings.Contains(op, `"node_health"`) {
		t.Fatalf("operator must still see node_health: %s", op)
	}
	if strings.Contains(op, `"geo_anomaly"`) || strings.Contains(op, `"geo_auto_suspended"`) {
		t.Fatalf("operator must NOT see the geo entries (the Geo tab is admin-only): %s", op)
	}
	if !strings.Contains(op, `"warning":0`) {
		t.Fatalf("counts must be recomputed without the geo warnings: %s", op)
	}
}
