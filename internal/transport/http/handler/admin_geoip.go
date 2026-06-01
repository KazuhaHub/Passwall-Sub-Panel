package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/geo"
)

// AdminGeoIPHandler exposes /api/admin/settings/geoip/* — the offline geo
// database status (available .mmdb files + which is active) and a manual
// "update now" trigger.
type AdminGeoIPHandler struct {
	geo *geo.Service // nil-tolerant
}

func NewAdminGeoIPHandler(geoSvc *geo.Service) *AdminGeoIPHandler {
	return &AdminGeoIPHandler{geo: geoSvc}
}

// Status lists the .mmdb files present, their type/granularity/build date, and
// which is active — so the admin UI can render the source dropdown + status.
func (h *AdminGeoIPHandler) Status(c *gin.Context) {
	if h.geo == nil {
		c.JSON(http.StatusOK, geo.Status{Available: []geo.DBStatus{}})
		return
	}
	c.JSON(http.StatusOK, h.geo.Status(c.Request.Context()))
}

// Update kicks off an immediate download/refresh of the configured source's
// database (no user IPs involved — only a public DB is fetched) and returns
// right away. The download runs in the background because it can take minutes —
// doing it inline made a reverse proxy time out and answer 502 before the panel
// could reply. The client polls GET /settings/geoip/status for progress and the
// result (status.update.{updating,last_error,last_file}).
func (h *AdminGeoIPHandler) Update(c *gin.Context) {
	if h.geo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Geo service not available"})
		return
	}
	if err := h.geo.StartUpdate(); err != nil {
		// Only failure here is "already running" — a benign conflict.
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "started"})
}
