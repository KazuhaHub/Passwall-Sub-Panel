package handler

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const destSettingsPath = "/api/admin/dest/settings"

// destSettingsRouter mounts the access-control settings routes and the system
// settings PUT on one store, as the router does.
func destSettingsRouter(repo ports.SettingsRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := NewAdminDestSettingsHandler(repo)
	router.GET(destSettingsPath, h.Get)
	router.PUT(destSettingsPath, h.Put)
	router.PUT("/api/admin/settings/ui", NewAdminSettingsHandler(repo, nil, nil, nil).Put)
	return router
}

// destSettingsResponse is the settings view as the SPA reads it; settings is
// kept raw so the test sees exactly which keys were served.
type destSettingsResponse struct {
	Settings  map[string]json.RawMessage   `json:"settings"`
	Defaults  map[string]float64           `json:"defaults"`
	Effective ports.AccessControlEffective `json:"effective"`
}

func decodeDestSettings(t *testing.T, code int, body []byte) (destSettingsResponse, ports.AccessControlSettings) {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", code, body)
	}
	var view destSettingsResponse
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	var typed struct {
		Settings ports.AccessControlSettings `json:"settings"`
	}
	if err := json.Unmarshal(body, &typed); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return view, typed.Settings
}

// The page reads everything it needs in one GET: the stored values as stored,
// the shipped default of every number for the empty field's placeholder, and
// what the panel runs with — nothing else from the record.
func TestDestSettingsGet_ServesSettingsDefaultsAndEffective(t *testing.T) {
	stored := storedEverywhere()
	stored.DestPolicyApplyMinSeconds = 5 // below its bound: in effect 30
	stored.DestListRefreshHours = 0      // unset: in effect 24
	stored.DestRulesUpdateMode = "auto"
	repo := &policySettingsRepo{settings: stored}

	rec := requestRiskPolicy(destSettingsRouter(repo), http.MethodGet, destSettingsPath, "")
	view, got := decodeDestSettings(t, rec.Code, rec.Body.Bytes())

	keys := make([]string, 0, len(view.Settings))
	for k := range view.Settings {
		keys = append(keys, k)
	}
	if want := slices.Sorted(slices.Values(ports.AccessControlSettingsKeys())); !slices.Equal(slices.Sorted(slices.Values(keys)), want) {
		t.Errorf("settings keys = %v, want exactly %v", slices.Sorted(slices.Values(keys)), want)
	}
	if got != stored.AccessControlSettings() {
		t.Errorf("settings = %+v, want the stored %+v", got, stored.AccessControlSettings())
	}
	if !reflect.DeepEqual(view.Defaults, ports.AccessControlSettingsDefaults()) {
		t.Errorf("defaults = %v", view.Defaults)
	}
	if want := ports.AccessControlEffectiveOf(stored); view.Effective != want || view.Effective.PolicyApplyMinSeconds != 30 || view.Effective.ListRefreshHours != 24 {
		t.Errorf("effective = %+v, want %+v (clamped 30 s, default 24 h)", view.Effective, want)
	}
	if _, saves, _ := repo.snapshot(); saves != 0 {
		t.Fatalf("a GET saved %d times", saves)
	}
}

// The PUT writes the whole record (the store has no partial save), so it must
// never move a setting it does not own: every other field comes back as stored.
func TestDestSettingsPut_ChangesOnlyDestKeys(t *testing.T) {
	stored := storedEverywhere()
	repo := &policySettingsRepo{settings: stored}
	body := `{"settings":{"dest_policy_apply_min_seconds":120,"dest_list_refresh_hours":12,"dest_rules_update_mode":"manual","dest_hit_retention_days":60,"dest_trial_retention_days":3}}`

	rec := requestRiskPolicy(destSettingsRouter(repo), http.MethodPut, destSettingsPath, body)
	_, echoed := decodeDestSettings(t, rec.Code, rec.Body.Bytes())
	want := ports.AccessControlSettings{DestPolicyApplyMinSeconds: 120, DestListRefreshHours: 12, DestRulesUpdateMode: "manual", DestHitRetentionDays: 60, DestTrialRetentionDays: 3}
	if echoed != want {
		t.Errorf("the answer's settings = %+v, want %+v", echoed, want)
	}
	got, saves, _ := repo.snapshot()
	if saves != 1 || got.AccessControlSettings() != want {
		t.Fatalf("saves %d, stored %+v", saves, got.AccessControlSettings())
	}
	gv, sv := reflect.ValueOf(got), reflect.ValueOf(stored)
	for i := 0; i < gv.NumField(); i++ {
		f := gv.Type().Field(i)
		if isDestTagField(f) {
			continue
		}
		if !reflect.DeepEqual(gv.Field(i).Interface(), sv.Field(i).Interface()) {
			t.Errorf("%s changed from %v to %v: the dest settings PUT does not own it", f.Name, sv.Field(i).Interface(), gv.Field(i).Interface())
		}
	}
}

func isDestTagField(f reflect.StructField) bool {
	return strings.HasPrefix(strings.Split(f.Tag.Get("json"), ",")[0], "dest_")
}

// A page sends only the keys it changed, so a key the body leaves out keeps
// its stored value, and a key sent as 0 is applied as sent (back to default).
func TestDestSettingsPut_OmittedKeysKeepTheirStoredValues(t *testing.T) {
	repo := &policySettingsRepo{settings: ports.UISettings{
		LoginMode: "local_only", DestPolicyApplyMinSeconds: 90, DestListRefreshHours: 12, DestRulesUpdateMode: "auto",
		DestHitRetentionDays: 45,
	}}
	rec := requestRiskPolicy(destSettingsRouter(repo), http.MethodPut, destSettingsPath, `{"settings":{"dest_list_refresh_hours":0}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	got, saves, _ := repo.snapshot()
	if saves != 1 || got.DestListRefreshHours != 0 || got.DestPolicyApplyMinSeconds != 90 || got.DestRulesUpdateMode != "auto" || got.DestHitRetentionDays != 45 || got.LoginMode != "local_only" {
		t.Fatalf("saves %d, stored %+v", saves, got)
	}
}

// Nothing is half-applied: a key out of range, a bad mode, an unknown key, a
// wrongly typed value or a body with no settings object refuses the whole save
// and names the field.
func TestDestSettingsPut_RefusesTheWholeSave(t *testing.T) {
	for name, tc := range map[string]struct{ body, field string }{
		"low debounce":       {`{"settings":{"dest_policy_apply_min_seconds":10}}`, "dest_policy_apply_min_seconds"},
		"high refresh":       {`{"settings":{"dest_list_refresh_hours":500}}`, "dest_list_refresh_hours"},
		"bad mode":           {`{"settings":{"dest_rules_update_mode":"always"}}`, "dest_rules_update_mode"},
		"hit retention high": {`{"settings":{"dest_hit_retention_days":366}}`, "dest_hit_retention_days"},
		"trial retention":    {`{"settings":{"dest_trial_retention_days":31}}`, "dest_trial_retention_days"},
		"negative retention": {`{"settings":{"dest_hit_retention_days":-1}}`, "dest_hit_retention_days"},
		"one bad of two":     {`{"settings":{"dest_list_refresh_hours":24,"dest_policy_apply_min_seconds":1}}`, "dest_policy_apply_min_seconds"},
		"unknown key":        {`{"settings":{"dest_nope":1}}`, ""},
		"foreign key":        {`{"settings":{"risk_min_days":1}}`, ""},
		"wrong type":         {`{"settings":{"dest_list_refresh_hours":"soon"}}`, ""},
		"settings not found": {`{}`, ""},
		"settings a list":    {`{"settings":[1]}`, ""},
	} {
		repo := &policySettingsRepo{settings: ports.UISettings{DestListRefreshHours: 7}}
		rec := requestRiskPolicy(destSettingsRouter(repo), http.MethodPut, destSettingsPath, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, rec.Code, rec.Body.String())
			continue
		}
		if tc.field != "" && !strings.Contains(rec.Body.String(), `"field":"`+tc.field+`"`) {
			t.Errorf("%s: the answer does not name %s: %s", name, tc.field, rec.Body.String())
		}
		if _, saves, _ := repo.snapshot(); saves != 0 {
			t.Errorf("%s: a refused save still saved %d time(s)", name, saves)
		}
	}
}

// ---- the system settings page leaves the dest keys alone ----

// sentDestOtherThan is a body whose every dest key differs from the stored
// one, so a key the settings PUT still read would show up as moved.
func sentDestOtherThan(stored ports.AccessControlSettings) map[string]any {
	return map[string]any{
		"dest_policy_apply_min_seconds": stored.DestPolicyApplyMinSeconds + 7,
		"dest_list_refresh_hours":       stored.DestListRefreshHours + 7,
		"dest_rules_update_mode":        stored.DestRulesUpdateMode + "-sent",
		"dest_hit_retention_days":       stored.DestHitRetentionDays + 7,
		"dest_trial_retention_days":     stored.DestTrialRetentionDays + 7,
	}
}

// Every dest key comes back exactly as stored, whatever the request says,
// while the rest of the save still happens.
func TestSettingsPut_PreservesEveryDestKey(t *testing.T) {
	stored := storedEverywhere()
	stored.DestRulesUpdateMode = "auto"
	repo := &policySettingsRepo{settings: stored}

	body := sentDestOtherThan(stored.AccessControlSettings())
	body["login_mode"] = "local_only"
	body["site_title"] = "after"
	payload, _ := json.Marshal(body)
	rec := requestRiskPolicy(destSettingsRouter(repo), http.MethodPut, "/api/admin/settings/ui", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings PUT = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got, saves, _ := repo.snapshot()
	if saves != 1 || got.SiteTitle != "after" {
		t.Fatalf("saves %d, site_title %q: the rest of the settings must still save", saves, got.SiteTitle)
	}
	if got.AccessControlSettings() != stored.AccessControlSettings() {
		t.Errorf("the dest settings moved from %+v to %+v: the access-control page owns them",
			stored.AccessControlSettings(), got.AccessControlSettings())
	}
}

// Source-level, because a request field read into the saved record is the
// whole regression and a round trip can only sample it. go/ast rather than a
// text search, so a comment that mentions req.Dest... is not a finding.
func TestSettingsPut_NeverReadsDestFieldsFromTheRequest(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "admin_settings.go", nil, 0)
	if err != nil {
		t.Fatalf("parse admin_settings.go: %v", err)
	}
	owned := map[string]bool{}
	pt := reflect.TypeOf(ports.AccessControlSettings{})
	for i := 0; i < pt.NumField(); i++ {
		owned[pt.Field(i).Name] = true
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "req" && owned[sel.Sel.Name] {
			found = append(found, "req."+sel.Sel.Name)
		}
		return true
	})
	slices.Sort(found)
	if found = slices.Compact(found); len(found) > 0 {
		t.Fatalf("the settings PUT reads dest fields off the request: %v", found)
	}
}

// The system settings page does not show these keys: its DTO must not carry
// them, or two pages would offer the same knob.
func TestSettingsDTOCarriesNoDestField(t *testing.T) {
	if got := jsonTagsWithPrefix(reflect.TypeOf(settingsDTO{}), "dest_"); len(got) != 0 {
		t.Errorf("the system settings DTO carries %v: they belong to /dest/settings", got)
	}
}
