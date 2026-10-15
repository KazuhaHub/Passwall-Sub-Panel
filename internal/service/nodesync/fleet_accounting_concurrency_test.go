package nodesync

import (
	"fmt"
	"sync"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestPendingQuotaAndCurrentReportReadsDoNotShareContention(t *testing.T) {
	s := &Service{
		anchors: map[int64]protocol.ClientCounters{},
		reports: map[string]receivedFullReport{"owned": {
			report:       protocol.NodeReport{AgentID: "owned", Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), Present: true, UpBytes: 17, CounterEpoch: 1}}},
			receivedAtMS: 1000,
		}},
	}
	checkIndependent := func(lock sync.Locker, operation func() bool) {
		t.Helper()
		lock.Lock()
		done := make(chan bool, 1)
		go func() { done <- operation() }()
		var valid, finished bool
		select {
		case valid = <-done:
			finished = true
		case <-time.After(5 * time.Second):
		}
		lock.Unlock()
		if !finished {
			// Join the worker before failing even when the old shared-lock
			// implementation is under test, rather than leaking a blocked read.
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
			t.Fatal("pending quota and current report operations blocked each other")
		}
		if !valid {
			t.Fatal("independent operation changed its current values")
		}
	}
	checkIndependent(&s.anchorsMu, func() bool {
		report, at, found := s.fullReport("owned")
		if !found || at != 1000 || len(report.Clients) != 1 || report.Clients[0].UpBytes != 17 {
			return false
		}
		report.Clients[0].UpBytes = 999
		current, _, ok := s.fullReport("owned")
		return ok && current.Clients[0].UpBytes == 17
	})
	checkIndependent(&s.mu, func() bool {
		return s.pendingCounterDelta(domain.PSPClientAccounting{ID: 1, LastRawUpBytes: 7, LastCounterEpoch: 1}, protocol.ClientCounters{Key: protocol.NewClientKey(1), UpBytes: 17, CounterEpoch: 1}) == 10
	})
	checkIndependent(s.anchorsMu.RLocker(), func() bool {
		return s.pendingCounterDelta(domain.PSPClientAccounting{ID: 1, LastRawUpBytes: 7, LastCounterEpoch: 1}, protocol.ClientCounters{Key: protocol.NewClientKey(1), UpBytes: 17, CounterEpoch: 1}) == 10
	})
	checkIndependent(s.anchorsMu.RLocker(), func() bool {
		pending, coverage, at := s.accountFleetFromReports(
			[]domain.PSPClientAccounting{{ID: 1, UserID: 1, PanelID: 1, LastRawUpBytes: 7, LastCounterEpoch: 1}},
			[]*domain.NodeAgent{{AgentID: "owned", PanelID: 1}},
			map[string]fleetCounterReport{"owned": {receivedAtMS: 1000, clients: []fleetClientCounter{{key: protocol.NewClientKey(1), present: true, upBytes: 17, counterEpoch: 1}}}},
			60, time.UnixMilli(1000))
		return len(pending) == 1 && pending[1] == 10 && coverage.Entries == 1 && coverage.EntriesStale == 0 && at == 1000
	})
}

func TestFleetQuotaChunksPreserveAllCurrentCountersDuringParallelReportsAndResets(t *testing.T) {
	for _, anchored := range []bool{false, true} {
		name := "persisted_epoch"
		if anchored {
			name = "reset_anchor"
		}
		t.Run(name, func(t *testing.T) { testParallelFleetQuota(t, anchored) })
	}
}

func testParallelFleetQuota(t *testing.T, anchored bool) {
	t.Helper()
	now := time.UnixMilli(100000)
	var clients []domain.PSPClientAccounting
	var agents []*domain.NodeAgent
	reports := make(map[string]fleetCounterReport)
	s := &Service{anchors: map[int64]protocol.ClientCounters{}, reports: map[string]receivedFullReport{}}
	for owner := range 65 {
		name := fmt.Sprintf("owned-%d", owner)
		agents = append(agents, &domain.NodeAgent{AgentID: name, PanelID: int64(owner + 1)})
		report := fleetCounterReport{receivedAtMS: now.UnixMilli()}
		for user := range 200 {
			id := int64(owner*200 + user + 1)
			key := protocol.NewClientKey(id)
			client := domain.PSPClientAccounting{ID: id, UserID: int64(user + 1), PanelID: int64(owner + 1), LastCounterEpoch: 1}
			if !anchored {
				client.LastCounterEpoch, client.LastRawUpBytes, client.LastRawDownBytes = 2, 5, 7
			}
			clients = append(clients, client)
			report.clients = append(report.clients, fleetClientCounter{key: key, present: true, upBytes: 15, downBytes: 17, counterEpoch: 2})
			if anchored {
				s.anchors[id] = protocol.ClientCounters{Key: key, UpBytes: 5, DownBytes: 7, CounterEpoch: 2}
			}
		}
		reports[name] = report
	}
	var workers sync.WaitGroup
	errors := make(chan string, 17)
	start := make(chan struct{})
	for worker := range 16 {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			for round := range 4 {
				pending, coverage, oldest := s.accountFleetFromReports(clients, agents, reports, 60, now)
				if len(pending) != 200 || coverage.Entries != 13000 || coverage.Subjects != 200 || coverage.EntriesStale != 0 || oldest != now.UnixMilli() {
					errors <- "parallel accounting narrowed the complete current fleet"
					return
				}
				for _, amount := range pending {
					if amount != 1300 {
						errors <- "parallel accounting changed a current epoch delta"
						return
					}
				}
				pending[1] = 999 // A response must not become another call's cache.
				id := int64(100000 + worker)
				client := domain.PSPClientAccounting{ID: id, LastCounterEpoch: 1}
				counter := protocol.ClientCounters{Key: protocol.NewClientKey(id), UpBytes: 11, DownBytes: 13, CounterEpoch: uint64(3 + round)}
				if s.pendingCounterDelta(client, counter) != 0 {
					errors <- "parallel reset reused a different client's anchor"
					return
				}
				counter.UpBytes += 5
				if s.pendingCounterDelta(client, counter) != 5 {
					errors <- "parallel reset lost its own current epoch delta"
					return
				}
			}
		}(worker)
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for generation := range 64 {
			s.mu.Lock()
			s.reports["current"] = receivedFullReport{report: protocol.NodeReport{AgentID: "current", Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), UpBytes: int64(generation)}}}, receivedAtMS: now.UnixMilli()}
			s.mu.Unlock()
			report, at, found := s.fullReport("current")
			if !found || at != now.UnixMilli() || len(report.Clients) != 1 || report.Clients[0].UpBytes != int64(generation) {
				errors <- "pending accounting changed an independent current report"
				return
			}
		}
	}()
	close(start)
	workers.Wait()
	close(errors)
	for message := range errors {
		t.Error(message)
	}
}
