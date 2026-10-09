package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/gin-gonic/gin"
)

var destinationDefaults = map[string]int{
	"dest_hit_retention_days": 30, "dest_trial_retention_days": 7, "dest_usage_retention_days": 7,
	"dest_list_refresh_hours": 24, "dest_policy_apply_min_seconds": 60,
}

func destinationRouter(repo ports.SettingsRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewAdminDestinationSettingsHandler(repo, nil)
	r.GET("/api/admin/dest/settings", h.Get)
	r.PUT("/api/admin/dest/settings", h.Put)
	return r
}
func requestDestinationSettings(t *testing.T, router http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	if method == http.MethodPut {
		body = `{"settings":` + body + `}`
	}
	req := httptest.NewRequest(method, "/api/admin/dest/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func destinationNumbers(t *testing.T, responseBody []byte, want map[string]int) {
	t.Helper()
	var response map[string]json.RawMessage
	if err := json.Unmarshal(responseBody, &response); err != nil {
		t.Fatal(err)
	}
	if nested := response["settings"]; nested != nil {
		if err := json.Unmarshal(nested, &response); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range want {
		var got int
		if raw := response[key]; len(raw) == 0 || string(raw) == "null" {
			t.Fatalf("missing concrete destination setting %s", key)
		} else if err := json.Unmarshal(raw, &got); err != nil || got != value {
			t.Fatalf("%s=%s want %d / %v", key, raw, value, err)
		}
	}
}

func TestAdminDestinationSettingsDefaultsRoundTripAndOmission(t *testing.T) {
	repo := &nodeTaskLifecycleSettingsRepo{}
	router := destinationRouter(repo)
	response := requestDestinationSettings(t, router, http.MethodGet, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET=%d", response.Code)
	}
	destinationNumbers(t, response.Body.Bytes(), map[string]int{"dest_hit_retention_days": 0, "dest_trial_retention_days": 0, "dest_usage_retention_days": 0, "dest_list_refresh_hours": 0, "dest_policy_apply_min_seconds": 0})
	custom := map[string]int{"dest_hit_retention_days": 40, "dest_trial_retention_days": 30, "dest_usage_retention_days": 3, "dest_list_refresh_hours": 6, "dest_policy_apply_min_seconds": 30}
	request := map[string]any{}
	for key, value := range custom {
		request[key] = value
	}
	body, _ := json.Marshal(request)
	response = requestDestinationSettings(t, router, http.MethodPut, string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("PUT=%d: %s", response.Code, response.Body.String())
	}
	destinationNumbers(t, response.Body.Bytes(), custom)
	var diagnostics struct {
		Effective map[string]int `json:"effective"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil || diagnostics.Effective["dest_trial_retention_days"] != 30 {
		t.Fatalf("trial retention did not respect hit window: %+v / %v", diagnostics.Effective, err)
	}
	for _, body := range []string{`{}`, `{"dest_list_refresh_hours":null,"dest_hit_retention_days":null}`, `{"dest_policy_apply_min_seconds":120}`} {
		response = requestDestinationSettings(t, router, http.MethodPut, body)
		if response.Code != http.StatusOK {
			t.Fatalf("compatible update=%d: %s", response.Code, response.Body.String())
		}
		if body == `{"dest_policy_apply_min_seconds":120}` {
			custom["dest_policy_apply_min_seconds"] = 120
		}
		destinationNumbers(t, response.Body.Bytes(), custom)
	}
	destinationNumbers(t, requestDestinationSettings(t, router, http.MethodGet, "").Body.Bytes(), custom)
}

func TestAdminDestinationSettingsRejectInvalidValuesBeforeSaving(t *testing.T) {
	for key, max := range map[string]int{"dest_hit_retention_days": 365, "dest_trial_retention_days": 30, "dest_usage_retention_days": 30, "dest_list_refresh_hours": 168, "dest_policy_apply_min_seconds": 3600} {
		for _, value := range []any{-1, max + 1, "30", 1.5, true, []int{1}} {
			repo := &nodeTaskLifecycleSettingsRepo{}
			body, _ := json.Marshal(map[string]any{key: value})
			response := requestDestinationSettings(t, destinationRouter(repo), http.MethodPut, string(body))
			if response.Code != http.StatusBadRequest || repo.saves != 0 {
				t.Fatalf("%s=%v: HTTP=%d saves=%d", key, value, response.Code, repo.saves)
			}
		}
	}
}

func TestAdminDestinationSettingsZeroUsesDefaultsAndResetsAllValues(t *testing.T) {
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "destination-settings-defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := sqlstore.NewRepos(db).Settings
	router := destinationRouter(repo)
	response := requestDestinationSettings(t, router, http.MethodPut, `{"dest_hit_retention_days":40,"dest_trial_retention_days":30,"dest_usage_retention_days":3,"dest_list_refresh_hours":6,"dest_policy_apply_min_seconds":120}`)
	if response.Code != http.StatusOK {
		t.Fatalf("custom PUT=%d", response.Code)
	}
	response = requestDestinationSettings(t, router, http.MethodPut, `{"dest_trial_retention_days":0}`)
	if response.Code != http.StatusOK {
		t.Fatalf("zero must select default, PUT=%d: %s", response.Code, response.Body.String())
	}
	var answer struct{ Settings, Effective, Defaults map[string]int }
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Settings["dest_trial_retention_days"] != 0 || answer.Effective["dest_trial_retention_days"] != 7 || answer.Settings["dest_hit_retention_days"] != 40 {
		t.Fatalf("unset/default/omitted fields drifted: %+v", answer)
	}
	response = requestDestinationSettings(t, router, http.MethodPut, `{"dest_hit_retention_days":0,"dest_trial_retention_days":0,"dest_usage_retention_days":0,"dest_list_refresh_hours":0,"dest_policy_apply_min_seconds":0}`)
	if response.Code != http.StatusOK {
		t.Fatalf("reset all PUT=%d", response.Code)
	}
	loaded, err := repo.Load(t.Context(), ports.UISettings{})
	if err != nil || loaded.DestinationSettings() != (domain.DestinationSettings{}) {
		t.Fatalf("reset must persist zero defaults: %+v / %v", loaded.DestinationSettings(), err)
	}
	response = requestDestinationSettings(t, router, http.MethodGet, "")
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(answer.Effective, destinationDefaults) || !reflect.DeepEqual(answer.Defaults, destinationDefaults) {
		t.Fatalf("bounded defaults drifted after read: %+v", answer)
	}
}

type destinationFailingSettingsRepo struct {
	nodeTaskLifecycleSettingsRepo
	loads int
	err   error
}

func (r *destinationFailingSettingsRepo) Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	r.loads++
	return r.nodeTaskLifecycleSettingsRepo.Load(ctx, defaults)
}
func (r *destinationFailingSettingsRepo) Save(ctx context.Context, value ports.UISettings) error {
	if r.err != nil {
		return r.err
	}
	return r.nodeTaskLifecycleSettingsRepo.Save(ctx, value)
}

func TestAdminDestinationSettingsDocumentValidationIsAtomic(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"dest_list_refresh_hours":6,"unknown":1}`, `{"dest_list_refresh_hours":6,"dest_policy_apply_min_seconds":"120"}`} {
		repo := &destinationFailingSettingsRepo{}
		response := requestDestinationSettings(t, destinationRouter(repo), http.MethodPut, body)
		if response.Code != http.StatusBadRequest || repo.loads != 0 || repo.saves != 0 {
			t.Fatalf("invalid document touched settings: HTTP=%d loads=%d saves=%d", response.Code, repo.loads, repo.saves)
		}
	}
	repo := &nodeTaskLifecycleSettingsRepo{}
	response := requestDestinationSettings(t, destinationRouter(repo), http.MethodPut, `{"dest_hit_retention_days":1}`)
	var rejected struct {
		Errors map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &rejected); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusBadRequest || repo.saves != 0 || rejected.Errors["dest_trial_retention_days"] == "" || rejected.Errors["dest_hit_retention_days"] == "" {
		t.Fatalf("cross-field violation not attributed to both fields: HTTP=%d saves=%d errors=%v", response.Code, repo.saves, rejected.Errors)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if response := requestDestinationSettings(t, destinationRouter(nil), method, `{}`); response.Code != http.StatusServiceUnavailable {
			t.Fatalf("unavailable settings HTTP=%d", response.Code)
		}
	}
}

func TestAdminDestinationSettingsRefreshNotificationFollowsSuccessfulChange(t *testing.T) {
	initial := ports.UISettings{SiteTitle: "preserve title", RiskMaxDevices: 5}
	initial.SetDestinationSettings(domain.DefaultDestinationSettings())
	repo := &destinationFailingSettingsRepo{nodeTaskLifecycleSettingsRepo: nodeTaskLifecycleSettingsRepo{settings: initial}}
	notifications := 0
	router := gin.New()
	h := NewAdminDestinationSettingsHandler(repo, func() {
		if repo.settings.DestListRefreshHours != 6 {
			t.Fatal("notification preceded committed settings")
		}
		notifications++
	})
	router.PUT("/api/admin/dest/settings", h.Put)
	repo.err = errors.New("settings persistence unavailable")
	if response := requestDestinationSettings(t, router, http.MethodPut, `{"dest_list_refresh_hours":6}`); response.Code != http.StatusInternalServerError || notifications != 0 || !reflect.DeepEqual(repo.settings, initial) {
		t.Fatalf("failed save notified or changed settings: HTTP=%d notices=%d", response.Code, notifications)
	}
	repo.err = nil
	for _, body := range []string{`{"dest_list_refresh_hours":6}`, `{"dest_list_refresh_hours":6}`, `{}`, `{"dest_usage_retention_days":3}`} {
		if response := requestDestinationSettings(t, router, http.MethodPut, body); response.Code != http.StatusOK {
			t.Fatalf("PUT=%d", response.Code)
		}
	}
	if notifications != 1 || repo.saves != 2 || repo.settings.SiteTitle != initial.SiteTitle || repo.settings.RiskMaxDevices != initial.RiskMaxDevices {
		t.Fatalf("refresh/save/ownership drift: notices=%d saves=%d settings=%+v", notifications, repo.saves, repo.settings)
	}
}

func TestAdminDestinationSettingsPersistAndSurviveStaleGeneralSettingsPage(t *testing.T) {
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "destination-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := sqlstore.NewRepos(db).Settings
	for _, body := range []string{`{"dest_hit_retention_days":40,"dest_trial_retention_days":30,"dest_list_refresh_hours":6}`, `{"dest_policy_apply_min_seconds":120}`} {
		if response := requestDestinationSettings(t, destinationRouter(repo), http.MethodPut, body); response.Code != http.StatusOK {
			t.Fatalf("PUT=%d: %s", response.Code, response.Body.String())
		}
	}
	before, err := repo.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	// Older/stale tabs may echo the old values or even unknown JSON shapes.
	for _, body := range []string{`{"login_mode":"local_only","site_title":"changed","dest_list_refresh_hours":24,"dest_policy_apply_min_seconds":60}`, `{"login_mode":"local_only","site_title":"changed","dest_trial_retention_days":"stale","dest_hit_retention_days":0}`} {
		response := requestNodeTaskLifecycleSettings(t, nodeTaskLifecycleSettingsRouter(repo), http.MethodPut, body)
		if response.Code != http.StatusOK {
			t.Fatalf("general settings PUT=%d: %s", response.Code, response.Body.String())
		}
		after, err := repo.Load(t.Context(), ports.UISettings{})
		if err != nil || after.DestinationSettings() != before.DestinationSettings() || after.SiteTitle != "changed" {
			t.Fatalf("general page overwrote owned destination settings: before=%+v after=%+v / %v", before.DestinationSettings(), after.DestinationSettings(), err)
		}
	}
	destinationNumbers(t, requestDestinationSettings(t, destinationRouter(repo), http.MethodGet, "").Body.Bytes(), map[string]int{"dest_hit_retention_days": 40, "dest_trial_retention_days": 30, "dest_usage_retention_days": 0, "dest_list_refresh_hours": 6, "dest_policy_apply_min_seconds": 120})
}
