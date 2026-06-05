package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/alert"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// AdminAlertsHandler serves the unified notification feed the admin top-bar
// bell consumes. The alerts are derived live from current state by
// alert.Service — no events table, so a cleared condition just stops appearing.
type AdminAlertsHandler struct {
	alerts *alert.Service
}

func NewAdminAlertsHandler(alerts *alert.Service) *AdminAlertsHandler {
	return &AdminAlertsHandler{alerts: alerts}
}

// List returns {alerts, counts}. counts is the per-severity tally the bell
// badge renders (badge number = error+warning+info, colour = highest present).
func (h *AdminAlertsHandler) List(c *gin.Context) {
	items, counts := h.alerts.List(c.Request.Context())

	// This route is staff-visible (admin + operator), but cert / panel-upgrade
	// alerts deep-link to admin-only pages. Hide them from operators so the bell
	// never offers a link to a 403 page; recompute counts so the badge matches.
	if claims := middleware.ClaimsFrom(c); claims == nil || claims.Role != domain.RoleAdmin {
		filtered := make([]alert.Alert, 0, len(items))
		for _, a := range items {
			if a.Type.AdminOnly() {
				continue
			}
			filtered = append(filtered, a)
		}
		items = filtered
		counts = alert.Tally(items)
	}

	if items == nil {
		items = []alert.Alert{} // emit [] not null so the SPA can map() safely
	}
	c.JSON(http.StatusOK, gin.H{"alerts": items, "counts": counts})
}
