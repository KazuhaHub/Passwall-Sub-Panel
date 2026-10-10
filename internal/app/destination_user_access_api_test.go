package app

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestBuildDestinationUserAccessReturnsGroupAndExemptionWithoutUsageData(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	path := fmt.Sprintf("users/%d", f.user.ID)
	w := destinationListRequest(t, a, token, "GET", path, nil)
	var view struct {
		Group struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			Mode  string `json:"mode"`
			Stage string `json:"stage"`
		} `json:"group"`
		Exemption any `json:"exemption"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Group.ID != f.group.ID || view.Group.Name != f.group.Name || view.Group.Mode != "open" || view.Group.Stage != "" || view.Exemption != nil {
		t.Fatal("assembled user access endpoint omitted current group/default mode")
	}
	if _, err := a.database.ExecContext(t.Context(), "INSERT INTO dest_group_modes (group_id, mode, stage, list_ids, updated_at) VALUES (?, ?, ?, ?, ?)", f.group.ID, "allowlist", "trial", "[]", time.Now()); err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, a, token, "POST", "exemptions", map[string]any{"user_id": f.user.ID, "reason": "Account access fixture", "expires_at": time.Now().Add(-time.Minute).UnixMilli()})
	if w.Code != 201 {
		t.Fatal("exemption fixture failed")
	}
	before, err := a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, a, token, "GET", path, nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Group.Mode != "allowlist" || view.Group.Stage != "trial" || view.Exemption == nil {
		t.Fatal("user access did not read persisted mode and expired exemption")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["usage_available"]) != "false" || string(fields["usage_nodes"]) != "[]" || string(fields["usage_retention_days"]) != "7" {
		t.Fatal("inactive usage must expose availability/retention without reading usage")
	}
	if string(fields["hits_available"]) != "false" || string(fields["recent_hits"]) == "null" {
		t.Fatal("account hit telemetry must be present without pretending collection has started")
	}
	var exemption struct {
		UserID  int64  `json:"user_id"`
		Expired bool   `json:"expired"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal(fields["exemption"], &exemption) != nil || exemption.UserID != f.user.ID || !exemption.Expired || exemption.Reason != "Account access fixture" {
		t.Fatal("account access hid or changed the expired exemption")
	}
	after, err := a.destDefinitions.State(t.Context())
	if err != nil || after.Generation != before.Generation {
		t.Fatal("user access read mutated definitions")
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{"users/0", 400}, {"users/not-an-id", 400}, {"users/9223372036854775807", 404}, {path + "?usage=24h", 200}, {path + "?usage=8d", 400}} {
		if got := destinationListRequest(t, a, token, "GET", tc.path, nil); got.Code != tc.status {
			t.Fatalf("%s HTTP=%d, want %d", tc.path, got.Code, tc.status)
		}
	}
}
