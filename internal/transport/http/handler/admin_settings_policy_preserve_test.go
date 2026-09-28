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

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The risk policy has one writer: PUT /risk-center/policy. The system
// settings page still loads the whole record (and a tab opened before the
// policy moved still posts every geo_anomaly_* and risk_* key it read), but
// its save writes back the policy it LOADED, never the one it was sent. Two
// writers for one key would let a stale settings tab silently revert what an
// admin just set on the policy page — the page that owns the key would lose
// to the page that only happens to carry it.

// sentPolicyOtherThan is a policy body whose every key differs from the
// stored one, so a key the settings PUT still read would show up as moved.
func sentPolicyOtherThan(stored ports.RiskCenterPolicy) map[string]any {
	body := map[string]any{}
	st := reflect.TypeOf(stored)
	sv := reflect.ValueOf(stored)
	for i := 0; i < st.NumField(); i++ {
		tag := strings.Split(st.Field(i).Tag.Get("json"), ",")[0]
		switch f := sv.Field(i); f.Kind() {
		case reflect.String:
			body[tag] = f.String() + "-sent"
		case reflect.Bool:
			body[tag] = !f.Bool()
		case reflect.Int:
			body[tag] = f.Int() + 7
		case reflect.Float64:
			body[tag] = f.Float() + 0.25
		default:
			panic("unhandled policy field kind " + f.Kind().String())
		}
	}
	// A list the old settings save would have parsed and accepted, so the
	// test fails on "saved what was sent", not on the ignore-list check.
	body["geo_anomaly_ignore_addresses"] = "198.51.100.0/24"
	return body
}

// Every policy key comes back exactly as stored, whatever the request says,
// while the rest of the save still happens — and the answer shows the kept
// values (D8), so the page never displays a policy that was not saved.
func TestSettingsPut_PreservesEveryPolicyKey(t *testing.T) {
	stored := storedEverywhere()
	repo := &policySettingsRepo{settings: stored}

	body := sentPolicyOtherThan(stored.RiskCenterPolicy())
	body["login_mode"] = "local_only"
	body["site_title"] = "after"
	payload, _ := json.Marshal(body)
	rec := requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, "/api/admin/settings/ui", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings PUT = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	got, saves, _ := repo.snapshot()
	if saves != 1 || got.SiteTitle != "after" {
		t.Fatalf("saves %d, site_title %q: the rest of the settings must still save", saves, got.SiteTitle)
	}
	if got.RiskCenterPolicy() != stored.RiskCenterPolicy() {
		gv, sv := reflect.ValueOf(got.RiskCenterPolicy()), reflect.ValueOf(stored.RiskCenterPolicy())
		for i := 0; i < gv.NumField(); i++ {
			if gv.Field(i).Interface() != sv.Field(i).Interface() {
				t.Errorf("%s moved from %v to %v: the risk center owns it", gv.Type().Field(i).Name, sv.Field(i).Interface(), gv.Field(i).Interface())
			}
		}
	}

	var answer map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantJSON, _ := json.Marshal(stored.RiskCenterPolicy())
	var want map[string]json.RawMessage
	_ = json.Unmarshal(wantJSON, &want)
	for _, key := range ports.RiskCenterPolicyKeys() {
		if string(answer[key]) != string(want[key]) {
			t.Errorf("answer %s = %s, want the kept %s", key, answer[key], want[key])
		}
	}
}

// Source-level, because a request field read into the saved record is the
// whole regression, and a round trip can only sample it: a new policy key
// copied off the request by habit would pass every value-based test that
// happens not to set it. go/ast rather than a text search, so a comment or
// a string that mentions req.Risk… is not a finding and a selector split
// across lines still is.
func TestSettingsPut_NeverReadsPolicyFieldsFromTheRequest(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "admin_settings.go", nil, 0)
	if err != nil {
		t.Fatalf("parse admin_settings.go: %v", err)
	}
	policy := map[string]bool{}
	pt := reflect.TypeOf(ports.RiskCenterPolicy{})
	for i := 0; i < pt.NumField(); i++ {
		policy[pt.Field(i).Name] = true
	}
	if len(policy) != len(ports.RiskCenterPolicyKeys()) {
		t.Fatalf("RiskCenterPolicy has %d fields but %d keys", len(policy), len(ports.RiskCenterPolicyKeys()))
	}

	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "req" && policy[sel.Sel.Name] {
			found = append(found, "req."+sel.Sel.Name)
		}
		return true
	})
	slices.Sort(found)
	if found = slices.Compact(found); len(found) > 0 {
		t.Fatalf("the settings PUT reads policy fields off the request: %v", found)
	}
}

// The ignore list used to be validated by this page too. It no longer saves
// the list, so it must no longer refuse over it: a list that does not parse
// (stored before the check existed, or written by hand) came back in every
// settings save and made the whole page unsavable over a field it does not
// own. The list stays exactly as stored, for the policy page to fix.
func TestSettingsPut_AStoredBadIgnoreListNoLongerBlocksSaves(t *testing.T) {
	const bad = "203.0.113.7\n1.2.3.999"
	repo := &policySettingsRepo{settings: ports.UISettings{LoginMode: "local_only", GeoAnomalyIgnoreAddresses: bad}}

	payload, _ := json.Marshal(map[string]any{
		"login_mode":                   "local_only",
		"site_title":                   "after",
		"geo_anomaly_ignore_addresses": bad, // what the page read, sent back
	})
	rec := requestRiskPolicy(riskPolicyRouter(repo), http.MethodPut, "/api/admin/settings/ui", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings PUT = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got, saves, _ := repo.snapshot()
	if saves != 1 || got.SiteTitle != "after" {
		t.Fatalf("saves %d, site_title %q: want the settings saved", saves, got.SiteTitle)
	}
	if got.GeoAnomalyIgnoreAddresses != bad {
		t.Fatalf("ignore list = %q, want it kept as stored: %q", got.GeoAnomalyIgnoreAddresses, bad)
	}
}
