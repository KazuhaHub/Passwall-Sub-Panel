package transit

import (
	"context"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type fakeNodes []*domain.Node

func (f fakeNodes) List(context.Context) ([]*domain.Node, error) { return f, nil }

type fakeRepo struct {
	ids      map[string]int64
	applied  map[int64]domain.ManagedForward
	counters map[int64][3]int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{ids: map[string]int64{}, applied: map[int64]domain.ManagedForward{}, counters: map[int64][3]int64{}}
}

func (f *fakeRepo) Ensure(_ context.Context, nodeID int64, relayID string) (domain.TransitForward, error) {
	key := relayID
	if _, ok := f.ids[key]; !ok {
		f.ids[key] = int64(len(f.ids) + 1)
	}
	return domain.TransitForward{ID: f.ids[key], NodeID: nodeID, RelayID: relayID}, nil
}

func (f *fakeRepo) SetApplied(_ context.Context, id int64, applied domain.ManagedForward, _ time.Time) (bool, error) {
	previous, had := f.applied[id]
	f.applied[id] = applied
	return !had || previous != applied, nil
}

func (f *fakeRepo) ClearApplied(_ context.Context, id int64) (bool, error) {
	_, had := f.applied[id]
	delete(f.applied, id)
	return had, nil
}

func (f *fakeRepo) RecordCounters(_ context.Context, id int64, up, down int64, epoch uint64) error {
	f.counters[id] = [3]int64{up, down, int64(epoch)}
	return nil
}

func landing(id, panel int64, address string, port int, relays ...domain.RelayLine) *domain.Node {
	return &domain.Node{ID: id, PanelID: panel, ServerAddress: address, DesiredPort: port, Enabled: true, Kind: domain.NodeKindReal, Relays: relays}
}

func TestForwardsForPublishesOnlyThisEntrysReachableLines(t *testing.T) {
	nodes := fakeNodes{
		landing(1, 1, "203.0.113.9", 443,
			domain.RelayLine{ID: "a", Port: 20001, Enabled: true, EntryPanelID: 9},
			domain.RelayLine{ID: "other-entry", Port: 20002, Enabled: true, EntryPanelID: 8},
			domain.RelayLine{ID: "disabled", Port: 20003, Enabled: false, EntryPanelID: 9},
			domain.RelayLine{ID: "external", Address: "x", Port: 20004, Enabled: true},
		),
		landing(2, 1, "", 443, domain.RelayLine{ID: "no-address", Port: 20005, Enabled: true, EntryPanelID: 9}),
		func() *domain.Node {
			n := landing(3, 1, "Land.Example.", 8443, domain.RelayLine{ID: "b", Port: 20006, Enabled: true, EntryPanelID: 9})
			return n
		}(),
		func() *domain.Node {
			n := landing(4, 1, "203.0.113.10", 443, domain.RelayLine{ID: "landing-off", Port: 20007, Enabled: true, EntryPanelID: 9})
			n.Enabled = false
			return n
		}(),
	}
	service := New(nodes, newFakeRepo())
	forwards, err := service.ForwardsFor(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(forwards) != 2 {
		t.Fatalf("forwards = %+v", forwards)
	}
	if forwards[0].Port != 20001 || forwards[0].Target != "203.0.113.9" || forwards[0].TargetPort != 443 || forwards[0].Network != nodeprotocol.ForwardNetworkTCPUDP {
		t.Fatalf("forward a = %+v", forwards[0])
	}
	// The landing's address is normalised to what the wire accepts.
	if forwards[1].Target != "land.example" || forwards[1].TargetPort != 8443 {
		t.Fatalf("forward b = %+v", forwards[1])
	}
}

func TestConfirmAndCountersStayWithinWhatWasSent(t *testing.T) {
	repo := newFakeRepo()
	service := New(fakeNodes{}, repo)
	sent := &nodeprotocol.Transit{Forwards: []nodeprotocol.Forward{
		{Key: nodeprotocol.NewForwardKey(1), Port: 20001, Target: "203.0.113.9", TargetPort: 443, Network: nodeprotocol.ForwardNetworkTCPUDP},
		{Key: nodeprotocol.NewForwardKey(2), Port: 20002, Target: "203.0.113.9", TargetPort: 443, Network: nodeprotocol.ForwardNetworkTCPUDP},
	}}
	repo.applied[2] = domain.ManagedForward{EntryPanelID: 9}
	statuses := map[string]nodeprotocol.ObjectStatus{
		"fwd_1": {State: nodeprotocol.ObjectApplied},
		"fwd_2": {State: nodeprotocol.ObjectRejected},
	}
	changed, err := service.Confirm(context.Background(), 9, sent, statuses, time.Now())
	if err != nil || !changed {
		t.Fatalf("Confirm = %v, %v", changed, err)
	}
	if got := repo.applied[1]; got != (domain.ManagedForward{EntryPanelID: 9, Port: 20001, Target: "203.0.113.9", TargetPort: 443}) {
		t.Fatalf("applied fwd_1 = %+v", got)
	}
	if _, still := repo.applied[2]; still {
		t.Fatal("a rejected forward kept its confirmation")
	}

	err = service.RecordCounters(context.Background(), sent, []nodeprotocol.ForwardCounters{
		{Key: nodeprotocol.NewForwardKey(1), Present: true, UpBytes: 5, DownBytes: 6, CounterEpoch: 3},
		{Key: nodeprotocol.NewForwardKey(77), Present: true, UpBytes: 999}, // not sent to this entry
		{Key: nodeprotocol.NewForwardKey(2), Present: false, UpBytes: 999}, // not running
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.counters) != 1 || repo.counters[1] != [3]int64{5, 6, 3} {
		t.Fatalf("counters = %v", repo.counters)
	}
}
