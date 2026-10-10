package destpolicy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

type groupAdminFixture struct {
	defs    domain.DestDefinitions
	mode    domain.DestGroupMode
	writes  int
	initial [2]domain.DestList
	saveErr error
}

func (s *groupAdminFixture) ReadDefinitions(context.Context) (domain.DestDefinitions, error) {
	return s.defs, nil
}
func (s *groupAdminFixture) GetGroupMode(context.Context, int64) (domain.DestGroupMode, error) {
	return s.mode, nil
}
func (s *groupAdminFixture) SaveGroupMode(_ context.Context, mode *domain.DestGroupMode, expected time.Time, initial [2]domain.DestList, now time.Time) error {
	s.writes++
	if s.saveErr != nil {
		return s.saveErr
	}
	if !s.mode.UpdatedAt.Equal(expected) {
		return domain.ErrConflict
	}
	if s.mode.Mode == mode.Mode && s.mode.Stage == mode.Stage && reflect.DeepEqual(s.mode.ListIDs, mode.ListIDs) && initial[0].Name == "" && initial[1].Name == "" {
		*mode = s.mode
		return nil
	}
	s.initial = initial
	mode.UpdatedAt = now
	if mode.Mode == "allowlist" {
		if mode.BaseListID == 0 {
			mode.BaseListID = 20
		}
		if mode.ExtraListID == 0 {
			mode.ExtraListID = 21
		}
	}
	s.mode = *mode
	return nil
}

func TestGroupAdministrationDoesNotInvalidateOnNoopOrFailedCommit(t *testing.T) {
	s := newGroupAdminFixture(t)
	changes := 0
	readGroup := func(context.Context, int64) (*domain.Group, error) {
		return &domain.Group{ID: 7, Name: "Visitors"}, nil
	}
	budget := func(context.Context, domain.DestDefinitions) (Budget, error) { return Budget{}, nil }
	a := NewGroupAdministrator(s, readGroup, budget, func(context.Context, int64) { changes++ })
	if mode, err := a.Save(t.Context(), 7, GroupModeInput{Mode: "open"}, time.Time{}); err != nil || !mode.UpdatedAt.IsZero() || changes != 0 {
		t.Fatal("no-op invalidated memberships or invented edit version")
	}
	s.saveErr = errors.New("private group transaction failure")
	if mode, err := a.Save(t.Context(), 7, GroupModeInput{Mode: "allowlist", Stage: "trial", ListIDs: []int64{1}}, time.Time{}); !errors.Is(err, s.saveErr) || !reflect.DeepEqual(mode, domain.DestGroupMode{}) || changes != 0 {
		t.Fatal("failed commit returned data or invalidated memberships")
	}
	if _, err := NewGroupAdministrator(s, readGroup, budget, nil).Save(t.Context(), 7, GroupModeInput{Mode: "open"}, time.Time{}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("unwired membership invalidation permitted a mode write")
	}
}
func newGroupAdminFixture(t *testing.T) *groupAdminFixture {
	t.Helper()
	parsed, err := destlist.ParseCustom([]byte("domain:example.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	return &groupAdminFixture{mode: domain.DestGroupMode{GroupID: 7, Mode: "open"}, defs: domain.DestDefinitions{Lists: []domain.DestList{{ID: 1, Name: "Allowed sites", Kind: domain.DestListCustom, SourceText: []byte("domain:example.test\n"), Entries: parsed.Entries, EntryCount: parsed.EntryCount, ContentSHA256: parsed.ContentSHA256, ParseReport: &parsed.Report}}}}
}
func TestGroupAdministrationPreviewsOwnedListsWithoutWritingAndCommitsOnce(t *testing.T) {
	s := newGroupAdminFixture(t)
	var changes []int64
	budgetReads := 0
	a := NewGroupAdministrator(s, func(context.Context, int64) (*domain.Group, error) {
		return &domain.Group{ID: 7, Name: "Visitors"}, nil
	}, func(_ context.Context, defs domain.DestDefinitions) (Budget, error) {
		budgetReads++
		if len(defs.Groups) != 1 || len(defs.Lists) != 3 || defs.Groups[0].BaseListID <= 1 || defs.Groups[0].ExtraListID <= 1 {
			t.Fatal("budget omitted prospective mode or private owned lists")
		}
		return DefinitionBudget(defs, nil)
	}, func(_ context.Context, id int64) { changes = append(changes, id) })
	input := GroupModeInput{Mode: "allowlist", Stage: "trial", ListIDs: []int64{1, 1}, BaseHosts: []string{"DNS.Example.test.", "dns.example.test"}}
	preview, err := a.Preview(t.Context(), 7, input)
	if err != nil || preview.Mode.Mode != "allowlist" || preview.Budget.Domains.Used != 2 || s.writes != 0 || len(changes) != 0 || budgetReads != 1 || len(s.defs.Lists) != 1 || s.mode.Mode != "open" {
		t.Fatalf("group preview mutated source or omitted budget: %v", err)
	}
	mode, err := a.Save(t.Context(), 7, input, time.Time{})
	if err != nil || s.writes != 1 || !reflect.DeepEqual(changes, []int64{7}) || mode.BaseListID != 20 || mode.ExtraListID != 21 || !reflect.DeepEqual(mode.ListIDs, []int64{1}) || string(s.initial[0].Entries) != "full:dns.example.test\n" || s.initial[1].EntryCount != 0 || !strings.Contains(s.initial[0].Name, "Visitors") || budgetReads != 2 {
		t.Fatalf("group mode commit=%+v: %v", mode, err)
	}
}

func TestGroupAdministrationRejectsInvalidTransitionsStaleEditsAndUnsafeBaseHosts(t *testing.T) {
	for _, name := range []string{"direct-enforce", "invalid-mode", "invalid-stage", "invalid-id", "foreign-list", "missing-list", "broad-host", "url-host", "ip-host", "stale", "budget"} {
		t.Run(name, func(t *testing.T) {
			s := newGroupAdminFixture(t)
			input := GroupModeInput{Mode: "allowlist", Stage: "trial", ListIDs: []int64{1}}
			id := int64(7)
			expected := time.Time{}
			switch name {
			case "direct-enforce":
				input.Stage = "enforce"
			case "invalid-mode":
				input.Mode = "unknown"
			case "invalid-stage":
				input.Stage = "unknown"
			case "invalid-id":
				id = 0
			case "foreign-list":
				s.defs.Lists[0].OwnerGroupID = 8
			case "missing-list":
				input.ListIDs = []int64{99}
			case "broad-host":
				input.BaseHosts = []string{"com"}
			case "url-host":
				input.BaseHosts = []string{"https://secret.example.test/path"}
			case "ip-host":
				input.BaseHosts = []string{"1.1.1.1"}
			case "stale":
				expected = time.UnixMilli(123)
			}
			changes := 0
			a := NewGroupAdministrator(s, func(context.Context, int64) (*domain.Group, error) {
				return &domain.Group{ID: 7, Name: "Visitors"}, nil
			}, func(context.Context, domain.DestDefinitions) (Budget, error) {
				if name == "budget" {
					return Budget{}, domain.ErrResourceExhausted
				}
				return Budget{}, nil
			}, func(context.Context, int64) { changes++ })
			mode, err := a.Save(t.Context(), id, input, expected)
			if err == nil || !reflect.DeepEqual(mode, domain.DestGroupMode{}) || s.writes != 0 || changes != 0 {
				t.Fatal("invalid group update reached commit or invalidation")
			}
		})
	}
}

func TestGroupAdministrationRequiresFetchedListsOnlyForEnforcementAndPreservesOwnedContent(t *testing.T) {
	s := newGroupAdminFixture(t)
	s.mode.Mode, s.mode.Stage = "allowlist", "trial"
	s.mode.UpdatedAt = time.UnixMilli(100)
	s.mode.BaseListID, s.mode.ExtraListID = 2, 3
	s.defs.Groups = []domain.DestGroupMode{s.mode}
	s.defs.Lists[0].Kind, s.defs.Lists[0].LastFetchedAt = domain.DestListRemote, nil
	for _, id := range []int64{2, 3} {
		s.defs.Lists = append(s.defs.Lists, domain.DestList{ID: id, Name: "Private", OwnerGroupID: 7, Kind: domain.DestListCustom})
	}
	changes := 0
	a := NewGroupAdministrator(s, func(context.Context, int64) (*domain.Group, error) {
		return &domain.Group{ID: 7, Name: "Visitors"}, nil
	}, func(context.Context, domain.DestDefinitions) (Budget, error) { return Budget{}, nil }, func(context.Context, int64) { changes++ })
	if _, err := a.Preview(t.Context(), 7, GroupModeInput{Mode: "allowlist", Stage: "trial", ListIDs: []int64{1}}); err != nil {
		t.Fatal("trial rejected unfetched list")
	}
	input := GroupModeInput{Mode: "allowlist", Stage: "enforce", ListIDs: []int64{1}}
	if _, err := a.Save(t.Context(), 7, input, s.mode.UpdatedAt); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "dest_list_not_ready") || s.writes != 0 || changes != 0 {
		t.Fatal("enforcement accepted an unfetched list")
	}
	fetched := time.UnixMilli(50)
	s.defs.Lists[0].LastFetchedAt, s.defs.Lists[0].LastError = &fetched, "refresh failed but last good content remains"
	if _, err := a.Save(t.Context(), 7, input, s.mode.UpdatedAt); err != nil || s.initial[0].Name != "" || s.initial[1].Name != "" || changes != 1 {
		t.Fatal("enforcement replaced owned source or rejected last good fetched content")
	}
}
