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
	claims := middleware.ClaimsFrom(c)
	admin := claims != nil && claims.Role == domain.RoleAdmin
	// The service is told who is asking so it can skip what only an admin
	// may see and is costly to compute (the risk queue's fleet-wide count).
	items, counts := h.alerts.List(c.Request.Context(), admin)

	// This route is staff-visible (admin + operator), but cert / panel-upgrade
	// alerts deep-link to admin-only pages. Hide them from operators so the bell
	// never offers a link to a 403 page; recompute counts so the badge matches.
	// Kept as the second line for the risk queue too: a category that forgets
	// to consult admin in the service still never reaches an operator.
	if !admin {
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
