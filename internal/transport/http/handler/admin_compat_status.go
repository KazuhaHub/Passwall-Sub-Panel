package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// CompatStatus reports what the panel's compatibility decisions are currently
// based on, and how fresh that is.
//
// WHY AN OPERATOR NEEDS THIS. Every refusal in this area has the same shape from
// outside — "this upgrade is not offered" — and the reasons behind it are
// different and live in different places: a tested range fetched from the
// network, a compiled floor, a policy document with its own revision and expiry.
// Without a way to see them, the only available diagnosis is to guess, and the
// guess is usually "the panel is broken" rather than "the range is two versions
// old".
//
// READ-ONLY AND DELIBERATELY BORING. It reports state; it changes none. A stale or
// missing value is reported AS stale or missing rather than smoothed over — the
// point of the endpoint is to make the difference visible.
type compatStatusResponse struct {
	XUI compatRangeStatus `json:"xui"`
	SUI compatRangeStatus `json:"sui"`
}

type compatRangeStatus struct {
	MinVersion  string     `json:"min_version,omitempty"`
	MaxTested   string     `json:"max_tested,omitempty"`
	RefreshedAt *time.Time `json:"refreshed_at,omitempty"`
	// LastError is the most recent fetch failure, if any. Present alongside a
	// usable range it means "this range is the last good one", which is exactly
	// the state an operator cannot otherwise distinguish from a fresh fetch.
	LastError string `json:"last_error,omitempty"`
}

// CompatStatus is the read-only view. Registered on the staff group: reading the
// panel's own compatibility state is diagnosis, not a break-glass action.
func (h *AdminServersHandler) CompatStatus(c *gin.Context) {
	c.JSON(http.StatusOK, buildCompatStatus(version.LastRefreshAt(), version.LastRefreshError()))
}

// buildCompatStatus is pure so the shapes — including the awkward one, a range
// that could not be refreshed and is being served from the last good fetch — can
// be tested without a running panel.
func buildCompatStatus(refreshedAt time.Time, refreshErr error) compatStatusResponse {
	response := compatStatusResponse{
		XUI: compatRangeStatus{MinVersion: version.ActiveMinXUI(), MaxTested: version.ActiveMaxTestedXUI()},
		SUI: compatRangeStatus{MaxTested: version.ActiveMaxTestedSUI()},
	}
	if !refreshedAt.IsZero() {
		at := refreshedAt.UTC()
		response.XUI.RefreshedAt = &at
	}
	if refreshErr != nil {
		response.XUI.LastError = refreshErr.Error()
	}
	return response
}
