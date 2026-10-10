package app

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func destinationGroupSummaryToken(t *testing.T, a *App, u *domain.User) string {
	t.Helper()
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
	}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func destinationGroupSummaryRequest(t *testing.T, a *App, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/api/admin/groups"+path, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(w, req)
	return w
}

func TestBuildGroupListExposesOnlyModeSummaryToOperators(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	admin := destinationRefreshAdminToken(t, a)
	operator := &domain.User{UPN: "group-summary-operator@example.test", Role: domain.RoleOperator, Enabled: true, UUID: "group-summary-operator", SubToken: "group-summary-operator-sub"}
	if err := a.repos.User.Create(t.Context(), operator); err != nil {
		t.Fatal(err)
	}
	token := destinationGroupSummaryToken(t, a, operator)
	assertSummary := func(w *httptest.ResponseRecorder, want string) {
		t.Helper()
		var page struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatalf("group mode summary HTTP=%d", w.Code)
		}
		for _, item := range page.Items {
			for _, key := range []string{"list_ids", "base_list_id", "extra_list_id", "source_text", "entries", "stage", "stage_changed_at"} {
				if _, exists := item[key]; exists {
					t.Fatalf("staff group list exposed %s", key)
				}
			}
			if string(item["id"]) == fmt.Sprint(f.group.ID) {
				if string(item["dest_mode"]) != fmt.Sprintf("%q", want) {
					t.Fatal("group list omitted or invented its access mode")
				}
				return
			}
		}
		t.Fatal("group missing from staff list")
	}
	assertSummary(destinationGroupSummaryRequest(t, a, token, "GET", "", nil), "open")
	if _, err := a.database.ExecContext(t.Context(), "INSERT INTO dest_group_modes (group_id,mode,stage,list_ids,base_list_id,extra_list_id,updated_at) VALUES (?,?,?,?,?,?,?)", f.group.ID, "allowlist", "trial", "[7788]", 7789, 7790, time.Now()); err != nil {
		t.Fatal(err)
	}
	assertSummary(destinationGroupSummaryRequest(t, a, token, "GET", "", nil), "allowlist_trial")
	path := fmt.Sprintf("/%d", f.group.ID)
	if w := destinationGroupSummaryRequest(t, a, token, "PUT", path, map[string]any{"name": "Unauthorized"}); w.Code != 403 {
		t.Fatal("operator changed group structure")
	}
	if w := destinationGroupSummaryRequest(t, a, destinationGroupSummaryToken(t, a, f.user), "GET", "", nil); w.Code != 403 {
		t.Fatal("ordinary user read staff group list")
	}
	if w := destinationListRequest(t, a, token, "GET", "lists", nil); w.Code != 403 {
		t.Fatal("operator read private lists")
	}
	before, err := a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if w := destinationGroupSummaryRequest(t, a, admin, "PUT", path, map[string]any{"name": "Renamed", "dest_mode": "open", "mode": "open", "stage": "", "list_ids": []int64{}}); w.Code != 200 {
		t.Fatalf("ordinary group update HTTP=%d", w.Code)
	}
	mode, err := a.destDefinitions.GetGroupMode(t.Context(), f.group.ID)
	if err != nil || mode.Mode != "allowlist" || mode.Stage != "trial" || mode.BaseListID != 7789 || mode.ExtraListID != 7790 || len(mode.ListIDs) != 1 || mode.ListIDs[0] != 7788 {
		t.Fatal("ordinary group save modified private access definitions")
	}
	after, err := a.destDefinitions.State(t.Context())
	if err != nil || after.Generation != before.Generation {
		t.Fatal("ordinary group save changed destination generation")
	}
	if _, err := a.database.ExecContext(t.Context(), "UPDATE dest_group_modes SET stage = ? WHERE group_id = ?", "enforce", f.group.ID); err != nil {
		t.Fatal(err)
	}
	assertSummary(destinationGroupSummaryRequest(t, a, token, "GET", "", nil), "allowlist_enforce")
	if _, err := a.database.ExecContext(t.Context(), "UPDATE dest_group_modes SET mode = ? WHERE group_id = ?", "private-corrupt-mode", f.group.ID); err != nil {
		t.Fatal(err)
	}
	w := destinationGroupSummaryRequest(t, a, token, "GET", "", nil)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private-corrupt-mode") || strings.Contains(w.Body.String(), "dest_mode") {
		t.Fatal("corrupt private state returned partial/default-open staff summary")
	}
}
