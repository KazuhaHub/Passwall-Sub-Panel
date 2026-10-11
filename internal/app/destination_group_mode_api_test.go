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

type destinationGroupModeAPIResult struct {
	GroupID        int64   `json:"group_id"`
	Mode           string  `json:"mode"`
	Stage          string  `json:"stage"`
	ListIDs        []int64 `json:"list_ids"`
	BaseListID     int64   `json:"base_list_id"`
	ExtraListID    int64   `json:"extra_list_id"`
	UpdatedAt      int64   `json:"updated_at"`
	StageChangedAt *int64  `json:"stage_changed_at"`
}

func TestBuildGroupModeHTTPUsesAtomicOwnedListsCASAndPreservesRetainedSource(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("groups/%d", f.group.ID)
	input := map[string]any{"mode": "allowlist", "stage": "enforce", "list_ids": []int64{}, "updated_at": int64(0)}
	before, err := a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "PUT", path, input)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "dest_mode_invalid_transition") {
		t.Fatal("HTTP allowed open-to-enforce")
	}
	input["stage"], input["base_hosts"] = "trial", []string{"https://dns.example.test"}
	if w := destinationListRequest(t, a, token, "PUT", path, input); w.Code != 400 {
		t.Fatal("invalid base host was accepted")
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || len(defs.Lists) != 0 || len(defs.Groups) != 0 || defs.State.Generation != before.Generation {
		t.Fatal("failed initial mode requests left orphan lists or a new generation")
	}
	input["base_hosts"] = []string{"DNS.Example.test.", "dns.example.test"}
	w = destinationListRequest(t, a, token, "PUT", path, input)
	var mode destinationGroupModeAPIResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &mode) != nil || mode.GroupID != f.group.ID || mode.Mode != "allowlist" || mode.Stage != "trial" || mode.UpdatedAt <= 0 || mode.StageChangedAt == nil || *mode.StageChangedAt != mode.UpdatedAt || mode.BaseListID <= 0 || mode.ExtraListID <= 0 || mode.BaseListID == mode.ExtraListID || mode.ListIDs == nil || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("initial mode HTTP=%d body=%s", w.Code, w.Body.String())
	}
	base, err := a.destDefinitions.GetList(t.Context(), mode.BaseListID)
	if err != nil || base.OwnerGroupID != f.group.ID || string(base.Entries) != "full:dns.example.test\n" {
		t.Fatal("initial HTTP mode did not create normalized owned base")
	}
	defs, err = a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || len(defs.Lists) != 2 || len(defs.Groups) != 1 || defs.State.Generation != before.Generation+1 {
		t.Fatal("initial HTTP mode did not commit exactly two lists and one generation")
	}
	delete(input, "base_hosts")
	input["updated_at"] = mode.UpdatedAt
	w = destinationListRequest(t, a, token, "PUT", path, input)
	var noop destinationGroupModeAPIResult
	state, stateErr := a.destDefinitions.State(t.Context())
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &noop) != nil || noop.UpdatedAt != mode.UpdatedAt || stateErr != nil || state.Generation != defs.State.Generation {
		t.Fatal("no-op HTTP save invented a generation or edit version")
	}
	input["updated_at"], input["mode"], input["stage"] = int64(0), "open", ""
	w = destinationListRequest(t, a, token, "PUT", path, input)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "dest_mode_stale") {
		t.Fatal("stale HTTP edit overwrote saved mode")
	}
	// An unfetched remote list is selectable in trial, but cannot enter enforce.
	list := domain.DestList{Name: "Pending group list", Kind: domain.DestListRemote, SourceURL: "https://example.test/rules"}
	if err := a.destDefinitions.SaveList(t.Context(), &list, time.Time{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	input["mode"], input["stage"], input["updated_at"], input["list_ids"] = "allowlist", "trial", mode.UpdatedAt, []int64{list.ID}
	w = destinationListRequest(t, a, token, "PUT", path, input)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &mode) != nil {
		t.Fatal("trial refused an unfetched selected list")
	}
	input["stage"], input["updated_at"] = "enforce", mode.UpdatedAt
	w = destinationListRequest(t, a, token, "PUT", path, input)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "dest_list_not_ready") {
		t.Fatal("HTTP enforced an unfetched list")
	}
	for _, stage := range []string{"enforce", "trial", ""} {
		input["stage"], input["updated_at"], input["list_ids"] = stage, mode.UpdatedAt, []int64{}
		input["mode"] = "allowlist"
		if stage == "" {
			input["mode"] = "open"
		}
		oldVersion := mode.UpdatedAt
		w = destinationListRequest(t, a, token, "PUT", path, input)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &mode) != nil || mode.UpdatedAt <= oldVersion || mode.BaseListID != base.ID || mode.ExtraListID <= 0 {
			t.Fatalf("HTTP transition %s failed: status=%d", stage, w.Code)
		}
	}
	retained, err := a.destDefinitions.GetList(t.Context(), base.ID)
	if err != nil || retained.ContentSHA256 != base.ContentSHA256 || string(retained.SourceText) != string(base.SourceText) {
		t.Fatal("mode transitions replaced retained owned list source")
	}
	rows, _, err := a.repos.Audit.List(t.Context(), ports.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Target == "/api/admin/dest/groups/:group_id" {
			found = true
		}
	}
	if !found {
		t.Fatal("mode write route omitted automatic audit capture")
	}
}

func TestBuildGroupModeHTTPIsAdministratorOnlyAndRequiresARealGroup(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("groups/%d", f.group.ID)
	input := map[string]any{"mode": "open", "stage": "", "list_ids": []int64{}, "updated_at": int64(0)}
	if w := destinationListRequest(t, a, token, "PUT", "groups/999999", input); w.Code != 404 || !strings.Contains(w.Body.String(), "dest_group_not_found") {
		t.Fatal("missing HTTP group did not return 404")
	}
	if w := destinationListRequest(t, a, "", "PUT", path, input); w.Code != 401 {
		t.Fatal("anonymous mode write")
	}
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []domain.Role{domain.RoleOperator, domain.RoleUser} {
		u := &domain.User{UPN: "group-mode-" + string(role) + "@example.test", Role: role, Enabled: true, UUID: "group-mode-" + string(role), SubToken: "group-mode-sub-" + string(role)}
		if err := a.repos.User.Create(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		otherToken, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
			return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
		}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}
		if w := destinationListRequest(t, a, otherToken, "PUT", path, input); w.Code != 403 {
			t.Fatalf("%s could change group mode", role)
		}
	}
	defs, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || len(defs.Groups) != 0 || len(defs.Lists) != 0 {
		t.Fatal("unauthorized mode request changed definitions")
	}
}
