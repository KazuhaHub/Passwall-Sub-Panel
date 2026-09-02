package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type stubRecords struct {
	recs []domain.GeoRecord
	err  error
}

func (s *stubRecords) List(context.Context) ([]domain.GeoRecord, error) {
	return s.recs, s.err
}

func getJSON(t *testing.T, h func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	h(c)
	return rec
}

func decodeRows(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	return body.Items
}

// "Nothing to report" and "this build cannot report" must not look the same.
// Both answering 200 with an empty list would let a deployment that never
// wired the detector read as a fleet with nobody sharing.
func TestGeoAnomalyList_UnwiredIsNotAnEmptyList(t *testing.T) {
	rec := getJSON(t, NewAdminGeoAnomalyHandler(nil, nil).List)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 — an unwired detector must not answer with an empty list", rec.Code)
	}
}

func TestGeoAnomalyList_StoreErrorIsAnError(t *testing.T) {
	h := NewAdminGeoAnomalyHandler(&stubRecords{err: errors.New("db down")}, nil)
	if rec := getJSON(t, h.List); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// Every state is returned, not only the flagged ones.
//
// "unknown", "exempt" and "disabled" all look identical to "no flags" if the
// server filters, and a fleet whose geo database has quietly stopped working
// would then be indistinguishable from a clean one. The denominator has to
// stay reachable.
func TestGeoAnomalyList_ReturnsEveryStateNotOnlyFlagged(t *testing.T) {
	h := NewAdminGeoAnomalyHandler(&stubRecords{recs: []domain.GeoRecord{
		{UserID: 1, State: domain.GeoStateFlagged},
		{UserID: 2, State: domain.GeoStateUnknown},
		{UserID: 3, State: domain.GeoStateExempt},
		{UserID: 4, State: domain.GeoStateClean},
	}}, nil)
	rows := decodeRows(t, getJSON(t, h.List))
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want all 4 states", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r["state"].(string)] = true
	}
	for _, want := range []string{"flagged", "unknown", "exempt", "clean"} {
		if !seen[want] {
			t.Errorf("state %q was filtered out; the denominator must stay visible", want)
		}
	}
}

// The reason must reach the client. A flag with no reason is not a basis for
// acting on somebody's account — the operator can only trust it blindly or
// ignore it, and both make the feature useless.
func TestGeoAnomalyList_CarriesTheReasonAndEvidence(t *testing.T) {
	h := NewAdminGeoAnomalyHandler(&stubRecords{recs: []domain.GeoRecord{{
		UserID:   7,
		State:    domain.GeoStateFlagged,
		Reason:   "in 2 places at once ([DE JP]); tolerance is 1",
		Places:   []string{"DE", "JP"},
		LiveIPs:  4,
		Complete: true,
		Streak:   domain.GeoStreak{Over: 3, Flagged: true},
	}}}, nil)
	rows := decodeRows(t, getJSON(t, h.List))
	r := rows[0]
	if r["reason"] == "" {
		t.Fatal("the reason was dropped")
	}
	if got := r["places"].([]any); len(got) != 2 {
		t.Fatalf("places = %v, want both", got)
	}
	if r["live_ips"].(float64) != 4 || r["over_streak"].(float64) != 3 {
		t.Fatalf("evidence lost: %v", r)
	}
}

// A count that was only a floor must say so. This is the field a reader is
// most likely to skip, and skipping it turns a partial count into a clean
// bill of health.
func TestGeoAnomalyList_RendersThatACountWasAFloor(t *testing.T) {
	h := NewAdminGeoAnomalyHandler(&stubRecords{recs: []domain.GeoRecord{
		{UserID: 7, State: domain.GeoStateClean, LiveIPs: 1, Complete: false},
	}}, nil)
	rows := decodeRows(t, getJSON(t, h.List))
	if rows[0]["complete"].(bool) {
		t.Fatal("a floor was rendered as a total")
	}
}

// null vs [] matters to a client that renders a length.
func TestGeoAnomalyList_NoPlacesIsAnEmptyListNotNull(t *testing.T) {
	h := NewAdminGeoAnomalyHandler(&stubRecords{recs: []domain.GeoRecord{
		{UserID: 7, State: domain.GeoStateIdle},
	}}, nil)
	rows := decodeRows(t, getJSON(t, h.List))
	if rows[0]["places"] == nil {
		t.Fatal("places must serialise as [] so a client can read its length")
	}
}
