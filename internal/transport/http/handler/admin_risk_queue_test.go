package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/riskcenter"
)

// serveRiskUser serves h.User with the :id path parameter set.
func serveRiskUser(t *testing.T, h *AdminRiskCenterHandler, id string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/risk-center/users/"+id, nil)
	c.Params = gin.Params{{Key: "id", Value: id}}
	h.User(c)
	return rec
}

// The queue's query string reaches the service parsed: the status, a comma
// list of sources (blanks dropped), the level, the two switches, the
// search and the page. A malformed switch is refused before the service is
// asked; the service's own refusal of an unknown value is a 400 too.
func TestAdminRiskQueue_ParsesTheQuery(t *testing.T) {
	svc := &fakeRiskCenter{queue: riskcenter.QueueView{Page: 2, PageSize: 10}}
	h := NewAdminRiskCenterHandler(svc)
	rec := serveRiskCenter(t, h.Queue, http.MethodGet,
		"/api/admin/risk-center/queue?status=dismissed&source=geo,%20devices,&level=flagged&auto_suspended=true&urgent=1&q=%20ali%20&page=2&page_size=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	want := riskcenter.QueueQuery{Status: "dismissed", Sources: []string{"geo", "devices"}, Level: domain.FlagLevelFlagged,
		AutoSuspended: true, Urgent: true, Q: "ali", Page: 2, PageSize: 10}
	if !reflect.DeepEqual(svc.queueQ, want) {
		t.Fatalf("query = %+v, want %+v", svc.queueQ, want)
	}

	rec = serveRiskCenter(t, h.Queue, http.MethodGet, "/api/admin/risk-center/queue")
	if q := svc.queueQ; rec.Code != http.StatusOK || q.Status != "" || q.Sources != nil || q.AutoSuspended || q.Urgent || q.Page != 1 || q.PageSize != 25 {
		t.Fatalf("no parameters = %d, query %+v; want the defaults", rec.Code, q)
	}

	before := svc.calls
	for _, target := range []string{
		"/api/admin/risk-center/queue?auto_suspended=maybe",
		"/api/admin/risk-center/queue?urgent=2",
	} {
		if rec := serveRiskCenter(t, h.Queue, http.MethodGet, target); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", target, rec.Code)
		}
	}
	if svc.calls != before {
		t.Fatal("a malformed switch reached the service")
	}
	svc.queueErr = domain.ErrValidation
	rec = serveRiskCenter(t, h.Queue, http.MethodGet, "/api/admin/risk-center/queue?status=pending")
	if obj := decodeObject(t, rec); rec.Code != http.StatusBadRequest || obj["error"] == nil {
		t.Fatalf("the service's refusal = %d %s, want 400 with an error", rec.Code, rec.Body.String())
	}
}

// The queue's wire shape, rule by rule (the SPA reads each without a null
// check): one item per account with its names and group, its level, hold
// and urgency, its service state and — only when the service is held — the
// hold's reason and time; sources as [] never null, worst first, the hold
// last; geo exactly the /geo-anomalies item, and null unless geo is a
// source; signals as [] never null, each exactly as /risk-signals serves
// it; the review badge with escalated as [] never null. A row with no
// attention (a quiet trusted account) is all empty, never absent fields.
// The online count is null before the first snapshot.
func TestAdminRiskQueue_DTOShape(t *testing.T) {
	heldAt := time.UnixMilli(1790000000000).UTC()
	alice := &domain.User{ID: 7, UPN: "alice", DisplayName: "Alice", GroupID: 2, Enabled: true,
		ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &heldAt}
	quiet := &domain.User{ID: 9, UPN: "quiet", DisplayName: "Quiet", GroupID: 1, Enabled: true}
	geoRec := domain.GeoRecord{UserID: 7, State: domain.GeoStateIdle, Reason: "in 2 places", Places: []string{"DE", "JP"},
		Streak: domain.GeoStreak{Flagged: true, Tier: domain.GeoTierCountry, Over: 3}, Complete: true, UpdatedAtMS: 1789999990000,
		Evidence: domain.GeoEvidence{V: domain.GeoEvidenceVersion, Spots: []domain.GeoSpot{{CC: "DE", N: 1}, {CC: "JP", N: 1}}}}
	sub := domain.RiskSignal{UserID: 7, Kind: domain.RiskKindSubSpread, State: domain.GeoStateSuspect, Code: "spread_building",
		Evidence: json.RawMessage(`{"days":2}`), UpdatedAtMS: 1789999980000}
	svc := &fakeRiskCenter{queue: riskcenter.QueueView{
		Rows: []riskcenter.QueueRow{
			{
				User: alice, GroupName: "Team A", Geo: &geoRec, Signals: []domain.RiskSignal{sub}, ChangedAtMS: 1789999995000,
				Attention: domain.AccountAttention{
					Levels:    domain.AttentionLevels{"geo_auto": domain.FlagLevelSuspended, "sub_spread": domain.FlagLevelSuspect, "geo": domain.FlagLevelFlagged},
					Review:    domain.RiskReview{UserID: 7, DismissedAtMS: 5, Accepted: domain.DismissSnapshot{"geo": {Level: domain.FlagLevelSuspect}}},
					HasReview: true, HeldSinceMS: heldAt.UnixMilli(),
					State: domain.ReviewState{Dismissed: true, Reopened: true, Escalated: []string{"geo"}},
				},
			},
			{
				User: quiet, GroupName: "Default",
				Attention: domain.AccountAttention{Review: domain.RiskReview{UserID: 9, Trusted: true}, HasReview: true},
			},
		},
		Total: 2, Page: 1, PageSize: 25,
		Counts: riskcenter.QueueCounts{Urgent: 2, Flagged: 3, Suspect: 5, AutoSuspended: 1, Dismissed: 2, Trusted: 1, GeoUnknown: 4},
	}}
	h := NewAdminRiskCenterHandler(svc)
	rec := serveRiskCenter(t, h.Queue, http.MethodGet, "/api/admin/risk-center/queue")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	obj := decodeObject(t, rec)
	if obj["total"] != float64(2) || obj["page"] != float64(1) || obj["page_size"] != float64(25) || obj["global_detectors_off"] != false {
		t.Fatalf("envelope = %s", rec.Body.String())
	}
	counts, _ := obj["counts"].(map[string]any)
	wantCounts := map[string]any{"online": nil, "online_taken_at": nil, "online_stale": false, "urgent": float64(2),
		"flagged": float64(3), "suspect": float64(5), "auto_suspended": float64(1), "dismissed": float64(2),
		"trusted": float64(1), "geo_unknown": float64(4)}
	if !reflect.DeepEqual(counts, wantCounts) {
		t.Fatalf("counts = %v, want %v", counts, wantCounts)
	}
	items, _ := obj["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", obj["items"])
	}

	a, _ := items[0].(map[string]any)
	for key, want := range map[string]any{
		"user_id": float64(7), "upn": "alice", "display_name": "Alice", "group_id": float64(2), "group_name": "Team A",
		"level": "flagged", "auto_suspended": true, "urgent": true, "service_state": "manual_suspended",
		"service_disabled_reason": "geo_auto", "service_disabled_at_ms": float64(heldAt.UnixMilli()),
		"changed_at_ms": float64(1789999995000),
	} {
		if a[key] != want {
			t.Errorf("item.%s = %v, want %v", key, a[key], want)
		}
	}
	wantSources := []any{
		map[string]any{"source": "geo", "level": "flagged"},
		map[string]any{"source": "sub_spread", "level": "suspect"},
		map[string]any{"source": "geo_auto", "level": "suspended"},
	}
	if !reflect.DeepEqual(a["sources"], wantSources) {
		t.Fatalf("sources = %v, want %v", a["sources"], wantSources)
	}
	wantReview := map[string]any{"dismissed": true, "reopened": true, "lapsed": false, "trusted": false, "escalated": []any{"geo"}}
	if !reflect.DeepEqual(a["review"], wantReview) {
		t.Fatalf("review = %v, want %v", a["review"], wantReview)
	}

	// geo is exactly what /geo-anomalies serves for the same record and user.
	geoRec2 := geoRec
	geoH := NewAdminGeoAnomalyHandler(&stubRecords{recs: []domain.GeoRecord{geoRec2}}, geoUsersStub{byID: map[int64]*domain.User{7: alice}})
	geoRows := decodeRows(t, getJSON(t, geoH.List))
	if len(geoRows) != 1 || !reflect.DeepEqual(a["geo"], any(geoRows[0])) {
		t.Fatalf("geo = %v\nwant the /geo-anomalies item %v", a["geo"], geoRows[0])
	}
	// signals are exactly what /risk-signals serves for the same rows.
	sigH := NewAdminRiskSignalHandler(&stubRiskSignals{rows: []domain.RiskSignal{sub}}, nil)
	sigItems := decodeRows(t, getJSON(t, sigH.List))
	if len(sigItems) != 1 || !reflect.DeepEqual(a["signals"], sigItems[0]["signals"]) {
		t.Fatalf("signals = %v\nwant the /risk-signals signals %v", a["signals"], sigItems[0]["signals"])
	}

	b, _ := items[1].(map[string]any)
	for key, want := range map[string]any{
		"user_id": float64(9), "level": "", "auto_suspended": false, "urgent": false, "geo": nil,
		"service_state": "active", "changed_at_ms": float64(0),
	} {
		if b[key] != want {
			t.Errorf("quiet item.%s = %v, want %v", key, b[key], want)
		}
	}
	for _, key := range []string{"sources", "signals"} {
		if list, ok := b[key].([]any); !ok || len(list) != 0 {
			t.Errorf("quiet item.%s = %v, want []", key, b[key])
		}
	}
	for _, key := range []string{"service_disabled_reason", "service_disabled_at_ms"} {
		if _, present := b[key]; present {
			t.Errorf("quiet item carries %s while its service is active", key)
		}
	}
	if review, _ := b["review"].(map[string]any); review["trusted"] != true || !reflect.DeepEqual(review["escalated"], []any{}) {
		t.Fatalf("quiet review = %v, want trusted and escalated []", b["review"])
	}

	// With a snapshot, the online count and its time.
	online, taken := 31, time.Date(2026, 9, 28, 10, 0, 5, 0, time.UTC)
	svc.queue.Counts = riskcenter.QueueCounts{Online: &online, OnlineTakenAt: taken, OnlineStale: true}
	counts, _ = decodeObject(t, serveRiskCenter(t, h.Queue, http.MethodGet, "/api/admin/risk-center/queue"))["counts"].(map[string]any)
	if counts["online"] != float64(31) || counts["online_taken_at"] != "2026-09-28T10:00:05Z" || counts["online_stale"] != true {
		t.Fatalf("counts = %v, want online 31 at the snapshot time, stale", counts)
	}
}

// One account's drawer: the account (with its access decision and hold),
// its attention, its review with the admins named, its verdicts each with
// its staleness, the live view limited to it (exactly what /live?user_id=
// serves), and its devices. A bad id is a 400, a missing account a 404.
func TestAdminRiskUser_SummaryDTO(t *testing.T) {
	heldAt := time.UnixMilli(1790000000000).UTC()
	u := &domain.User{ID: 7, UPN: "alice", DisplayName: "Alice", Role: domain.RoleUser, GroupID: 2, Enabled: true,
		TrafficLimitBytes: 5 << 30, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisableDetail: "held",
		ServiceDisabledAt: &heldAt}
	geoRec := domain.GeoRecord{UserID: 7, State: domain.GeoStateClean, Places: nil, UpdatedAtMS: 1}
	dev := domain.RiskSignal{UserID: 7, Kind: domain.RiskKindDevices, State: domain.GeoStateClean, Code: "within", UpdatedAtMS: 1}
	sub := domain.RiskSignal{UserID: 7, Kind: domain.RiskKindSubSpread, State: domain.GeoStateSuspect, Code: "spread_building", UpdatedAtMS: 2}
	live := riskcenter.LiveView{StaleAfter: 15 * time.Minute, Stale: true, Users: []riskcenter.LiveUser{{UserID: 7, UPN: "alice"}}, Total: 1}
	svc := &fakeRiskCenter{summary: riskcenter.UserSummary{
		User: u, GroupName: "Team A",
		Attention: domain.AccountAttention{
			Levels:    domain.AttentionLevels{"sub_spread": domain.FlagLevelSuspect, "geo_auto": domain.FlagLevelSuspended},
			HasReview: true,
			Review: domain.RiskReview{UserID: 7, DismissedAtMS: 11, DismissedBy: 3, Note: "seen",
				Accepted: domain.DismissSnapshot{"sub_spread": {Level: domain.FlagLevelSuspect, AtMS: 10}},
				Trusted:  true, TrustedAtMS: 12, TrustedBy: 4},
			State: domain.ReviewState{Dismissed: true},
		},
		DismissedByUPN: "root", TrustedByUPN: "",
		Geo: &geoRec, GeoStale: true,
		Signals:    []domain.RiskSignal{sub, dev},
		StaleKinds: map[domain.RiskKind]bool{domain.RiskKindDevices: true},
		Live:       live,
		Devices: []domain.UserDevice{{Label: "iPhone 16", DeviceID4: "ab12", ClientType: "mihomo", UA: "Happ/3.13", Fetches: 12,
			FirstAtMS: 1, LastAtMS: 2, Sources: []string{"198.51.100.20"}}},
		DeviceWindow: 24 * time.Hour,
	}}
	h := NewAdminRiskCenterHandler(svc)
	rec := serveRiskUser(t, h, "7")
	if rec.Code != http.StatusOK || svc.summaryID != 7 {
		t.Fatalf("status %d for id %d: %s", rec.Code, svc.summaryID, rec.Body.String())
	}
	obj := decodeObject(t, rec)

	user, _ := obj["user"].(map[string]any)
	for key, want := range map[string]any{
		"id": float64(7), "upn": "alice", "display_name": "Alice", "role": "user", "group_id": float64(2), "group_name": "Team A",
		"enabled": true, "traffic_limit_bytes": float64(5 << 30), "service_disabled_reason": "geo_auto",
		"service_disable_detail": "held", "service_disabled_at_ms": float64(heldAt.UnixMilli()),
	} {
		if user[key] != want {
			t.Errorf("user.%s = %v, want %v", key, user[key], want)
		}
	}
	if access, _ := user["access"].(map[string]any); access["service_state"] != "manual_suspended" || access["account_state"] != "active" {
		t.Fatalf("user.access = %v, want the access decision", user["access"])
	}
	wantAttention := []any{map[string]any{"source": "sub_spread", "level": "suspect"}, map[string]any{"source": "geo_auto", "level": "suspended"}}
	if !reflect.DeepEqual(obj["attention"], wantAttention) {
		t.Fatalf("attention = %v, want %v", obj["attention"], wantAttention)
	}
	wantReview := map[string]any{"dismissed": true, "dismissed_at_ms": float64(11), "dismissed_by": float64(3), "dismissed_by_upn": "root",
		"note": "seen", "levels": map[string]any{"sub_spread": "suspect"}, "reopened": false, "lapsed": false, "escalated": []any{},
		"trusted": true, "trusted_at_ms": float64(12), "trusted_by": float64(4), "trusted_by_upn": ""}
	if !reflect.DeepEqual(obj["review"], wantReview) {
		t.Fatalf("review = %v\nwant %v", obj["review"], wantReview)
	}
	geo, _ := obj["geo"].(map[string]any)
	if geo["stale"] != true || geo["user_id"] != float64(7) || geo["state"] != "clean" || !reflect.DeepEqual(geo["places"], []any{}) {
		t.Fatalf("geo = %v, want the row with stale true", obj["geo"])
	}
	signals, _ := obj["signals"].([]any)
	if len(signals) != 2 {
		t.Fatalf("signals = %v", obj["signals"])
	}
	if s0, _ := signals[0].(map[string]any); s0["kind"] != "sub_spread" || s0["stale"] != false || s0["code"] != "spread_building" {
		t.Fatalf("signals[0] = %v", s0)
	}
	if s1, _ := signals[1].(map[string]any); s1["kind"] != "devices" || s1["stale"] != true {
		t.Fatalf("signals[1] = %v", s1)
	}
	devices, _ := obj["devices"].([]any)
	wantDevice := map[string]any{"label": "iPhone 16", "device_id4": "ab12", "client_type": "mihomo", "ua": "Happ/3.13",
		"fetches": float64(12), "first_at_ms": float64(1), "last_at_ms": float64(2), "sources": []any{"198.51.100.20"}, "sources_more": float64(0)}
	if len(devices) != 1 || !reflect.DeepEqual(devices[0], wantDevice) {
		t.Fatalf("devices = %v, want [%v]", obj["devices"], wantDevice)
	}
	if obj["device_window_hours"] != float64(24) || obj["devices_unavailable"] != false {
		t.Fatalf("device window %v unavailable %v", obj["device_window_hours"], obj["devices_unavailable"])
	}

	// live is exactly what /live?user_id=7&page=1&page_size=1 serves.
	svc.view = live
	liveObj := decodeObject(t, serveRiskCenter(t, h.Live, http.MethodGet, "/api/admin/risk-center/live?user_id=7&page=1&page_size=1"))
	if !reflect.DeepEqual(obj["live"], any(liveObj)) {
		t.Fatalf("live = %v\nwant the /live answer %v", obj["live"], liveObj)
	}

	// No geo row, no devices: null and [].
	svc.summary.Geo, svc.summary.Devices, svc.summary.Signals = nil, nil, nil
	obj = decodeObject(t, serveRiskUser(t, h, "7"))
	if obj["geo"] != nil || !reflect.DeepEqual(obj["devices"], []any{}) || !reflect.DeepEqual(obj["signals"], []any{}) {
		t.Fatalf("empty summary: geo %v devices %v signals %v, want null, [] and []", obj["geo"], obj["devices"], obj["signals"])
	}

	before := svc.calls
	for _, id := range []string{"abc", "0", "-1", "7x"} {
		rec := serveRiskUser(t, h, id)
		if obj := decodeObject(t, rec); rec.Code != http.StatusBadRequest || obj["error"] != "Invalid id" {
			t.Fatalf("id %q = %d %s, want 400 Invalid id", id, rec.Code, rec.Body.String())
		}
	}
	if svc.calls != before {
		t.Fatal("a bad id reached the service")
	}
	svc.summaryErr = domain.ErrNotFound
	rec = serveRiskUser(t, h, "404")
	if obj := decodeObject(t, rec); rec.Code != http.StatusNotFound || obj["error"] != "Not found" {
		t.Fatalf("a missing account = %d %s, want 404 Not found", rec.Code, rec.Body.String())
	}
}

// The Users page's chips: an object keyed by the decimal account id, every
// account at attention and every trusted one — a trusted account with no
// attention has level "".
func TestAdminRiskLevels_KeyedByID(t *testing.T) {
	svc := &fakeRiskCenter{levels: map[int64]riskcenter.UserLevel{
		7: {Level: domain.FlagLevelFlagged, Open: true},
		8: {AutoSuspended: true, Open: true},
		9: {Trusted: true},
	}}
	h := NewAdminRiskCenterHandler(svc)
	rec := serveRiskCenter(t, h.Levels, http.MethodGet, "/api/admin/risk-center/levels")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	want := map[string]any{
		"7": map[string]any{"level": "flagged", "auto_suspended": false, "open": true, "dismissed": false, "trusted": false},
		"8": map[string]any{"level": "", "auto_suspended": true, "open": true, "dismissed": false, "trusted": false},
		"9": map[string]any{"level": "", "auto_suspended": false, "open": false, "dismissed": false, "trusted": true},
	}
	if got := decodeObject(t, rec); !reflect.DeepEqual(got, want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}

	svc.levels = nil
	if rec := serveRiskCenter(t, h.Levels, http.MethodGet, "/"); rec.Body.String() != "{}" {
		t.Fatalf("no levels = %s, want {}", rec.Body.String())
	}
	svc.levelsErr = errors.New("db gone")
	if rec := serveRiskCenter(t, h.Levels, http.MethodGet, "/"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a failed read = %d, want 500", rec.Code)
	}
}

// A review record names its admin as actor_upn when that admin still
// exists (the service resolves it); otherwise, and on every detector
// record, there is no actor_upn at all.
func TestAdminRiskFlags_ActorUPN(t *testing.T) {
	svc := &fakeRiskCenter{
		flags: []domain.FlagRecord{
			domain.ReviewFlag(7, domain.FlagReviewDismissed, domain.ReviewFlagParams{By: 3}, time.UnixMilli(5)),
			domain.ReviewFlag(7, domain.FlagReviewTrusted, domain.ReviewFlagParams{By: 4}, time.UnixMilli(4)),
			{UserID: 7, Source: domain.FlagSourceGeo, Event: domain.FlagEnterFlagged, Params: json.RawMessage(`{"by":3}`), AtMS: 3},
		},
		flagN:  3,
		actors: map[int64]string{3: "root"},
	}
	h := NewAdminRiskCenterHandler(svc)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	rec := serveRiskCenter(t, h.Flags, http.MethodGet, "/api/admin/risk-center/flags")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 3 {
		t.Fatalf("flags = %s (%v)", rec.Body.String(), err)
	}
	if body.Items[0]["actor_upn"] != "root" {
		t.Fatalf("dismissal actor = %v, want root", body.Items[0]["actor_upn"])
	}
	for i := 1; i < 3; i++ {
		if _, present := body.Items[i]["actor_upn"]; present {
			t.Fatalf("item %d carries actor_upn %v, want none", i, body.Items[i]["actor_upn"])
		}
	}
}
