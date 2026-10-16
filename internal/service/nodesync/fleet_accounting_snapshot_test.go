package nodesync

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type immutableFleetClients struct {
	*projectedFleetClients
	reads int
	err   error
}

func (r *immutableFleetClients) ReadFleetAccountingSnapshot(ctx context.Context) (domain.FleetAccountingSnapshot, error) {
	r.reads++
	if err := ctx.Err(); err != nil {
		return domain.FleetAccountingSnapshot{}, err
	}
	if r.err != nil {
		return domain.FleetAccountingSnapshot{}, r.err
	}
	rows := make([]domain.PSPClientAccounting, 0, len(r.rows))
	for _, row := range r.rows {
		if row != nil {
			rows = append(rows, domain.PSPClientAccounting{ID: row.ID, UserID: row.UserID, PanelID: row.PanelID,
				LastRawUpBytes: row.LastRawUpBytes, LastRawDownBytes: row.LastRawDownBytes, LastCounterEpoch: row.LastCounterEpoch})
		}
	}
	return domain.NewFleetAccountingSnapshot(rows), nil
}

func TestFleetAccountingSnapshotUsesCurrentOptionalReadAndCurrentReports(t *testing.T) {
	legacy := &legacyFleetClients{rows: []*domain.PSPClient{{ID: 1, UserID: 7, PanelID: 10, LastCounterEpoch: 1, LastRawUpBytes: 4}}}
	projected := &projectedFleetClients{legacyFleetClients: legacy}
	repo := &immutableFleetClients{projectedFleetClients: projected}
	s := &Service{clients: repo, anchors: make(map[int64]protocol.ClientCounters), reports: map[string]receivedFullReport{
		"owned": {receivedAtMS: 100000, report: protocol.NodeReport{Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), Present: true, CounterEpoch: 1, UpBytes: 14}}}},
	}}
	snapshot, err := s.loadFleetAccountingSnapshot(t.Context())
	if err != nil || snapshot.Len() != 1 || repo.reads != 1 || projected.calls != 0 || legacy.calls != 0 {
		t.Fatal("immutable repository read used mutable or definition fallback")
	}
	agents := []*domain.NodeAgent{{AgentID: "owned", PanelID: 10}}
	pending, coverage, oldest := s.accountFleetSnapshot(snapshot, agents, 60, time.UnixMilli(100000))
	if pending[7] != 10 || coverage.Entries != 1 || coverage.EntriesStale != 0 || oldest != 100000 {
		t.Fatal("snapshot omitted current quota or coverage")
	}
	pending[7] = 999
	s.mu.Lock()
	s.reports["owned"] = receivedFullReport{receivedAtMS: 101000, report: protocol.NodeReport{Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), Present: false, CounterEpoch: 1, UpBytes: 24}}}}
	s.mu.Unlock()
	pending, coverage, oldest = s.accountFleetSnapshot(snapshot, agents, 60, time.UnixMilli(101000))
	if pending[7] != 20 || coverage.EntriesStale != 1 || oldest != 101000 {
		t.Fatal("shared durable snapshot cached live counters, presence or receipt time")
	}
	legacy.rows[0].LastRawUpBytes = 14
	latest, err := s.loadFleetAccountingSnapshot(t.Context())
	if err != nil || latest.Row(0).LastRawUpBytes != 14 || snapshot.Row(0).LastRawUpBytes != 4 || repo.reads != 2 {
		t.Fatal("later repository call reused an old or aliased snapshot")
	}
	for _, fallback := range []ports.PSPClientRepo{projected, legacy} {
		s.clients = fallback
		rows, err := s.loadFleetAccountingSnapshot(t.Context())
		if err != nil || !reflect.DeepEqual(rows.CloneRows(), latest.CloneRows()) {
			t.Fatal("optional snapshot changed a legacy repository's current projection")
		}
	}
	s.clients = repo
	failure := errors.New("snapshot read failed")
	repo.err = failure
	if rows, err := s.loadFleetAccountingSnapshot(t.Context()); rows.Len() != 0 || !errors.Is(err, failure) {
		t.Fatal("snapshot error fell back to partial or stale accounting")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if rows, err := s.loadFleetAccountingSnapshot(ctx); rows.Len() != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled snapshot read returned accounting")
	}
	if projected.calls != 1 || legacy.calls != 1 {
		t.Fatal("failure or cancellation silently tried another repository API")
	}
}
