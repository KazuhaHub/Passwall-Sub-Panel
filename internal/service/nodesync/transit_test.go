package nodesync

import (
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

// managedLanding adds a landing node on another server whose relay lines
// enter through the fixture's agent (panel 9).
func (f *configAppliedFixture) managedLanding(t *testing.T, ports ...int) *domain.Node {
	t.Helper()
	landing := &domain.Node{
		PanelID: 3, InboundID: 1, DisplayName: "landing", ServerAddress: "203.0.113.9",
		DesiredProtocol: "vless", DesiredPort: 443, Enabled: true, Region: "JP",
	}
	for i, port := range ports {
		landing.Relays = append(landing.Relays, domain.RelayLine{
			ID: string(rune('a' + i)), Name: "via entry", Address: "entry.example", Port: port,
			Enabled: true, EntryPanelID: f.agent.PanelID, Tags: []string{},
		})
	}
	if err := f.repos.Node.Create(t.Context(), landing); err != nil {
		t.Fatal(err)
	}
	return landing
}

func (f *configAppliedFixture) syncReport(t *testing.T, report nodeprotocol.NodeReport) nodeprotocol.SyncResponse {
	t.Helper()
	response, err := f.service.Sync(t.Context(), report)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestManagedRelayIsPublishedOnlyToACapableEntryAndConfirmedByIt(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.SetTransit(transit.New(f.repos.Node, f.repos.Transit))
	// 444 is the entry's own listener port: that line must be withheld.
	landing := f.managedLanding(t, 20443, 444)

	// An agent that did not declare the capability gets no Transit.
	plain := f.mint(t)
	if plain.Config.Body.Transit != nil {
		t.Fatalf("transit sent without the capability: %+v", plain.Config.Body.Transit)
	}

	capable := nodeprotocol.NodeReport{
		AgentID: f.agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(),
		Capabilities: []string{nodeprotocol.CapabilityTransitForward},
	}
	response := f.syncReport(t, capable)
	if response.Config.Body == nil || response.Config.Body.Transit == nil {
		t.Fatal("a capable entry got no transit")
	}
	forwards := response.Config.Body.Transit.Forwards
	if len(forwards) != 1 || forwards[0].Port != 20443 || forwards[0].Target != "203.0.113.9" || forwards[0].TargetPort != 443 {
		t.Fatalf("forwards = %+v", forwards)
	}
	key := forwards[0].Key

	// Not yet confirmed: the line is withheld.
	stored, err := f.repos.Node.GetByID(t.Context(), landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Relays[0].Publishable() {
		t.Fatal("an unconfirmed managed line is publishable")
	}

	// The entry holds this config and reports the forward applied.
	receipt := f.receipt(response)
	receipt.Capabilities = capable.Capabilities
	receipt.Objects = append(receipt.Objects, nodeprotocol.ObjectStatus{
		Stream: nodeprotocol.StreamConfig, Key: string(key), State: nodeprotocol.ObjectApplied, SinceVersion: response.Config.Version,
	})
	receipt.ForwardCounters = []nodeprotocol.ForwardCounters{
		{Key: key, Present: true, UpBytes: 10, DownBytes: 20, CounterEpoch: 1},
		{Key: nodeprotocol.NewForwardKey(999), Present: true, UpBytes: 1 << 40, CounterEpoch: 1},
	}
	before := f.invalidations
	f.syncReport(t, receipt)
	if f.invalidations == before {
		t.Fatal("confirming a managed line did not refresh subscriptions")
	}
	stored, err = f.repos.Node.GetByID(t.Context(), landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Relays[0].Publishable() || stored.Relays[1].Publishable() {
		t.Fatalf("publishable = %v / %v", stored.Relays[0].Publishable(), stored.Relays[1].Publishable())
	}
	rows, err := f.repos.Transit.ListByNodes(t.Context(), []int64{landing.ID})
	if err != nil {
		t.Fatal(err)
	}
	var booked int64
	for _, row := range rows {
		booked += row.UpBytes + row.DownBytes
	}
	if booked != 30 {
		t.Fatalf("booked %d bytes, want 30 (a key not sent to this entry must be ignored)", booked)
	}

	// The entry now rejects it: the confirmation is withdrawn.
	rejected := receipt
	rejected.ForwardCounters = nil
	rejected.Objects = []nodeprotocol.ObjectStatus{receipt.Objects[0], {
		Stream: nodeprotocol.StreamConfig, Key: string(key), State: nodeprotocol.ObjectRejected,
		SinceVersion: response.Config.Version, FirstFailedAtMS: f.now.UnixMilli(), IssueCode: "core_config_rejected",
	}}
	f.syncReport(t, rejected)
	stored, err = f.repos.Node.GetByID(t.Context(), landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Relays[0].Publishable() {
		t.Fatal("a rejected forward kept its line published")
	}
}
