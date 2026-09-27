package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/riskcenter"
)

// fakeRiskCenter is the risk center service as the handler sees it: it
// records what it was asked and answers what the test set.
type fakeRiskCenter struct {
	calls int

	liveQ   riskcenter.LiveQuery
	view    riskcenter.LiveView
	liveErr error

	refresh    riskcenter.RefreshResult
	refreshErr error

	histQ    ports.ConnectionHistoryFilter
	hist     []domain.ConnectionRecord
	histName map[int64]string
	histN    int64

	flagQ ports.FlagRecordFilter
	flags []domain.FlagRecord
	flagN int64
}

func (f *fakeRiskCenter) Live(_ context.Context, q riskcenter.LiveQuery) (riskcenter.LiveView, error) {
	f.calls++
	f.liveQ = q
	return f.view, f.liveErr
}

func (f *fakeRiskCenter) Refresh(context.Context) (riskcenter.RefreshResult, error) {
	f.calls++
	return f.refresh, f.refreshErr
}

func (f *fakeRiskCenter) History(_ context.Context, q ports.ConnectionHistoryFilter) ([]domain.ConnectionRecord, map[int64]string, int64, error) {
	f.calls++
	f.histQ = q
	return f.hist, f.histName, f.histN, nil
}

func (f *fakeRiskCenter) Flags(_ context.Context, q ports.FlagRecordFilter) ([]domain.FlagRecord, int64, error) {
	f.calls++
	f.flagQ = q
	return f.flags, f.flagN, nil
}

func serveRiskCenter(t *testing.T, h func(*gin.Context), method, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, nil)
	h(c)
	return rec
}

func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return out
}

// The live view's JSON is what the SPA renders the 实时连接 tab from: the
// snapshot's time, source, age and staleness; which panels could not be read
// and which have no live read at all, by NAME; the refresh button's state;
// and one item per account with its connections, each carrying its panel's
// name, the raw node guid, the source and its smallest address, the place
// (never a coordinate) and the devices inferred behind it — an empty list,
// not null, when none matched. The query string is parsed into the service's
// query, and a malformed id is a 400, never a fleet-wide answer.
func TestAdminRiskCenter_LiveDTO(t *testing.T) {
	taken := time.Date(2026, 9, 26, 10, 0, 5, 0, time.UTC)
	snap := &domain.LiveConnSnapshot{
		TakenAt: taken, Source: domain.LiveSnapshotFromPoll, PanelsAsked: 3,
		Unread: []int64{2}, Unsupported: []int64{4}, Unreferenced: 1, Truncated: 6,
		Conns: make([]domain.LiveConnection, 131), Users: map[int64]domain.LiveUserMeta{7: {}, 8: {}},
	}
	svc := &fakeRiskCenter{view: riskcenter.LiveView{
		Snapshot: snap, Age: 42 * time.Second, StaleAfter: 15 * time.Minute,
		RefreshCooldown: 30 * time.Second, RefreshAvailableIn: 12500 * time.Millisecond,
		DeviceWindow: 24 * time.Hour,
		Panels:       []riskcenter.PanelRef{{ID: 1, Name: "jp-1"}, {ID: 2, Name: "hk-1"}, {ID: 4, Name: "sui-1"}},
		Users: []riskcenter.LiveUser{{
			UserID: 7, UPN: "alice", DisplayName: "Alice", Meta: domain.LiveUserMeta{Stale: 2, Unread: 1},
			Conns: []riskcenter.LiveConn{
				{
					LiveConnection: domain.LiveConnection{UserID: 7, PanelID: 1, Node: "guid-a", SourceKey: "2001:db8:1:2::/64",
						IP: "2001:db8:1:2::5", SeenAt: 1790000000,
						Place: domain.ConnPlace{CountryCode: "CN", Country: "China", Region: "Guangdong", RegionCode: "GD", City: "Shenzhen"}},
					PanelName: "jp-1",
					Devices: []domain.ConnDevice{{Label: "iOS 17.5 · iPhone15,2", DeviceID4: "ab12", ClientType: "clash-meta",
						UA: "ClashMetaForAndroid/2.10", Fetches: 3, LastAtMS: 1790000000000}},
				},
				{LiveConnection: domain.LiveConnection{UserID: 7, PanelID: 1, SourceKey: "10.0.0.8", IP: "10.0.0.8",
					Exclusion: domain.AddressExcludedInternal}, PanelName: "jp-1"},
			},
		}},
		Total: 57,
	}}
	h := NewAdminRiskCenterHandler(svc)
	rec := serveRiskCenter(t, h.Live, http.MethodGet,
		"/api/admin/risk-center/live?page=2&page_size=10&user_id=7&panel_id=1&exclusion=kept&sort_by=user_id&sort_dir=asc")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	q := svc.liveQ
	if q.Page != 2 || q.PageSize != 10 || q.UserID != 7 || q.PanelID != 1 || q.Exclusion != "kept" || q.SortBy != "user_id" || q.SortDir != "asc" {
		t.Fatalf("query = %+v, want the query string parsed", q)
	}

	var body struct {
		Snapshot struct {
			TakenAt           *time.Time `json:"taken_at"`
			Source            string     `json:"source"`
			AgeSeconds        int64      `json:"age_seconds"`
			Stale             bool       `json:"stale"`
			StaleAfterSeconds int64      `json:"stale_after_seconds"`
			PanelsAsked       int        `json:"panels_asked"`
			PanelsUnread      []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"panels_unread"`
			PanelsUnsupported []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"panels_unsupported"`
			UnreferencedNodes int `json:"unreferenced_nodes"`
			Users             int `json:"users"`
			Connections       int `json:"connections"`
			Truncated         int `json:"truncated"`
		} `json:"snapshot"`
		Refresh struct {
			CooldownSeconds    int64 `json:"cooldown_seconds"`
			AvailableInSeconds int64 `json:"available_in_seconds"`
		} `json:"refresh"`
		DeviceWindowHours  int64 `json:"device_window_hours"`
		DevicesUnavailable bool  `json:"devices_unavailable"`
		Panels             []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"panels"`
		Items []struct {
			UserID         int64  `json:"user_id"`
			UPN            string `json:"upn"`
			DisplayName    string `json:"display_name"`
			StaleAddresses int    `json:"stale_addresses"`
			UnreadPanels   int    `json:"unread_panels"`
			Connections    []struct {
				PanelID   int64           `json:"panel_id"`
				PanelName string          `json:"panel_name"`
				Node      string          `json:"node"`
				SourceKey string          `json:"source_key"`
				IP        string          `json:"ip"`
				Exclusion string          `json:"exclusion"`
				SeenAt    int64           `json:"seen_at"`
				Region    json.RawMessage `json:"region"`
				Devices   json.RawMessage `json:"devices"`
			} `json:"connections"`
		} `json:"items"`
		Total    int64 `json:"total"`
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	s := body.Snapshot
	if s.TakenAt == nil || !s.TakenAt.Equal(taken) || s.Source != "poll" || s.AgeSeconds != 42 || s.Stale ||
		s.StaleAfterSeconds != 900 || s.PanelsAsked != 3 || s.UnreferencedNodes != 1 || s.Users != 2 ||
		s.Connections != 131 || s.Truncated != 6 {
		t.Fatalf("snapshot = %+v", s)
	}
	if len(s.PanelsUnread) != 1 || s.PanelsUnread[0].ID != 2 || s.PanelsUnread[0].Name != "hk-1" ||
		len(s.PanelsUnsupported) != 1 || s.PanelsUnsupported[0].Name != "sui-1" {
		t.Fatalf("unread %+v, unsupported %+v; want each panel by id and name", s.PanelsUnread, s.PanelsUnsupported)
	}
	if body.Refresh.CooldownSeconds != 30 || body.Refresh.AvailableInSeconds != 13 {
		t.Fatalf("refresh = %+v, want 30s cooldown and 13s left (rounded up)", body.Refresh)
	}
	if body.DeviceWindowHours != 24 || body.DevicesUnavailable || len(body.Panels) != 3 {
		t.Fatalf("device window %d, unavailable %v, %d panels", body.DeviceWindowHours, body.DevicesUnavailable, len(body.Panels))
	}
	if body.Total != 57 || body.Page != 2 || body.PageSize != 10 {
		t.Fatalf("envelope = %d / %d / %d, want 57 / 2 / 10", body.Total, body.Page, body.PageSize)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	it := body.Items[0]
	if it.UserID != 7 || it.UPN != "alice" || it.DisplayName != "Alice" || it.StaleAddresses != 2 || it.UnreadPanels != 1 || len(it.Connections) != 2 {
		t.Fatalf("item = %+v", it)
	}
	c := it.Connections[0]
	if c.PanelID != 1 || c.PanelName != "jp-1" || c.Node != "guid-a" || c.SourceKey != "2001:db8:1:2::/64" ||
		c.IP != "2001:db8:1:2::5" || c.Exclusion != "" || c.SeenAt != 1790000000 {
		t.Fatalf("connection = %+v", c)
	}
	var region map[string]any
	if err := json.Unmarshal(c.Region, &region); err != nil || region["country_code"] != "CN" || region["region_code"] != "GD" || region["city"] != "Shenzhen" || len(region) != 5 {
		t.Fatalf("region = %s, want the five place fields and nothing else", c.Region)
	}
	var devices []map[string]any
	if err := json.Unmarshal(c.Devices, &devices); err != nil || len(devices) != 1 ||
		devices[0]["device_id4"] != "ab12" || devices[0]["label"] != "iOS 17.5 · iPhone15,2" ||
		devices[0]["client_type"] != "clash-meta" || devices[0]["ua"] != "ClashMetaForAndroid/2.10" ||
		devices[0]["fetches"] != float64(3) || devices[0]["last_at_ms"] != float64(1790000000000) {
		t.Fatalf("devices = %s", c.Devices)
	}
	internal := it.Connections[1]
	if string(internal.Devices) != "[]" || string(internal.Region) != "null" || internal.Exclusion != "internal" {
		t.Fatalf("an internal connection: devices %s, region %s; want [] and null", internal.Devices, internal.Region)
	}

	// Before the first poll: no time, no source, stale.
	svc.view = riskcenter.LiveView{StaleAfter: 15 * time.Minute, Stale: true, Users: []riskcenter.LiveUser{}}
	rec = serveRiskCenter(t, h.Live, http.MethodGet, "/api/admin/risk-center/live")
	obj := decodeObject(t, rec)
	snapObj, _ := obj["snapshot"].(map[string]any)
	if snapObj == nil || snapObj["taken_at"] != nil || snapObj["source"] != "" || snapObj["stale"] != true {
		t.Fatalf("no snapshot = %s, want taken_at null, source \"\", stale", rec.Body.String())
	}
	if items, ok := obj["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("items = %v, want []", obj["items"])
	}

	// A malformed filter is refused before the service is asked.
	before := svc.calls
	for _, target := range []string{"/api/admin/risk-center/live?user_id=abc", "/api/admin/risk-center/live?panel_id=1x"} {
		if rec := serveRiskCenter(t, h.Live, http.MethodGet, target); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", target, rec.Code)
		}
	}
	if svc.calls != before {
		t.Fatal("a malformed filter reached the service")
	}
	svc.liveErr = domain.ErrValidation
	if rec := serveRiskCenter(t, h.Live, http.MethodGet, "/api/admin/risk-center/live?exclusion=nonsense"); rec.Code != http.StatusBadRequest {
		t.Fatalf("the service's validation error = %d, want 400", rec.Code)
	}
}

// A refused refresh is a 429 the SPA can act on: a machine-readable error, the
// reason (cooldown or in progress) and the seconds to wait, rounded up, in
// the body and in Retry-After. A refresh answered from the poll that just
// ran is a 200 that says so; a real one names the panels it could not read.
func TestAdminRiskCenter_RefreshThrottledIs429WithRetryAfter(t *testing.T) {
	svc := &fakeRiskCenter{refreshErr: &riskcenter.RefreshThrottled{Reason: "cooldown", RetryAfter: 20300 * time.Millisecond}}
	h := NewAdminRiskCenterHandler(svc)
	rec := serveRiskCenter(t, h.Refresh, http.MethodPost, "/api/admin/risk-center/live/refresh")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "21" {
		t.Fatalf("status %d, Retry-After %q; want 429 and 21", rec.Code, rec.Header().Get("Retry-After"))
	}
	obj := decodeObject(t, rec)
	if obj["error"] != "refresh_throttled" || obj["reason"] != "cooldown" || obj["retry_after_seconds"] != float64(21) {
		t.Fatalf("body = %s", rec.Body.String())
	}

	taken := time.Date(2026, 9, 26, 10, 0, 5, 0, time.UTC)
	svc.refreshErr = nil
	svc.refresh = riskcenter.RefreshResult{Refreshed: true,
		Snapshot: &domain.LiveConnSnapshot{TakenAt: taken, Source: domain.LiveSnapshotFromRefresh, PanelsAsked: 3,
			Unread: []int64{2}, Unsupported: []int64{4}, Conns: make([]domain.LiveConnection, 5)},
		Panels: []riskcenter.PanelRef{{ID: 2, Name: "hk-1"}, {ID: 4, Name: "sui-1"}}}
	rec = serveRiskCenter(t, h.Refresh, http.MethodPost, "/api/admin/risk-center/live/refresh")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	obj = decodeObject(t, rec)
	unread, _ := obj["panels_unread"].([]any)
	unsupported, _ := obj["panels_unsupported"].([]any)
	if obj["refreshed"] != true || obj["reason"] != "" || obj["source"] != "refresh" || obj["panels_asked"] != float64(3) ||
		obj["connections"] != float64(5) || obj["taken_at"] != "2026-09-26T10:00:05Z" || len(unread) != 1 || len(unsupported) != 1 {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if p, _ := unread[0].(map[string]any); p["id"] != float64(2) || p["name"] != "hk-1" {
		t.Fatalf("unread = %v, want hk-1 by id and name", unread)
	}

	svc.refresh = riskcenter.RefreshResult{Reason: "just_polled",
		Snapshot: &domain.LiveConnSnapshot{TakenAt: taken, Source: domain.LiveSnapshotFromPoll}}
	obj = decodeObject(t, serveRiskCenter(t, h.Refresh, http.MethodPost, "/api/admin/risk-center/live/refresh"))
	if obj["refreshed"] != false || obj["reason"] != "just_polled" || obj["source"] != "poll" {
		t.Fatalf("just polled = %v", obj)
	}

	svc.refreshErr = errors.New("refresh live connections: list shared clients: db gone")
	if rec := serveRiskCenter(t, h.Refresh, http.MethodPost, "/api/admin/risk-center/live/refresh"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a failed refresh = %d, want 500", rec.Code)
	}
}

// The connection history and the flag records are paged lists in the admin
// lists' envelope ({items,total,page,page_size}), with their filters parsed
// from the query string and a malformed one refused. History rows name
// their panel; a flag record's params are served as the stored JSON (null
// when there are none).
func TestAdminRiskCenter_HistoryAndFlagsUseTheEnvelope(t *testing.T) {
	svc := &fakeRiskCenter{
		hist: []domain.ConnectionRecord{{UserID: 7, UPN: "alice", DisplayName: "Alice", PanelID: 1, Node: "guid-a",
			SourceKey: "203.0.113.7", IP: "203.0.113.7", Place: domain.ConnPlace{CountryCode: "JP"},
			FirstSeenMS: 1, LastSeenMS: 2, Count: 3}},
		histName: map[int64]string{1: "jp-1"}, histN: 41,
		flags: []domain.FlagRecord{
			{ID: 9, UserID: 7, UPN: "alice", Source: "geo", Event: domain.FlagEnterFlagged, Level: domain.FlagLevelFlagged,
				State: domain.GeoStateFlagged, Code: "over", Params: json.RawMessage(`{"over":3}`), AtMS: 5},
			{ID: 8, UserID: 7, Source: "geo_auto", Event: domain.FlagAutoLiftedAdmin, Code: "admin_resume", AtMS: 4},
		},
		flagN: 2,
	}
	h := NewAdminRiskCenterHandler(svc)

	rec := serveRiskCenter(t, h.Connections, http.MethodGet,
		"/api/admin/risk-center/connections?page=3&page_size=20&user_id=7&panel_id=1&exclusion=excluded"+
			"&since=2026-09-20T00:00:00Z&until=2026-09-26T00:00:00Z&search=jp&sort_by=count&sort_dir=asc")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	f := svc.histQ
	if f.Page != 3 || f.PageSize != 20 || f.UserID == nil || *f.UserID != 7 || f.PanelID == nil || *f.PanelID != 1 ||
		f.Exclusion != "excluded" || f.Since == nil || f.Until == nil || f.Search != "jp" || f.SortBy != "count" || f.SortDir != "asc" {
		t.Fatalf("history filter = %+v", f)
	}
	obj := decodeObject(t, rec)
	items, _ := obj["items"].([]any)
	if obj["total"] != float64(41) || obj["page"] != float64(3) || obj["page_size"] != float64(20) || len(items) != 1 {
		t.Fatalf("history envelope = %s", rec.Body.String())
	}
	row, _ := items[0].(map[string]any)
	region, _ := row["region"].(map[string]any)
	if row["panel_name"] != "jp-1" || row["upn"] != "alice" || row["ip"] != "203.0.113.7" || row["count"] != float64(3) ||
		row["first_seen_ms"] != float64(1) || row["last_seen_ms"] != float64(2) || region["country_code"] != "JP" {
		t.Fatalf("history row = %v", row)
	}

	rec = serveRiskCenter(t, h.Flags, http.MethodGet,
		"/api/admin/risk-center/flags?user_id=7&source=geo&level=flagged&event=enter_flagged&since=2026-09-20T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	g := svc.flagQ
	if g.UserID == nil || *g.UserID != 7 || g.Source != "geo" || g.Level != "flagged" || g.Event != "enter_flagged" || g.Since == nil || g.Until != nil {
		t.Fatalf("flag filter = %+v", g)
	}
	var flags struct {
		Items []map[string]json.RawMessage `json:"items"`
		Total int64                        `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &flags); err != nil || flags.Total != 2 || len(flags.Items) != 2 {
		t.Fatalf("flags = %s (%v)", rec.Body.String(), err)
	}
	for _, key := range []string{"id", "user_id", "upn", "display_name", "source", "event", "level", "prev_level", "state", "code", "params", "at_ms"} {
		if _, ok := flags.Items[0][key]; !ok {
			t.Errorf("flag record has no %q: %s", key, rec.Body.String())
		}
	}
	if string(flags.Items[0]["params"]) != `{"over":3}` || string(flags.Items[1]["params"]) != "null" {
		t.Fatalf("params = %s / %s, want the stored JSON and null", flags.Items[0]["params"], flags.Items[1]["params"])
	}

	before := svc.calls
	for _, target := range []string{
		"/api/admin/risk-center/connections?since=yesterday",
		"/api/admin/risk-center/connections?user_id=x",
		"/api/admin/risk-center/flags?until=2026-13-01",
		"/api/admin/risk-center/flags?user_id=-",
	} {
		handle := h.Connections
		if strings.Contains(target, "/flags") {
			handle = h.Flags
		}
		if rec := serveRiskCenter(t, handle, http.MethodGet, target); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", target, rec.Code)
		}
	}
	if svc.calls != before {
		t.Fatal("a malformed filter reached the service")
	}
}

// "Nothing to show" and "this build cannot show it" must not look alike:
// every risk-center endpoint of a deployment that never wired the service
// answers 503, not an empty list or a refresh that did nothing.
func TestAdminRiskCenter_UnwiredIs503(t *testing.T) {
	h := NewAdminRiskCenterHandler(nil)
	for _, c := range []struct {
		name   string
		handle func(*gin.Context)
		method string
	}{
		{"live", h.Live, http.MethodGet},
		{"refresh", h.Refresh, http.MethodPost},
		{"connections", h.Connections, http.MethodGet},
		{"flags", h.Flags, http.MethodGet},
	} {
		if rec := serveRiskCenter(t, c.handle, c.method, "/"); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s unwired = %d, want 503", c.name, rec.Code)
		}
	}
}
