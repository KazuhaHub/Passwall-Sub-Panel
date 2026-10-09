package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const riskPolicyPath = "/api/admin/risk-center/policy"

// policySettingsRepo is the settings store both settings writers share. It
// logs every load and save in order, remembers the defaults it was loaded
// with, and — when block is set — holds the first Save until block closes,
// announcing on blocked that it is holding.
type policySettingsRepo struct {
	mu           sync.Mutex
	settings     ports.UISettings
	saves        int
	events       []string
	lastDefaults ports.UISettings

	block   chan struct{}
	blocked chan struct{}
}

func (r *policySettingsRepo) Load(_ context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "load")
	r.lastDefaults = defaults
	return r.settings, nil
}

func (r *policySettingsRepo) Save(_ context.Context, s ports.UISettings) error {
	r.mu.Lock()
	first := r.saves == 0
	r.saves++
	r.events = append(r.events, "save")
	r.mu.Unlock()
	if first && r.block != nil {
		close(r.blocked)
		<-r.block
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings = s
	r.events = append(r.events, "saved")
	return nil
}

func (r *policySettingsRepo) snapshot() (ports.UISettings, int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.settings, r.saves, slices.Clone(r.events)
}

// riskPolicyRouter mounts the policy routes and the system settings PUT on
// one store, as the router does.
func riskPolicyRouter(repo ports.SettingsRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	policy := NewAdminRiskPolicyHandler(repo)
	router.GET(riskPolicyPath, policy.Get)
	router.PUT(riskPolicyPath, policy.Put)
	router.PUT("/api/admin/settings/ui", NewAdminSettingsHandler(repo, nil, nil, nil).Put)
	return router
}

func requestRiskPolicy(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// riskPolicyResponse is the policy view as the SPA reads it; settings is
// kept raw so the test sees exactly which keys were served.
type riskPolicyResponse struct {
	Settings  map[string]json.RawMessage `json:"settings"`
	Defaults  map[string]float64         `json:"defaults"`
	Effective map[string]int             `json:"effective"`
}

func decodeRiskPolicy(t *testing.T, rec *httptest.ResponseRecorder) (riskPolicyResponse, ports.RiskCenterPolicy) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var view riskPolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	var typed struct {
		Settings ports.RiskCenterPolicy `json:"settings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &typed); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return view, typed.Settings
}

// isRiskPolicyField says whether a UISettings field belongs to the policy,
// by the key list the endpoint serves.
func isRiskPolicyField(f reflect.StructField) bool {
	return slices.Contains(ports.RiskCenterPolicyKeys(), strings.Split(f.Tag.Get("json"), ",")[0])
}

// storedEverywhere is a stored settings record whose every scalar field
// holds a value of its own — the ones the policy does not own included —
// so a save that moves anything it should not shows up as a difference.
func storedEverywhere() ports.UISettings {
	var s ports.UISettings
	v := reflect.ValueOf(&s).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("stored-" + v.Type().Field(i).Name)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int, reflect.Int64:
			f.SetInt(int64(1000 + i))
		case reflect.Float64:
			f.SetFloat(float64(i) + 0.5)
		}
	}
	s.QuickLinks = []ports.QuickLink{{Label: "stored", URL: "https://example.test/"}}
	// A stored list the policy PUT must accept when it is resent unchanged.
	s.GeoAnomalyIgnoreAddresses = "203.0.113.7"
	return s
}

// The page reads everything it needs in one GET: the 48 stored values as
// stored (0 = unset, and whatever an admin typed), the shipped default of
// every number to show in an empty field, and what the runtime knobs became
// in effect for the stored values — nothing else from the settings, so the
// page cannot come to depend on a key it does not own.
func TestRiskPolicyGet_ServesSettingsDefaultsAndEffective(t *testing.T) {
	stored := storedEverywhere()
	stored.CronTrafficPullMinutes = 10
	stored.RiskRefreshIntervalMinutes = 5 // below its bound: in effect 10
	stored.RiskUsageFlagDays = 0          // unset: in effect the default
	repo := &policySettingsRepo{settings: stored}

	view, pol := decodeRiskPolicy(t, requestRiskPolicy(riskPolicyRouter(repo), http.MethodGet, riskPolicyPath, ""))

	keys := make([]string, 0, len(view.Settings))
	for k := range view.Settings {
		keys = append(keys, k)
	}
	if want := slices.Sorted(slices.Values(ports.RiskCenterPolicyKeys())); !slices.Equal(slices.Sorted(slices.Values(keys)), want) {
		t.Errorf("settings keys = %v\nwant exactly the policy keys %v", slices.Sorted(slices.Values(keys)), want)
	}
	if pol != stored.RiskCenterPolicy() {
		t.Errorf("settings = %+v\nwant the stored policy %+v", pol, stored.RiskCenterPolicy())
	}
	if !reflect.DeepEqual(view.Defaults, ports.RiskCenterPolicyDefaults()) {
		t.Errorf("defaults = %v\nwant %v", view.Defaults, ports.RiskCenterPolicyDefaults())
	}
	effective, _ := ports.RuntimeEffective(stored)
	if !reflect.DeepEqual(view.Effective, effective) {
		t.Errorf("effective = %v\nwant RuntimeEffective of the stored settings %v", view.Effective, effective)
	}
	if view.Effective["risk_refresh_interval_minutes"] != 10 {
		t.Errorf("effective refresh interval = %d, want the clamped 10", view.Effective["risk_refresh_interval_minutes"])
	}
	if _, saves, _ := repo.snapshot(); saves != 0 {
		t.Fatalf("a GET saved %d times", saves)
	}
}

// The policy PUT writes the whole settings record (the store has no partial
// save), so what it must never do is move a setting it does not own: every
// other field comes back exactly as stored, and every policy field exactly
// as sent.
func TestRiskPolicyPut_ChangesOnlyPolicyKeys(t *testing.T) {
	stored := storedEverywhere()
	repo := &policySettingsRepo{settings: stored}

	var sent ports.RiskCenterPolicy
	sv := reflect.ValueOf(&sent).Elem()
	for i := 0; i < sv.NumField(); i++ {
		f := sv.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("sent" + strings.Repeat("x", i))
		case reflect.Bool:
			f.SetBool(false) // stored true everywhere
		case reflect.Int:
			f.SetInt(int64(2 + i))
		case reflect.Float64:
			f.SetFloat(float64(i) + 0.75)
		}
	}
	sent.GeoAnomalyIgnoreAddresses = "198.51.100.0/24"
	body, _ := json.Marshal(map[string]any{"settings": sent})

	_, echoed := decodeRiskPolicy(t, requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, riskPolicyPath, string(body)))
	if echoed != sent {
		t.Errorf("the answer's settings = %+v\nwant what was saved %+v", echoed, sent)
	}
	got, saves, _ := repo.snapshot()
	if saves != 1 {
		t.Fatalf("saves = %d, want 1", saves)
	}
	if got.RiskCenterPolicy() != sent {
		t.Errorf("stored policy = %+v\nwant %+v", got.RiskCenterPolicy(), sent)
	}
	gv, stv := reflect.ValueOf(got), reflect.ValueOf(stored)
	for i := 0; i < gv.NumField(); i++ {
		f := gv.Type().Field(i)
		if isRiskPolicyField(f) {
			continue
		}
		if !reflect.DeepEqual(gv.Field(i).Interface(), stv.Field(i).Interface()) {
			t.Errorf("%s changed from %v to %v: the policy PUT does not own it", f.Name, stv.Field(i).Interface(), gv.Field(i).Interface())
		}
	}
}

// The page PUTs only the keys it changed (D10), so a key the body leaves
// out keeps its stored value — a stale tab saving one field can never
// revert what another admin set on another — and a key sent as false or 0
// is applied as sent, not taken for "left out".
func TestRiskPolicyPut_OmittedKeysKeepTheirStoredValues(t *testing.T) {
	repo := &policySettingsRepo{settings: ports.UISettings{
		LoginMode:            "local_only",
		RiskMinDays:          4,
		RiskMaxDevices:       5,
		GeoAnomalyCoTravel:   "JP,TW",
		GeoAnomalyBanEnabled: true,
		RiskUsageRatio:       2.5,
	}}

	_, echoed := decodeRiskPolicy(t, requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, riskPolicyPath,
		`{"settings":{"risk_min_days":2,"geo_anomaly_ban_enabled":false,"risk_usage_ratio":0}}`))

	got, saves, _ := repo.snapshot()
	if saves != 1 {
		t.Fatalf("saves = %d, want 1", saves)
	}
	if got.RiskMinDays != 2 || got.GeoAnomalyBanEnabled || got.RiskUsageRatio != 0 {
		t.Errorf("sent keys not applied as sent: min_days %d, ban_enabled %v, usage_ratio %v", got.RiskMinDays, got.GeoAnomalyBanEnabled, got.RiskUsageRatio)
	}
	if got.RiskMaxDevices != 5 || got.GeoAnomalyCoTravel != "JP,TW" || got.LoginMode != "local_only" {
		t.Errorf("omitted keys lost their stored values: max_devices %d, co_travel %q, login_mode %q", got.RiskMaxDevices, got.GeoAnomalyCoTravel, got.LoginMode)
	}
	if echoed != got.RiskCenterPolicy() {
		t.Errorf("the answer = %+v\nwant the saved policy %+v", echoed, got.RiskCenterPolicy())
	}
}

// Nothing is half-applied. json.Unmarshal fills the keys before a bad one
// and reports the bad one at the end, so a body that decodes only partly
// must still save nothing; a body with no settings object is not a policy.
func TestRiskPolicyPut_ADecodeErrorSavesNothing(t *testing.T) {
	for name, body := range map[string]string{
		"a string for a number after valid keys": `{"settings":{"risk_min_days":2,"risk_max_devices":"five","risk_usage_floor_gb":9}}`,
		"a number for a switch":                  `{"settings":{"risk_devices_off":1}}`,
		"settings absent":                        `{"risk_min_days":2}`,
		"settings null":                          `{"settings":null}`,
		"settings an array":                      `{"settings":[{"risk_min_days":2}]}`,
		"settings a string":                      `{"settings":"risk_min_days=2"}`,
		"malformed JSON":                         `{"settings":{"risk_min_days":2`,
		"no body":                                "",
	} {
		t.Run(name, func(t *testing.T) {
			stored := ports.UISettings{LoginMode: "local_only", RiskMinDays: 4, RiskMaxDevices: 5, RiskUsageFloorGB: 3}
			repo := &policySettingsRepo{settings: stored}
			rec := requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, riskPolicyPath, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			got, saves, _ := repo.snapshot()
			if saves != 0 || !reflect.DeepEqual(got, stored) {
				t.Fatalf("a refused body wrote something: saves=%d stored=%+v", saves, got)
			}
		})
	}
}

// The ignore list is the one policy value validated on save: a typo in it
// fails OPEN (the relay it was meant to cover keeps accusing people), and
// nothing downstream repairs it. So the save is refused whole, and the
// answer names the field and every bad entry, so the page can mark that
// field and show the admin which lines to fix.
func TestRiskPolicyPut_RejectsABadIgnoreListNamingIt(t *testing.T) {
	stored := ports.UISettings{LoginMode: "local_only", RiskMinDays: 4}
	repo := &policySettingsRepo{settings: stored}
	body, _ := json.Marshal(map[string]any{"settings": map[string]any{
		"risk_min_days":                2,
		"geo_anomaly_ignore_addresses": "203.0.113.7 # office\n1.2.3.999, 198.51.100.0/24\n10.0.0.0/33",
	}})
	rec := requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, riskPolicyPath, string(body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Error string   `json:"error"`
		Field string   `json:"field"`
		Bad   []string `json:"bad"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if got.Field != "geo_anomaly_ignore_addresses" {
		t.Errorf("field = %q, want geo_anomaly_ignore_addresses", got.Field)
	}
	if want := []string{"1.2.3.999", "10.0.0.0/33"}; !slices.Equal(got.Bad, want) {
		t.Errorf("bad = %v, want %v", got.Bad, want)
	}
	for _, bad := range []string{"1.2.3.999", "10.0.0.0/33"} {
		if !strings.Contains(got.Error, bad) {
			t.Errorf("the error must name %s: %q", bad, got.Error)
		}
	}
	if after, saves, _ := repo.snapshot(); saves != 0 || !reflect.DeepEqual(after, stored) {
		t.Fatalf("a refused list wrote something: saves=%d stored=%+v", saves, after)
	}
}

// The three texts are stored trimmed, as the system settings page stored
// them, so the same list typed on either page compares equal and a scope
// with a stray space is not an unrecognised one.
func TestRiskPolicyPut_TrimsTheThreeTexts(t *testing.T) {
	repo := &policySettingsRepo{settings: ports.UISettings{LoginMode: "local_only"}}
	body, _ := json.Marshal(map[string]any{"settings": map[string]any{
		"geo_anomaly_scope":            " region \n",
		"geo_anomaly_co_travel":        "\n  JP,TW\nDE,AT  \n",
		"geo_anomaly_ignore_addresses": "  203.0.113.7 # office\n198.51.100.0/24 \n",
	}})
	decodeRiskPolicy(t, requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, riskPolicyPath, string(body)))
	got, _, _ := repo.snapshot()
	if got.GeoAnomalyScope != "region" || got.GeoAnomalyCoTravel != "JP,TW\nDE,AT" ||
		got.GeoAnomalyIgnoreAddresses != "203.0.113.7 # office\n198.51.100.0/24" {
		t.Fatalf("stored scope %q, co_travel %q, ignore %q: want each trimmed and otherwise as typed",
			got.GeoAnomalyScope, got.GeoAnomalyCoTravel, got.GeoAnomalyIgnoreAddresses)
	}
}

// A client that sends the whole view back — defaults and effective beside
// its settings, or a read-only map inside them — changes only the settings
// it sent: defaults and the values in effect are the server's to state.
func TestRiskPolicyPut_IgnoresEffectiveAndDefaults(t *testing.T) {
	repo := &policySettingsRepo{settings: ports.UISettings{LoginMode: "local_only"}}
	body := `{"settings":{"risk_usage_flag_days":3,"runtime_effective":{"risk_usage_flag_days":9},"effective":{"risk_window_days":2}},` +
		`"effective":{"risk_usage_flag_days":9,"risk_window_days":2},"defaults":{"risk_usage_flag_days":7,"risk_min_days":1}}`
	view, _ := decodeRiskPolicy(t, requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, riskPolicyPath, body))
	got, _, _ := repo.snapshot()
	want := ports.UISettings{LoginMode: "local_only", RiskUsageFlagDays: 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored %+v\nwant only risk_usage_flag_days set", got)
	}
	if !reflect.DeepEqual(view.Defaults, ports.RiskCenterPolicyDefaults()) {
		t.Errorf("defaults = %v, want the shipped ones", view.Defaults)
	}
	if view.Effective["risk_usage_flag_days"] != 3 || view.Effective["risk_window_days"] != 7 {
		t.Errorf("effective flag days %d, window %d: want 3 and 7, from what was saved", view.Effective["risk_usage_flag_days"], view.Effective["risk_window_days"])
	}
}

// The policy PUT saves the WHOLE record, so on an install that never saved
// the settings page it must load with that page's defaults: loaded with
// none, a first policy save would persist an empty login mode and site
// title as if an admin had chosen them.
func TestRiskPolicyPut_LoadsWithTheSettingsPageDefaults(t *testing.T) {
	repo := &policySettingsRepo{}
	decodeRiskPolicy(t, requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, riskPolicyPath, `{"settings":{"risk_min_days":2}}`))
	repo.mu.Lock()
	got := repo.lastDefaults
	repo.mu.Unlock()
	if want := (&AdminSettingsHandler{}).defaults(); !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded with defaults %+v\nwant the settings page's %+v", got, want)
	}
}

// Both settings writers load the whole record, change their part and save
// the whole record back. Two saves interleaving would each write back the
// other's part as it was before: the second save silently reverts the
// first. They are serialized by one lock (D6): here the system settings PUT
// is held inside its Save while a policy PUT arrives, and the policy PUT
// must not even load until that save is done — so the record ends with
// BOTH changes.
func TestSettingsWritesAreSerialized(t *testing.T) {
	repo := &policySettingsRepo{
		settings: ports.UISettings{LoginMode: "local_only", SiteTitle: "before", RiskMaxDevices: 3},
		block:    make(chan struct{}),
		blocked:  make(chan struct{}),
	}
	router := riskPolicyRouter(repo)

	var wg sync.WaitGroup
	var settingsRec, policyRec *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		// An older system page still sends the policy keys it holds (this
		// tab's copy says max_devices 3). The settings PUT ignores them but
		// writes back the policy it LOADED, so unserialized it would still
		// put the 3 back over the policy PUT's 5.
		settingsRec = requestRiskPolicy(router, http.MethodPut, "/api/admin/settings/ui",
			`{"login_mode":"local_only","site_title":"after","risk_max_devices":3}`)
	}()
	select {
	case <-repo.blocked:
	case <-time.After(5 * time.Second):
		close(repo.block)
		wg.Wait()
		t.Fatalf("the settings PUT never reached its save: %d %s", settingsRec.Code, settingsRec.Body.String())
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		policyRec = requestRiskPolicy(router, http.MethodPut, riskPolicyPath, `{"settings":{"risk_max_devices":5}}`)
	}()
	// Every chance for an unserialized policy PUT to load the old record.
	time.Sleep(100 * time.Millisecond)
	_, _, during := repo.snapshot()
	close(repo.block)
	wg.Wait()

	if settingsRec.Code != http.StatusOK || policyRec.Code != http.StatusOK {
		t.Fatalf("settings PUT %d %s; policy PUT %d %s", settingsRec.Code, settingsRec.Body.String(), policyRec.Code, policyRec.Body.String())
	}
	if want := []string{"load", "save"}; !slices.Equal(during, want) {
		t.Errorf("while the settings save was held the store saw %v, want %v: the policy PUT loaded a record about to be overwritten", during, want)
	}
	got, saves, events := repo.snapshot()
	// The settings writer rereads publication metadata after its save while
	// still holding the shared lock. Only then may the policy writer load the
	// updated record and save its change.
	if want := []string{"load", "save", "saved", "load", "load", "save", "saved"}; !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
	if saves != 2 || got.SiteTitle != "after" || got.RiskMaxDevices != 5 {
		t.Fatalf("final record: saves %d, site_title %q, max_devices %d — want both changes (after, 5)", saves, got.SiteTitle, got.RiskMaxDevices)
	}
}
