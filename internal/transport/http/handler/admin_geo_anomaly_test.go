package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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

// geoUsersStub is a users repository that answers GetByID only — the one
// read the list handler makes per row.
type geoUsersStub struct {
	ports.UserRepo
	byID map[int64]*domain.User
}

func (s geoUsersStub) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if u, ok := s.byID[id]; ok {
		return u, nil
	}
	return nil, domain.ErrNotFound
}

// Everything the v2 detector knows about a verdict reaches the admin: the
// tier that raised it, the latch, how far along the suspension streak is,
// how many sources were judged against how many were set aside, and the
// structured evidence. A verdict without those is a label the admin can only
// trust blindly.
func TestGeoAnomalyList_CarriesTierEvidenceCounts(t *testing.T) {
	evidence := domain.GeoEvidence{
		V: domain.GeoEvidenceVersion,
		Spots: []domain.GeoSpot{
			{CC: "CN", Region: "Guangdong", City: "Shenzhen", N: 2},
			{CC: "CN", Region: "Hunan", City: "Changsha", N: 1},
		},
		Excluded: domain.GeoExcluded{Shared: 1, Infra: 2},
		Stale:    21,
		Coverage: domain.GeoCoverage{Placed: 3, RegionKnown: 3, CityKnown: 3},
		Networks: 3,
		Spread:   domain.GeoSpread{Countries: 1, Regions: 2, RegionCountry: "CN", Cities: 2, CityCountry: "CN"},
	}
	h := NewAdminGeoAnomalyHandler(&stubRecords{recs: []domain.GeoRecord{{
		UserID:     7,
		State:      domain.GeoStateFlagged,
		Places:     []string{"CN"},
		LiveIPs:    27,
		Concurrent: 3,
		Excluded:   3,
		Evidence:   evidence,
		Complete:   true,
		Streak:     domain.GeoStreak{Over: 4, Flagged: true, Tier: domain.GeoTierRegion, BanOver: 2},
	}}}, nil)
	r := decodeRows(t, getJSON(t, h.List))[0]

	if r["tier"] != "region" || r["flagged"] != true {
		t.Fatalf("tier/flagged = %v/%v, want region/true", r["tier"], r["flagged"])
	}
	if r["ban_streak"] != float64(2) {
		t.Fatalf("ban_streak = %v, want 2", r["ban_streak"])
	}
	if r["concurrent_ips"] != float64(3) || r["excluded_ips"] != float64(3) || r["live_ips"] != float64(27) {
		t.Fatalf("concurrent/excluded/live = %v/%v/%v, want 3/3/27", r["concurrent_ips"], r["excluded_ips"], r["live_ips"])
	}
	ev, ok := r["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("evidence = %v, want an object", r["evidence"])
	}
	if ev["v"] != float64(1) || ev["stale"] != float64(21) || ev["networks"] != float64(3) {
		t.Fatalf("evidence header = %v, want v 1, stale 21, networks 3", ev)
	}
	spots, ok := ev["spots"].([]any)
	if !ok || len(spots) != 2 {
		t.Fatalf("evidence.spots = %v, want both spots", ev["spots"])
	}
	first, _ := spots[0].(map[string]any)
	if first["cc"] != "CN" || first["region"] != "Guangdong" || first["city"] != "Shenzhen" || first["n"] != float64(2) {
		t.Fatalf("first spot = %v, want CN/Guangdong/Shenzhen n 2", first)
	}
	excluded, _ := ev["excluded"].(map[string]any)
	if excluded["shared"] != float64(1) || excluded["infra"] != float64(2) {
		t.Fatalf("evidence.excluded = %v, want shared 1, infra 2", ev["excluded"])
	}
	spread, _ := ev["spread"].(map[string]any)
	if spread["regions"] != float64(2) || spread["region_country"] != "CN" {
		t.Fatalf("evidence.spread = %v, want 2 regions of CN", ev["spread"])
	}
}

// A row an older build wrote has no evidence. It must still serialise as an
// object with spots [] — a client that reads spots.length on null throws,
// and one that treats null as "no data" cannot tell legacy from empty
// (v 0 is how it tells).
func TestGeoAnomalyList_NoSpotsIsEmptyNotNull(t *testing.T) {
	h := NewAdminGeoAnomalyHandler(&stubRecords{recs: []domain.GeoRecord{
		{UserID: 7, State: domain.GeoStateClean},
	}}, nil)
	r := decodeRows(t, getJSON(t, h.List))[0]
	ev, ok := r["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("evidence = %v, want an object even for a legacy row", r["evidence"])
	}
	if ev["v"] != float64(0) {
		t.Fatalf("evidence.v = %v, want 0 for a row with no evidence", ev["v"])
	}
	if spots, ok := ev["spots"].([]any); !ok || len(spots) != 0 {
		t.Fatalf("evidence.spots = %#v, want [] rather than null", ev["spots"])
	}
	if r["tier"] != "" {
		t.Fatalf("tier = %v, want \"\" when nothing was over", r["tier"])
	}
}

// Idle freezes the streak, so a flagged account that disconnected is still
// flagged — and the row must say so. State alone reads "idle", which on its
// own looks like a cleared account; the latch is what tells the admin the
// flag is still standing.
func TestGeoAnomalyList_CarriesTheLatchForAnIdleUser(t *testing.T) {
	h := NewAdminGeoAnomalyHandler(&stubRecords{recs: []domain.GeoRecord{
		{UserID: 7, State: domain.GeoStateIdle, Streak: domain.GeoStreak{Over: 3, Flagged: true, Tier: domain.GeoTierCity}},
		{UserID: 8, State: domain.GeoStateIdle},
	}}, nil)
	rows := decodeRows(t, getJSON(t, h.List))
	if rows[0]["state"] != "idle" || rows[0]["flagged"] != true || rows[0]["tier"] != "city" {
		t.Fatalf("latched idle row = state %v flagged %v tier %v, want idle/true/city",
			rows[0]["state"], rows[0]["flagged"], rows[0]["tier"])
	}
	if rows[1]["flagged"] != false {
		t.Fatalf("unflagged idle row flagged = %v, want false", rows[1]["flagged"])
	}
}

// An account the detector suspended is marked as such on its row, with when
// — the admin reviewing the Geo tab must see that the automation already
// acted, and for how long, before deciding whether to resume it.
func TestGeoAnomalyList_CarriesTheAutoSuspension(t *testing.T) {
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	users := geoUsersStub{byID: map[int64]*domain.User{
		7: {ID: 7, UPN: "held@example.test", ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at},
		8: {ID: 8, UPN: "free@example.test"},
	}}
	h := NewAdminGeoAnomalyHandler(&stubRecords{recs: []domain.GeoRecord{
		{UserID: 7, State: domain.GeoStateIdle, Streak: domain.GeoStreak{Flagged: true}},
		{UserID: 8, State: domain.GeoStateClean},
	}}, users)
	rows := decodeRows(t, getJSON(t, h.List))
	if rows[0]["upn"] != "held@example.test" {
		t.Fatalf("upn = %v, want the looked-up name", rows[0]["upn"])
	}
	if rows[0]["service_disabled_reason"] != "geo_auto" {
		t.Fatalf("service_disabled_reason = %v, want geo_auto", rows[0]["service_disabled_reason"])
	}
	if rows[0]["service_disabled_at_ms"] != float64(at.UnixMilli()) {
		t.Fatalf("service_disabled_at_ms = %v, want %d", rows[0]["service_disabled_at_ms"], at.UnixMilli())
	}
	if _, present := rows[1]["service_disabled_reason"]; present {
		t.Fatalf("an unheld user carries service_disabled_reason %v; it must be omitted", rows[1]["service_disabled_reason"])
	}
	if _, present := rows[1]["service_disabled_at_ms"]; present {
		t.Fatalf("an unheld user carries service_disabled_at_ms %v; it must be omitted", rows[1]["service_disabled_at_ms"])
	}
}
