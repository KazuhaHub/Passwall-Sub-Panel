package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type stubRiskSignals struct {
	rows  []domain.RiskSignal
	err   error
	calls int
}

func (s *stubRiskSignals) List(context.Context) ([]domain.RiskSignal, error) {
	s.calls++
	return s.rows, s.err
}

// riskItemJSON is one item as the SPA reads it. Geo and Evidence stay raw so
// a test can tell a JSON null from a missing key: the SPA reads both fields
// on every row, and "null" is how it knows there is nothing to show.
type riskItemJSON struct {
	UserID  int64           `json:"user_id"`
	UPN     string          `json:"upn"`
	Display string          `json:"display_name"`
	Geo     json.RawMessage `json:"geo"`
	Signals []struct {
		Kind        string          `json:"kind"`
		State       string          `json:"state"`
		Code        string          `json:"code"`
		Evidence    json.RawMessage `json:"evidence"`
		UpdatedAtMS int64           `json:"updated_at_ms"`
	} `json:"signals"`
}

func decodeRiskItems(t *testing.T, rec *httptest.ResponseRecorder) []riskItemJSON {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items *[]riskItemJSON `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if body.Items == nil {
		t.Fatalf("items is missing or null in %s; the SPA maps over it", rec.Body.String())
	}
	return *body.Items
}

func signalKinds(item riskItemJSON) []string {
	out := make([]string, 0, len(item.Signals))
	for _, s := range item.Signals {
		out = append(out, s.Kind)
	}
	return out
}

// One item per account, the account's signals in display order and its
// concurrent-location verdict beside them.
//
// The store returns rows by (user_id, kind), and kind order is alphabetical —
// devices, login_country, sub_spread, usage_shift — which is not the order
// the columns are drawn in; the handler owns the display order. The geo
// summary is joined in so an admin reads one account's signals in one row,
// and it keeps the LATCH apart from the state: a flagged account that went
// idle reads state idle and is still flagged. An account with a geo row but
// no risk row is not an item — this is the risk view, and the Geo tab lists
// it.
func TestRiskSignalList_GroupsByUserWithGeoSummary(t *testing.T) {
	signals := &stubRiskSignals{rows: []domain.RiskSignal{
		// Out of user order on purpose: the handler orders by user_id.
		{UserID: 9, Kind: domain.RiskKindLoginCountry, State: domain.GeoStateClean, Code: domain.RiskCodeKnownCountries,
			Evidence: json.RawMessage(`{"v":1,"known":["CN"]}`), UpdatedAtMS: 5000, UPN: "nine@example.test"},
		{UserID: 3, Kind: domain.RiskKindDevices, State: domain.GeoStateSuspect, Code: domain.RiskCodeOverBuilding,
			Evidence: json.RawMessage(`{"v":1,"distinct":4}`), UpdatedAtMS: 1000, UPN: "three@example.test", DisplayName: "Three"},
		{UserID: 3, Kind: domain.RiskKindSubSpread, State: domain.GeoStateFlagged, Code: domain.RiskCodeSpread,
			Evidence: json.RawMessage(`{"v":1,"groups":2}`), UpdatedAtMS: 2000, UPN: "three@example.test", DisplayName: "Three"},
		{UserID: 3, Kind: domain.RiskKindUsageShift, State: domain.GeoStateIdle, Code: domain.RiskCodeNoUsage,
			UpdatedAtMS: 3000, UPN: "three@example.test", DisplayName: "Three"},
	}}
	geo := &stubRecords{recs: []domain.GeoRecord{
		{UserID: 3, State: domain.GeoStateIdle, Streak: domain.GeoStreak{Flagged: true, Tier: domain.GeoTierRegion}, UpdatedAtMS: 777},
		{UserID: 5, State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Flagged: true}},
	}}
	items := decodeRiskItems(t, getJSON(t, NewAdminRiskSignalHandler(signals, geo).List))

	if len(items) != 2 || items[0].UserID != 3 || items[1].UserID != 9 {
		t.Fatalf("items = %+v, want users 3 then 9 — one item per account with a risk row, by user_id", items)
	}
	three := items[0]
	if three.UPN != "three@example.test" || three.Display != "Three" {
		t.Fatalf("user 3 names = %q / %q, want the joined UPN and display name", three.UPN, three.Display)
	}
	if got := strings.Join(signalKinds(three), ","); got != "sub_spread,devices,usage_shift" {
		t.Fatalf("user 3 signals = %s, want sub_spread,devices,usage_shift (RiskKinds order, only kinds with a row)", got)
	}
	spread := three.Signals[0]
	if spread.State != "flagged" || spread.Code != "spread" || spread.UpdatedAtMS != 2000 || string(spread.Evidence) != `{"v":1,"groups":2}` {
		t.Fatalf("sub_spread = %+v (evidence %s), want flagged/spread at 2000 with the stored evidence verbatim", spread, spread.Evidence)
	}
	if usage := three.Signals[2]; usage.State != "idle" || usage.Code != "no_usage" || string(usage.Evidence) != "null" {
		t.Fatalf("usage_shift = %+v (evidence %q), want idle/no_usage with evidence null", usage, usage.Evidence)
	}
	var g struct {
		State       string `json:"state"`
		Flagged     bool   `json:"flagged"`
		Tier        string `json:"tier"`
		UpdatedAtMS int64  `json:"updated_at_ms"`
	}
	if err := json.Unmarshal(three.Geo, &g); err != nil {
		t.Fatalf("user 3 geo %s: %v", three.Geo, err)
	}
	if g.State != "idle" || !g.Flagged || g.Tier != "region" || g.UpdatedAtMS != 777 {
		t.Fatalf("user 3 geo = %+v, want state idle, still flagged, tier region, updated 777", g)
	}

	nine := items[1]
	if string(nine.Geo) != "null" {
		t.Fatalf("user 9 geo = %s, want null: the detector has no row for this account", nine.Geo)
	}
	if got := strings.Join(signalKinds(nine), ","); got != "login_country" || string(nine.Signals[0].Evidence) != `{"v":1,"known":["CN"]}` {
		t.Fatalf("user 9 signals = %s (%s), want login_country with its evidence", got, nine.Signals[0].Evidence)
	}
}

// Every state is returned, not only flagged and suspect.
//
// Unknown, idle, disabled and exempt all look like "nothing wrong" if the
// server filters to what needs attention, and a fleet whose HWID capture or
// geo database has quietly stopped working would then look like a fleet
// with nobody to review. The attention filter is the client's, on a switch
// the admin can turn off.
func TestRiskSignalList_ReturnsEveryState(t *testing.T) {
	states := []domain.GeoState{
		domain.GeoStateDisabled, domain.GeoStateExempt, domain.GeoStateUnknown, domain.GeoStateIdle,
		domain.GeoStateClean, domain.GeoStateSuspect, domain.GeoStateFlagged,
	}
	rows := make([]domain.RiskSignal, 0, len(states))
	for i, st := range states {
		rows = append(rows, domain.RiskSignal{UserID: int64(i + 1), Kind: domain.RiskKindDevices, State: st, Code: domain.RiskCodeWithin})
	}
	items := decodeRiskItems(t, getJSON(t, NewAdminRiskSignalHandler(&stubRiskSignals{rows: rows}, nil).List))
	seen := map[string]bool{}
	for _, it := range items {
		for _, s := range it.Signals {
			seen[s.State] = true
		}
	}
	for _, st := range states {
		if !seen[string(st)] {
			t.Errorf("state %q was filtered out; the denominator must stay visible", st)
		}
	}
	if len(items) != len(states) {
		t.Fatalf("items = %d, want %d — one per account", len(items), len(states))
	}
}

// "Nothing to report" and "this build cannot report" must not look the same.
// A deployment that never wired the store answering 200 with an empty list
// would read as a fleet with no risk at all — while a wired store with no
// rows yet (the first hour after an upgrade) is exactly that empty list.
func TestRiskSignalList_503WhenUnwired(t *testing.T) {
	for name, h := range map[string]*AdminRiskSignalHandler{
		"no stores":        NewAdminRiskSignalHandler(nil, nil),
		"geo only is none": NewAdminRiskSignalHandler(nil, &stubRecords{}),
	} {
		rec := getJSON(t, h.List)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status %d, want 503 — an unwired store must not answer with an empty list", name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "risk signals are not wired in this deployment") {
			t.Fatalf("%s: body %s, want the not-wired error", name, rec.Body.String())
		}
	}
	if items := decodeRiskItems(t, getJSON(t, NewAdminRiskSignalHandler(&stubRiskSignals{}, nil).List)); len(items) != 0 {
		t.Fatalf("a wired store with no rows = %+v, want an empty list", items)
	}
}

// The geo summary is optional: without the concurrent-location store every
// account's geo is null, and the signals are served all the same — one
// missing source must not take the other down.
func TestRiskSignalList_GeoNullWithoutGeoRecords(t *testing.T) {
	signals := &stubRiskSignals{rows: []domain.RiskSignal{
		{UserID: 1, Kind: domain.RiskKindSubSpread, State: domain.GeoStateFlagged, Code: domain.RiskCodeSpread},
		{UserID: 2, Kind: domain.RiskKindDevices, State: domain.GeoStateUnknown, Code: domain.RiskCodeNoHWID},
	}}
	items := decodeRiskItems(t, getJSON(t, NewAdminRiskSignalHandler(signals, nil).List))
	if len(items) != 2 {
		t.Fatalf("items = %+v, want both accounts", items)
	}
	for _, it := range items {
		if string(it.Geo) != "null" {
			t.Fatalf("user %d geo = %q, want null when no geo store is wired", it.UserID, it.Geo)
		}
		if len(it.Signals) != 1 {
			t.Fatalf("user %d signals = %+v, want its one row", it.UserID, it.Signals)
		}
	}
}

// A read that fails is an error, not a list. A failed geo read in
// particular must not be served as "geo: null" — that says the detector has
// nothing on these accounts, when the truth is that nobody could look.
func TestRiskSignalList_ReadErrorsAreErrors(t *testing.T) {
	rows := []domain.RiskSignal{{UserID: 1, Kind: domain.RiskKindDevices, State: domain.GeoStateFlagged, Code: domain.RiskCodeOver}}
	if rec := getJSON(t, NewAdminRiskSignalHandler(&stubRiskSignals{err: errors.New("db down")}, nil).List); rec.Code != http.StatusInternalServerError {
		t.Fatalf("signals read error: status %d, want 500", rec.Code)
	}
	rec := getJSON(t, NewAdminRiskSignalHandler(&stubRiskSignals{rows: rows}, &stubRecords{err: errors.New("db down")}).List)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("geo read error: status %d (%s), want 500 rather than a list with every geo null", rec.Code, rec.Body.String())
	}
}

// A kind this build does not know is a row a newer build wrote before a
// downgrade. Nothing here rewrites it, so its state and time are frozen:
// served, it would sit beside live verdicts as if it were one, and hold the
// row's oldest "last computed" time back forever. Only the known kinds are
// returned — and an account whose only rows are unknown is not an item.
func TestRiskSignalList_OnlyKindsThisBuildKnows(t *testing.T) {
	signals := &stubRiskSignals{rows: []domain.RiskSignal{
		{UserID: 1, Kind: domain.RiskKindDevices, State: domain.GeoStateClean, Code: domain.RiskCodeWithin},
		{UserID: 1, Kind: domain.RiskKind("travel"), State: domain.GeoStateFlagged, Code: "far"},
		{UserID: 2, Kind: domain.RiskKind("travel"), State: domain.GeoStateFlagged, Code: "far"},
	}}
	items := decodeRiskItems(t, getJSON(t, NewAdminRiskSignalHandler(signals, nil).List))
	if len(items) != 1 || items[0].UserID != 1 {
		t.Fatalf("items = %+v, want only user 1", items)
	}
	if got := strings.Join(signalKinds(items[0]), ","); got != "devices" {
		t.Fatalf("user 1 signals = %s, want devices alone", got)
	}
}

// The single-account lookup asks for one account's signals (?user_id=): the
// answer is that account's item alone, with its geo summary, the shape the
// fleet list has. A malformed id is a 400, never the fleet.
func TestAdminRiskSignals_UserIDFilter(t *testing.T) {
	signals := &stubRiskSignals{rows: []domain.RiskSignal{
		{UserID: 1, Kind: domain.RiskKindDevices, State: domain.GeoStateClean, Code: domain.RiskCodeWithin},
		{UserID: 2, Kind: domain.RiskKindSubSpread, State: domain.GeoStateFlagged, Code: domain.RiskCodeSpread},
		{UserID: 2, Kind: domain.RiskKindDevices, State: domain.GeoStateSuspect, Code: domain.RiskCodeOver},
	}}
	geo := &stubRecords{recs: []domain.GeoRecord{{UserID: 1, State: domain.GeoStateClean}, {UserID: 2, State: domain.GeoStateFlagged, Streak: domain.GeoStreak{Flagged: true}}}}
	h := NewAdminRiskSignalHandler(signals, geo)

	items := decodeRiskItems(t, getJSONAt(t, h.List, "/api/admin/risk-signals?user_id=2"))
	if len(items) != 1 || items[0].UserID != 2 || len(items[0].Signals) != 2 || !strings.Contains(string(items[0].Geo), `"flagged":true`) {
		t.Fatalf("items = %+v, want account 2 alone with both signals and its geo summary", items)
	}
	if items := decodeRiskItems(t, getJSONAt(t, h.List, "/api/admin/risk-signals?user_id=3")); len(items) != 0 {
		t.Fatalf("an account with no signal row = %+v, want an empty list", items)
	}
	if rec := getJSONAt(t, h.List, "/api/admin/risk-signals?user_id=two"); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed user_id = %d, want 400", rec.Code)
	}
}
