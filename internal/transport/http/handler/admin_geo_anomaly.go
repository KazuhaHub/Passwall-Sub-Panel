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
	// Tier is the tier the flag (or the last over-sample) was raised at —
	// country, region or city — and "" when nothing was over. Kept while the
	// flag is latched, so an idle flagged row can still say what raised it.
	Tier string `json:"tier"`
	// Flagged is the LATCH, separate from State. An idle or unreadable sample
	// freezes the streak, so a flagged account that disconnected reads
	// state "idle" and is still flagged; without this field that row looks
	// cleared.
	Flagged bool `json:"flagged"`
	// Places is the COUNTRIES behind the verdict, co-travel folded. Regions
	// and cities are in Evidence. A row an older build wrote may still show
	// that build's places until the user's next judgement.
	Places []string `json:"places"`
	// LiveIPs is every address the upstream still remembered for this user —
	// its whole retention window, not what was judged (ConcurrentIPs).
	LiveIPs int `json:"live_ips"`
	// ConcurrentIPs are the sources judged (live at poll time and not set
	// aside); ExcludedIPs the live sources set aside (shared exits, the
	// ignore list, infrastructure, internal ranges). Next to LiveIPs they
	// show how much of the window was memory and how much was deliberately
	// not counted.
	ConcurrentIPs int `json:"concurrent_ips"`
	ExcludedIPs   int `json:"excluded_ips"`
	// Complete=false means LiveIPs is a FLOOR: a panel holding this user's
	// clients could not be read. Rendered, never silently dropped — a partial
	// count shown as a total reads as "this account is fine" exactly when the
	// evidence is missing.
	Complete    bool `json:"complete"`
	OverStreak  int  `json:"over_streak"`
	UnderStreak int  `json:"under_streak"`
	// BanStreak is consecutive judged polls over the SUSPENSION tolerances:
	// how far into the sustain window an account is, in a group that armed
	// automatic suspension (always 0 where it is off). It resets when a
	// suspension fires, because the ban consumes the streak.
	BanStreak int `json:"ban_streak"`
	// Evidence is the structured account of the verdict — spots, exclusions,
	// coverage, spread; never an address. v 0 is a row an older build wrote.
	Evidence    domain.GeoEvidence `json:"evidence"`
	UpdatedAtMS int64              `json:"updated_at_ms"`
	// ServiceDisabledReason / ServiceDisabledAtMS are the account's current
	// service hold, read from the same user lookup as the name. geo_auto
	// means the detector already suspended it (and since when, for the time
	// box); anything else is somebody else's hold, shown so the admin does
	// not act on an account that is already paused. Omitted when active.
	ServiceDisabledReason string `json:"service_disabled_reason,omitempty"`
	ServiceDisabledAtMS   int64  `json:"service_disabled_at_ms,omitempty"`
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
		// respondError, not err.Error(): the helper exists precisely to stop
		// GORM internals — driver, table and constraint names — reaching a
		// browser. Admin-only reach makes the impact small, but a handler that
		// opts out of the shared mapping is how that helper stops being true
		// of the codebase.
		respondError(c, err)
		return
	}

	rows := make([]geoAnomalyRow, 0, len(recs))
	for _, r := range recs {
		row := geoAnomalyRow{
			UserID:        r.UserID,
			State:         string(r.State),
			Reason:        r.Reason,
			Tier:          string(r.Streak.Tier),
			Flagged:       r.Streak.Flagged,
			Places:        r.Places,
			LiveIPs:       r.LiveIPs,
			ConcurrentIPs: r.Concurrent,
			ExcludedIPs:   r.Excluded,
			Complete:      r.Complete,
			OverStreak:    r.Streak.Over,
			UnderStreak:   r.Streak.Under,
			BanStreak:     r.Streak.BanOver,
			Evidence:      r.Evidence,
			UpdatedAtMS:   r.UpdatedAtMS,
		}
		if row.Places == nil {
			// So the client renders an empty list rather than null.
			row.Places = []string{}
		}
		if row.Evidence.Spots == nil {
			// Same, for a row an older build wrote (evidence NULL, v 0): the
			// client reads spots.length on every row, and v — not a null —
			// is how it tells "not recorded" from "nothing found".
			row.Evidence.Spots = []domain.GeoSpot{}
		}
		// Names are a convenience: a numeric id is not something an operator
		// can act on. A user that has since been deleted keeps its row with no
		// name rather than disappearing — dropping it would silently shrink a
		// list somebody is auditing.
		if h.users != nil {
			if u, uerr := h.users.GetByID(c.Request.Context(), r.UserID); uerr == nil && u != nil {
				row.UPN, row.Display = u.UPN, u.DisplayName
				row.ServiceDisabledReason = string(u.ServiceDisabledReason)
				if u.ServiceDisabledAt != nil {
					row.ServiceDisabledAtMS = u.ServiceDisabledAt.UnixMilli()
				}
			}
		}
		rows = append(rows, row)
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}
