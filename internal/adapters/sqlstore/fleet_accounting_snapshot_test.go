package sqlstore

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestFleetSnapshotAndMutableCallersShareOnlyTheirCurrentBatch(t *testing.T) {
	var reads fleetAccountingReads
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	load := func(context.Context) ([]domain.PSPClientAccounting, error) {
		call := calls.Add(1)
		if call == 1 {
			close(started)
			<-release
		}
		return []domain.PSPClientAccounting{{ID: call, UserID: 7, PanelID: 11, LastRawUpBytes: 13, LastRawDownBytes: 17, LastCounterEpoch: 1 << 55}}, nil
	}
	first := startFleetRead(&reads, t.Context(), load)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("initial current read did not start")
	}
	type result struct {
		snapshot domain.FleetAccountingSnapshot
		rows     []domain.PSPClientAccounting
		err      error
	}
	results := make(chan result, 65)
	for i := range 65 {
		go func() {
			if i%2 == 0 {
				snapshot, err := reads.readSnapshot(t.Context(), load)
				results <- result{snapshot: snapshot, err: err}
			} else {
				rows, err := reads.read(t.Context(), load)
				results <- result{rows: rows, err: err}
			}
		}()
	}
	awaitFleetPending(t, &reads, 65)
	close(release)
	if first := awaitFleetRead(t, first); first.err != nil || len(first.rows) != 1 || first.rows[0].ID != 1 {
		t.Fatal("original batch lost its own current read")
	}
	want := domain.PSPClientAccounting{ID: 2, UserID: 7, PanelID: 11, LastRawUpBytes: 13, LastRawDownBytes: 17, LastCounterEpoch: 1 << 55}
	var snapshots []domain.FleetAccountingSnapshot
	for range 65 {
		select {
		case got := <-results:
			if got.err != nil {
				t.Fatal(got.err)
			}
			if got.rows != nil {
				if len(got.rows) != 1 || cap(got.rows) != 1 || got.rows[0] != want {
					t.Fatal("mutable caller lost complete independent current rows")
				}
				got.rows[0] = domain.PSPClientAccounting{}
			} else {
				if got.snapshot.Len() != 1 || got.snapshot.Row(0) != want {
					t.Fatal("immutable caller reused an earlier SQL batch")
				}
				snapshots = append(snapshots, got.snapshot)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("mixed fleet readers did not finish")
		}
	}
	for _, snapshot := range snapshots {
		if snapshot.Row(0) != want {
			t.Fatal("mutable sibling changed shared immutable accounting")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("mixed readers did not share their one following query")
	}
	latest, err := reads.readSnapshot(t.Context(), load)
	if err != nil || latest.Len() != 1 || latest.Row(0).ID != 3 || calls.Load() != 3 {
		t.Fatal("sequential snapshot reused a completed batch")
	}
	failure := errors.New("partial snapshot read")
	failed, err := reads.readSnapshot(t.Context(), func(context.Context) ([]domain.PSPClientAccounting, error) {
		return []domain.PSPClientAccounting{want}, failure
	})
	if failed.Len() != 0 || failed.CloneRows() != nil || !errors.Is(err, failure) {
		t.Fatal("failed read exposed a partial immutable snapshot")
	}
}

func TestSQLFleetSnapshotPreservesCurrentWritesDeletesAndOwnedResults(t *testing.T) {
	r, _ := inboundBatchFixture(t, 0)
	initial := pspClientRow{ID: 7, UserID: 3, PanelID: 11, LastRawUpBytes: -17, LastRawDownBytes: 23, LastCounterEpoch: 1 << 55}
	if err := r.db.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}
	first, err := r.ReadFleetAccountingSnapshot(t.Context())
	if err != nil || first.Len() != 1 || first.Row(0) != (domain.PSPClientAccounting{ID: 7, UserID: 3, PanelID: 11, LastRawUpBytes: -17, LastRawDownBytes: 23, LastCounterEpoch: 1 << 55}) {
		t.Fatal("SQL snapshot lost complete numeric fields")
	}
	if err := r.db.Model(&pspClientRow{}).Where("id = ?", 7).Updates(map[string]any{"user_id": 5, "panel_id": 13, "last_raw_up_bytes": 29, "last_raw_down_bytes": 31, "last_counter_epoch": 9}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&pspClientRow{ID: 2, UserID: 1, PanelID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	second, err := r.ReadFleetAccountingSnapshot(t.Context())
	want := []domain.PSPClientAccounting{{ID: 2, UserID: 1, PanelID: 1}, {ID: 7, UserID: 5, PanelID: 13, LastRawUpBytes: 29, LastRawDownBytes: 31, LastCounterEpoch: 9}}
	if err != nil || !reflect.DeepEqual(second.CloneRows(), want) || first.Row(0).LastRawUpBytes != -17 {
		t.Fatal("new SQL batch lost ordering, ownership changes or baseline writes")
	}
	mutable, err := r.ListFleetAccounting(t.Context())
	if err != nil || !reflect.DeepEqual(mutable, want) {
		t.Fatal("mutable API changed its complete current result")
	}
	mutable[0] = domain.PSPClientAccounting{}
	if !reflect.DeepEqual(second.CloneRows(), want) {
		t.Fatal("mutable API changed a retained immutable read")
	}
	if err := r.db.Where("id IN ?", []int64{2, 7}).Delete(&pspClientRow{}).Error; err != nil {
		t.Fatal(err)
	}
	empty, err := r.ReadFleetAccountingSnapshot(t.Context())
	if err != nil || empty.Len() != 0 || empty.CloneRows() == nil || second.Len() != 2 {
		t.Fatal("deleted rows survived in a later SQL read")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if canceled, err := r.ReadFleetAccountingSnapshot(ctx); canceled.CloneRows() != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled SQL snapshot exposed current or stale rows")
	}
}
