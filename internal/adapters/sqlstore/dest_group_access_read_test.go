package sqlstore

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func TestDestinationGroupModeSummaryIsBulkAndReadsNoPrivateLists(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	trial, enforce := g.ID+1, g.ID+2
	for _, row := range []destGroupModeRow{
		{GroupID: trial, Mode: "allowlist", Stage: "trial", ListIDs: jsonInt64s{77}, BaseListID: 78, ExtraListID: 79, UpdatedAt: now},
		{GroupID: enforce, Mode: "allowlist", Stage: "enforce", ListIDs: jsonInt64s{87}, BaseListID: 88, ExtraListID: 89, UpdatedAt: now},
	} {
		if err := r.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	name := "group_access_summary_columns"
	if err := r.db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		calls++
		if tx.Statement.Table != "dest_group_modes" || !reflect.DeepEqual(tx.Statement.Selects, []string{"group_id", "mode", "stage"}) {
			t.Error("staff projection read private definitions, lists or unrelated data")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.db.Callback().Query().Remove(name) })
	got, err := r.ReadDestinationGroupModes(t.Context(), []int64{g.ID, trial, enforce, trial})
	want := map[int64]domain.DestGroupAccessMode{g.ID: domain.DestGroupAccessOpen, trial: domain.DestGroupAccessTrial, enforce: domain.DestGroupAccessEnforce}
	if err != nil || !reflect.DeepEqual(got, want) || calls != 1 {
		t.Fatalf("group summaries %+v, calls=%d: %v", got, calls, err)
	}
	empty, err := r.ReadDestinationGroupModes(t.Context(), nil)
	if err != nil || empty == nil || len(empty) != 0 || calls != 1 {
		t.Fatal("empty page performed a query or invented data")
	}
	if _, err := r.ReadDestinationGroupModes(t.Context(), []int64{0}); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("invalid group identity accepted")
	}
}

func TestDestinationGroupModeSummaryNeverPretendsFailedOrCorruptModeIsOpen(t *testing.T) {
	r, g, now := modeGroupFixture(t)
	if err := r.db.Create(&destGroupModeRow{GroupID: g.ID, Mode: "open", Stage: "trial", UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if got, err := r.ReadDestinationGroupModes(t.Context(), []int64{g.ID}); !errors.Is(err, domain.ErrUnavailable) || got != nil {
		t.Fatal("corrupt stage returned partial/default-open mode")
	}
	if err := r.db.Where("group_id = ?", g.ID).Delete(&destGroupModeRow{}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := r.ReadDestinationGroupModes(ctx, []int64{g.ID}); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("canceled query returned default-open mode")
	}
	name := "group_access_summary_failure"
	if err := r.db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) { tx.AddError(errors.New("private-group-mode-driver-marker")) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.db.Callback().Query().Remove(name) })
	if got, err := r.ReadDestinationGroupModes(t.Context(), []int64{g.ID}); err != domain.ErrUnavailable || got != nil {
		t.Fatal("storage failure exposed driver details or partial metadata")
	}
}
