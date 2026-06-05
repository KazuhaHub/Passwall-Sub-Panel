package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		Nodes:  alNodes{n: []*domain.Node{{ID: 1, DisplayName: "n1", Enabled: true, HealthState: domain.NodeHealthUnreachable}}},
		Panels: alPanels{p: []*domain.XUIPanel{{ID: 2, Name: "p2", PanelVersion: "3.2.6"}}},
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
