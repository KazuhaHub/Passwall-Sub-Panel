package sqlstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"gorm.io/gorm"
)

type groupModeDefinitionStore interface {
	GetGroupMode(context.Context, int64) (domain.DestGroupMode, error)
	SaveGroupMode(context.Context, *domain.DestGroupMode, time.Time, [2]domain.DestList, time.Time) error
}

func requireGroupModeStore(t *testing.T, r *DestDefinitionRepo) groupModeDefinitionStore {
	t.Helper()
	store, ok := any(r).(groupModeDefinitionStore)
	if !ok {
		t.Fatal("definition repository omitted atomic group-mode operations")
	}
	return store
}

// SQL drivers may attach different locations to the same UTC instant.
// Compare every mode field while normalizing timestamp representation.
func comparableDestinationMode(mode domain.DestGroupMode) domain.DestGroupMode {
	mode.UpdatedAt = mode.UpdatedAt.UTC()
	if mode.StageChangedAt != nil {
		instant := mode.StageChangedAt.UTC()
		mode.StageChangedAt = &instant
	}
	return mode
}

func modeGroupFixture(t *testing.T) (*DestDefinitionRepo, *domain.Group, time.Time) {
	t.Helper()
	r := newDestDefinitionRepo(t)
	g := &domain.Group{Slug: "owned-mode", Name: "Owned mode", TagFilter: domain.TagFilter{All: true}}
	if err := NewRepos(r.db).Group.Create(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	return r, g, time.UnixMilli(1791000000000).UTC()
}

func initialModeLists(t *testing.T) [2]domain.DestList {
	t.Helper()
	base, err := destlist.ParseCustom([]byte("full:dns.example.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	extra, err := destlist.ParseCustom(nil)
	if err != nil {
		t.Fatal(err)
	}
	return [2]domain.DestList{
		{Name: "Base allow", Kind: domain.DestListCustom, SourceText: []byte("full:dns.example.test\n"), Entries: base.Entries, EntryCount: base.EntryCount, ContentSHA256: base.ContentSHA256, ParseReport: &base.Report},
		{Name: "Extra allow", Kind: domain.DestListCustom, Entries: extra.Entries, EntryCount: extra.EntryCount, ContentSHA256: extra.ContentSHA256, ParseReport: &extra.Report},
	}
}

func TestDestinationGroupModeCreatesOwnedListsAndGenerationAtomically(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	store := requireGroupModeStore(t, r)
	open, err := store.GetGroupMode(t.Context(), g.ID)
	if err != nil || open.Mode != "open" || !open.UpdatedAt.IsZero() {
		t.Fatal("unset group mode did not read as open")
	}
	m := domain.DestGroupMode{GroupID: g.ID, Mode: "allowlist", Stage: "trial", BaseListID: 999, ExtraListID: 998}
	if err := store.SaveGroupMode(t.Context(), &m, time.Time{}, initialModeLists(t), now); err != nil {
		t.Fatal(err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || len(defs.Lists) != 2 || len(defs.Groups) != 1 || defs.State.Generation != 1 || m.BaseListID <= 0 || m.ExtraListID <= 0 || m.BaseListID == 999 || m.ExtraListID == 998 {
		t.Fatal("owned lists/mode/generation were not one commit")
	}
	for _, list := range defs.Lists {
		if list.OwnerGroupID != g.ID {
			t.Fatal("owned list trusted client-supplied ownership")
		}
	}
	version := m.UpdatedAt
	stageAt := *m.StageChangedAt
	if err := store.SaveGroupMode(t.Context(), &m, version, [2]domain.DestList{}, now); err != nil {
		t.Fatal(err)
	}
	state, _ := r.State(t.Context())
	if state.Generation != 1 || !m.UpdatedAt.Equal(version) {
		t.Fatal("no-op mode edit advanced publication")
	}
	m.Stage = "enforce"
	if err := store.SaveGroupMode(t.Context(), &m, version, [2]domain.DestList{}, now); err != nil || !m.UpdatedAt.After(version) {
		t.Fatal("same-millisecond transition reused edit version")
	}
	if err := store.SaveGroupMode(t.Context(), &m, version, [2]domain.DestList{}, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale mode edit was accepted")
	}
	m.Stage = "trial"
	if err := store.SaveGroupMode(t.Context(), &m, m.UpdatedAt, [2]domain.DestList{}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	m.Mode, m.Stage = "open", ""
	if err := store.SaveGroupMode(t.Context(), &m, m.UpdatedAt, [2]domain.DestList{}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	ids := []int64{m.BaseListID, m.ExtraListID}
	m.Mode, m.Stage = "allowlist", "trial"
	if err := store.SaveGroupMode(t.Context(), &m, m.UpdatedAt, initialModeLists(t), now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []int64{m.BaseListID, m.ExtraListID}) || !m.StageChangedAt.After(stageAt) {
		t.Fatal("reopening duplicated retained owned lists or lost transition time")
	}
}

func TestDestinationGroupModeRejectsDirectEnforceAndUnreadyReferences(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	store := requireGroupModeStore(t, r)
	m := domain.DestGroupMode{GroupID: g.ID, Mode: "allowlist", Stage: "enforce"}
	if err := store.SaveGroupMode(t.Context(), &m, time.Time{}, initialModeLists(t), now); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "dest_mode_invalid_transition") {
		t.Fatal("new allowlist bypassed trial")
	}
	remote := domain.DestList{Name: "not fetched", Kind: domain.DestListRemote, SourceURL: "https://example.test/list"}
	if err := r.SaveList(t.Context(), &remote, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	m.Stage, m.ListIDs = "trial", []int64{remote.ID}
	if err := store.SaveGroupMode(t.Context(), &m, time.Time{}, initialModeLists(t), now); err != nil {
		t.Fatal(err)
	}
	before := m
	m.Stage = "enforce"
	if err := store.SaveGroupMode(t.Context(), &m, m.UpdatedAt, [2]domain.DestList{}, now); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "dest_list_not_ready") {
		t.Fatal("unready remote list entered enforce")
	}
	after, err := store.GetGroupMode(t.Context(), g.ID)
	if err != nil || !reflect.DeepEqual(comparableDestinationMode(before), comparableDestinationMode(after)) {
		t.Fatal("failed readiness transition changed stored mode")
	}
	if err := r.CommitListRefresh(t.Context(), remote, domain.DestListRefresh{ContentSHA256: "empty-ready"}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGroupMode(t.Context(), &m, m.UpdatedAt, [2]domain.DestList{}, now.Add(time.Second)); err != nil {
		t.Fatal("ready empty list was turned into an extra service gate")
	}
}

func TestDestinationGroupModeRollsBackOwnedListAndGenerationFailures(t *testing.T) {
	for _, phase := range []string{"second-owned-list", "generation"} {
		t.Run(phase, func(t *testing.T) {
			r, g, now := modeGroupFixture(t)
			store := requireGroupModeStore(t, r)
			problem := errors.New("mode write failure")
			created := 0
			if phase == "second-owned-list" {
				r.db.Callback().Create().Before("gorm:create").Register("mode-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == "dest_lists" {
						created++
						if created == 2 {
							tx.AddError(problem)
						}
					}
				})
				t.Cleanup(func() { _ = r.db.Callback().Create().Remove("mode-failure") })
			} else {
				r.db.Callback().Update().Before("gorm:update").Register("mode-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == "dest_policy_state" {
						tx.AddError(problem)
					}
				})
				t.Cleanup(func() { _ = r.db.Callback().Update().Remove("mode-failure") })
			}
			m := domain.DestGroupMode{GroupID: g.ID, Mode: "allowlist", Stage: "trial"}
			before := m
			if err := store.SaveGroupMode(t.Context(), &m, time.Time{}, initialModeLists(t), now); !errors.Is(err, problem) || !reflect.DeepEqual(before, m) {
				t.Fatal("failed mode transaction changed caller result or hid error")
			}
			defs, err := r.ReadDefinitions(t.Context())
			if err != nil || len(defs.Lists) != 0 || len(defs.Groups) != 0 || defs.State.Generation != 0 {
				t.Fatal("failed mode initialization left orphan lists or a partial publication")
			}
		})
	}
}

func TestDestinationOwnedListsCannotBePolicyReferences(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	owned := initialModeLists(t)[0]
	owned.OwnerGroupID = g.ID
	if err := r.SaveList(t.Context(), &owned, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	p := destinationTestPolicy("owned reference")
	p.ListIDs = []int64{owned.ID}
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("policy reused a private group-owned list")
	}
}

func TestDestinationOpenOwnedListDeletionDetachesItsMode(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	owned := initialModeLists(t)
	for i := range owned {
		owned[i].OwnerGroupID = g.ID
		if err := r.SaveList(t.Context(), &owned[i], time.Time{}, now); err != nil {
			t.Fatal(err)
		}
	}
	row := destGroupModeRow{GroupID: g.ID, Mode: "open", BaseListID: owned[0].ID, ExtraListID: owned[1].ID, UpdatedAt: now}
	if err := r.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteList(t.Context(), owned[0].ID, now); err != nil {
		t.Fatal("closed allowlist prevented owned-list deletion")
	}
	if err := r.db.First(&row, "group_id = ?", g.ID).Error; err != nil || row.BaseListID != 0 || row.ExtraListID != owned[1].ID || !row.UpdatedAt.After(now) {
		t.Fatal("deletion left a stale closed-mode pointer or edit version")
	}
}

func TestDestinationGroupDeleteCleansItsModeAndOwnedLists(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	owned := initialModeLists(t)
	for i := range owned {
		owned[i].OwnerGroupID = g.ID
		if err := r.SaveList(t.Context(), &owned[i], time.Time{}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.db.Create(&destGroupModeRow{GroupID: g.ID, Mode: "allowlist", Stage: "trial", BaseListID: owned[0].ID, ExtraListID: owned[1].ID, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := NewRepos(r.db).Group.Delete(t.Context(), g.ID); err != nil {
		t.Fatal(err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || len(defs.Lists) != 0 || len(defs.Groups) != 0 || defs.State.Generation != 3 {
		t.Fatal("group deletion left owned definition rows or missed generation")
	}
}

func TestDestinationModeClosedListDeletionAndReopenPreserveOtherContents(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	store := requireGroupModeStore(t, r)
	m := domain.DestGroupMode{GroupID: g.ID, Mode: "allowlist", Stage: "trial"}
	if err := store.SaveGroupMode(t.Context(), &m, time.Time{}, initialModeLists(t), now); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteList(t.Context(), m.BaseListID, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("active owned list was deleted")
	}
	extra, err := r.GetList(t.Context(), m.ExtraListID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := destlist.ParseCustom([]byte("domain:kept.example.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	extra.SourceText, extra.Entries, extra.ContentSHA256, extra.EntryCount = []byte("domain:kept.example.test\n"), parsed.Entries, parsed.ContentSHA256, parsed.EntryCount
	if err := r.SaveList(t.Context(), &extra, extra.UpdatedAt, now); err != nil {
		t.Fatal(err)
	}
	m.Mode, m.Stage = "open", ""
	if err := store.SaveGroupMode(t.Context(), &m, m.UpdatedAt, [2]domain.DestList{}, now); err != nil {
		t.Fatal(err)
	}
	deleted := m.BaseListID
	if err := r.DeleteList(t.Context(), deleted, now); err != nil {
		t.Fatal(err)
	}
	m, err = store.GetGroupMode(t.Context(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.Mode, m.Stage = "allowlist", "trial"
	if err := store.SaveGroupMode(t.Context(), &m, m.UpdatedAt, initialModeLists(t), now); err != nil {
		t.Fatal(err)
	}
	kept, err := r.GetList(t.Context(), m.ExtraListID)
	defs, readErr := r.ReadDefinitions(t.Context())
	extra.CreatedAt, extra.UpdatedAt = extra.CreatedAt.UTC(), extra.UpdatedAt.UTC()
	kept.CreatedAt, kept.UpdatedAt = kept.CreatedAt.UTC(), kept.UpdatedAt.UTC()
	if err != nil || readErr != nil || len(defs.Lists) != 2 || m.BaseListID == deleted || !reflect.DeepEqual(extra, kept) {
		t.Fatal("reopening duplicated lists or overwrote retained user content")
	}
}

func TestDestinationModeRejectsForeignOwnedReferencesAndRetainsStageTime(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	store := requireGroupModeStore(t, r)
	m := domain.DestGroupMode{GroupID: g.ID, Mode: "allowlist", Stage: "trial"}
	if err := store.SaveGroupMode(t.Context(), &m, time.Time{}, initialModeLists(t), now); err != nil {
		t.Fatal(err)
	}
	other := &domain.Group{Slug: "other", Name: "Other"}
	if err := NewRepos(r.db).Group.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	foreign := domain.DestGroupMode{GroupID: other.ID, Mode: "allowlist", Stage: "trial", ListIDs: []int64{m.BaseListID}}
	if err := store.SaveGroupMode(t.Context(), &foreign, time.Time{}, initialModeLists(t), now); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("another group referenced private content")
	}
	shared := initialModeLists(t)[0]
	if err := r.SaveList(t.Context(), &shared, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	stageAt := *m.StageChangedAt
	m.ListIDs = []int64{shared.ID, shared.ID}
	if err := store.SaveGroupMode(t.Context(), &m, m.UpdatedAt, [2]domain.DestList{}, now.Add(time.Minute)); err != nil || len(m.ListIDs) != 1 || !m.StageChangedAt.Equal(stageAt) {
		t.Fatal("list-only edit changed stage age or retained duplicate references")
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || len(defs.Groups) != 1 || len(defs.Lists) != 3 {
		t.Fatal("foreign reference refusal left orphan initialization")
	}
}

func TestDestinationGroupDeleteAndClosedListDetachRollBackOnGenerationFailure(t *testing.T) {
	for _, operation := range []string{"group", "closed-list"} {
		t.Run(operation, func(t *testing.T) {
			r, g, now := modeGroupFixture(t)
			store := requireGroupModeStore(t, r)
			m := domain.DestGroupMode{GroupID: g.ID, Mode: "allowlist", Stage: "trial"}
			if err := store.SaveGroupMode(t.Context(), &m, time.Time{}, initialModeLists(t), now); err != nil {
				t.Fatal(err)
			}
			if operation == "closed-list" {
				m.Mode, m.Stage = "open", ""
				if err := store.SaveGroupMode(t.Context(), &m, m.UpdatedAt, [2]domain.DestList{}, now); err != nil {
					t.Fatal(err)
				}
			}
			before, err := r.ReadDefinitions(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			problem := errors.New("generation write failure")
			r.db.Callback().Update().Before("gorm:update").Register("cleanup-failure", func(tx *gorm.DB) {
				if tx.Statement.Table == "dest_policy_state" {
					tx.AddError(problem)
				}
			})
			t.Cleanup(func() { _ = r.db.Callback().Update().Remove("cleanup-failure") })
			if operation == "group" {
				err = NewRepos(r.db).Group.Delete(t.Context(), g.ID)
			} else {
				err = r.DeleteList(t.Context(), m.BaseListID, now)
			}
			if !errors.Is(err, problem) {
				t.Fatal("cleanup swallowed generation failure")
			}
			after, readErr := r.ReadDefinitions(t.Context())
			_, groupErr := NewRepos(r.db).Group.GetByID(t.Context(), g.ID)
			if readErr != nil || groupErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed cleanup removed group/list/mode or advanced generation")
			}
		})
	}
}

func TestDestinationModeConcurrentInitializationCreatesOnePair(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	store := requireGroupModeStore(t, r)
	initial := initialModeLists(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			m := domain.DestGroupMode{GroupID: g.ID, Mode: "allowlist", Stage: "trial"}
			results <- store.SaveGroupMode(t.Context(), &m, time.Time{}, initial, now)
		})
	}
	close(start)
	wg.Wait()
	close(results)
	commits, conflicts := 0, 0
	for err := range results {
		if err == nil {
			commits++
		} else if errors.Is(err, domain.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || commits != 1 || conflicts != 1 || len(defs.Lists) != 2 || len(defs.Groups) != 1 || defs.State.Generation != 1 {
		t.Fatal("concurrent initialization duplicated ownership or publication")
	}
}
