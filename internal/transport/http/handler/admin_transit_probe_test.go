package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/linkprobe"
)

const transitProbeSettingsPath = "/api/admin/transit/probe-settings"

type probeStateStub struct {
	ports.TransitProbeRepo
	cleared int
}

func (s *probeStateStub) ClearWithheld(context.Context) (int64, error) {
	s.cleared++
	return 3, nil
}

type probeSettingsFixture struct {
	repo        *policySettingsRepo
	state       *probeStateStub
	invalidated int
	router      *gin.Engine
}

func newProbeSettingsFixture(stored ports.UISettings) *probeSettingsFixture {
	gin.SetMode(gin.TestMode)
	f := &probeSettingsFixture{repo: &policySettingsRepo{settings: stored}, state: &probeStateStub{}}
	h := NewAdminTransitProbeHandler(f.repo, nil, f.state, nil, nil, func() { f.invalidated++ })
	f.router = gin.New()
	f.router.GET(transitProbeSettingsPath, h.GetSettings)
	f.router.PUT(transitProbeSettingsPath, h.PutSettings)
	f.router.PUT("/api/admin/settings/ui", NewAdminSettingsHandler(f.repo, nil, nil, nil).Put)
	return f
}

func (f *probeSettingsFixture) put(body string) (int, map[string]any) {
	rec := requestRiskPolicy(f.router, http.MethodPut, transitProbeSettingsPath, body)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestTransitProbeSettingsGetServesDefaultsAndEffective(t *testing.T) {
	f := newProbeSettingsFixture(ports.UISettings{})
	rec := requestRiskPolicy(f.router, http.MethodGet, transitProbeSettingsPath, "")
	var view struct {
		Settings  ports.TransitProbeSettings  `json:"settings"`
		Defaults  map[string]any              `json:"defaults"`
		Effective ports.TransitProbeEffective `json:"effective"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !view.Settings.TransitProbeEnabled || view.Effective != ports.TransitProbeEffectiveOf(ports.UISettings{}) ||
		view.Defaults["transit_probe_url"] != ports.TransitProbeURLDefault || view.Defaults["transit_probe_interval_seconds"] != float64(60) {
		t.Fatalf("view = %+v", view)
	}
	if _, saves, _ := f.repo.snapshot(); saves != 0 {
		t.Fatal("a GET saved")
	}
}

// Only the sent keys change, and nothing outside the transit_probe_ keys moves.
func TestTransitProbeSettingsPutMergesAndOwnsOnlyItsKeys(t *testing.T) {
	stored := storedEverywhere()
	stored.TransitProbeOff, stored.TransitProbeURLOff = false, false
	stored.TransitProbeURL = "https://stored.example/204"
	stored.TransitProbeIntervalSeconds, stored.TransitProbeTimeoutMS = 90, 2000
	stored.TransitProbeWithholdAfter, stored.TransitProbeRestoreAfter = 4, 5
	f := newProbeSettingsFixture(stored)

	code, _ := f.put(`{"settings":{"transit_probe_withhold":true,"transit_probe_interval_seconds":120}}`)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	got, saves, _ := f.repo.snapshot()
	want := stored.TransitProbeSettings()
	want.TransitProbeWithhold, want.TransitProbeIntervalSeconds = true, 120
	if saves != 1 || got.TransitProbeSettings() != want {
		t.Fatalf("saves %d settings %+v, want %+v", saves, got.TransitProbeSettings(), want)
	}
	gv, sv := reflect.ValueOf(got), reflect.ValueOf(stored)
	for i := 0; i < gv.NumField(); i++ {
		name := gv.Type().Field(i).Name
		if strings.HasPrefix(name, "TransitProbe") {
			continue
		}
		if !reflect.DeepEqual(gv.Field(i).Interface(), sv.Field(i).Interface()) {
			t.Errorf("%s changed: the probe settings PUT does not own it", name)
		}
	}
	if f.state.cleared != 0 || f.invalidated != 0 {
		t.Fatal("withholding stayed on but flags were cleared")
	}
}

func TestTransitProbeSettingsPutValidation(t *testing.T) {
	for _, tc := range []struct{ body, field string }{
		{`{"settings":{"transit_probe_interval_seconds":10}}`, "transit_probe_interval_seconds"},
		{`{"settings":{"transit_probe_timeout_ms":20000}}`, "transit_probe_timeout_ms"},
		{`{"settings":{"transit_probe_url":"http://example.com:0/"}}`, "transit_probe_url"},
		{`{"settings":{"transit_probe_url":"http://example.com:99999/"}}`, "transit_probe_url"},
		{`{"settings":{"transit_probe_url":"gopher://example.com/"}}`, "transit_probe_url"},
		{`{"settings":{"transit_probe_withhold_after":99}}`, "transit_probe_withhold_after"},
		{`{"settings":{"transit_probe_restore_after":-1}}`, "transit_probe_restore_after"},
		{`{"settings":{"transit_probe_typo":1}}`, ""},
		{`{"settings":{"transit_probe_interval_seconds":"60"}}`, ""},
		{`{"settings":[]}`, ""},
		{`{}`, ""},
	} {
		f := newProbeSettingsFixture(ports.UISettings{})
		code, body := f.put(tc.body)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.body, code)
			continue
		}
		if tc.field != "" && body["field"] != tc.field {
			t.Errorf("%s: field %v, want %s", tc.body, body["field"], tc.field)
		}
		if _, saves, _ := f.repo.snapshot(); saves != 0 || f.state.cleared != 0 {
			t.Errorf("%s: a refused save changed something", tc.body)
		}
	}
}

// A setting that is off stops withholding now: the flags are cleared and
// subscriptions refresh at once, not at the next report.
func TestTransitProbeSettingsTurningOffLiftsWithholding(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		stored     func(*ports.UISettings)
	}{
		{"withholding off", `{"settings":{"transit_probe_withhold":false}}`, func(s *ports.UISettings) { s.TransitProbeWithhold = true }},
		{"probing off", `{"settings":{"transit_probe_enabled":false}}`, func(s *ports.UISettings) { s.TransitProbeWithhold = true }},
		{"probing off, withholding was off", `{"settings":{"transit_probe_enabled":false}}`, func(*ports.UISettings) {}},
	} {
		var stored ports.UISettings
		tc.stored(&stored)
		f := newProbeSettingsFixture(stored)
		if code, _ := f.put(tc.body); code != http.StatusOK || f.state.cleared != 1 || f.invalidated != 1 {
			t.Errorf("%s: status %d cleared %d invalidated %d", tc.name, code, f.state.cleared, f.invalidated)
		}
	}
}

// The system settings page keeps these keys exactly as stored, and does not
// carry them at all.
func TestSettingsPutPreservesEveryTransitProbeKey(t *testing.T) {
	stored := storedEverywhere()
	f := newProbeSettingsFixture(stored)
	payload, _ := json.Marshal(map[string]any{
		"login_mode": "local_only", "site_title": "after",
		"transit_probe_enabled": !stored.TransitProbeSettings().TransitProbeEnabled, "transit_probe_interval_seconds": 31,
		"transit_probe_url": "http://other.example/", "transit_probe_withhold": false, "transit_probe_off": false,
	})
	rec := requestRiskPolicy(f.router, http.MethodPut, "/api/admin/settings/ui", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings PUT = %d: %s", rec.Code, rec.Body.String())
	}
	got, saves, _ := f.repo.snapshot()
	if saves != 1 || got.SiteTitle != "after" {
		t.Fatalf("saves %d site_title %q", saves, got.SiteTitle)
	}
	if got.TransitProbeSettings() != stored.TransitProbeSettings() {
		t.Fatalf("the probe settings moved from %+v to %+v", stored.TransitProbeSettings(), got.TransitProbeSettings())
	}
	if tags := jsonTagsWithPrefix(reflect.TypeOf(settingsDTO{}), "transit_probe_"); len(tags) != 0 {
		t.Fatalf("the system settings DTO carries %v", tags)
	}
}

func TestCoarseProbeQualityLeaksNothingBeyondStateAndRTT(t *testing.T) {
	for _, tc := range []struct {
		q       *domain.ProbeQuality
		quality string
		rtt     int
	}{
		{nil, "", 0},
		{&domain.ProbeQuality{State: domain.ProbeUnknown, MedianRTTMS: 40}, "", 0},
		{&domain.ProbeQuality{State: domain.ProbeOK, MedianRTTMS: 40, LossPercent: 3}, "ok", 40},
		{&domain.ProbeQuality{State: domain.ProbeDegraded, MedianRTTMS: 90, LossPercent: 30}, "degraded", 90},
		{&domain.ProbeQuality{State: domain.ProbeDown, MedianRTTMS: 0, Withheld: true}, "down", 0},
	} {
		if quality, rtt := coarseProbeQuality(tc.q); quality != tc.quality || rtt != tc.rtt {
			t.Errorf("coarseProbeQuality(%+v) = %q, %d, want %q, %d", tc.q, quality, rtt, tc.quality, tc.rtt)
		}
	}
	line := domain.RelayLine{Name: "via entry", Enabled: true, Probe: &domain.ProbeQuality{State: domain.ProbeDegraded, MedianRTTMS: 90, LossPercent: 30, Withheld: true}}
	status := relayStatusForNode(&domain.Node{Relays: []domain.RelayLine{line}}, 0)
	raw, _ := json.Marshal(status)
	for _, leaked := range []string{"loss", "withheld", "reported", "30"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("the user's relay status leaks %q: %s", leaked, raw)
		}
	}
	if status.Quality != "degraded" || status.RTTMS != 90 {
		t.Fatalf("status = %+v", status)
	}
}

// scopedProbeSettings gives a plain settings repo the scoped reads the service
// takes; only Load is used.
type scopedProbeSettings struct{ ports.SettingsRepo }

func (scopedProbeSettings) LoadForGroup(context.Context, int64, ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{}, nil
}
func (scopedProbeSettings) LoadForUser(context.Context, *domain.User, ports.UISettings) (ports.UISettings, error) {
	return ports.UISettings{}, nil
}

type probeListRepo struct {
	ports.TransitProbeRepo
	rows []domain.ProbeState
}

func (r probeListRepo) List(context.Context) ([]domain.ProbeState, error) { return r.rows, nil }

type linkEndsStub struct{ ports.TransitLinkRepo }

func (linkEndsStub) LinkEnds(_ context.Context, ids []int64) (map[int64]domain.TransitLinkEnds, error) {
	out := map[int64]domain.TransitLinkEnds{}
	for _, id := range ids {
		if id == 4 {
			out[id] = domain.TransitLinkEnds{EntryPanelID: 5, ExitPanelID: 6}
		}
	}
	return out, nil
}

type forwardsStub struct{ ports.TransitRepo }

func (forwardsStub) ListByIDs(_ context.Context, ids []int64) ([]domain.TransitForward, error) {
	var out []domain.TransitForward
	for _, id := range ids {
		if id == 7 {
			out = append(out, domain.TransitForward{ID: 7, NodeID: 11, RelayID: "a"})
		}
	}
	return out, nil
}

// The admin read endpoint serves every stored row (stale ones as unknown),
// resolved to what the key names, and filters by server.
func TestListProbesResolvesKeysAndFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now()
	rows := []domain.ProbeState{
		{PanelID: 5, ObjectKey: "lnk_4", Kind: "e2e", Attempts: 20, Failures: 20, State: domain.ProbeDown, Withheld: true, ValidUntil: now.Add(time.Hour)},
		{PanelID: 5, ObjectKey: "fwd_7", Kind: "forward", Attempts: 20, Failures: 0, RTTMedianMS: 33, State: domain.ProbeOK, ValidUntil: now.Add(-time.Hour)},
		{PanelID: 8, ObjectKey: "lnk_9", Kind: "transport", Attempts: 20, State: domain.ProbeOK, ValidUntil: now.Add(time.Hour)},
	}
	settings := &policySettingsRepo{}
	svc := linkprobe.New(scopedProbeSettings{settings}, probeListRepo{rows: rows}, nil)
	h := NewAdminTransitProbeHandler(settings, svc, nil, linkEndsStub{}, forwardsStub{}, nil)
	router := gin.New()
	router.GET("/probes", h.ListProbes)

	var out struct {
		Probes []transitProbeItemDTO `json:"probes"`
	}
	rec := requestRiskPolicy(router, http.MethodGet, "/probes?panel_id=5", "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Probes) != 2 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	link, forward := out.Probes[1], out.Probes[0] // sorted by key: fwd_7 before lnk_4
	if forward.Key != "fwd_7" || forward.NodeID != 11 || forward.RelayID != "a" || forward.State != "unknown" || !forward.Stale || forward.Withheld {
		t.Fatalf("forward = %+v: stale rows read unknown and never withhold", forward)
	}
	if link.Key != "lnk_4" || link.EntryPanelID != 5 || link.ExitPanelID != 6 || link.State != "down" || !link.Withheld || link.LossPercent != 100 {
		t.Fatalf("link = %+v", link)
	}
	if rec := requestRiskPolicy(router, http.MethodGet, "/probes", ""); json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Probes) != 3 {
		t.Fatalf("all servers: %d rows", len(out.Probes))
	}
	if rec := requestRiskPolicy(router, http.MethodGet, "/probes?panel_id=x", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad panel_id: %d", rec.Code)
	}
}
