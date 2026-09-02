package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// GeoRecordLister is the read side of the concurrent-location detector.
type GeoRecordLister interface {
	List(ctx context.Context) ([]domain.GeoRecord, error)
}

// AdminGeoAnomalyHandler lists what the detector currently believes.
//
// adminGroup, not staffGroup. The list names people the panel suspects of
// sharing an account, on evidence that is a signal rather than proof, and
// deciding what to do with that is the owner's call — the same reasoning that
// keeps the diagnostics snapshot off the operator role.
type AdminGeoAnomalyHandler struct {
	records GeoRecordLister
	users   ports.UserRepo
}

func NewAdminGeoAnomalyHandler(records GeoRecordLister, users ports.UserRepo) *AdminGeoAnomalyHandler {
	return &AdminGeoAnomalyHandler{records: records, users: users}
}

// geoAnomalyRow is one user's current state, with enough context to act on it.
//
// The reason travels with the state on purpose. "Flagged" alone is not a basis
// for touching somebody's account, and an operator who cannot see "in 2 places
// at once ([DE JP]); tolerance is 1, sustained for 3 of 3 checks" has to either
// trust the panel blindly or ignore it — both of which make the feature
// useless.
type geoAnomalyRow struct {
	UserID  int64  `json:"user_id"`
	UPN     string `json:"upn,omitempty"`
	Display string `json:"display_name,omitempty"`
	State   string `json:"state"`
	Reason  string `json:"reason"`
	// Places is the concurrent locations behind the verdict, at whatever
	// granularity the effective policy uses.
	Places  []string `json:"places"`
	LiveIPs int      `json:"live_ips"`
	// Complete=false means LiveIPs is a FLOOR: a panel holding this user's
	// clients could not be read. Rendered, never silently dropped — a partial
	// count shown as a total reads as "this account is fine" exactly when the
	// evidence is missing.
	Complete    bool  `json:"complete"`
	OverStreak  int   `json:"over_streak"`
	UnderStreak int   `json:"under_streak"`
	UpdatedAtMS int64 `json:"updated_at_ms"`
}

// List returns every judged user, newest first.
//
// Every state is returned, not only the flagged ones. "unknown", "exempt" and
// "disabled" all look identical to "no flags" if a reader filters to flagged
// on the server, and a fleet whose geo database has quietly stopped working
// would then look like a fleet with nobody sharing. Filtering is the client's
// job precisely because the denominator has to stay reachable.
func (h *AdminGeoAnomalyHandler) List(c *gin.Context) {
	if h.records == nil {
		// Not an empty list: a caller cannot distinguish "nothing to report"
		// from "this build cannot report" if both answer 200 with [].
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "concurrent-location detection is not wired in this deployment",
		})
		return
	}
	recs, err := h.records.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows := make([]geoAnomalyRow, 0, len(recs))
	for _, r := range recs {
		row := geoAnomalyRow{
			UserID:      r.UserID,
			State:       string(r.State),
			Reason:      r.Reason,
			Places:      r.Places,
			LiveIPs:     r.LiveIPs,
			Complete:    r.Complete,
			OverStreak:  r.Streak.Over,
			UnderStreak: r.Streak.Under,
			UpdatedAtMS: r.UpdatedAtMS,
		}
		if row.Places == nil {
			// So the client renders an empty list rather than null.
			row.Places = []string{}
		}
		// Names are a convenience: a numeric id is not something an operator
		// can act on. A user that has since been deleted keeps its row with no
		// name rather than disappearing — dropping it would silently shrink a
		// list somebody is auditing.
		if h.users != nil {
			if u, uerr := h.users.GetByID(c.Request.Context(), r.UserID); uerr == nil && u != nil {
				row.UPN, row.Display = u.UPN, u.DisplayName
			}
		}
		rows = append(rows, row)
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}
