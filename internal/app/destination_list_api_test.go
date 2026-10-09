package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func destinationListRequest(t *testing.T, a *App, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/api/admin/dest/"+path, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(w, req)
	return w
}

func TestBuildDestinationListCRUDAndReadOnlyPreview(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	input := map[string]any{"name": "Mixed", "kind": "custom", "text": "# kept comment\nEXAMPLE.TEST\ndomain:com\n"}
	preview := destinationListRequest(t, a, token, http.MethodPost, "lists/preview", input)
	if preview.Code != 200 {
		t.Fatalf("preview HTTP=%d", preview.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(preview.Body.Bytes(), &raw); err != nil || raw["parse_report"] == nil || raw["entries"] == nil {
		t.Fatal("preview did not return parser JSON")
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || len(defs.Lists) != 0 || defs.State.Generation != 0 {
		t.Fatal("preview changed definitions")
	}
	created := destinationListRequest(t, a, token, http.MethodPost, "lists", input)
	if created.Code != http.StatusCreated {
		t.Fatalf("create HTTP=%d", created.Code)
	}
	var view struct {
		ID          int64                   `json:"id"`
		UpdatedAt   int64                   `json:"updated_at"`
		SourceText  *string                 `json:"source_text"`
		Entries     []string                `json:"entries"`
		ParseReport *domain.DestParseReport `json:"parse_report"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &view); err != nil || view.ID <= 0 || view.UpdatedAt <= 0 || view.ParseReport == nil || view.ParseReport.IgnoredBroad != 1 || view.SourceText != nil {
		t.Fatal("create omitted parse report/edit version or returned raw source")
	}
	path := fmt.Sprintf("lists/%d", view.ID)
	detail := destinationListRequest(t, a, token, http.MethodGet, path+"?text=1", nil)
	if err := json.Unmarshal(detail.Body.Bytes(), &view); detail.Code != 200 || err != nil || view.SourceText == nil || *view.SourceText != input["text"] {
		t.Fatal("custom edit text did not survive actual SQL/HTTP")
	}
	version := view.UpdatedAt
	input["name"], input["updated_at"], input["text"] = "Edited", version, "full:kept.example.test\n"
	edited := destinationListRequest(t, a, token, http.MethodPut, path, input)
	if err := json.Unmarshal(edited.Body.Bytes(), &view); edited.Code != 200 || err != nil || view.UpdatedAt <= version {
		t.Fatal("PUT did not advance millisecond CAS version")
	}
	stale := destinationListRequest(t, a, token, http.MethodPut, path, input)
	if stale.Code != 409 || !strings.Contains(stale.Body.String(), "dest_list_stale") {
		t.Fatal("stale PUT lacked stable conflict code")
	}
	if deleted := destinationListRequest(t, a, token, http.MethodDelete, path, nil); deleted.Code != 204 {
		t.Fatalf("delete HTTP=%d", deleted.Code)
	}
	if missing := destinationListRequest(t, a, token, http.MethodGet, path, nil); missing.Code != 404 {
		t.Fatal("deleted list remained visible")
	}
}

func TestBuildDestinationListsPermitFourMiBTextButBoundDetailSamples(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	var text strings.Builder
	text.WriteString("#" + strings.Repeat("x", (1<<20)+512) + "\n")
	for i := 0; i < 240; i++ {
		fmt.Fprintf(&text, "full:h%d.example.test\n", i)
	}
	response := destinationListRequest(t, a, token, http.MethodPost, "lists", map[string]any{"name": "large", "kind": "custom", "text": text.String()})
	var view struct {
		ID         int64    `json:"id"`
		Entries    []string `json:"entries"`
		EntryCount int      `json:"entry_count"`
		SourceText *string  `json:"source_text"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); response.Code != 201 || err != nil || view.EntryCount != 240 || len(view.Entries) != 200 || view.SourceText != nil {
		t.Fatal("list body admission or detail sampling violated limits")
	}
	preview := destinationListRequest(t, a, token, http.MethodPost, "lists/preview", map[string]any{"name": "large", "kind": "custom", "text": text.String()})
	if err := json.Unmarshal(preview.Body.Bytes(), &view); preview.Code != 200 || err != nil || len(view.Entries) != 50 {
		t.Fatal("preview sample was not bounded to 50")
	}
}

func TestBuildDestinationListRoutesAreAdminOnlyAndRejectOwnedInput(t *testing.T) {
	a := buildDestinationListsFixture(t)
	admin := destinationRefreshAdminToken(t, a)
	u := &domain.User{UPN: "list-operator@example.test", Role: domain.RoleOperator, Enabled: true, UUID: "list-operator-uuid", SubToken: "list-operator-sub"}
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
	for _, route := range []struct{ method, path string }{{"POST", "lists"}, {"POST", "lists/preview"}, {"GET", "lists"}, {"GET", "lists/1"}, {"PUT", "lists/1"}, {"DELETE", "lists/1"}, {"POST", "lists/1/entries"}, {"POST", "lists/1/refresh"}, {"GET", "geosite/categories"}, {"POST", "geosite/refresh"}} {
		for _, auth := range []struct {
			token  string
			status int
		}{{"", 401}, {operator, 403}} {
			if w := destinationListRequest(t, a, auth.token, route.method, route.path, map[string]any{}); w.Code != auth.status {
				t.Fatalf("%s %s HTTP=%d want=%d", route.method, route.path, w.Code, auth.status)
			}
		}
	}
	for _, input := range []map[string]any{{"name": "bad", "kind": "custom", "owner_group_id": 1}, {"name": "bad", "kind": "custom", "id": 8}, {"name": "bad", "kind": "remote", "source_url": "http://insecure.invalid/private?token=secret"}} {
		if w := destinationListRequest(t, a, admin, "POST", "lists", input); w.Code != 400 {
			t.Fatalf("invalid list HTTP=%d", w.Code)
		}
	}
}
