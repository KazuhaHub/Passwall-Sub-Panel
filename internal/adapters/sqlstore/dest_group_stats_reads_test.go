package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func groupStatsReadFixture(t *testing.T) (*DestAuditRepo, domain.DestGroupStatsQuery, int64) {
	t.Helper()
	r, trial, panel, _ := groupTrialReadFixture(t)
	return r, domain.DestGroupStatsQuery{GroupIDs: []int64{trial.GroupID, 888888, trial.GroupID}, TrialSince: trial.Since, DeniedSince: trial.Since, Until: trial.Until}, panel
}

func TestDestinationGroupStatsMergeAllSitesAndUseIndependentWindows(t *testing.T) {
	r, q, _ := groupStatsReadFixture(t)
	ids := append([]int64{}, q.GroupIDs...)
	got, err := r.ReadDestinationGroupStats(t.Context(), q)
	if err != nil || len(got) != 2 || got[q.GroupIDs[0]] != (domain.DestGroupStats{TrialSites: 27, TrialCount: 38, DeniedCount: 1}) || got[888888] != (domain.DestGroupStats{}) || !reflect.DeepEqual(ids, q.GroupIDs) {
		t.Fatalf("batch group stats: %+v %v", got, err)
	}
	q.DeniedSince = q.TrialSince.Add(time.Hour)
	got, err = r.ReadDestinationGroupStats(t.Context(), q)
	if err != nil || got[q.GroupIDs[0]].TrialSites != 27 || got[q.GroupIDs[0]].DeniedCount != 0 {
		t.Fatal("denied window changed trial scope or included an older denial")
	}
	q.TrialSince = q.DeniedSince
	got, err = r.ReadDestinationGroupStats(t.Context(), q)
	if err != nil || got[q.GroupIDs[0]] != (domain.DestGroupStats{TrialSites: 1, TrialCount: 5}) {
		t.Fatal("trial window lost cross-hour merging or source isolation")
	}
	q.GroupIDs = nil
	empty, err := r.ReadDestinationGroupStats(t.Context(), q)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("empty requested groups became a fleet-wide query")
	}
}

func TestDestinationGroupStatsSaturateWithoutSQLSumAndDiscardCorruption(t *testing.T) {
	r, q, panel := groupStatsReadFixture(t)
	source := fmt.Sprintf("g%d", q.GroupIDs[0])
	rows := []destHitRow{
		cleanupHit(panel, 0, q.TrialSince.UnixMilli(), source, "observe", "overflow.test", 0),
		cleanupHit(panel+1, 0, q.TrialSince.UnixMilli(), source, "observe", "overflow.test", 0),
		cleanupHit(panel, 123456, q.TrialSince.UnixMilli(), source, "block", "private-overflow-host.test", 443),
	}
	rows[0].Count, rows[2].Count = math.MaxInt64, math.MaxInt64
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDestinationGroupStats(t.Context(), q)
	if err != nil || got[q.GroupIDs[0]] != (domain.DestGroupStats{TrialSites: 28, TrialCount: math.MaxInt64, DeniedCount: math.MaxInt64}) {
		t.Fatal("group stats overflowed, truncated sites or summed in SQL")
	}
	if err := r.db.Model(&destHitRow{}).Where("dest = ?", "overflow.test").Update("count", -1).Error; err != nil {
		t.Fatal(err)
	}
	got, err = r.ReadDestinationGroupStats(t.Context(), q)
	if err == nil || got != nil {
		t.Fatal("corrupt trial counters returned partial group stats")
	}
}

func TestDestinationGroupStatsKeepDistinctSiteSpellingsAcrossHoursAndNodes(t *testing.T) {
	r, q, panel := groupStatsReadFixture(t)
	rows := []destHitRow{}
	for _, site := range []string{"a-b.test", "ab.test"} {
		rows = append(rows,
			cleanupHit(panel, 0, q.TrialSince.UnixMilli(), fmt.Sprintf("g%d", q.GroupIDs[0]), "observe", site, 0),
			cleanupHit(panel+1, 0, q.TrialSince.Add(time.Hour).UnixMilli(), fmt.Sprintf("g%d", q.GroupIDs[0]), "observe", site, 0))
	}
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDestinationGroupStats(t.Context(), q)
	if err != nil || got[q.GroupIDs[0]] != (domain.DestGroupStats{TrialSites: 29, TrialCount: 42, DeniedCount: 1}) {
		t.Fatal("site ordering merged distinct spellings or counted repeated sites twice")
	}
}

func TestDestinationGroupStatsChunkQueriesStayNarrowAndWithholdLateFailure(t *testing.T) {
	r, q, _ := groupStatsReadFixture(t)
	q.GroupIDs = q.GroupIDs[:1]
	for i := int64(1000); i < 1514; i++ {
		q.GroupIDs = append(q.GroupIDs, q.GroupIDs[0]+i)
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	calls, fail := 0, false
	const callback = "private_group_stats_projection"
	if err := r.db.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
		calls++
		if tx.Statement.Table != "dest_hits" || !reflect.DeepEqual(tx.Statement.Selects, []string{"source", "dest", "count"}) && !reflect.DeepEqual(tx.Statement.Selects, []string{"source", "count"}) {
			t.Error("group counters loaded accounts, full-host denial records or metadata")
		}
		if fail && calls == 3 {
			_ = tx.AddError(errors.New("private-group-counter-driver-detail"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove(callback) })
	got, err := r.ReadDestinationGroupStats(t.Context(), q)
	if err != nil || len(got) != 515 || calls != 4 || got[q.GroupIDs[0]].TrialCount != 38 || len(recorder.queries) != 0 {
		t.Fatalf("chunked projection: groups=%d calls=%d traces=%d error=%v", len(got), calls, len(recorder.queries), err)
	}
	calls, fail = 0, true
	got, err = r.ReadDestinationGroupStats(t.Context(), q)
	if calls != 3 || got != nil || err != errAuditStorage || len(recorder.queries) != 0 {
		t.Fatal("late group-counter failure leaked traces, details or partial groups")
	}
}

func TestDestinationGroupStatsRejectInvalidAndCanceledBeforeStorage(t *testing.T) {
	r, q, _ := groupStatsReadFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := r.ReadDestinationGroupStats(ctx, q)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("canceled group stats did not preserve cancellation")
	}
	q.GroupIDs = []int64{0}
	if got, err := r.ReadDestinationGroupStats(t.Context(), q); !errors.Is(err, domain.ErrValidation) || got != nil {
		t.Fatal("invalid group scope became a broad stats read")
	}
}
