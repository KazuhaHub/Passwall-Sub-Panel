package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type destinationExemptionAPIResult struct {
	UserID       int64   `json:"user_id"`
	UPN          *string `json:"upn"`
	Reason       string  `json:"reason"`
	CreatedBy    int64   `json:"created_by"`
	CreatedByUPN *string `json:"created_by_upn"`
	CreatedAt    int64   `json:"created_at"`
	ExpiresAt    *int64  `json:"expires_at"`
	Expired      bool    `json:"expired"`
}

func TestBuildDestinationExemptionCRUDAndExpiredVisibility(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	past := time.Now().Add(-time.Minute).UnixMilli()
	input := map[string]any{"user_id": f.user.ID, "reason": "Temporary debugging", "expires_at": past}
	w := destinationListRequest(t, a, token, "POST", "exemptions", input)
	var created destinationExemptionAPIResult
	if err := json.Unmarshal(w.Body.Bytes(), &created); w.Code != 201 || err != nil || created.UserID != f.user.ID || created.UPN == nil || *created.UPN != f.user.UPN || created.CreatedBy <= 0 || created.CreatedByUPN == nil || *created.CreatedByUPN != "destination-admin@example.test" || created.CreatedAt <= 0 || created.ExpiresAt == nil || *created.ExpiresAt != past || !created.Expired {
		t.Fatalf("exemption create omitted identity/UTC expiry: HTTP=%d", w.Code)
	}
	w = destinationListRequest(t, a, token, "POST", "exemptions", input)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "dest_exemption_exists") {
		t.Fatal("duplicate exemption lacked conflict code")
	}
	w = destinationListRequest(t, a, token, "GET", "exemptions", nil)
	var listed struct {
		Items []destinationExemptionAPIResult `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); w.Code != 200 || err != nil || len(listed.Items) != 1 || !listed.Items[0].Expired {
		t.Fatal("expired exemption disappeared before durable cleanup")
	}
	path := fmt.Sprintf("exemptions/%d", f.user.ID)
	w = destinationListRequest(t, a, token, "GET", path, nil)
	var detail destinationExemptionAPIResult
	if err := json.Unmarshal(w.Body.Bytes(), &detail); w.Code != 200 || err != nil || detail.CreatedBy != created.CreatedBy {
		t.Fatal("single exemption unavailable")
	}
	w = destinationListRequest(t, a, token, "PUT", path, map[string]any{"user_id": f.user.ID, "reason": "Permanent debugging", "expires_at": nil})
	if err := json.Unmarshal(w.Body.Bytes(), &detail); w.Code != 200 || err != nil || detail.ExpiresAt != nil || detail.Expired || detail.CreatedBy != created.CreatedBy || detail.CreatedAt != created.CreatedAt {
		t.Fatal("exemption edit replaced creator or failed nullable expiry")
	}
	state, _ := a.destDefinitions.State(t.Context())
	generation := state.Generation
	w = destinationListRequest(t, a, token, "PUT", path, map[string]any{"reason": "Permanent debugging"})
	state, _ = a.destDefinitions.State(t.Context())
	if w.Code != 200 || state.Generation != generation {
		t.Fatal("unchanged exemption edit advanced generation")
	}
	w = destinationListRequest(t, a, token, "DELETE", path, nil)
	if w.Code != 204 {
		t.Fatalf("exemption delete HTTP=%d", w.Code)
	}
	w = destinationListRequest(t, a, token, "GET", path, nil)
	if w.Code != 404 {
		t.Fatal("deleted exemption did not return missing")
	}
}

func TestBuildDestinationExemptionAuthorizationAndInputOwnership(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	admin := destinationRefreshAdminToken(t, a)
	u := &domain.User{UPN: "exemption-operator@example.test", Role: domain.RoleOperator, Enabled: true, UUID: "exemption-operator-uuid", SubToken: "exemption-operator-sub"}
	if err := a.repos.User.Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	s, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	operator, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: s.JWTIssuer}
	}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{{"GET", "exemptions"}, {"GET", "exemptions/1"}, {"POST", "exemptions"}, {"PUT", "exemptions/1"}, {"DELETE", "exemptions/1"}, {"POST", "exceptions"}, {"GET", "users/1"}, {"POST", "publish"}, {"PUT", "pause"}, {"POST", "test"}} {
		for _, auth := range []struct {
			token  string
			status int
		}{{"", 401}, {operator, 403}} {
			w := destinationListRequest(t, a, auth.token, route.method, route.path, map[string]any{"user_id": f.user.ID, "reason": "Denied"})
			if w.Code != auth.status {
				t.Fatalf("%s %s authorization HTTP=%d", route.method, route.path, w.Code)
			}
		}
	}
	for _, input := range []map[string]any{
		{"user_id": f.user.ID, "reason": "Forged", "created_by": 999},
		{"user_id": f.user.ID, "reason": ""},
		{"user_id": f.user.ID, "reason": strings.Repeat("x", 256)},
		{"user_id": f.user.ID, "reason": "Invalid expiry", "expires_at": 0},
		{"user_id": 0, "reason": "Invalid owner"},
	} {
		w := destinationListRequest(t, a, admin, "POST", "exemptions", input)
		if w.Code != 400 {
			t.Fatalf("invalid exemption HTTP=%d", w.Code)
		}
	}
	w := destinationListRequest(t, a, admin, "POST", "exemptions", map[string]any{"user_id": 987654, "reason": "Missing owner"})
	if w.Code != 404 {
		t.Fatal("missing exemption owner was accepted")
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || len(defs.Exemptions) != 0 || defs.State.Generation != 0 {
		t.Fatal("invalid exemption partially persisted")
	}
}
