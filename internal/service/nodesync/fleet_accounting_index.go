package nodesync

import (
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type fleetAccountingOwner struct {
	panelID int64
	atMS    int64
	stale   bool
	used    bool
}

// Reuse one numeric client index for pending usage and coverage. RowID rejects
// noncanonical spellings, so a counter that resolves to an ID is exactly the
// key NewClientKey would mint. Presence remains last-counter-wins; pending
// deltas still visit every counter in report order, including duplicates.
func (s *Service) accountFleetFromReports(clients []domain.PSPClientAccounting, agents []*domain.NodeAgent, reports map[string]fleetCounterReport, freshness int, now time.Time) (map[int64]int64, protocol.SegmentCounts, int64) {
	index := make(map[int64]int, len(clients))
	for i := range clients {
		if _, duplicate := index[clients[i].ID]; duplicate || clients[i].ID <= 0 {
			return s.accountFleetSeparateIndexes(clients, agents, reports, freshness, now)
		}
		index[clients[i].ID] = i
	}
	byPanel := make(map[int64]string, len(agents))
	owners := make(map[string]*fleetAccountingOwner, len(agents))
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		// Production ownership is one-to-one. Keep the previous independent
		// last-row-wins maps for unusual adapters instead of reinterpreting
		// duplicate panel/agent rows as a different ownership relation.
		_, repeatedPanel := byPanel[agent.PanelID]
		_, repeatedAgent := owners[agent.AgentID]
		if repeatedPanel || repeatedAgent {
			return s.accountFleetSeparateIndexes(clients, agents, reports, freshness, now)
		}
		received, found := reports[agent.AgentID]
		owner := &fleetAccountingOwner{panelID: agent.PanelID, atMS: received.receivedAtMS,
			stale: !found || fullReportStale(received.receivedAtMS, now, freshness)}
		if owner.atMS > now.UnixMilli() {
			owner.atMS = 0
		}
		owners[agent.AgentID], byPanel[agent.PanelID] = owner, agent.AgentID
	}
	present := make([]bool, len(clients))
	pending := make(map[int64]int64)
	for agentID, report := range reports {
		owner := owners[agentID]
		if owner == nil {
			continue
		}
		for _, counter := range report.clients {
			id, err := counter.key.RowID()
			if err != nil {
				continue
			}
			i, found := index[id]
			if !found || clients[i].PanelID != owner.panelID {
				continue
			}
			present[i] = counter.present
			client := clients[i]
			pending[client.UserID] = saturatingAdd(pending[client.UserID], s.pendingCounterDelta(client, protocol.ClientCounters{
				Key: counter.key, Present: counter.present, UpBytes: counter.upBytes,
				DownBytes: counter.downBytes, CounterEpoch: counter.counterEpoch,
			}))
		}
	}
	coverage := protocol.SegmentCounts{}
	subjects := make(map[int64]struct{})
	for i, client := range clients {
		agentID, native := byPanel[client.PanelID]
		if !native {
			continue
		}
		owner := owners[agentID]
		owner.used = true
		coverage.Entries++
		subjects[client.UserID] = struct{}{}
		if owner.stale || !present[i] {
			coverage.EntriesStale++
		}
	}
	coverage.Subjects = len(subjects)
	var oldest int64
	for _, owner := range owners {
		if !owner.used {
			continue
		}
		if owner.atMS <= 0 {
			return pending, coverage, 0
		}
		if oldest == 0 || owner.atMS < oldest {
			oldest = owner.atMS
		}
	}
	return pending, coverage, oldest
}

func (s *Service) accountFleetSeparateIndexes(clients []domain.PSPClientAccounting, agents []*domain.NodeAgent, reports map[string]fleetCounterReport, freshness int, now time.Time) (map[int64]int64, protocol.SegmentCounts, int64) {
	clientByID := make(map[int64]*domain.PSPClientAccounting, len(clients))
	for i := range clients {
		clientByID[clients[i].ID] = &clients[i]
	}
	pending := s.pendingUsageFromReports(clientByID, agents, reports)
	coverage, oldest := aggregateCoverageFromReports(clients, agents, reports, freshness, now)
	return pending, coverage, oldest
}
