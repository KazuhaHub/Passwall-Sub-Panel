package nodesync

import (
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func cloneFleetTestReports(in map[string]fleetCounterReport) map[string]fleetCounterReport {
	out := make(map[string]fleetCounterReport, len(in))
	for id, report := range in {
		report.clients = slices.Clone(report.clients)
		out[id] = report
	}
	return out
}

func cloneFleetTestAnchors(in map[int64]protocol.ClientCounters) map[int64]protocol.ClientCounters {
	out := make(map[int64]protocol.ClientCounters, len(in))
	for id, counter := range in {
		out[id] = counter
	}
	return out
}

func assertFleetIndexMatchesIndependentPaths(t *testing.T, clients []domain.PSPClientAccounting, agents []*domain.NodeAgent, reports map[string]fleetCounterReport, anchors map[int64]protocol.ClientCounters, freshness int, now time.Time) {
	t.Helper()
	before := cloneFleetTestReports(reports)
	rowsBefore := slices.Clone(clients)
	old, next := &Service{anchors: cloneFleetTestAnchors(anchors)}, &Service{anchors: cloneFleetTestAnchors(anchors)}
	index := make(map[int64]*domain.PSPClientAccounting, len(clients))
	for i := range clients {
		index[clients[i].ID] = &clients[i]
	}
	wantPending := old.pendingUsageFromReports(index, agents, reports)
	wantCoverage, wantOldest := aggregateCoverageFromReports(clients, agents, reports, freshness, now)
	gotPending, gotCoverage, gotOldest := next.accountFleetFromReports(clients, agents, reports, freshness, now)
	if !reflect.DeepEqual(gotPending, wantPending) || gotCoverage != wantCoverage || gotOldest != wantOldest || !reflect.DeepEqual(next.anchors, old.anchors) {
		t.Fatal("shared fleet index changed pending usage, coverage, oldest receipt or reset anchors")
	}
	shared := &Service{anchors: cloneFleetTestAnchors(anchors)}
	snapshot := domain.NewFleetAccountingSnapshot(clients)
	gotPending, gotCoverage, gotOldest = shared.accountFleetSnapshotFromReports(snapshot, agents, reports, freshness, now)
	if !reflect.DeepEqual(gotPending, wantPending) || gotCoverage != wantCoverage || gotOldest != wantOldest || !reflect.DeepEqual(shared.anchors, old.anchors) {
		t.Fatal("immutable fleet snapshot changed independent accounting semantics")
	}
	if !reflect.DeepEqual(before, reports) || !reflect.DeepEqual(rowsBefore, clients) {
		t.Fatal("shared fleet index mutated its current inputs")
	}
}

func TestFleetAccountingIndexMatchesIndependentPathsAcrossCurrentCounterAndOwnershipStates(t *testing.T) {
	now := time.UnixMilli(1000000)
	rng := rand.New(rand.NewPCG(71, 913))
	for round := range 40 {
		var clients []domain.PSPClientAccounting
		agents := []*domain.NodeAgent{nil}
		reports := make(map[string]fleetCounterReport)
		anchors := make(map[int64]protocol.ClientCounters)
		for owner := 1; owner <= 7; owner++ {
			agentID := fmt.Sprintf("owned-%d", owner)
			agents = append(agents, &domain.NodeAgent{AgentID: agentID, PanelID: int64(owner)})
			report := fleetCounterReport{receivedAtMS: now.UnixMilli() - []int64{0, 60000, 60001, 999999, -1, 1, 500}[((owner+round)%7)]}
			for user := 1; user <= 31; user++ {
				id := int64(owner*1000 + user)
				client := domain.PSPClientAccounting{ID: id, UserID: int64(user), PanelID: int64(owner), LastRawUpBytes: int64(rng.IntN(200)), LastRawDownBytes: int64(rng.IntN(200)), LastCounterEpoch: uint64(rng.IntN(4))}
				clients = append(clients, client)
				counter := fleetClientCounter{key: protocol.NewClientKey(id), present: rng.IntN(2) == 0, upBytes: int64(rng.IntN(600)) - 20, downBytes: int64(rng.IntN(600)) - 20, counterEpoch: uint64(rng.IntN(4))}
				if user%6 != 0 {
					report.clients = append(report.clients, counter)
				}
				if user%7 == 0 {
					counter.present = !counter.present
					counter.upBytes += 7
					report.clients = append(report.clients, counter)
				}
				if user%3 == 0 {
					anchors[id] = protocol.ClientCounters{Key: counter.key, UpBytes: 11, DownBytes: 13, CounterEpoch: uint64(rng.IntN(4))}
				}
			}
			// Foreign ownership, unknown IDs and noncanonical identities must not
			// enlarge quota numerators or improve current coverage.
			report.clients = append(report.clients,
				fleetClientCounter{key: protocol.NewClientKey(int64(((owner%7)+1)*1000 + 1)), upBytes: 999, counterEpoch: 1, present: true},
				fleetClientCounter{key: protocol.ClientKey("cli_0001"), upBytes: 999, counterEpoch: 1, present: true},
				fleetClientCounter{key: protocol.ClientKey("usr_1"), upBytes: 999, counterEpoch: 1, present: true},
				fleetClientCounter{key: protocol.NewClientKey(9999999), upBytes: math.MaxInt64, counterEpoch: 2},
			)
			if owner != (round%7)+1 {
				reports[agentID] = report
			}
		}
		clients = append(clients, domain.PSPClientAccounting{ID: 99998, UserID: 99, PanelID: 99})
		reports["retired"] = fleetCounterReport{receivedAtMS: now.UnixMilli(), clients: []fleetClientCounter{{key: protocol.NewClientKey(1001), upBytes: math.MaxInt64, counterEpoch: 1, present: true}}}
		assertFleetIndexMatchesIndependentPaths(t, clients, agents, reports, anchors, 60, now)
	}
}

func TestFleetAccountingIndexPreservesUnusualAdapterInputsAndSaturation(t *testing.T) {
	now := time.UnixMilli(100000)
	base := []domain.PSPClientAccounting{{ID: 1, UserID: 1, PanelID: 1, LastCounterEpoch: 1}, {ID: 2, UserID: 1, PanelID: 1, LastCounterEpoch: 1}}
	agents := []*domain.NodeAgent{{AgentID: "a", PanelID: 1}, nil}
	reports := map[string]fleetCounterReport{"a": {receivedAtMS: now.UnixMilli(), clients: []fleetClientCounter{
		{key: protocol.NewClientKey(1), present: true, upBytes: math.MaxInt64, downBytes: 1, counterEpoch: 1},
		{key: protocol.NewClientKey(1), present: false, upBytes: 2, counterEpoch: 1},
		{key: protocol.NewClientKey(2), present: true, upBytes: 3, counterEpoch: 1},
		{key: protocol.ClientKey("cli_0"), present: true}, {key: protocol.ClientKey("cli_-1"), present: true},
	}}}
	for _, rows := range [][]domain.PSPClientAccounting{nil, base, append(slices.Clone(base), base[0]), append(slices.Clone(base), domain.PSPClientAccounting{ID: 0, PanelID: 1}), append(slices.Clone(base), domain.PSPClientAccounting{ID: -1, PanelID: 1})} {
		for _, owners := range [][]*domain.NodeAgent{nil, agents, append(slices.Clone(agents), &domain.NodeAgent{AgentID: "b", PanelID: 1}), append(slices.Clone(agents), &domain.NodeAgent{AgentID: "a", PanelID: 2})} {
			for _, freshness := range []int{0, 1, 60} {
				assertFleetIndexMatchesIndependentPaths(t, rows, owners, reports, nil, freshness, now)
			}
		}
	}
}

func TestFleetAccountingIndexReadsCurrentReportsAndDoesNotAliasResults(t *testing.T) {
	now := time.UnixMilli(100000)
	clients := []domain.PSPClientAccounting{{ID: 1, UserID: 1, PanelID: 1, LastCounterEpoch: 1}}
	agents := []*domain.NodeAgent{{AgentID: "a", PanelID: 1}}
	s := &Service{anchors: map[int64]protocol.ClientCounters{}, reports: map[string]receivedFullReport{"a": {receivedAtMS: now.UnixMilli(), report: protocol.NodeReport{Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), UpBytes: 10, CounterEpoch: 1, Present: true}}}}}}
	first, coverage, _ := s.accountFleet(clients, agents, 60, now)
	if first[1] != 10 || coverage.Entries != 1 || coverage.EntriesStale != 0 {
		t.Fatal("initial current fleet accounting changed")
	}
	s.mu.Lock()
	s.reports["a"] = receivedFullReport{receivedAtMS: now.UnixMilli() - 60001, report: protocol.NodeReport{Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), UpBytes: 30, CounterEpoch: 1, Present: false}}}}
	s.mu.Unlock()
	first[1] = 999
	next, coverage, oldest := s.accountFleet(clients, agents, 60, now)
	if next[1] != 30 || coverage.EntriesStale != 1 || oldest != now.UnixMilli()-60001 {
		t.Fatal("fleet accounting cached an old report, aliased output or dropped stale pending usage")
	}
	clients[0].LastRawUpBytes = 20
	if current, _, _ := s.accountFleet(clients, agents, 60, now); current[1] != 10 {
		t.Fatal("fleet accounting ignored the current persisted baseline")
	}
}

func TestFleetAccountingChunksPreserveMixedEpochTransitionsAcrossReadAndWriteBoundaries(t *testing.T) {
	now := time.UnixMilli(100000)
	var clients []domain.PSPClientAccounting
	report := fleetCounterReport{receivedAtMS: now.UnixMilli()}
	anchors := make(map[int64]protocol.ClientCounters)
	for id := int64(1); id <= 193; id++ {
		client := domain.PSPClientAccounting{ID: id, UserID: id%7 + 1, PanelID: 1, LastCounterEpoch: 2, LastRawUpBytes: 5, LastRawDownBytes: 7}
		counter := fleetClientCounter{key: protocol.NewClientKey(id), present: true, upBytes: 15, downBytes: 17, counterEpoch: 2}
		switch {
		case id >= 65 && id <= 128:
			counter.counterEpoch = 3
			if id%2 == 0 {
				anchors[id] = protocol.ClientCounters{CounterEpoch: 3, UpBytes: 11, DownBytes: 13}
			}
		case id >= 129:
			anchors[id] = protocol.ClientCounters{CounterEpoch: 9, UpBytes: 99, DownBytes: 101}
			if id <= 192 {
				counter.counterEpoch = 0
			}
		}
		clients = append(clients, client)
		report.clients = append(report.clients, counter)
	}
	// Keep the duplicate's additional delta and last-counter presence across
	// the final partial chunk; a noncanonical key cannot change either one.
	report.clients = append(report.clients,
		fleetClientCounter{key: protocol.NewClientKey(1), present: false, upBytes: 19, downBytes: 17, counterEpoch: 2},
		fleetClientCounter{key: protocol.ClientKey("cli_0001"), present: true, upBytes: 999, counterEpoch: 2})
	agents := []*domain.NodeAgent{{AgentID: "owned", PanelID: 1}}
	reports := map[string]fleetCounterReport{"owned": report}
	assertFleetIndexMatchesIndependentPaths(t, clients, agents, reports, anchors, 60, now)
	s := &Service{anchors: cloneFleetTestAnchors(anchors)}
	pending, coverage, oldest := s.accountFleetFromReports(clients, agents, reports, 60, now)
	var sum int64
	for _, amount := range pending {
		sum += amount
	}
	_, retainedCurrent := s.anchors[193]
	if sum != 1580 || len(pending) != 7 || coverage.Entries != 193 || coverage.Subjects != 7 || coverage.EntriesStale != 1 || oldest != now.UnixMilli() || retainedCurrent || !reflect.DeepEqual(s.anchors[129], anchors[129]) || s.anchors[65].CounterEpoch != 3 || s.anchors[65].UpBytes != 15 {
		t.Fatal("read/write quota chunks changed deltas, coverage, zero epochs or anchor transitions")
	}
}

func TestFleetAccountingSharedIndexReducesPerResponseAllocationsForComplete65OwnerFleet(t *testing.T) {
	var clients []domain.PSPClientAccounting
	var agents []*domain.NodeAgent
	reports := make(map[string]fleetCounterReport)
	now := time.UnixMilli(100000)
	for owner := range 65 {
		name := fmt.Sprintf("owned-%d", owner)
		agents = append(agents, &domain.NodeAgent{AgentID: name, PanelID: int64(owner + 1)})
		report := fleetCounterReport{receivedAtMS: now.UnixMilli()}
		for user := range 200 {
			id := int64(owner*200 + user + 1)
			clients = append(clients, domain.PSPClientAccounting{ID: id, UserID: int64(user + 1), PanelID: int64(owner + 1), LastCounterEpoch: 1})
			report.clients = append(report.clients, fleetClientCounter{key: protocol.NewClientKey(id), present: true, upBytes: 10, counterEpoch: 1})
		}
		reports[name] = report
	}
	assertFleetIndexMatchesIndependentPaths(t, clients, agents, reports, nil, 60, now)
	s := &Service{anchors: map[int64]protocol.ClientCounters{}}
	snapshot := domain.NewFleetAccountingSnapshot(clients)
	measure := func(mode int) float64 {
		return testing.AllocsPerRun(3, func() {
			var coverage protocol.SegmentCounts
			switch mode {
			case 2:
				_, coverage, _ = s.accountFleetSnapshotFromReports(snapshot, agents, reports, 60, now)
			case 1:
				_, coverage, _ = s.accountFleetFromReports(clients, agents, reports, 60, now)
			default:
				_, coverage, _ = s.accountFleetSeparateIndexes(clients, agents, reports, 60, now)
			}
			if coverage.Entries != 13000 || coverage.Subjects != 200 || coverage.EntriesStale != 0 {
				t.Fatal("allocation measurement narrowed the complete fleet")
			}
		})
	}
	old, next, reused := measure(0), measure(1), measure(2)
	if next >= old {
		t.Fatalf("shared fleet index did not reduce allocations: old=%.0f new=%.0f", old, next)
	}
	if reused >= next {
		t.Fatalf("immutable batch did not avoid per-response fleet allocations: slice=%.0f snapshot=%.0f", next, reused)
	}
}
