package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestBuildDestinationRiskPolicyPersistsPartialWritesAndRejectsPartialDecode(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	call := func(method string, settings map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		var body []byte
		if settings != nil {
			var err error
			body, err = json.Marshal(map[string]any{"settings": settings})
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, "/api/admin/risk-center/policy", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(w, req)
		return w
	}
	w := call("PUT", map[string]any{"risk_dest_block_off": true, "risk_dest_block_threshold": 37})
	var view struct {
		Settings  ports.RiskCenterPolicy `json:"settings"`
		Defaults  map[string]float64     `json:"defaults"`
		Effective map[string]int         `json:"effective"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || !view.Settings.RiskDestBlockOff || view.Settings.RiskDestBlockThreshold != 37 || view.Defaults["risk_dest_block_threshold"] != 20 {
		t.Fatalf("destination policy roundtrip HTTP=%d", w.Code)
	}
	if _, exists := view.Effective["risk_dest_block_threshold"]; exists {
		t.Fatal("policy threshold became runtime knob")
	}
	if w := call("PUT", map[string]any{"risk_dest_block_off": false, "risk_dest_block_threshold": "bad"}); w.Code != 400 {
		t.Fatal("bad destination threshold partly saved")
	}
	stored, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil || !stored.RiskDestBlockOff || stored.RiskDestBlockThreshold != 37 {
		t.Fatal("failed policy decode changed storage")
	}
	w = call("PUT", map[string]any{"risk_dest_block_threshold": 0})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || !view.Settings.RiskDestBlockOff || view.Settings.RiskDestBlockThreshold != 0 {
		t.Fatal("omitted switch did not keep prior value or explicit zero was lost")
	}
	w = call("GET", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || !view.Settings.RiskDestBlockOff || view.Settings.RiskDestBlockThreshold != 0 || view.Defaults["risk_dest_block_threshold"] != 20 {
		t.Fatal("stored destination policy not readable")
	}
}

func TestBuildDestinationRiskPolicyIsReadableButPreservedBySystemSettings(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	stored, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	stored.RiskDestBlockOff, stored.RiskDestBlockThreshold = true, 37
	if err := a.settings.Save(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "PUT"} {
		var body []byte
		if method == "PUT" {
			body = []byte(`{"login_mode":"local_only","site_title":"risk DTO check","risk_dest_block_off":false,"risk_dest_block_threshold":99}`)
		}
		req := httptest.NewRequest(method, "/api/admin/settings/ui", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(w, req)
		var view map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || string(view["risk_dest_block_off"]) != "true" || string(view["risk_dest_block_threshold"]) != "37" {
			t.Fatalf("system settings %s omitted/changed risk policy HTTP=%d", method, w.Code)
		}
	}
	got, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil || !got.RiskDestBlockOff || got.RiskDestBlockThreshold != 37 || got.SiteTitle != "risk DTO check" {
		t.Fatal("system settings changed owned risk policy or skipped unrelated save")
	}
}
