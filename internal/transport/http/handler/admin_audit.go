package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/geo"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// AdminAuditHandler exposes /api/admin/audit — paginated audit log
// retrieval with optional actor / action / time-range filters.
type AdminAuditHandler struct {
	repo ports.AuditRepo
	geo  *geo.Service // nil-tolerant: nil/disabled → no region field
}

func NewAdminAuditHandler(repo ports.AuditRepo, geoSvc *geo.Service) *AdminAuditHandler {
	return &AdminAuditHandler{repo: repo, geo: geoSvc}
}

// adminOnlyAuditTargets are the audit rows only an administrator reads: the
// risk center's writes. Its routes are all adminGroup, but AuditWrites keeps
// every write's route, params and body, and this read is staffGroup — so,
// unfiltered, an operator would read a dismissal's note (which the dialog
// promises is for admins only, and which may carry an address), the
// detector levels it accepted, and which accounts were dismissed or
// trusted. Matched as a prefix of the stored target, which is the route
// template (the raw path when no route matched), so a route added under it
// is covered without touching this list.
var adminOnlyAuditTargets = []string{"/api/admin/risk-center/"}

// auditView is an AuditEntry plus its resolved IP region (omitted when geo is
// disabled, the IP is private/unmapped, or no .mmdb is loaded — lookups are
// fully offline against the active local database, no cache).
type auditView struct {
	*domain.AuditEntry
	Region *domain.GeoLocation `json:"region,omitempty"`
}

func (h *AdminAuditHandler) List(c *gin.Context) {
	p := parsePagination(c)
	filter := ports.AuditFilter{
		Pagination: p,
		Actor:      c.Query("actor"),
		Action:     c.Query("action"),
		Search:     firstNonEmpty(p.Keyword, c.Query("search")),
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
	// Left out in the query, not after it, so the total and every page
	// agree. Nil claims (a route mounted without auth, a test harness)
	// reads as "not an admin": the rows are shown on a positive admin
	// check only.
	if claims := middleware.ClaimsFrom(c); claims == nil || claims.Role != domain.RoleAdmin {
		filter.ExcludeTargetPrefixes = adminOnlyAuditTargets
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
	views := make([]auditView, len(items))
	for i, it := range items {
		views[i] = auditView{AuditEntry: it}
		if loc, ok := regions[it.IP]; ok {
			locCopy := loc
			views[i].Region = &locCopy
		}
	}
	c.JSON(http.StatusOK, pagedEnvelope(views, total, p))
}

func (h *AdminAuditHandler) Clear(c *gin.Context) {
	if err := h.repo.Clear(c.Request.Context()); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
