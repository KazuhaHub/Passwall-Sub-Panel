package domain

import (
	"math"
	"reflect"
	"sync"
	"testing"
)

func TestFleetAccountingSnapshotOwnsCompleteRowsAndIndex(t *testing.T) {
	want := []PSPClientAccounting{
		{ID: 7, UserID: 3, PanelID: 11, LastRawUpBytes: -17, LastRawDownBytes: math.MaxInt64, LastCounterEpoch: math.MaxUint64},
		{ID: 2, UserID: 5, PanelID: 13, LastRawUpBytes: 19, LastRawDownBytes: 23, LastCounterEpoch: 1 << 55},
	}
	input := append([]PSPClientAccounting(nil), want...)
	snapshot := NewFleetAccountingSnapshot(input)
	input[0] = PSPClientAccounting{}
	if snapshot.Len() != len(want) || !snapshot.HasUniqueIDs() {
		t.Fatal("snapshot narrowed or reordered the complete input")
	}
	clone := snapshot.CloneRows()
	if !reflect.DeepEqual(clone, want) || cap(clone) != len(want) {
		t.Fatal("snapshot changed a numeric field or exposed spare capacity")
	}
	clone[0] = PSPClientAccounting{}
	row := snapshot.Row(0)
	row.ID = 99
	if row == snapshot.Row(0) || snapshot.Row(0) != want[0] {
		t.Fatal("a mutable row or slice aliases the immutable snapshot")
	}
	var workers sync.WaitGroup
	for range 65 {
		workers.Go(func() {
			for i, expected := range want {
				index, found := snapshot.IndexForID(expected.ID)
				if !found || index != i || snapshot.Row(index) != expected {
					t.Error("concurrent reader lost original ordering or current fields")
				}
			}
			owned := snapshot.CloneRows()
			owned[0] = PSPClientAccounting{}
		})
	}
	workers.Wait()
	if _, found := snapshot.IndexForID(999); found {
		t.Fatal("unknown client became a fleet entry")
	}
	for _, rows := range [][]PSPClientAccounting{nil, {}} {
		empty := NewFleetAccountingSnapshot(rows)
		if empty.Len() != 0 || !empty.HasUniqueIDs() || !reflect.DeepEqual(empty.CloneRows(), rows) {
			t.Fatal("nil or empty read changed its public representation")
		}
	}
	for _, rows := range [][]PSPClientAccounting{
		{{ID: 1}, {ID: 1, UserID: 2}}, {{ID: 0}}, {{ID: -1}},
	} {
		unusual := NewFleetAccountingSnapshot(rows)
		if unusual.HasUniqueIDs() || !reflect.DeepEqual(unusual.CloneRows(), rows) {
			t.Fatal("unusual IDs were dropped or reinterpreted instead of retaining fallback inputs")
		}
		if _, found := unusual.IndexForID(1); found {
			t.Fatal("invalid input exposed a partial index")
		}
	}
}
