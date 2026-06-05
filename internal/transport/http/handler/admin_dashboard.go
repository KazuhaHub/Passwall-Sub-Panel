package handler

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// AdminDashboardHandler serves the aggregate summary the admin landing
// page needs. Pre-fix the SPA fetched listUsers({page_size:500}) +
// listNodes({page_size:500}) + listGroups() purely to compute four
// counter tiles + a five-row "expiring soon" list + a five-row "node
// alerts" list — every admin page-load downloaded the entire user and
// node lists. This endpoint aggregates server-side and returns just the
// small lists the page actually renders.
type AdminDashboardHandler struct {
	users  ports.UserRepo
	nodes  ports.NodeRepo
	groups ports.GroupRepo
	panels ports.XUIPanelRepo
	certs  ports.CertificateRepo
}

func NewAdminDashboardHandler(users ports.UserRepo, nodes ports.NodeRepo, groups ports.GroupRepo, panels ports.XUIPanelRepo, certs ports.CertificateRepo) *AdminDashboardHandler {
	return &AdminDashboardHandler{users: users, nodes: nodes, groups: groups, panels: panels, certs: certs}
}

// expiringWindowDays mirrors the frontend's EXPIRING_WINDOW_DAYS so the
// rendered list and the server-computed list always agree on which
// users are surfaced. The UI card title ("即将到期(7 天内)") hard-codes
// "7 days", so this MUST match. v3.6.1-beta.6 shipped with 14 here
// while the UI said 7 — a user 10 days out would appear under a card
// promising 7. Both ends now agree on 7.
const expiringWindowDays = 7

// dashboardExpiringRow is the minimum shape the dashboard's "expiring
// soon" list needs. Trimmed from the full user DTO to keep the response
// small — the dashboard never renders sub_url / lifetime counters /
// emergency state / sso bindings.
type dashboardExpiringRow struct {
	ID          int64  `json:"id"`
	UPN         string `json:"upn"`
	DisplayName string `json:"display_name,omitempty"`
	// Always emitted — handler filters out users with nil ExpireAt
	// before appending. Frontend types it non-optional.
	ExpireAt *time.Time `json:"expire_at"`
}

// dashboardNodeAlert is the per-row shape for the "node health alerts"
// list. Trimmed for the same reason as dashboardExpiringRow.
type dashboardNodeAlert struct {
	ID          int64                  `json:"id"`
	DisplayName string                 `json:"display_name"`
	PanelName   string                 `json:"panel_name"`
	HealthState domain.NodeHealthState `json:"health_state"`
}

// dashboardCertAlert surfaces a PSP-managed certificate whose last issuance or
// renewal failed, so the admin landing page shows cert problems alongside node
// health alerts.
type dashboardCertAlert struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
}

type dashboardSummaryResponse struct {
	UserTotal     int                    `json:"user_total"`
	UserEnabled   int                    `json:"user_enabled"`
	UserDisabled  int                    `json:"user_disabled"`
	UserEmergency int                    `json:"user_emergency"`
	NodeTotal     int                    `json:"node_total"`
	NodeEnabled   int                    `json:"node_enabled"`
	NodeHealthy   int                    `json:"node_healthy"`
	GroupCount    int                    `json:"group_count"`
	ExpiringUsers []dashboardExpiringRow `json:"expiring_users"`
	NodeAlerts    []dashboardNodeAlert   `json:"node_alerts"`
	CertAlerts    []dashboardCertAlert   `json:"cert_alerts"`
}

// Summary returns the aggregate values the admin dashboard renders.
func (h *AdminDashboardHandler) Summary(c *gin.Context) {
	ctx := c.Request.Context()

	// Summary counters come from COUNT queries, and the "expiring soon" list from
	// one bounded ExpiringBetween query — instead of paging the ENTIRE users table
	// into memory just to tally four numbers and pick five rows.
	now := time.Now()
	counts, err := h.users.CountByStatus(ctx, now)
	if err != nil {
		respondError(c, err)
		return
	}
	nodes, err := h.nodes.List(ctx)
	if err != nil {
		respondError(c, err)
		return
	}
	groups, err := h.groups.List(ctx)
	if err != nil {
		respondError(c, err)
		return
	}

	resp := dashboardSummaryResponse{
		UserTotal:     int(counts.Total),
		UserEnabled:   int(counts.Enabled),
		UserDisabled:  int(counts.Disabled),
		UserEmergency: int(counts.Emergency),
		GroupCount:    len(groups),
	}
	windowEnd := now.Add(expiringWindowDays * 24 * time.Hour)
	expiring, err := h.users.ListExpiringBetween(ctx, now, windowEnd, 5)
	if err != nil {
		respondError(c, err)
		return
	}
	resp.ExpiringUsers = make([]dashboardExpiringRow, 0, len(expiring))
	for _, u := range expiring {
		resp.ExpiringUsers = append(resp.ExpiringUsers, dashboardExpiringRow{
			ID: u.ID, UPN: u.UPN, DisplayName: u.DisplayName, ExpireAt: u.ExpireAt,
		})
	}

	panelNames := h.loadPanelNames(ctx)
	alerts := make([]*domain.Node, 0)
	for _, n := range nodes {
		if n.IsSeparator() {
			continue
		}
		resp.NodeTotal++
		if !n.Enabled {
			continue
		}
		resp.NodeEnabled++
		if n.HealthState == domain.NodeHealthOK {
			resp.NodeHealthy++
		} else if n.HealthState != "" {
			alerts = append(alerts, n)
		}
	}
	// Sort BEFORE truncating to 5 — pre-fix the surfaced alerts were
	// just "first 5 in nodes.List order" (id ASC), so among 10
	// unhealthy nodes admin saw 5 essentially-random ones. Sort most-
	// recently-checked first so the dashboard surfaces the freshest
	// signal; nodes with no HealthCheckedAt fall to the end.
	sort.Slice(alerts, func(i, j int) bool {
		ai, bi := alerts[i].HealthCheckedAt, alerts[j].HealthCheckedAt
		if ai == nil && bi == nil {
			return alerts[i].ID < alerts[j].ID
		}
		if ai == nil {
			return false
		}
		if bi == nil {
			return true
		}
		return ai.After(*bi)
	})
	if len(alerts) > 5 {
		alerts = alerts[:5]
	}
	resp.NodeAlerts = make([]dashboardNodeAlert, 0, len(alerts))
	for _, n := range alerts {
		resp.NodeAlerts = append(resp.NodeAlerts, dashboardNodeAlert{
			ID: n.ID, DisplayName: n.DisplayName, PanelName: panelNames[n.PanelID], HealthState: n.HealthState,
		})
	}

	// PSP-managed certificates whose last issuance/renewal failed (v3.6.4) —
	// surfaced alongside node health alerts. The cert row itself is the alert
	// source (status=failed + last_error), so no separate alerts table.
	resp.CertAlerts = make([]dashboardCertAlert, 0)
	if h.certs != nil {
		if failed, ferr := h.certs.ListByStatus(ctx, domain.CertStatusFailed); ferr == nil {
			for _, cert := range failed {
				resp.CertAlerts = append(resp.CertAlerts, dashboardCertAlert{
					ID: cert.ID, Name: cert.Name, Status: string(cert.Status), LastError: cert.LastError,
				})
			}
		}
	}

	c.JSON(http.StatusOK, resp)
}

func (h *AdminDashboardHandler) loadPanelNames(ctx context.Context) map[int64]string {
	names := map[int64]string{}
	if h.panels == nil {
		return names
	}
	panels, err := h.panels.List(ctx)
	if err != nil {
		return names
	}
	for _, p := range panels {
		names[p.ID] = p.Name
	}
	return names
}
