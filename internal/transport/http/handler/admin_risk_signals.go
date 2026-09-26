package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// RiskSignalLister is the read side of the observe-only risk signals: every
// stored (user, kind) row whose account still exists, with the account's
// names joined in.
type RiskSignalLister interface {
	List(ctx context.Context) ([]domain.RiskSignal, error)
}

// AdminRiskSignalHandler lists the risk signals, one item per account.
//
// adminGroup, beside the Geo tab and for its reason: the list names people
// on signals rather than proof, and what to do about that is the owner's
// call. Nothing here acts on an account — the signals are shown, never
// enforced, and this handler is read-only.
type AdminRiskSignalHandler struct {
	signals RiskSignalLister
	// geo is optional: without it every item's geo is null and the signals
	// are served all the same.
	geo GeoRecordLister
}

func NewAdminRiskSignalHandler(signals RiskSignalLister, geo GeoRecordLister) *AdminRiskSignalHandler {
	return &AdminRiskSignalHandler{signals: signals, geo: geo}
}

// riskUserRow is one account: its risk signals and, beside them, the
// concurrent-location verdict, so an admin reads one account in one row
// instead of cross-referencing two tabs by id.
type riskUserRow struct {
	UserID  int64  `json:"user_id"`
	UPN     string `json:"upn,omitempty"`
	Display string `json:"display_name,omitempty"`
	// Geo is null when the detector has no row for the account, or when no
	// geo store is wired. Never null because a read failed: that is an error.
	Geo *riskGeoSummary `json:"geo"`
	// Signals are in domain.RiskKinds order — the table's column order —
	// and hold only the kinds with a row. A missing kind is one the worker
	// has not computed for this account yet, which the client draws as
	// such rather than as clean.
	Signals []riskSignalDTO `json:"signals"`
}

// riskGeoSummary is the concurrent-location verdict reduced to what the risk
// table shows; the Geo tab has the rest. Flagged is the LATCH, apart from
// State for the reason geoAnomalyRow keeps it apart: a flagged account that
// went idle reads state idle and is still flagged.
type riskGeoSummary struct {
	State       string `json:"state"`
	Flagged     bool   `json:"flagged"`
	Tier        string `json:"tier"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

// riskSignalDTO is one signal as stored: a state, the code the client
// localizes, and the evidence verbatim. The store refuses evidence that is
// not valid JSON, which is what makes serving it raw safe; NULL evidence
// (idle, disabled, exempt) is JSON null.
type riskSignalDTO struct {
	Kind        string          `json:"kind"`
	State       string          `json:"state"`
	Code        string          `json:"code"`
	Evidence    json.RawMessage `json:"evidence"`
	UpdatedAtMS int64           `json:"updated_at_ms"`
}

// List returns every account with at least one signal row, by user_id.
//
// Every state is returned, not only flagged and suspect. Unknown, idle,
// disabled and exempt all look like "nothing wrong" if the server filters,
// and a fleet whose HWID capture or geo database quietly stopped working
// would then read as a fleet with nobody to review. The attention filter is
// the client's, on a switch the admin can turn off, precisely because the
// denominator has to stay reachable.
func (h *AdminRiskSignalHandler) List(c *gin.Context) {
	if h.signals == nil {
		// Not an empty list: "nothing to report" and "this build cannot
		// report" must not both answer 200 with [].
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "risk signals are not wired in this deployment",
		})
		return
	}
	ctx := c.Request.Context()
	signals, err := h.signals.List(ctx)
	if err != nil {
		// respondError, not err.Error(), for the reason the Geo tab's
		// handler gives: GORM internals must not reach a browser.
		respondError(c, err)
		return
	}
	var geo map[int64]*riskGeoSummary
	if h.geo != nil {
		recs, err := h.geo.List(ctx)
		if err != nil {
			// An error, not every geo null: null says the detector has
			// nothing on the account, and here nobody could look.
			respondError(c, err)
			return
		}
		geo = make(map[int64]*riskGeoSummary, len(recs))
		for _, r := range recs {
			geo[r.UserID] = &riskGeoSummary{
				State:       string(r.State),
				Flagged:     r.Streak.Flagged,
				Tier:        string(r.Streak.Tier),
				UpdatedAtMS: r.UpdatedAtMS,
			}
		}
	}

	// The display position of each kind this build computes. A kind outside
	// it is a row a newer build wrote before a downgrade: nothing here
	// rewrites it, so its state and time are frozen, and served beside live
	// verdicts it would read as one — and hold the row's oldest "last
	// computed" time back for good. It is left out, and an account whose
	// only rows are such kinds is not an item.
	rank := make(map[domain.RiskKind]int, len(domain.RiskKinds()))
	for i, k := range domain.RiskKinds() {
		rank[k] = i
	}
	byUser := map[int64]*riskUserRow{}
	for _, s := range signals {
		if _, known := rank[s.Kind]; !known {
			continue
		}
		row := byUser[s.UserID]
		if row == nil {
			row = &riskUserRow{UserID: s.UserID, UPN: s.UPN, Display: s.DisplayName, Geo: geo[s.UserID]}
			byUser[s.UserID] = row
		}
		evidence := s.Evidence
		if len(evidence) == 0 {
			// Nil marshals as null; an empty non-nil value would make the
			// whole response fail to encode.
			evidence = nil
		}
		row.Signals = append(row.Signals, riskSignalDTO{
			Kind:        string(s.Kind),
			State:       string(s.State),
			Code:        string(s.Code),
			Evidence:    evidence,
			UpdatedAtMS: s.UpdatedAtMS,
		})
	}

	// Ordered here rather than trusted from the store: the store orders
	// kinds alphabetically, which is not the display order, and the
	// response's order is this handler's contract either way.
	rows := make([]riskUserRow, 0, len(byUser))
	for _, row := range byUser {
		sort.SliceStable(row.Signals, func(i, j int) bool {
			return rank[domain.RiskKind(row.Signals[i].Kind)] < rank[domain.RiskKind(row.Signals[j].Kind)]
		})
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UserID < rows[j].UserID })
	c.JSON(http.StatusOK, gin.H{"items": rows})
}
