package sqlstore

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func TestDestinationGroupExceptionConcurrentAppendsRemainPrivateAndIdempotent(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	m := domain.DestGroupMode{GroupID: g.ID, Mode: "allowlist", Stage: "trial"}
	if err := r.SaveGroupMode(t.Context(), &m, m.UpdatedAt, initialModeLists(t), now); err != nil {
		t.Fatal(err)
	}
	base, err := r.GetList(t.Context(), m.BaseListID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, entry := range []string{"domain:one.example.test", "full:two.example.test"} {
		wg.Go(func() {
			id, err := r.AddGroupException(t.Context(), g.ID, now, globalExceptionEdit(entry))
			if err != nil || id != m.ExtraListID {
				t.Errorf("group append identity=%d: %v", id, err)
			}
		})
	}
	wg.Wait()
	extra, err := r.GetList(t.Context(), m.ExtraListID)
	if err != nil || extra.EntryCount != 2 || extra.OwnerGroupID != g.ID || !strings.Contains(string(extra.SourceText), "domain:one.example.test") || !strings.Contains(string(extra.SourceText), "full:two.example.test") {
		t.Fatal("concurrent group append lost or leaked owned content")
	}
	before, err := r.ReadDefinitions(t.Context())
	if err != nil || before.State.Generation != 3 || len(before.Lists) != 2 || len(before.Policies) != 0 {
		t.Fatal("group exception created a global bundle or advanced generation incorrectly")
	}
	id, err := r.AddGroupException(t.Context(), g.ID, now, globalExceptionEdit("domain:one.example.test"))
	after, readErr := r.ReadDefinitions(t.Context())
	current, modeErr := r.GetGroupMode(t.Context(), g.ID)
	baseAfter, baseErr := r.GetList(t.Context(), m.BaseListID)
	extraAfter, extraErr := r.GetList(t.Context(), m.ExtraListID)
	if err != nil || id != m.ExtraListID || readErr != nil || modeErr != nil || baseErr != nil || extraErr != nil || after.State.Generation != before.State.Generation || !extraAfter.UpdatedAt.Equal(extra.UpdatedAt) || !baseAfter.UpdatedAt.Equal(base.UpdatedAt) || baseAfter.ContentSHA256 != base.ContentSHA256 || !current.UpdatedAt.Equal(m.UpdatedAt) {
		t.Fatal("duplicate group exception changed versions, mode or unrelated base list")
	}
}

func TestDestinationGroupExceptionRechecksModeOwnershipAndRollsBackLateFailure(t *testing.T) {
	for _, problem := range []string{"closed", "foreign", "base", "late"} {
		t.Run(problem, func(t *testing.T) {
			r, g, now := modeGroupFixture(t)
			m := domain.DestGroupMode{GroupID: g.ID, Mode: "allowlist", Stage: "trial"}
			if err := r.SaveGroupMode(t.Context(), &m, m.UpdatedAt, initialModeLists(t), now); err != nil {
				t.Fatal(err)
			}
			extra, err := r.GetList(t.Context(), m.ExtraListID)
			if err != nil {
				t.Fatal(err)
			}
			switch problem {
			case "closed":
				err = r.db.Model(&destGroupModeRow{}).Where("group_id = ?", g.ID).Updates(map[string]any{"mode": "open", "stage": ""}).Error
			case "foreign":
				err = r.db.Model(&destListRow{}).Where("id = ?", m.ExtraListID).Update("owner_group_id", g.ID+99).Error
			case "base":
				err = r.db.Model(&destGroupModeRow{}).Where("group_id = ?", g.ID).Update("extra_list_id", m.BaseListID).Error
			case "late":
				const callback = "group_exception_late_failure"
				err = r.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "dest_policy_state" {
						tx.AddError(errors.New("private-late-group-marker"))
					}
				})
				t.Cleanup(func() { r.db.Callback().Update().Remove(callback) })
			}
			if err != nil {
				t.Fatal(err)
			}
			called := false
			id, err := r.AddGroupException(t.Context(), g.ID, now, func(list domain.DestList) (domain.DestList, error) {
				called = true
				return globalExceptionEdit("domain:example.test")(list)
			})
			if err == nil || id != 0 || called != (problem == "late") {
				t.Fatal("group append ignored current mode/ownership or returned partial identity")
			}
			after, readErr := r.GetList(t.Context(), m.ExtraListID)
			state, stateErr := r.State(t.Context())
			if readErr != nil || stateErr != nil || after.ContentSHA256 != extra.ContentSHA256 || !after.UpdatedAt.Equal(extra.UpdatedAt) || state.Generation != 1 {
				t.Fatal("failed group append retained partial content/version/generation")
			}
		})
	}
}
