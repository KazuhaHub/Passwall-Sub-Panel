package nodesync

import (
	"context"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type changingFleetUsers struct {
	ports.UserRepo
	user   *domain.User
	onRead func()
}

func (r changingFleetUsers) ListByIDs(context.Context, []int64) ([]*domain.User, error) {
	r.onRead()
	return []*domain.User{r.user}, nil
}

func TestBuildDirectivesUsesOneCurrentFleetObservationForQuotaAndCoverage(t *testing.T) {
	now := time.UnixMilli(100000)
	user := &domain.User{ID: 7, TrafficLimitBytes: 100}
	client := &domain.PSPClient{ID: 11, UserID: user.ID, PanelID: 9, LastCounterEpoch: 1}
	agent := &domain.NodeAgent{AgentID: "owned", PanelID: client.PanelID}
	counter := protocol.ClientCounters{Key: protocol.NewClientKey(client.ID), Present: true, CounterEpoch: 1, UpBytes: 10}
	service := &Service{
		clients:  emergencyDirectiveClients{client: client},
		agents:   emergencyDirectiveAgents{agent: agent},
		settings: emergencyDirectiveSettings{},
		reports: map[string]receivedFullReport{
			agent.AgentID: {report: protocol.NodeReport{Clients: []protocol.ClientCounters{counter}}, receivedAtMS: 95000},
		},
		anchors: make(map[int64]protocol.ClientCounters),
		grants:  make(map[string]map[protocol.ClientKey]int64),
	}
	service.users = changingFleetUsers{user: user, onRead: func() {
		// A concurrent authenticated observation can arrive after accounting
		// but before directives finish loading their current account closure.
		service.mu.Lock()
		changed := service.reports[agent.AgentID]
		changed.report.Clients[0].UpBytes = 80
		changed.report.Clients[0].Present = false
		changed.receivedAtMS = 101000
		service.reports[agent.AgentID] = changed
		service.mu.Unlock()
	}}
	snapshot := &ports.NativeDesiredSnapshot{Clients: []ports.NativeDesiredClient{{Client: client}}}
	current := protocol.NodeReport{Clients: []protocol.ClientCounters{counter}}
	version := protocol.Version{Epoch: 1, Version: 1}
	for _, want := range []struct {
		headroom, oldest int64
		stale            int
	}{{90, 95000, 0}, {20, 0, 1}} {
		body, envelope, err := service.buildDirectives(t.Context(), agent, snapshot, current, version, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(body.Quota) != 1 || body.Quota[0].HeadroomBytes == nil || *body.Quota[0].HeadroomBytes != want.headroom {
			t.Fatal("quota lost the current owned pending counters")
		}
		if body.Coverage.Entries != 1 || body.Coverage.Subjects != 1 || body.Coverage.EntriesStale != want.stale || envelope.NumeratorAsOfMS != want.oldest {
			t.Fatal("quota and coverage used different observations or reused a previous response snapshot")
		}
	}
}
