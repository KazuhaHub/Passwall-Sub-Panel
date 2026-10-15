package nodesync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func clientIdentityObservationFixture(size int) (protocol.NodeReport, *ports.NativeDesiredSnapshot) {
	report := protocol.NodeReport{
		AgentID: "owned", AgentVersion: "v1", CoreEngine: "xray", CoreVersion: "26.6.27", CoreState: "running",
		Have:             emptyProtocolHave(),
		ListenerCounters: []protocol.ListenerCounters{{Key: protocol.NewListenerKey(9), Present: true, UpBytes: 19}},
		Objects:          []protocol.ObjectStatus{{Stream: protocol.StreamConfig, Key: string(protocol.NewListenerKey(9)), State: protocol.ObjectApplied}},
	}
	for id := int64(1); id <= int64(size); id++ {
		report.Clients = append(report.Clients, protocol.ClientCounters{Key: protocol.NewClientKey(id), UpBytes: id, CounterEpoch: 1, LiveIPs: []string{"203.0.113.2", "203.0.113.1"}})
		report.Objects = append(report.Objects, protocol.ObjectStatus{Stream: protocol.StreamRoster, Key: string(protocol.NewClientKey(id)), State: protocol.ObjectApplied})
	}
	return report, &ports.NativeDesiredSnapshot{Clients: []ports.NativeDesiredClient{{Client: &domain.PSPClient{ID: 1}}, {Client: nil}}}
}

func TestClientIdentityReportCopiesOnlyRequestedIdentityAndOwnsItsCollections(t *testing.T) {
	in, desired := clientIdentityObservationFixture(200)
	before := cloneReport(in)
	out := cloneClientIdentityReport(in, desired)
	if out.AgentID != in.AgentID || out.CoreState != in.CoreState || !reflect.DeepEqual(out.Have, in.Have) || len(out.Clients) != 1 || out.Clients[0].Key != protocol.NewClientKey(1) || out.Clients[0].UpBytes != 1 || out.Clients[0].Present || len(out.Clients[0].LiveIPs) != 0 || len(out.Objects) != 1 || out.Objects[0].Stream != protocol.StreamRoster || len(out.ListenerCounters) != 0 {
		t.Fatal("scoped observation changed the closed-client identity or copied unrelated traffic")
	}
	out.Clients[0].UpBytes = 999
	out.Objects[0].State = protocol.ObjectRejected
	out.Have[protocol.StreamRoster] = protocol.StreamState{Applied: protocol.Version{Epoch: 999}}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("scoped identity result mutated its full source")
	}
	empty := cloneClientIdentityReport(in, &ports.NativeDesiredSnapshot{})
	if len(empty.Clients) != 0 || len(empty.Objects) != 0 {
		t.Fatal("empty desired identity imported unrelated observations")
	}
}

func TestClientIdentityReportAllocationsDoNotGrowWithUnrelatedClients(t *testing.T) {
	measure := func(size int) float64 {
		in, desired := clientIdentityObservationFixture(size)
		return testing.AllocsPerRun(5, func() {
			out := cloneClientIdentityReport(in, desired)
			if len(out.Clients) != 1 || len(out.Objects) != 1 {
				t.Fatal("identity projection lost its selected observation")
			}
		})
	}
	small, large := measure(1), measure(2000)
	if large > small+2 {
		t.Fatalf("unrelated clients enlarged scoped allocation count: small=%.0f large=%.0f", small, large)
	}
}

type interleavedDesiredClient struct {
	ports.NativeDesiredSnapshotRepo
	scoped ports.NativeDesiredClientSnapshotRepo
	after  func()
	err    error
}

func (r *interleavedDesiredClient) LoadClient(ctx context.Context, panelID int64, email string) (*ports.NativeDesiredSnapshot, error) {
	if r.err != nil {
		return nil, r.err
	}
	snapshot, err := r.scoped.LoadClient(ctx, panelID, email)
	if r.after != nil {
		r.after()
	}
	return snapshot, err
}

type fullDesiredOnly struct {
	ports.NativeDesiredSnapshotRepo
}

func TestScopedClientObservationMatchesFullFacadeAndRetainsItsOwnReport(t *testing.T) {
	f := newConfigAppliedFixture(t)
	var clients []*domain.PSPClient
	for n := range 2 {
		user := &domain.User{UPN: fmt.Sprintf("identity-%d@example.test", n), Role: domain.RoleUser, SubToken: fmt.Sprintf("identity-token-%d", n), UUID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n+1), Enabled: true}
		if err := f.repos.User.Create(t.Context(), user); err != nil {
			t.Fatal(err)
		}
		client := &domain.PSPClient{UserID: user.ID, PanelID: f.agent.PanelID, Email: fmt.Sprintf("identity-%d@psp.local", n), UUID: user.UUID, DesiredEnable: true, DesiredMinted: true}
		id, err := f.repos.PSPClient.Create(t.Context(), client)
		if err != nil {
			t.Fatal(err)
		}
		client.ID = id
		if err := f.repos.PSPClient.SetInbounds(t.Context(), id, []domain.PSPClientInbound{{ClientID: id, NodeID: f.node.ID, State: domain.ClientApplyPending}}); err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
	}
	minted := f.mint(t)
	report := f.receipt(minted)
	report.Have[protocol.StreamRoster] = protocol.StreamState{Applied: minted.Roster.Version, ETag: minted.Roster.ETag}
	report.ListenerCounters = []protocol.ListenerCounters{{Key: protocol.NewListenerKey(f.node.ID), Present: true, CounterEpoch: 1}}
	for _, client := range clients {
		report.Clients = append(report.Clients, protocol.ClientCounters{Key: protocol.NewClientKey(client.ID), Present: false, CounterEpoch: 1, Gate: protocol.GateClosed, LiveIPs: []string{"203.0.113.2", "203.0.113.1"}})
		report.Objects = append(report.Objects, protocol.ObjectStatus{Stream: protocol.StreamRoster, Key: string(protocol.NewClientKey(client.ID)), State: protocol.ObjectApplied, SinceVersion: minted.Roster.Version})
	}
	if _, err := f.service.Sync(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	full, err := f.service.NativePanelSnapshot(t.Context(), f.agent.PanelID)
	if err != nil || len(full.Clients) != 2 || len(full.LiveClientIPs) != 2 || len(full.Inbounds) != 1 || len(full.Inbounds[0].ClientStats) != 2 {
		t.Fatalf("full facade omitted unrelated client traffic: %v", err)
	}
	for _, client := range clients {
		scoped, err := f.service.NativePanelClientSnapshot(t.Context(), f.agent.PanelID, client.Email)
		want := full.Clients[client.Email]
		if err != nil || scoped == nil || !reflect.DeepEqual(*scoped, want) {
			t.Fatalf("scoped closed-client identity differs from full facade: %v", err)
		}
	}
	if absent, err := f.service.NativePanelClientSnapshot(t.Context(), f.agent.PanelID, "absent@psp.local"); err != nil || absent != nil {
		t.Fatalf("missing desired email imported another identity: %v", err)
	}
	changed := *clients[0]
	changed.UUID = "00000000-0000-4000-8000-000000000099"
	if err := f.repos.PSPClient.UpdateDefinition(t.Context(), &changed); err != nil {
		t.Fatal(err)
	}
	if pending, err := f.service.NativePanelClientSnapshot(t.Context(), f.agent.PanelID, changed.Email); err != nil || pending != nil {
		t.Fatalf("current unacknowledged credential became an observed identity: %v", err)
	}
	if err := f.repos.PSPClient.UpdateDefinition(t.Context(), clients[0]); err != nil {
		t.Fatal(err)
	}
	f.service.mu.RLock()
	saved := f.service.reports[f.agent.AgentID]
	f.service.mu.RUnlock()
	for _, change := range []struct {
		name  string
		apply func(*protocol.NodeReport)
	}{
		{"rejected", func(r *protocol.NodeReport) {
			for i := range r.Objects {
				if r.Objects[i].Stream == protocol.StreamRoster && r.Objects[i].Key == string(protocol.NewClientKey(clients[0].ID)) {
					r.Objects[i].State = protocol.ObjectRejected
				}
			}
		}},
		{"wrong epoch", func(r *protocol.NodeReport) {
			state := r.Have[protocol.StreamRoster]
			state.Applied.Epoch++
			r.Have[protocol.StreamRoster] = state
		}},
	} {
		replacement := saved
		replacement.report = cloneObservationReport(saved.report)
		change.apply(&replacement.report)
		f.service.mu.Lock()
		f.service.reports[f.agent.AgentID] = replacement
		f.service.mu.Unlock()
		if rejected, err := f.service.NativePanelClientSnapshot(t.Context(), f.agent.PanelID, clients[0].Email); err != nil || rejected != nil {
			t.Fatalf("scoped lookup bypassed %s: %v", change.name, err)
		}
	}
	f.service.mu.Lock()
	f.service.reports[f.agent.AgentID] = saved
	f.service.mu.Unlock()
	original := f.service.desired
	f.service.desired = fullDesiredOnly{original}
	legacy, err := f.service.NativePanelClientSnapshot(t.Context(), f.agent.PanelID, clients[0].Email)
	if err != nil || legacy == nil || !reflect.DeepEqual(*legacy, full.Clients[clients[0].Email]) {
		t.Fatalf("legacy desired reader changed scoped identity: %v", err)
	}
	reader := &interleavedDesiredClient{NativeDesiredSnapshotRepo: original, scoped: original.(ports.NativeDesiredClientSnapshotRepo)}
	f.service.desired = reader
	reader.after = func() {
		// Ingestion replaces an owned deep copy; existing readers keep the
		// exact earlier immutable observation and its original receipt time.
		f.service.mu.Lock()
		old := f.service.reports[f.agent.AgentID]
		updated := old
		updated.report = cloneObservationReport(old.report)
		updated.report.Clients = nil
		f.service.reports[f.agent.AgentID] = updated
		f.service.mu.Unlock()
		reader.after = nil
	}
	first, err := f.service.NativePanelClientSnapshot(t.Context(), f.agent.PanelID, clients[0].Email)
	if err != nil || first == nil || !reflect.DeepEqual(*first, full.Clients[clients[0].Email]) {
		t.Fatalf("interleaved replacement changed an in-progress observation: %v", err)
	}
	if next, err := f.service.NativePanelClientSnapshot(t.Context(), f.agent.PanelID, clients[0].Email); err != nil || next != nil {
		t.Fatalf("next scoped lookup reused the earlier counter presence: %v", err)
	}
	reader.err = domain.ErrUnavailable
	if _, err := f.service.NativePanelClientSnapshot(t.Context(), f.agent.PanelID, clients[0].Email); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("scoped desired read failure was hidden: %v", err)
	}
}
