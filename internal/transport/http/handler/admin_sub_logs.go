package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/geo"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// AdminSubLogHandler exposes /api/admin/sub-logs — paginated subscription
// log retrieval with optional user ID / time-range filters.
type AdminSubLogHandler struct {
	repo     ports.SubLogRepo
	settings ports.SettingsRepo
	geo      *geo.Service // nil-tolerant: nil/disabled → no region field
}

func NewAdminSubLogHandler(repo ports.SubLogRepo, settings ports.SettingsRepo, geoSvc *geo.Service) *AdminSubLogHandler {
	return &AdminSubLogHandler{repo: repo, settings: settings, geo: geoSvc}
}

// subLogView is a SubLog plus its resolved IP region (omitted when geo is
// disabled or the IP isn't resolved yet). The user-detail "last region" view
// reads this off /api/admin/sub-logs?user_id=X&page_size=1.
type subLogView struct {
	*domain.SubLog
	Region *domain.GeoLocation `json:"region,omitempty"`
	// The device the fetch declared, for ADMINS ONLY. This route is on the
	// staff group, so operators read it too, and domain.SubLog keeps its own
	// device fields off the wire (json:"-") for exactly that reason: they
	// reach JSON only through these two, which List fills after a role
	// check. Admins get the label and the first 4 hex characters of the
	// per-account digest — enough to tell one account's devices apart at a
	// glance, not a value worth copying anywhere.
	DeviceLabelForAdmin string `json:"device_label,omitempty"`
	DeviceIDPrefix      string `json:"device_id4,omitempty"`
}

// deviceIDPrefixLen is how much of the stored device digest an admin sees:
// domain.DeviceIDShownLen, the one length every admin view shows (the risk
// center's inferred devices show the same four characters).
const deviceIDPrefixLen = domain.DeviceIDShownLen

func (h *AdminSubLogHandler) List(c *gin.Context) {
	p := parsePagination(c)
	filter := ports.SubLogFilter{
		Pagination: p,
		Search:     firstNonEmpty(p.Keyword, c.Query("search")),
	}
	if v := c.Query("user_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.UserID = &id
		}
	}
	if v := c.Query("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.Since = &t
		}
	}
	if v := c.Query("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.Until = &t
		}
	}
	items, total, err := h.repo.List(c.Request.Context(), filter)
	if err != nil {
		respondError(c, err)
		return
	}
	ips := make([]string, 0, len(items))
	for _, it := range items {
		ips = append(ips, it.IP)
	}
	regions := map[string]domain.GeoLocation{}
	if h.geo != nil {
		regions = h.geo.Lookup(c.Request.Context(), ips)
	}
	// Nil claims (a route mounted without auth, a test harness) reads as
	// "not an admin": the device is shown on a positive admin check only.
	claims := middleware.ClaimsFrom(c)
	showDevice := claims != nil && claims.Role == domain.RoleAdmin
	views := make([]subLogView, len(items))
	for i, it := range items {
		views[i] = subLogView{SubLog: it}
		if loc, ok := regions[it.IP]; ok {
			locCopy := loc
			views[i].Region = &locCopy
		}
		if showDevice {
			views[i].DeviceLabelForAdmin = it.DeviceLabel
			if len(it.DeviceID) >= deviceIDPrefixLen {
				views[i].DeviceIDPrefix = it.DeviceID[:deviceIDPrefixLen]
			}
		}
	}
	c.JSON(http.StatusOK, pagedEnvelope(views, total, p))
}

func (h *AdminSubLogHandler) Clear(c *gin.Context) {
	if err := h.repo.Clear(c.Request.Context()); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminSubLogHandler) Purge(c *gin.Context) {
	s, err := h.settings.Load(c.Request.Context(), ports.UISettings{})
	if err != nil {
		respondError(c, err)
		return
	}
	if s.SubLogRetentionDays <= 0 {
		c.JSON(http.StatusOK, gin.H{"deleted": 0})
		return
	}
	cutoff := time.Now().AddDate(0, 0, -s.SubLogRetentionDays)
	deleted, err := h.repo.DeleteBefore(c.Request.Context(), cutoff)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}
