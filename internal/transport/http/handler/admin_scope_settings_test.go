package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakeScopeGroups struct{ exists map[int64]bool }

func (f fakeScopeGroups) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	if f.exists[id] {
		return &domain.Group{ID: id}, nil
	}
	return nil, domain.ErrNotFound
}

// fakeScopeRepo is an in-memory ports.ScopeSettingsRepo for one scope.
type fakeScopeRepo struct {
	rows map[string]ports.ScopeOverride
}

func newFakeScopeRepo() *fakeScopeRepo { return &fakeScopeRepo{rows: map[string]ports.ScopeOverride{}} }

func (f *fakeScopeRepo) ListOverrides(_ context.Context, _ string, _ int64) ([]ports.ScopeOverride, error) {
	out := make([]ports.ScopeOverride, 0, len(f.rows))
	for _, o := range f.rows {
		out = append(out, o)
	}
	return out, nil
}
func (f *fakeScopeRepo) SetOverride(_ context.Context, _ string, _ int64, o ports.ScopeOverride) error {
	f.rows[o.Type+"."+o.Name] = o
	return nil
}
func (f *fakeScopeRepo) DeleteOverride(_ context.Context, _ string, _ int64, typ, name string) error {
	delete(f.rows, typ+"."+name)
	return nil
}
func (f *fakeScopeRepo) DeleteScope(_ context.Context, _ string, _ int64) error {
	f.rows = map[string]ports.ScopeOverride{}
	return nil
}

func scopeRouter(h *AdminScopeSettingsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/admin/groups/:id/scope-settings", h.Get)
	r.PUT("/api/admin/groups/:id/scope-settings", h.SetOverride)
	r.DELETE("/api/admin/groups/:id/scope-settings/:type/:name", h.DeleteOverride)
	return r
}

func TestScopeSettingsHandler_SetGetDelete(t *testing.T) {
	repo := newFakeScopeRepo()
	h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
	r := scopeRouter(h)

	body, _ := json.Marshal(setScopeOverrideRequest{Type: "security", Name: "totp_enabled", Value: "1"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/groups/5/scope-settings", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d", w.Code)
	}
	var dto scopeSettingsDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dto.Overrides["security.totp_enabled"] != "1" {
		t.Errorf("GET overrides = %+v, want the set value", dto.Overrides)
	}
	if len(dto.Overridable) == 0 {
		t.Error("GET must list the overridable catalog")
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/admin/groups/5/scope-settings/security/totp_enabled", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d", w.Code)
	}
	if _, ok := repo.rows["security.totp_enabled"]; ok {
		t.Error("override must be gone after DELETE (inheritance restored)")
	}
}

func TestScopeSettingsHandler_RejectsNonOverridable(t *testing.T) {
	repo := newFakeScopeRepo()
	h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
	r := scopeRouter(h)
	// jwt_issuer is a real setting but global-only (not in the allowlist).
	body, _ := json.Marshal(setScopeOverrideRequest{Type: "auth", Name: "jwt_issuer", Value: "x"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT non-overridable = %d, want 400", w.Code)
	}
	if len(repo.rows) != 0 {
		t.Error("a rejected override must not be written")
	}
}

// The concurrent-location ignore list is global only: whether an address is
// somebody's relay or office exit does not depend on which group is looking,
// and a per-group copy would let one group's edit leave every other group
// judging the same relay as a place. Absence from OverridableScopeKeys IS the
// mechanism, so this pins the refusal at the write seam: adding the key to
// that set would turn it red.
func TestScopeSettingsHandler_RejectsIgnoreAddresses(t *testing.T) {
	repo := newFakeScopeRepo()
	h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
	r := scopeRouter(h)
	body, _ := json.Marshal(setScopeOverrideRequest{Type: "geo_anomaly", Name: "ignore_addresses", Value: "203.0.113.0/24"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT geo_anomaly.ignore_addresses = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not overridable per group") {
		t.Errorf("the refusal must say why: %s", w.Body.String())
	}
	if len(repo.rows) != 0 {
		t.Error("a rejected override must not be written")
	}
}

// Device capture on /sub is global only, for two reasons that do not depend
// on the group: the public endpoint reads it from the global settings it has
// already loaded (a per-group value would be silently ignored there), and
// whether the panel records a device identifier at all is a panel-wide
// privacy decision. Absence from OverridableScopeKeys is the mechanism; adding
// the key there turns this red.
func TestScopeSettingsHandler_RejectsHWIDCaptureOff(t *testing.T) {
	repo := newFakeScopeRepo()
	h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
	r := scopeRouter(h)
	body, _ := json.Marshal(setScopeOverrideRequest{Type: "risk", Name: "hwid_capture_off", Value: "true"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT risk.hwid_capture_off = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not overridable per group") {
		t.Errorf("the refusal must say why: %s", w.Body.String())
	}
	if len(repo.rows) != 0 {
		t.Error("a rejected override must not be written")
	}
}

// The detector's fleet-wide knobs — the former constants — are global only,
// each for a reason that does not depend on the group: freshness is judged
// per NODE before any user is known, the shared-exit rule counts accounts
// across the whole fleet, the per-poll caps bound one poll and the
// infrastructure cadences one loop. A group value would be stored, shown in
// the group editor and never read. Absence from OverridableScopeKeys is the
// mechanism, so the refusal is pinned at the write seam.
//
// Guard: green on arrival. Adding any one of these keys to
// OverridableScopeKeys turns it red.
func TestScopeSettingsHandler_RejectsGeoRuntimeKeys(t *testing.T) {
	for _, name := range []string{
		"fresh_window_seconds",
		"shared_exit_min_users",
		"ban_max_per_poll",
		"lift_max_per_poll",
		"infra_refresh_minutes",
		"infra_host_ttl_minutes",
	} {
		t.Run(name, func(t *testing.T) {
			repo := newFakeScopeRepo()
			h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
			r := scopeRouter(h)
			body, _ := json.Marshal(setScopeOverrideRequest{Type: "geo_anomaly", Name: name, Value: "2"})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("PUT geo_anomaly.%s = %d, want 400; body=%s", name, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "not overridable per group") {
				t.Errorf("the refusal must say why: %s", w.Body.String())
			}
			if len(repo.rows) != 0 {
				t.Error("a rejected override must not be written")
			}
		})
	}
}

// The eight risk-signal knobs are per-group, the counterpart of the refusal
// above: a group whose members legitimately trip one signal gets that signal
// switched off, or its tolerance raised, without touching the fleet. A knob
// missing from OverridableScopeKeys would be refused here with a 400 while
// the group editor offered it — so every one is written through the handler.
func TestScopeSettingsHandler_AcceptsRiskKnobs(t *testing.T) {
	for name, value := range map[string]string{
		"min_days":          "4",
		"max_devices":       "5",
		"usage_ratio":       "2.5",
		"usage_floor_gb":    "6",
		"sub_spread_off":    "1",
		"devices_off":       "1",
		"usage_shift_off":   "1",
		"login_country_off": "1",
	} {
		t.Run(name, func(t *testing.T) {
			repo := newFakeScopeRepo()
			h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
			r := scopeRouter(h)
			body, _ := json.Marshal(setScopeOverrideRequest{Type: "risk", Name: name, Value: value})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("PUT risk.%s = %d, want 200; body=%s", name, w.Code, w.Body.String())
			}
			if got, ok := repo.rows["risk."+name]; !ok || got.Value != value {
				t.Errorf("risk.%s override not stored as %q: %+v", name, value, repo.rows)
			}
		})
	}
}

// login_country's two thresholds are per-group like the knobs above: a
// group whose members sign in rarely can be judged after fewer earlier
// logins, and a group whose admin wants a new country kept on the table
// longer can hold it longer. Refused here, the group editor would offer a
// knob whose save fails.
func TestScopeSettingsHandler_AcceptsRiskLoginKnobs(t *testing.T) {
	for name, value := range map[string]string{
		"login_warmup_logins": "1",
		"login_hold_days":     "30",
	} {
		t.Run(name, func(t *testing.T) {
			repo := newFakeScopeRepo()
			h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
			r := scopeRouter(h)
			body, _ := json.Marshal(setScopeOverrideRequest{Type: "risk", Name: name, Value: value})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("PUT risk.%s = %d, want 200; body=%s", name, w.Code, w.Body.String())
			}
			if got, ok := repo.rows["risk."+name]; !ok || got.Value != value {
				t.Errorf("risk.%s override not stored as %q: %+v", name, value, repo.rows)
			}
		})
	}
}

// usage_shift's three thresholds are per-group like the knobs above: a group
// whose accounts are onboarded in bulk can be judged after a shorter
// warm-up, and a group whose usage is bursty by nature can need more
// over-days before it is flagged or suspect. Refused here, the group editor
// would offer a knob whose save fails.
func TestScopeSettingsHandler_AcceptsRiskUsageKnobs(t *testing.T) {
	for name, value := range map[string]string{
		"usage_warmup_days":  "7",
		"usage_flag_days":    "5",
		"usage_suspect_days": "3",
	} {
		t.Run(name, func(t *testing.T) {
			repo := newFakeScopeRepo()
			h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
			r := scopeRouter(h)
			body, _ := json.Marshal(setScopeOverrideRequest{Type: "risk", Name: name, Value: value})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("PUT risk.%s = %d, want 200; body=%s", name, w.Code, w.Body.String())
			}
			if got, ok := repo.rows["risk."+name]; !ok || got.Value != value {
				t.Errorf("risk.%s override not stored as %q: %+v", name, value, repo.rows)
			}
		})
	}
}

// The worker's fleet-wide knobs — the former constants — are global only:
// the loop runs once for the fleet on one cadence and one first delay, the
// bell counts the fleet, the fetch window and the login log are each read
// once per run for every account together, usage_shift's baseline and
// judged days are the length of the ONE fleet series every account's fleet
// factor is taken from, and connection_history and flag_records are each
// pruned by one hourly pass over every account's rows. A group value would be
// stored, shown in the group editor and never read, so absence from
// OverridableScopeKeys is pinned at the write seam.
//
// Guard: green on arrival. Adding any one of these keys to
// OverridableScopeKeys turns it red.
func TestScopeSettingsHandler_RejectsRiskRuntimeKeys(t *testing.T) {
	for _, name := range []string{
		"refresh_interval_minutes",
		"first_delay_minutes",
		"alert_freshness_hours",
		"window_days",
		"login_lookback_days",
		"usage_baseline_days",
		"usage_recent_days",
		"connection_retention_days",
		"flag_record_retention_days",
	} {
		t.Run(name, func(t *testing.T) {
			repo := newFakeScopeRepo()
			h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{5: true}}, repo)
			r := scopeRouter(h)
			body, _ := json.Marshal(setScopeOverrideRequest{Type: "risk", Name: name, Value: "30"})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/admin/groups/5/scope-settings", bytes.NewReader(body)))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("PUT risk.%s = %d, want 400; body=%s", name, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "not overridable per group") {
				t.Errorf("the refusal must say why: %s", w.Body.String())
			}
			if len(repo.rows) != 0 {
				t.Error("a rejected override must not be written")
			}
		})
	}
}

func TestScopeSettingsHandler_GroupNotFound(t *testing.T) {
	h := NewAdminScopeSettingsHandler(fakeScopeGroups{exists: map[int64]bool{}}, newFakeScopeRepo())
	r := scopeRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/groups/99/scope-settings", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET missing group = %d, want 404", w.Code)
	}
}

// TestOverridableScopeKeysAreKnown drift-guards the allowlist: every overridable
// key must be a live setting descriptor (else a write would strand on a dead key
// AND the resolver would silently skip the row). Validates the FULL "type.name"
// the resolver/write-gate key on — a name-only check would pass a mistyped type
// prefix (e.g. "sub.allow_user_personal_rules") that no-ops at runtime.
func TestOverridableScopeKeysAreKnown(t *testing.T) {
	known := sqlstore.KnownSettingKeys()
	for key := range ports.OverridableScopeKeys {
		if !strings.Contains(key, ".") {
			t.Errorf("overridable key %q must be \"type.name\"", key)
			continue
		}
		if !known[key] {
			t.Errorf("overridable key %q is not a known setting descriptor (type.name mismatch)", key)
		}
	}
}
