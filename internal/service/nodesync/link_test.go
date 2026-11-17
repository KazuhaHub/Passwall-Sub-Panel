package nodesync

import (
	"context"
	"strings"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/sourceaddr"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

var linkCapabilities = []string{nodeprotocol.CapabilityTransitForward, nodeprotocol.CapabilityTransitLink}

// exitServer adds a second Passwall Node server (panel 3) with one listener
// and its agent.
func (f *configAppliedFixture) exitServer(t *testing.T) *domain.NodeAgent {
	t.Helper()
	listener := &domain.Node{
		PanelID: 3, InboundID: 1, DisplayName: "exit", ServerAddress: "exit.example.test",
		DesiredProtocol: "vless", DesiredPort: 443, InboundListen: "0.0.0.0", Enabled: true, Region: "JP",
		InboundSettings: `{"decryption":"none"}`, StreamSettings: `{"network":"tcp"}`,
	}
	if err := f.repos.Node.Create(t.Context(), listener); err != nil {
		t.Fatal(err)
	}
	agent := &domain.NodeAgent{
		AgentID: "agt_exit", PanelID: 3, Epoch: 2, CredentialSHA256: strings.Repeat("b", 64),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27",
	}
	if err := f.repos.NodeAgent.Create(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	return agent
}

func (f *configAppliedFixture) syncAs(t *testing.T, ctx context.Context, report nodeprotocol.NodeReport) nodeprotocol.SyncResponse {
	t.Helper()
	response, err := f.service.Sync(ctx, report)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func linkReceipt(agent *domain.NodeAgent, at int64, response nodeprotocol.SyncResponse, key nodeprotocol.LinkKey, state nodeprotocol.ObjectState) nodeprotocol.NodeReport {
	have := emptyProtocolHave()
	have[nodeprotocol.StreamConfig] = nodeprotocol.StreamState{Applied: response.Config.Version, ETag: response.Config.ETag}
	status := nodeprotocol.ObjectStatus{Stream: nodeprotocol.StreamConfig, Key: string(key), State: state, SinceVersion: response.Config.Version}
	if state == nodeprotocol.ObjectRejected {
		status.FirstFailedAtMS, status.IssueCode = at, "core_config_rejected"
	}
	return nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1, ReportedAtMS: at,
		Have: have, Capabilities: linkCapabilities, Objects: []nodeprotocol.ObjectStatus{status},
	}
}

func TestTunnelIsPublishedToBothSidesAndGatesTheBoundNode(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.SetTransit(transit.New(f.repos.Node, f.repos.Transit).WithLinks(f.repos.TransitLinks, f.repos.NodeAgent))
	exit := f.exitServer(t)
	if err := f.db.Table("nodes").Where("id = ?", f.node.ID).Update("egress_panel_id", exit.PanelID).Error; err != nil {
		t.Fatal(err)
	}
	ready := func() bool {
		t.Helper()
		node, err := f.repos.Node.GetByID(t.Context(), f.node.ID)
		if err != nil {
			t.Fatal(err)
		}
		return node.EgressPublishable()
	}
	report := func(agent *domain.NodeAgent, caps []string) nodeprotocol.NodeReport {
		return nodeprotocol.NodeReport{AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
			ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(), Capabilities: caps}
	}

	// The exit syncs first, from the address the entry will dial. It holds the
	// incoming half, on a port none of its listeners uses.
	fromExit := sourceaddr.With(t.Context(), "198.51.100.7")
	exitResponse := f.syncAs(t, fromExit, report(exit, linkCapabilities))
	exitTransit := exitResponse.Config.Body.Transit
	if exitTransit == nil || len(exitTransit.LinksIn) != 1 || len(exitTransit.LinksOut) != 0 || len(exitTransit.Egress) != 0 {
		t.Fatalf("exit transit = %+v", exitTransit)
	}
	in := exitTransit.LinksIn[0]
	if in.Protocol != nodeprotocol.LinkProtocolSS2022 || in.Port == 443 || in.Port < 20000 {
		t.Fatalf("incoming link = %+v", in)
	}

	// An entry without the capability gets no tunnel, and its node stays
	// withheld rather than leaving from the entry.
	if plain := f.syncAs(t, t.Context(), report(f.agent, []string{nodeprotocol.CapabilityTransitForward})); plain.Config.Body.Transit != nil {
		t.Fatalf("tunnel sent without the capability: %+v", plain.Config.Body.Transit)
	}
	if ready() {
		t.Fatal("a node bound to an unsent tunnel is publishable")
	}

	entryResponse := f.syncAs(t, t.Context(), report(f.agent, linkCapabilities))
	entryTransit := entryResponse.Config.Body.Transit
	if entryTransit == nil || len(entryTransit.LinksOut) != 1 || len(entryTransit.LinksIn) != 0 {
		t.Fatalf("entry transit = %+v", entryTransit)
	}
	out := entryTransit.LinksOut[0]
	if out.Key != in.Key || out.Address != "198.51.100.7" || out.Port != in.Port || out.SS == nil || out.SS.Password != in.SS.Password {
		t.Fatalf("outgoing link %+v does not reach incoming link %+v", out, in)
	}
	if want := []nodeprotocol.EgressBinding{{Listener: nodeprotocol.NewListenerKey(f.node.ID), Link: out.Key}}; len(entryTransit.Egress) != 1 || entryTransit.Egress[0] != want[0] {
		t.Fatalf("egress = %+v", entryTransit.Egress)
	}

	// Each side confirms its half; only both together publish the node.
	f.syncAs(t, t.Context(), linkReceipt(f.agent, f.now.UnixMilli(), entryResponse, out.Key, nodeprotocol.ObjectApplied))
	if ready() {
		t.Fatal("published before the exit confirmed")
	}
	before := f.invalidations
	f.syncAs(t, fromExit, linkReceipt(exit, f.now.UnixMilli(), exitResponse, in.Key, nodeprotocol.ObjectApplied))
	if !ready() {
		t.Fatal("not published after both sides confirmed")
	}
	if f.invalidations == before {
		t.Fatal("confirming a tunnel did not refresh subscriptions")
	}

	// The exit rejects its half: the node is withheld again.
	f.syncAs(t, fromExit, linkReceipt(exit, f.now.UnixMilli(), exitResponse, in.Key, nodeprotocol.ObjectRejected))
	if ready() {
		t.Fatal("a rejected tunnel kept its node published")
	}
}

// Both ends of a link running sing-box meter it: each end's counters are booked
// on the side it holds, whatever engine reported them.
func TestSingBoxLinkCountersAreBookedOnEachSide(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.SetTransit(transit.New(f.repos.Node, f.repos.Transit).WithLinks(f.repos.TransitLinks, f.repos.NodeAgent))
	exit := f.exitServer(t)
	if err := f.db.Table("nodes").Where("id = ?", f.node.ID).Update("egress_panel_id", exit.PanelID).Error; err != nil {
		t.Fatal(err)
	}
	report := func(agent *domain.NodeAgent) nodeprotocol.NodeReport {
		return nodeprotocol.NodeReport{AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
			ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(), Capabilities: linkCapabilities, CoreEngine: "sing-box"}
	}
	fromExit := sourceaddr.With(t.Context(), "198.51.100.7")
	exitResponse := f.syncAs(t, fromExit, report(exit))
	entryResponse := f.syncAs(t, t.Context(), report(f.agent))
	key := entryResponse.Config.Body.Transit.LinksOut[0].Key

	counters := func(agent *domain.NodeAgent, response nodeprotocol.SyncResponse, up, down int64) nodeprotocol.NodeReport {
		receipt := linkReceipt(agent, f.now.UnixMilli(), response, key, nodeprotocol.ObjectApplied)
		receipt.CoreEngine = "sing-box"
		receipt.LinkCounters = []nodeprotocol.LinkCounters{{Key: key, Present: true, UpBytes: up, DownBytes: down, CounterEpoch: 1}}
		return receipt
	}
	f.syncAs(t, t.Context(), counters(f.agent, entryResponse, 1000, 2000))
	f.syncAs(t, fromExit, counters(exit, exitResponse, 900, 1900))

	id, err := key.RowID()
	if err != nil {
		t.Fatal(err)
	}
	links, err := f.repos.TransitLinks.ListLinks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.ID != id {
			continue
		}
		if link.OutUpBytes != 1000 || link.OutDownBytes != 2000 || link.InUpBytes != 900 || link.InDownBytes != 1900 {
			t.Fatalf("booked out %d/%d in %d/%d, want entry 1000/2000 and exit 900/1900",
				link.OutUpBytes, link.OutDownBytes, link.InUpBytes, link.InDownBytes)
		}
		return
	}
	t.Fatalf("link %d not found in %+v", id, links)
}
