package domain

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestNodeTaskLifecycleSnapshotFreezesOriginalPolicyAndFloor(t *testing.T) {
	const issued = int64(1_700_000_000_000)
	const deadline = issued + 300_000
	policy := DefaultNodeTaskLifecyclePolicy()
	snapshot, err := NewNodeTaskLifecycleSnapshot(issued, deadline, policy)
	if err != nil {
		t.Fatal(err)
	}
	// This is a fixed millisecond duration, independent of calendar/DST.
	const wantFloor = int64(1_707_776_300_000)
	if snapshot.IssuedAtMS != issued || snapshot.NotAfterMS != deadline ||
		snapshot.Policy != policy || snapshot.FullResultRetainUntilMS != wantFloor {
		t.Fatalf("snapshot = %+v, want original times/policy and floor %d", snapshot, wantFloor)
	}
	policy.ResultRetentionDays = 365
	clone := snapshot.Clone()
	clone.Policy.BackupRestoreDays = 1
	clone.NotAfterMS++
	if snapshot.Policy != DefaultNodeTaskLifecyclePolicy() || snapshot.NotAfterMS != deadline {
		t.Fatal("caller policy or clone mutation changed original snapshot")
	}
	if (*NodeTaskLifecycleSnapshot)(nil).Clone() != nil {
		t.Fatal("nil clone must preserve legacy absence")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	const wantJSON = `{"issued_at_ms":1700000000000,"not_after_ms":1700000300000,"policy":{"offline_reconcile_days":30,"backup_restore_days":30,"result_retention_days":90},"full_result_retain_until_ms":1707776300000}`
	if string(payload) != wantJSON {
		t.Fatalf("snapshot JSON = %s, want %s", payload, wantJSON)
	}
	var roundTrip NodeTaskLifecycleSnapshot
	if err := json.Unmarshal(payload, &roundTrip); err != nil || roundTrip != *snapshot || roundTrip.Validate() != nil {
		t.Fatalf("round-trip = %+v, error %v", roundTrip, err)
	}
}

func TestNodeTaskLifecycleSnapshotRejectsMissingAndImmutableFloorDrift(t *testing.T) {
	if err := (*NodeTaskLifecycleSnapshot)(nil).Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("nil snapshot validation = %v, want ErrValidation", err)
	}
	valid, err := NewNodeTaskLifecycleSnapshot(1000, 2000, DefaultNodeTaskLifecyclePolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*NodeTaskLifecycleSnapshot)
	}{
		{"zero_issued", func(s *NodeTaskLifecycleSnapshot) { s.IssuedAtMS = 0 }},
		{"negative_issued", func(s *NodeTaskLifecycleSnapshot) { s.IssuedAtMS = -1 }},
		{"zero_deadline", func(s *NodeTaskLifecycleSnapshot) { s.NotAfterMS = 0 }},
		{"equal_deadline", func(s *NodeTaskLifecycleSnapshot) { s.NotAfterMS = s.IssuedAtMS }},
		{"earlier_deadline", func(s *NodeTaskLifecycleSnapshot) { s.NotAfterMS = s.IssuedAtMS - 1 }},
		{"zero_policy", func(s *NodeTaskLifecycleSnapshot) { s.Policy = NodeTaskLifecyclePolicy{} }},
		{"short_retention", func(s *NodeTaskLifecycleSnapshot) { s.Policy.ResultRetentionDays = 29 }},
		{"unbounded_days", func(s *NodeTaskLifecycleSnapshot) { s.Policy.ResultRetentionDays = int(^uint(0) >> 1) }},
		{"zero_floor", func(s *NodeTaskLifecycleSnapshot) { s.FullResultRetainUntilMS = 0 }},
		{"shorter_floor", func(s *NodeTaskLifecycleSnapshot) { s.FullResultRetainUntilMS-- }},
		{"extended_original_floor", func(s *NodeTaskLifecycleSnapshot) { s.FullResultRetainUntilMS++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid.Clone()
			test.change(candidate)
			before := *candidate
			if err := candidate.Validate(); !errors.Is(err, ErrValidation) {
				t.Fatalf("validation = %v, want ErrValidation", err)
			}
			if *candidate != before {
				t.Fatal("validation silently rewrote snapshot")
			}
		})
	}
	for _, pair := range [][2]int64{{0, 2000}, {-1, 2000}, {1000, 1000}, {1000, 999}} {
		if snapshot, err := NewNodeTaskLifecycleSnapshot(pair[0], pair[1], valid.Policy); snapshot != nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("constructor(%d,%d) = (%+v,%v), want nil/ErrValidation", pair[0], pair[1], snapshot, err)
		}
	}
}

func TestNodeTaskLifecycleSnapshotInt64BoundaryFailsClosed(t *testing.T) {
	policy := NodeTaskLifecyclePolicy{OfflineReconcileDays: 1, BackupRestoreDays: 1, ResultRetentionDays: 1}
	const lastSafeDeadline = math.MaxInt64 - nodeTaskLifecycleDayMS
	snapshot, err := NewNodeTaskLifecycleSnapshot(1, lastSafeDeadline, policy)
	if err != nil || snapshot.FullResultRetainUntilMS != math.MaxInt64 || snapshot.Validate() != nil {
		t.Fatalf("last safe floor = (%+v,%v), want MaxInt64", snapshot, err)
	}
	for _, deadline := range []int64{lastSafeDeadline + 1, math.MaxInt64} {
		if snapshot, err := NewNodeTaskLifecycleSnapshot(1, deadline, policy); snapshot != nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("overflow deadline %d = (%+v,%v), want nil/ErrValidation", deadline, snapshot, err)
		}
	}
	policy = NodeTaskLifecyclePolicy{OfflineReconcileDays: MaxNodeTaskLifecycleDays, BackupRestoreDays: MaxNodeTaskLifecycleDays, ResultRetentionDays: MaxNodeTaskLifecycleDays}
	snapshot, err = NewNodeTaskLifecycleSnapshot(1, 2, policy)
	if err != nil || snapshot.FullResultRetainUntilMS != 315_360_000_002 {
		t.Fatalf("maximum bounded days = (%+v,%v), want safe int64 multiplication", snapshot, err)
	}
}
