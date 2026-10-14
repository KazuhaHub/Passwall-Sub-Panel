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

type legacyFleetClients struct {
	ports.PSPClientRepo
	rows  []*domain.PSPClient
	calls int
}

func (r *legacyFleetClients) ListAll(context.Context) ([]*domain.PSPClient, error) {
	r.calls++
	return r.rows, nil
}

type projectedFleetClients struct {
	*legacyFleetClients
	calls int
	err   error
}

func (r *projectedFleetClients) ListFleetAccounting(context.Context) ([]domain.PSPClientAccounting, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	var rows []domain.PSPClientAccounting
	for _, c := range r.rows {
		if c == nil {
			continue
		}
		rows = append(rows, domain.PSPClientAccounting{ID: c.ID, UserID: c.UserID, PanelID: c.PanelID,
			LastRawUpBytes: c.LastRawUpBytes, LastRawDownBytes: c.LastRawDownBytes, LastCounterEpoch: c.LastCounterEpoch})
	}
	return rows, nil
}

func TestFleetAccountingProjectionPreservesPendingUsageAndCoverage(t *testing.T) {
	full := []*domain.PSPClient{
		{ID: 1, UserID: 7, PanelID: 10, Email: "private-email", UUID: "private-uuid", LastCounterEpoch: 1, LastRawUpBytes: 4, LastRawDownBytes: 6},
		{ID: 2, UserID: 7, PanelID: 20, DesiredEnable: true, LastCounterEpoch: 1},
		{ID: 3, UserID: 8, PanelID: 20, LastCounterEpoch: 1},
		{ID: 4, UserID: 9, PanelID: 30, LastCounterEpoch: 1},
		nil, // Old repositories may include nil entries; they never count.
	}
	legacy := &legacyFleetClients{rows: full}
	projected := &projectedFleetClients{legacyFleetClients: legacy}
	service := &Service{clients: projected, anchors: make(map[int64]protocol.ClientCounters), reports: map[string]receivedFullReport{
		"a": {receivedAtMS: 95000, report: protocol.NodeReport{Clients: []protocol.ClientCounters{
			{Key: protocol.NewClientKey(1), Present: false, CounterEpoch: 1, UpBytes: 14, DownBytes: 26},
			{Key: protocol.NewClientKey(2), Present: true, CounterEpoch: 1, UpBytes: 9999},
		}}},
		"b":       {receivedAtMS: 30000, report: protocol.NodeReport{Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(2), Present: true, CounterEpoch: 1, UpBytes: 20, DownBytes: 30}}}},
		"retired": {receivedAtMS: 95000, report: protocol.NodeReport{Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), Present: true, CounterEpoch: 1, UpBytes: 9999}}}},
	}}
	rows, err := service.loadFleetAccounting(t.Context())
	if err != nil || projected.calls != 1 || legacy.calls != 0 {
		t.Fatal("optional projection fell back to full definitions")
	}
	agents := []*domain.NodeAgent{{AgentID: "a", PanelID: 10}, {AgentID: "b", PanelID: 20}}
	service.clients = legacy
	fallback, err := service.loadFleetAccounting(t.Context())
	if err != nil || legacy.calls != 1 || !reflect.DeepEqual(fallback, rows) {
		t.Fatal("legacy repository fallback changed accounting")
	}
	index := func(rows []domain.PSPClientAccounting) map[int64]*domain.PSPClientAccounting {
		out := make(map[int64]*domain.PSPClientAccounting)
		for i := range rows {
			out[rows[i].ID] = &rows[i]
		}
		return out
	}
	wantUsage := service.pendingUsage(index(fallback), agents)
	gotUsage := service.pendingUsage(index(rows), agents)
	if !reflect.DeepEqual(gotUsage, wantUsage) || gotUsage[7] != 80 {
		t.Fatal("projection changed closed-client usage or accepted cross-panel/retired counters")
	}
	wantCoverage, wantOldest := service.aggregateCoverage(fallback, agents, 60, time.UnixMilli(100000))
	coverage, oldest := service.aggregateCoverage(rows, agents, 60, time.UnixMilli(100000))
	if coverage != wantCoverage || oldest != wantOldest || coverage.Entries != 3 || coverage.Subjects != 2 || coverage.EntriesStale != 3 || oldest != 30000 {
		t.Fatal("projection changed disabled, missing, stale or legacy coverage")
	}
	full[0].LastRawUpBytes = 14
	latest, err := service.loadFleetAccounting(t.Context())
	if err != nil || latest[0].LastRawUpBytes != 14 || rows[0].LastRawUpBytes != 4 {
		t.Fatal("accounting values reused stale or aliased baselines")
	}
	if legacy.calls != 2 {
		t.Fatal("legacy accounting did not perform both current reads")
	}
	service.clients = projected
	failure := errors.New("owned accounting read failure")
	projected.err = failure
	if rows, err := service.loadFleetAccounting(t.Context()); rows != nil || !errors.Is(err, failure) || legacy.calls != 2 {
		t.Fatal("failed projection returned fallback or partial accounting")
	}
}

func TestFleetAccountingValuesRetainOnlySixNumericFields(t *testing.T) {
	shape := reflect.TypeFor[domain.PSPClientAccounting]()
	if shape.NumField() != 6 || shape.Size() != 48 {
		t.Fatal("fleet accounting retained full client payload allocations")
	}
	for i := range shape.NumField() {
		if kind := shape.Field(i).Type.Kind(); kind != reflect.Int64 && kind != reflect.Uint64 {
			t.Fatal("fleet accounting contains a credential or lifecycle payload")
		}
	}
}
