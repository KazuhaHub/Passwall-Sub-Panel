package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// runtimeMaps reads the two read-only maps off a settings response. Missing
// maps decode as nil, which every caller below treats as a failure.
func runtimeMaps(t *testing.T, body []byte) (effective, defaults map[string]int) {
	t.Helper()
	var out struct {
		Effective map[string]int `json:"runtime_effective"`
		Defaults  map[string]int `json:"runtime_defaults"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode settings response: %v\n%s", err, body)
	}
	return out.Effective, out.Defaults
}

// The settings page shows, beside each geo and risk runtime knob, the value
// the panel runs with and the shipped default, and it carries no copy of
// either (D18): the one definition of each clamp is the domain's. So the
// GET must serve both maps, computed from what is stored — a stored flag
// threshold of 1 is judged as 2 (one day over is a download), and the page
// has to say 2, not echo the 1 it can already read.
func TestSettingsGET_ServesRuntimeEffective(t *testing.T) {
	repo := &nodeTaskLifecycleSettingsRepo{settings: ports.UISettings{
		LoginMode:                  "local_only",
		CronTrafficPullMinutes:     5,
		RiskUsageFlagDays:          1,
		RiskRefreshIntervalMinutes: 1440,
		RiskAlertFreshnessHours:    1,
	}}
	res := requestNodeTaskLifecycleSettings(t, nodeTaskLifecycleSettingsRouter(repo), http.MethodGet, "")
	if res.Code != http.StatusOK {
		t.Fatalf("GET status %d: %s", res.Code, res.Body.String())
	}
	eff, def := runtimeMaps(t, res.Body.Bytes())
	wantEff, wantDef := ports.RuntimeEffective(repo.settings)
	if len(eff) != 23 || len(def) != 23 {
		t.Fatalf("runtime_effective has %d keys and runtime_defaults %d; want the 23 runtime knobs in each", len(eff), len(def))
	}
	for k, v := range wantEff {
		if eff[k] != v {
			t.Errorf("runtime_effective[%s] = %d, want %d", k, eff[k], v)
		}
	}
	for k, v := range wantDef {
		if def[k] != v {
			t.Errorf("runtime_defaults[%s] = %d, want %d", k, def[k], v)
		}
	}
	if eff["risk_usage_flag_days"] != 2 || def["risk_usage_flag_days"] != 4 {
		t.Errorf("flag days: in effect %d (want 2, the floor), default %d (want 4)", eff["risk_usage_flag_days"], def["risk_usage_flag_days"])
	}
	if eff["risk_alert_freshness_hours"] != 48 {
		t.Errorf("bell freshness in effect %d h, want 48 (two daily refreshes)", eff["risk_alert_freshness_hours"])
	}
	// The stored value is still served as stored: the field shows what the
	// admin typed, the caption what it became.
	var dto map[string]json.RawMessage
	_ = json.Unmarshal(res.Body.Bytes(), &dto)
	if string(dto["risk_usage_flag_days"]) != "1" {
		t.Errorf("risk_usage_flag_days served as %s, want the stored 1", dto["risk_usage_flag_days"])
	}
	if repo.saves != 0 {
		t.Fatal("GET wrote the settings")
	}
}

// The maps are output only. The SPA posts back the whole object it read,
// maps included, and an older tab may hold maps of another shape: neither
// may change what is saved or fail the save. The response carries maps
// recomputed from what was just saved, so the page shows the new values
// in effect without a second read.
func TestSettingsPUT_IgnoresRuntimeEffective(t *testing.T) {
	for name, maps := range map[string]map[string]any{
		"stale numbers": {
			"runtime_effective": map[string]int{"risk_usage_flag_days": 99, "risk_window_days": 1},
			"runtime_defaults":  map[string]int{"risk_usage_flag_days": 98},
		},
		"another shape": {
			"runtime_effective": "garbage",
			"runtime_defaults":  []int{1, 2, 3},
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &nodeTaskLifecycleSettingsRepo{settings: ports.UISettings{LoginMode: "local_only"}}
			body := map[string]any{
				"login_mode":             "local_only",
				"risk_usage_flag_days":   3,
				"risk_usage_recent_days": 0,
			}
			for k, v := range maps {
				body[k] = v
			}
			payload, _ := json.Marshal(body)
			res := requestNodeTaskLifecycleSettings(t, nodeTaskLifecycleSettingsRouter(repo), http.MethodPut, string(payload))
			if res.Code != http.StatusOK {
				t.Fatalf("PUT status %d: %s", res.Code, res.Body.String())
			}
			if repo.saves != 1 || repo.settings.RiskUsageFlagDays != 3 || repo.settings.RiskWindowDays != 0 {
				t.Fatalf("saved flag days %d, window %d (saves %d); want the 3 that was sent and the window untouched",
					repo.settings.RiskUsageFlagDays, repo.settings.RiskWindowDays, repo.saves)
			}
			eff, def := runtimeMaps(t, res.Body.Bytes())
			if eff["risk_usage_flag_days"] != 3 || eff["risk_window_days"] != 7 || def["risk_usage_flag_days"] != 4 {
				t.Errorf("response maps: flag in effect %d (want 3), window %d (want 7), flag default %d (want 4)",
					eff["risk_usage_flag_days"], eff["risk_window_days"], def["risk_usage_flag_days"])
			}
		})
	}
}
