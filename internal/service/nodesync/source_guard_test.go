package nodesync

import (
	"encoding/json"
	"strings"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/sourceaddr"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/sourceguard"
)

const (
	entryAddr  = "93.184.216.34"
	entryAddr2 = "93.184.216.35"
)

// guardFixture is the fixture's landing (panel 9) with an external line that
// names addresses and a managed forward entered through a second server.
func guardFixture(t *testing.T) (*configAppliedFixture, *domain.NodeAgent) {
	t.Helper()
	f := newConfigAppliedFixture(t)
	entry := &domain.NodeAgent{
		AgentID: "agt_entry", PanelID: 5, Epoch: 1, CredentialSHA256: strings.Repeat("b", 64),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27",
	}
	if err := f.repos.NodeAgent.Create(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	record(t, f, entry.AgentID, entryAddr)
	f.service.SetSourceGuard(sourceguard.NewPlanner(f.repos.NodeAgent, nil))
	node := f.storedNode(t)
	node.Relays = []domain.RelayLine{
		{ID: "ext", Name: "ext", Address: "ext.example", Port: 443, Enabled: true, SourceCIDRs: []string{"192.0.2.0/24"}},
		{ID: "fwd", Name: "fwd", Address: "entry.example", Port: 20443, Enabled: true, EntryPanelID: 5},
	}
	if err := f.repos.Node.UpdateMetadata(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	return f, entry
}

// record makes the panel see agentID's sync come from addr, and has it report
// its own interfaces (a full report from an agent that declares the capability).
func record(t *testing.T, f *configAppliedFixture, agentID, addr string, interfaces ...string) {
	t.Helper()
	f.syncAs(t, sourceaddr.With(t.Context(), addr), nodeprotocol.NodeReport{
		AgentID: agentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(),
		Capabilities:       []string{nodeprotocol.CapabilitySourceGuard},
		InterfaceAddresses: interfaces,
	})
}

func guardSync(t *testing.T, f *configAppliedFixture, capabilities ...string) nodeprotocol.SyncResponse {
	t.Helper()
	response, err := f.service.Sync(t.Context(), nodeprotocol.NodeReport{
		AgentID: f.agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(), Capabilities: capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestSourceSetsAreSentOnlyToAnAgentThatCanGuard(t *testing.T) {
	f, _ := guardFixture(t)
	plain := guardSync(t, f)
	if plain.Config.Body.Transit != nil && len(plain.Config.Body.Transit.SourceSets) > 0 {
		t.Fatalf("source sets sent without the capability: %+v", plain.Config.Body.Transit)
	}
	capable := guardSync(t, f, nodeprotocol.CapabilitySourceGuard)
	transit := capable.Config.Body.Transit
	if transit == nil || len(transit.SourceSets) != 2 {
		t.Fatalf("transit = %+v", transit)
	}
	byKey := map[nodeprotocol.SourceSetKey][]string{}
	for _, set := range transit.SourceSets {
		byKey[set.Key] = set.CIDRs
	}
	node := f.storedNode(t)
	if got := byKey[sourceguard.Key(node.ID, "ext")]; len(got) != 1 || got[0] != "192.0.2.0/24" {
		t.Fatalf("ext set = %v", got)
	}
	if got := byKey[sourceguard.Key(node.ID, "fwd")]; len(got) != 1 || got[0] != entryAddr+"/32" {
		t.Fatalf("fwd set = %v", got)
	}
	if err := nodeprotocol.ValidateSyncResponse(capable); err != nil {
		t.Fatal(err)
	}
}

func TestSourceSetChangeBumpsTheConfigAndNothingChangesWhenUnguarded(t *testing.T) {
	f, entry := guardFixture(t)
	first := guardSync(t, f, nodeprotocol.CapabilitySourceGuard)
	again := guardSync(t, f, nodeprotocol.CapabilitySourceGuard)
	if first.Config.Version != again.Config.Version || first.Config.ETag != again.Config.ETag {
		t.Fatal("an idle sync moved the config")
	}
	// The entry's address changes: the landing's set changes with it.
	record(t, f, entry.AgentID, entryAddr2)
	moved := guardSync(t, f, nodeprotocol.CapabilitySourceGuard)
	if moved.Config.Version == first.Config.Version || moved.Config.ETag == first.Config.ETag {
		t.Fatal("a changed set did not bump the config")
	}
	// A landing that cannot guard keeps bytes it always had.
	unguarded := guardSync(t, f)
	body, _ := json.Marshal(unguarded.Config.Body)
	if strings.Contains(string(body), "source_sets") {
		t.Fatalf("unguarded config mentions source sets: %s", body)
	}
}

func TestRosterCarriesGuardsAndStaysIdenticalWithout(t *testing.T) {
	node := &domain.Node{ID: 5, PanelID: 9, Relays: []domain.RelayLine{
		{ID: "ext", Enabled: true, SourceCIDRs: []string{"192.0.2.0/24"}},
		{ID: "tun", Enabled: true, Mode: domain.RelayModeTunnel, EntryPanelID: 3},
	}}
	snapshot := &ports.NativeDesiredSnapshot{
		Nodes: []*domain.Node{node},
		Clients: []ports.NativeDesiredClient{
			{Client: &domain.PSPClient{ID: 1, UserID: 1, NodeID: 5, LineID: domain.DirectLineID, DesiredEnable: true},
				Inbounds: []domain.PSPClientInbound{{ClientID: 1, NodeID: 5}}},
			{Client: &domain.PSPClient{ID: 2, UserID: 1, NodeID: 5, LineID: "ext", DesiredEnable: true},
				Inbounds: []domain.PSPClientInbound{{ClientID: 2, NodeID: 5}}},
			{Client: &domain.PSPClient{ID: 3, UserID: 1, NodeID: 5, LineID: "tun", DesiredEnable: true},
				Inbounds: []domain.PSPClientInbound{{ClientID: 3, NodeID: 5}}},
		},
	}
	version := nodeprotocol.Version{Epoch: 1, Version: 1}
	plan := sourceguard.Compute(snapshot.Nodes, domain.SourceInputs{})
	roster := buildRoster(snapshot, version, plan)
	if g := roster.Clients[0].SourceGuard; g == nil || len(g.DenyFrom) != 1 || g.AllowFrom != "" {
		t.Fatalf("direct client guard = %+v", g)
	}
	if g := roster.Clients[1].SourceGuard; g == nil || g.AllowFrom != sourceguard.Key(5, "ext") {
		t.Fatalf("relay client guard = %+v", g)
	}
	if roster.Clients[2].SourceGuard != nil {
		t.Fatalf("tunnel client guard = %+v", roster.Clients[2].SourceGuard)
	}
	for _, client := range roster.Clients {
		if err := nodeprotocol.ValidateClientSourceGuard(client.SourceGuard); err != nil {
			t.Fatal(err)
		}
	}

	// With nothing guarded the roster is byte for byte the one without guards.
	node.Relays = nil
	empty := buildRoster(snapshot, version, sourceguard.Compute(snapshot.Nodes, domain.SourceInputs{}))
	without := buildRoster(snapshot, version, sourceguard.Plan{})
	a, _ := json.Marshal(empty)
	b, _ := json.Marshal(without)
	if string(a) != string(b) || strings.Contains(string(a), "source_guard") {
		t.Fatalf("roster changed without a guard:\n%s\n%s", a, b)
	}
}

// The entry's own interface addresses join its set, and only a full report that
// declares the capability sets them: a partial one says nothing.
func TestEntryInterfaceAddressesJoinTheForwardSet(t *testing.T) {
	f, entry := guardFixture(t)
	record(t, f, entry.AgentID, entryAddr, "93.184.216.40")
	setOf := func() []string {
		for _, set := range guardSync(t, f, nodeprotocol.CapabilitySourceGuard).Config.Body.Transit.SourceSets {
			if set.Key == sourceguard.Key(f.node.ID, "fwd") {
				return set.CIDRs
			}
		}
		t.Fatal("no fwd set")
		return nil
	}
	if got := setOf(); len(got) != 2 || got[0] != entryAddr+"/32" || got[1] != "93.184.216.40/32" {
		t.Fatalf("set = %v", got)
	}
	f.syncAs(t, sourceaddr.With(t.Context(), entryAddr), nodeprotocol.NodeReport{
		AgentID: entry.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1, ReportedAtMS: f.now.UnixMilli(),
		Have: emptyProtocolHave(), Partial: true, Capabilities: []string{nodeprotocol.CapabilitySourceGuard},
	})
	if got := setOf(); len(got) != 2 {
		t.Fatalf("a partial report changed the set: %v", got)
	}
	record(t, f, entry.AgentID, entryAddr)
	if got := setOf(); len(got) != 1 {
		t.Fatalf("an empty full list did not clear the interfaces: %v", got)
	}
}

func TestRefusedSourcesAreKeptFromFullReportsOfOwnLandingsOnly(t *testing.T) {
	f, _ := guardFixture(t)
	f.service.SetSourceGuardHits(f.repos.SourceGuard)
	node := f.storedNode(t)
	ours := sourceguard.Key(node.ID, "ext")
	hit := func(set nodeprotocol.SourceSetKey, listener int64, ip string) nodeprotocol.SourceGuardHit {
		return nodeprotocol.SourceGuardHit{Set: set, Listener: nodeprotocol.NewListenerKey(listener), SourceIP: ip, Count: 4, LastSeenMS: f.now.UnixMilli()}
	}
	report := nodeprotocol.NodeReport{
		AgentID: f.agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1, ReportedAtMS: f.now.UnixMilli(),
		Have: emptyProtocolHave(), Capabilities: []string{nodeprotocol.CapabilitySourceGuard},
		SourceGuardHits: []nodeprotocol.SourceGuardHit{
			hit(ours, node.ID, "203.0.113.50"),
			// A set of another server's landing: ignored.
			hit(sourceguard.Key(node.ID+100, "ext"), node.ID, "203.0.113.51"),
		},
	}
	f.syncAs(t, t.Context(), report)
	got, err := f.repos.SourceGuard.ListHits(t.Context(), []int64{f.agent.PanelID})
	if err != nil || len(got) != 1 || got[0].IP != "203.0.113.50" || got[0].Count != 4 || got[0].Set != string(ours) {
		t.Fatalf("hits = %+v err=%v", got, err)
	}
	// Without the capability the hits are not read.
	report.Capabilities = nil
	report.SourceGuardHits = []nodeprotocol.SourceGuardHit{hit(ours, node.ID, "203.0.113.52")}
	f.syncAs(t, t.Context(), report)
	if got, _ = f.repos.SourceGuard.ListHits(t.Context(), []int64{f.agent.PanelID}); len(got) != 1 {
		t.Fatalf("hits from an agent without the capability were kept: %+v", got)
	}
}

func TestWithheldRelayLineIsReportedAsAnIssue(t *testing.T) {
	f, _ := guardFixture(t)
	node := f.storedNode(t)
	two := 2000
	node.Relays = []domain.RelayLine{{ID: "dear", Name: "dear", Address: "x.example", Port: 443, Enabled: true, TrafficFactorMilli: &two}}
	if err := f.repos.Node.UpdateMetadata(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	// The landing's server cannot guard: the line is withheld and an issue says so.
	guardSync(t, f)
	issues, _, err := f.repos.NodeAgentIssue.List(t.Context(), ports.NodeAgentIssueFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var found *domain.NodeAgentIssue
	for _, issue := range issues {
		if issue.Code == IssueSourceGuardWithheld {
			found = issue
		}
	}
	if found == nil || !strings.Contains(found.Detail, `"dear"`) || found.Key != string(nodeprotocol.NewListenerKey(node.ID)) {
		t.Fatalf("issues = %+v", issues)
	}
	if got := f.storedNode(t).Relays[0]; got.Publishable() || got.SourceWithheld != domain.SourceWithheldFactor {
		t.Fatalf("line = %+v", got)
	}
}
