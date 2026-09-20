package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The upgrade endpoints gained fields (target_pinnable, upgrade_mode, targets).
// Additive changes are only additive if the OLD fields are still there and still
// the same shape, and that is a claim a client depends on: a panel that reads
// `can_force` does not care that something new appeared beside it, but it cares a
// great deal if `can_force` changed meaning or went missing.
//
// So this file pins the SHAPES, not the values. It exists because the way an
// additive change breaks someone is by smuggling a rename inside it.

func decodeShape(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	return body
}

// requireKeys asserts each named key is present. Presence is the whole point:
// a renamed field is absent under its old name, which is how this breaks.
func requireKeys(t *testing.T, body map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := body[key]; !ok {
			t.Errorf("response lost the %q field; present keys: %v", key, sortedKeys(body))
		}
	}
}

// requireTypes pins the Go type behind each key. A bool that became a string, or
// a number that became a string, is a rename with the same spelling.
func requireTypes(t *testing.T, body map[string]any, want map[string]string) {
	t.Helper()
	for key, kind := range want {
		value, ok := body[key]
		if !ok {
			continue // absence is reported by requireKeys
		}
		got := "other"
		switch value.(type) {
		case bool:
			got = "bool"
		case string:
			got = "string"
		case float64:
			got = "number"
		}
		if got != kind {
			t.Errorf("%q is a %s, want a %s", key, got, kind)
		}
	}
}

func sortedKeys(body map[string]any) []string {
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func TestUpgradePreviewKeepsItsDocumentedShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &unpinnableUpgradeClient{info: &ports.PanelUpdateInfo{
		CurrentVersion: "3.5.1", LatestVersion: "v3.6.0", UpdateAvailable: true,
	}}
	h := &AdminServersHandler{
		repo: upgradeModeRepo{panel: &domain.XUIPanel{ID: 7, Kind: domain.PanelKind3XUI}},
		pool: fakeWebCertPool{client: client},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/servers/7/upgrade-preview", nil)
	h.UpgradePreview(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeShape(t, recorder)
	requireKeys(t, body,
		"update_available", "current_version", "target_version",
		"compat_status", "compat_message", "psp_min_xui", "psp_max_xui", "can_force",
	)
	// Types, because a field that changed from bool to string is a rename wearing
	// the same name.
	requireTypes(t, body, map[string]string{
		"update_available": "bool",
		"target_version":   "string",
		"compat_status":    "string",
		"can_force":        "bool",
	})
}

func TestUpgradePreviewAlreadyLatestKeepsItsShape(t *testing.T) {
	// The early return is a separate response and its fields are a separate
	// promise — the UI branches on `already_latest` before it reads anything else.
	gin.SetMode(gin.TestMode)
	client := &unpinnableUpgradeClient{info: &ports.PanelUpdateInfo{
		CurrentVersion: "3.6.0", LatestVersion: "v3.6.0", UpdateAvailable: false,
	}}
	h := &AdminServersHandler{
		repo: upgradeModeRepo{panel: &domain.XUIPanel{ID: 7, Kind: domain.PanelKind3XUI}},
		pool: fakeWebCertPool{client: client},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/servers/7/upgrade-preview", nil)
	h.UpgradePreview(c)

	body := decodeShape(t, recorder)
	requireKeys(t, body, "update_available", "current_version", "target_version", "already_latest")
}

func TestUpgradePanelRefusalKeepsItsDocumentedShape(t *testing.T) {
	// A client that reads `reason` to decide whether force is worth trying is
	// reading a contract. reason and can_force both have to survive.
	recorder, _ := coreUpgradeRefusal(t)
	body := decodeShape(t, recorder)
	requireKeys(t, body, "ok", "reason", "latest_version", "compat_status", "psp_min_xui", "psp_max_xui", "message", "can_force")
	if body["ok"] != false {
		t.Fatalf("ok = %v, want false on a refusal", body["ok"])
	}
	if _, ok := body["can_force"].(bool); !ok {
		t.Fatalf("can_force changed type: %T", body["can_force"])
	}
	if _, ok := body["reason"].(string); !ok {
		t.Fatalf("reason changed type: %T", body["reason"])
	}
}

// coreUpgradeRefusal drives the compat gate into refusing an untested target.
func coreUpgradeRefusal(t *testing.T) (*httptest.ResponseRecorder, *upgradeGateAudit) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := &unpinnableUpgradeClient{info: &ports.PanelUpdateInfo{
		CurrentVersion: "3.0.0", LatestVersion: "v3.0.0", UpdateAvailable: true,
	}}
	audit := &upgradeGateAudit{}
	h := &AdminServersHandler{
		repo:  upgradeModeRepo{panel: &domain.XUIPanel{ID: 7, Kind: domain.PanelKind3XUI}},
		pool:  fakeWebCertPool{client: client},
		audit: audit,
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/admin/servers/7/upgrade-panel", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpgradePanel(c)
	return recorder, audit
}
