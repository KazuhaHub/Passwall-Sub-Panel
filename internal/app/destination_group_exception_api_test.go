package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

func destinationGroupExceptionMode(t *testing.T, f destinationPolicyFixture) domain.DestGroupMode {
	t.Helper()
	var lists [2]domain.DestList
	for i, text := range []string{"full:dns.example.test\n", "# keep group comment\nfull:existing.example.test\n"} {
		parsed, err := destlist.ParseCustom([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		lists[i] = domain.DestList{Name: fmt.Sprintf("Private list %d", i), Kind: domain.DestListCustom, SourceText: []byte(text), Entries: parsed.Entries, EntryCount: parsed.EntryCount, RegexpCount: parsed.RegexpCount, ContentSHA256: parsed.ContentSHA256, ParseReport: &parsed.Report}
	}
	mode := domain.DestGroupMode{GroupID: f.group.ID, Mode: "allowlist", Stage: "trial"}
	if err := f.a.destDefinitions.SaveGroupMode(t.Context(), &mode, time.Time{}, lists, time.Now()); err != nil {
		t.Fatal(err)
	}
	return mode
}

func TestBuildDestinationGroupExceptionsAppendOnlyOwnedExtraWithoutGlobalPolicy(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	mode := destinationGroupExceptionMode(t, f)
	if w := destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": "example.test", "match": "host", "scope": "group", "group_id": int64(9223372036854775807)}); w.Code != 404 || !strings.Contains(w.Body.String(), "dest_group_not_found") {
		t.Fatal("unknown exception group was not rejected")
	}
	base, err := a.destDefinitions.GetList(t.Context(), mode.BaseListID)
	if err != nil {
		t.Fatal(err)
	}
	input := func(target string) map[string]any {
		return map[string]any{"scope": "group", "group_id": f.group.ID, "match": "host", "target": target}
	}
	var wg sync.WaitGroup
	for _, target := range []string{"one.example.test", "two.example.test"} {
		wg.Go(func() {
			w := destinationListRequest(t, a, token, "POST", "exceptions", input(target))
			var view struct {
				GroupID int64  `json:"group_id"`
				ListID  int64  `json:"list_id"`
				Entry   string `json:"entry"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.GroupID != f.group.ID || view.ListID != mode.ExtraListID || view.Entry != "full:"+target || strings.Contains(w.Body.String(), "policy_id") || strings.Contains(w.Body.String(), "created") {
				t.Errorf("group exception response HTTP=%d", w.Code)
			}
		})
	}
	wg.Wait()
	list, err := a.destDefinitions.GetList(t.Context(), mode.ExtraListID)
	if err != nil || list.EntryCount != 3 || !strings.Contains(string(list.SourceText), "# keep group comment") || !strings.Contains(string(list.SourceText), "full:one.example.test") || !strings.Contains(string(list.SourceText), "full:two.example.test") {
		t.Fatal("group exception lost current source")
	}
	before, err := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || before.State.Generation != 3 || len(before.Policies) != 0 || len(before.Lists) != 2 {
		t.Fatal("group exception created a global bundle or wrong generation")
	}
	if w := destinationListRequest(t, a, token, "POST", "exceptions", input("one.example.test")); w.Code != 200 {
		t.Fatal("duplicate group exception failed")
	}
	after, err := a.destDefinitions.ReadDefinitions(t.Context())
	current, modeErr := a.destDefinitions.GetGroupMode(t.Context(), f.group.ID)
	baseAfter, baseErr := a.destDefinitions.GetList(t.Context(), mode.BaseListID)
	extraAfter, extraErr := a.destDefinitions.GetList(t.Context(), mode.ExtraListID)
	if err != nil || modeErr != nil || baseErr != nil || extraErr != nil || after.State.Generation != before.State.Generation || baseAfter.ContentSHA256 != base.ContentSHA256 || !baseAfter.UpdatedAt.Equal(base.UpdatedAt) || !extraAfter.UpdatedAt.Equal(list.UpdatedAt) || !current.UpdatedAt.Equal(mode.UpdatedAt) {
		t.Fatal("idempotent exception changed mode, base list or generation")
	}
	operator := &domain.User{UPN: "group-exception-operator@example.test", Role: domain.RoleOperator, Enabled: true, UUID: "group-exception-operator", SubToken: "group-exception-sub"}
	if err := a.repos.User.Create(t.Context(), operator); err != nil {
		t.Fatal(err)
	}
	for _, user := range []*domain.User{operator, f.user} {
		if w := destinationListRequest(t, a, destinationGroupSummaryToken(t, a, user), "POST", "exceptions", input("private.example.test")); w.Code != 403 {
			t.Fatal("non-administrator wrote group exception")
		}
	}
	mode.Mode, mode.Stage = "open", ""
	if err := a.destDefinitions.SaveGroupMode(t.Context(), &mode, mode.UpdatedAt, [2]domain.DestList{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "POST", "exceptions", input("closed.example.test"))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "dest_mode_invalid_transition") {
		t.Fatal("closed group accepted private exception")
	}
}

func TestBuildDestinationGroupExceptionFailureReturnsNoIdentityAndRollsBack(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	mode := destinationGroupExceptionMode(t, f)
	before, err := a.destDefinitions.GetList(t.Context(), mode.ExtraListID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.ExecContext(t.Context(), "CREATE TRIGGER group_exception_api_failure BEFORE UPDATE OF generation ON dest_policy_state BEGIN SELECT RAISE(ABORT, 'private-group-exception-marker'); END"); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "POST", "exceptions", map[string]any{"target": "private.example.test", "match": "host", "scope": "group", "group_id": f.group.ID})
	if w.Code != 500 || strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "list_id") || strings.Contains(w.Body.String(), "entry") {
		t.Fatal("failed group append leaked SQL or partial commit identity")
	}
	after, err := a.destDefinitions.GetList(t.Context(), mode.ExtraListID)
	state, stateErr := a.destDefinitions.State(t.Context())
	if err != nil || stateErr != nil || before.ContentSHA256 != after.ContentSHA256 || !before.UpdatedAt.Equal(after.UpdatedAt) || state.Generation != 1 {
		t.Fatal("failed group append retained partial content/version/generation")
	}
}
