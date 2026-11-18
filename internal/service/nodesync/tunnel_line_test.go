package nodesync

import (
	"context"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/sourceaddr"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

// The entry server's snapshot holds the listener of a tunnel line and the
// clients attached to it, and not the landing that owns the line.
func tunnelSnapshot(factor int) (*ports.NativeDesiredSnapshot, *domain.PSPClient) {
	child := &domain.Node{ID: 20, PanelID: 9, DesiredProtocol: "vless", Enabled: true, ParentNodeID: 3, ParentLineID: "t", EgressPanelID: 4,
		TrafficFactorMilli: &factor}
	client := &domain.PSPClient{ID: 11, UserID: 7, PanelID: 9, NodeID: 3, LineID: "t", Email: "u7-lt@x", DesiredEnable: true,
		UUID: domain.DeriveLineUUID("user-uuid", domain.LineKey{NodeID: 3, LineID: "t"}, 1)}
	return &ports.NativeDesiredSnapshot{
		Nodes:   []*domain.Node{child},
		Clients: []ports.NativeDesiredClient{{Client: client, Inbounds: []domain.PSPClientInbound{{ClientID: 11, NodeID: 20}}}},
	}, client
}

func TestTheEntryServersRosterCarriesTheLinesDerivedIdentityOnTheListener(t *testing.T) {
	snapshot, client := tunnelSnapshot(1500)
	roster := buildRoster(snapshot, nodeprotocol.Version{Epoch: 1, Version: 1})
	if len(roster.Clients) != 1 {
		t.Fatalf("clients = %d", len(roster.Clients))
	}
	got := roster.Clients[0]
	if len(got.Listeners) != 1 || got.Listeners[0] != nodeprotocol.NewListenerKey(20) {
		t.Fatalf("listeners = %v, want the line's own listener", got.Listeners)
	}
	if got.Credentials.UUID != client.UUID || got.Credentials.UUID == "user-uuid" {
		t.Fatalf("uuid = %s, want the line's derived identity", got.Credentials.UUID)
	}
	if got.Key != nodeprotocol.NewClientKey(client.ID) || got.Subject != nodeprotocol.NewSubjectKey(client.UserID) {
		t.Fatalf("keys = %v / %v: the line's client must be the user's, one row per line", got.Key, got.Subject)
	}
}

// The user's quota is granted to the entry server's client through the line's
// own factor, which the listener carries because the landing is not on that
// server: ×1.5 turns 1500 charged bytes left into 1000 raw bytes of headroom.
func TestATunnelClientsQuotaIsConvertedThroughItsLinesFactor(t *testing.T) {
	snapshot, client := tunnelSnapshot(1500)
	user := &domain.User{ID: 7, TrafficLimitBytes: 3000, TrafficResetPeriod: domain.ResetMonthly, LifetimeTotalBytes: 1500}
	agent := &domain.NodeAgent{AgentID: "agt_tunnel", PanelID: client.PanelID}
	service := &Service{
		users:    emergencyDirectiveUsers{user: user},
		clients:  emergencyDirectiveClients{client: client},
		agents:   emergencyDirectiveAgents{agent: agent},
		settings: emergencyDirectiveSettings{},
		reports:  make(map[string]receivedFullReport),
		anchors:  make(map[int64]nodeprotocol.ClientCounters),
		grants:   make(map[string]map[nodeprotocol.ClientKey]int64),
	}
	report := nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{{Key: nodeprotocol.NewClientKey(client.ID), UpBytes: 100, DownBytes: 50, CounterEpoch: 1}}}
	body, _, err := service.buildDirectives(t.Context(), agent, snapshot, report, nodeprotocol.Version{Epoch: 1, Version: 1}, time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(body.Quota) != 1 {
		t.Fatalf("quota entries = %d", len(body.Quota))
	}
	one := int64(1000)
	assertHeadroom(t, "headroom", body.Quota[0].HeadroomBytes, &one)
}

func TestPendingUsageOfATunnelClientIsWeightedByItsLine(t *testing.T) {
	snapshot, client := tunnelSnapshot(3000)
	service := &Service{
		reports: map[string]receivedFullReport{
			"agent-e": {report: nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{
				{Key: nodeprotocol.NewClientKey(client.ID), Present: true, CounterEpoch: 1, UpBytes: 10, DownBytes: 20},
			}}},
		},
		anchors: make(map[int64]nodeprotocol.ClientCounters),
	}
	client.LastCounterEpoch = 1
	usage := service.pendingUsage(map[int64]*domain.PSPClient{client.ID: client}, []*domain.NodeAgent{{AgentID: "agent-e", PanelID: 9}},
		domain.LineFactors(snapshot.Nodes))
	if usage[7] != 90 {
		t.Fatalf("pending = %d, want 30 raw bytes at ×3 = 90", usage[7])
	}
}

// The whole path of one tunnel line through the sync: the entry server (panel
// 9, the fixture's node is the line's hidden listener) gets the listener, the
// outgoing half of the tunnel to the landing's server (panel 3) bound to that
// listener, and the roster entry of the line's own identity; the landing's
// server gets the incoming half. The line is published only when the listener
// is applied and both halves of the tunnel are, and withheld again when either
// side rejects.
func TestATunnelLineIsPublishedOnlyWhenItsListenerAndBothHalvesOfItsTunnelAreApplied(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.service.SetTransit(transit.New(f.repos.Node, f.repos.Transit).WithLinks(f.repos.TransitLinks, f.repos.NodeAgent))
	exit := f.exitServer(t)
	ctx := t.Context()

	// The landing is the exit server's listener; the fixture's node becomes the
	// hidden listener of its line "hk" on the entry server.
	landing, err := f.repos.Node.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var landingID int64
	for _, n := range landing {
		if n.PanelID == exit.PanelID {
			landingID = n.ID
		}
	}
	if err := f.db.Table("nodes").Where("id = ?", f.node.ID).Updates(map[string]any{
		"parent_node_id": landingID, "parent_line_id": "hk", "egress_panel_id": exit.PanelID,
		"config_sync_state": domain.ConfigSyncPending,
	}).Error; err != nil {
		t.Fatal(err)
	}
	landingNode, err := f.repos.Node.GetByID(ctx, landingID)
	if err != nil {
		t.Fatal(err)
	}
	landingNode.Relays = []domain.RelayLine{{ID: "hk", Name: "Hong Kong", Address: "hk.example.test", Enabled: true, EntryPanelID: f.node.PanelID,
		Mode: domain.RelayModeTunnel, TunnelNodeID: f.node.ID, Tags: []string{}, Region: "HK"}}
	if err := f.repos.Node.UpdateMetadata(ctx, landingNode); err != nil {
		t.Fatal(err)
	}
	// The user's client for the line: the landing's line, on the entry server,
	// attached to the line's listener, with the line's derived identity.
	user := &domain.User{UPN: "u@example.test", Email: "u@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "u@example.test",
		Role: domain.RoleUser, SubToken: "tok", UUID: "11111111-1111-4111-8111-111111111111", TrafficResetPeriod: domain.ResetMonthly, Enabled: true}
	if err := f.repos.User.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	identity := domain.DeriveLineUUID(user.UUID, domain.LineKey{NodeID: landingID, LineID: "hk"}, 1)
	client := &domain.PSPClient{UserID: user.ID, PanelID: f.node.PanelID, NodeID: landingID, LineID: "hk", Email: "u1-lt@psp.local",
		UUID: identity, Password: identity, DesiredEnable: true, DesiredMinted: true}
	if client.ID, err = f.repos.PSPClient.Create(ctx, client); err != nil {
		t.Fatal(err)
	}
	if err := f.repos.PSPClient.SetInbounds(ctx, client.ID, []domain.PSPClientInbound{{ClientID: client.ID, NodeID: f.node.ID, State: domain.ClientApplyPending}}); err != nil {
		t.Fatal(err)
	}

	published := func() bool {
		t.Helper()
		got, err := f.repos.Node.GetByID(ctx, landingID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Relays[0].Publishable()
	}
	report := func(agent *domain.NodeAgent) nodeprotocol.NodeReport {
		return nodeprotocol.NodeReport{AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
			ReportedAtMS: f.now.UnixMilli(), Have: emptyProtocolHave(), Capabilities: linkCapabilities}
	}
	receipt := func(agent *domain.NodeAgent, response nodeprotocol.SyncResponse, state nodeprotocol.ObjectState, keys ...string) nodeprotocol.NodeReport {
		have := emptyProtocolHave()
		have[nodeprotocol.StreamConfig] = nodeprotocol.StreamState{Applied: response.Config.Version, ETag: response.Config.ETag}
		var objects []nodeprotocol.ObjectStatus
		for _, key := range keys {
			status := nodeprotocol.ObjectStatus{Stream: nodeprotocol.StreamConfig, Key: key, State: state, SinceVersion: response.Config.Version}
			if state == nodeprotocol.ObjectRejected {
				status.FirstFailedAtMS, status.IssueCode = f.now.UnixMilli(), "core_config_rejected"
			}
			objects = append(objects, status)
		}
		return nodeprotocol.NodeReport{AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1, ReportedAtMS: f.now.UnixMilli(),
			Have: have, Capabilities: linkCapabilities, Objects: objects}
	}

	fromExit := sourceaddr.With(ctx, "198.51.100.7")
	exitResponse := f.syncAs(t, fromExit, report(exit))
	exitTransit := exitResponse.Config.Body.Transit
	if exitTransit == nil || len(exitTransit.LinksIn) != 1 {
		t.Fatalf("the landing's server did not get the incoming half: %+v", exitTransit)
	}
	in := exitTransit.LinksIn[0]

	entryResponse := f.syncAs(t, ctx, report(f.agent))
	listener := nodeprotocol.NewListenerKey(f.node.ID)
	if len(entryResponse.Config.Body.Listeners) != 1 || entryResponse.Config.Body.Listeners[0].Key != listener {
		t.Fatalf("listeners = %+v, want the line's own", entryResponse.Config.Body.Listeners)
	}
	entryTransit := entryResponse.Config.Body.Transit
	if entryTransit == nil || len(entryTransit.LinksOut) != 1 || len(entryTransit.Forwards) != 0 {
		t.Fatalf("entry transit = %+v: a tunnel line asks for a link and never for a forward", entryTransit)
	}
	out := entryTransit.LinksOut[0]
	if out.Key != in.Key || len(entryTransit.Egress) != 1 || entryTransit.Egress[0] != (nodeprotocol.EgressBinding{Listener: listener, Link: out.Key}) {
		t.Fatalf("egress = %+v, want the line's listener bound to the tunnel", entryTransit.Egress)
	}
	roster := entryResponse.Roster.Body
	if roster == nil || len(roster.Clients) != 1 {
		t.Fatalf("roster = %+v", entryResponse.Roster)
	}
	got := roster.Clients[0]
	if got.Credentials.UUID != identity || len(got.Listeners) != 1 || got.Listeners[0] != listener {
		t.Fatalf("roster client = %+v, want the line's derived identity on its listener", got)
	}

	if published() {
		t.Fatal("published before anything was applied")
	}
	// The listener is applied, the entry's half too: still not the exit's.
	f.syncAs(t, ctx, receipt(f.agent, entryResponse, nodeprotocol.ObjectApplied, string(listener), string(out.Key)))
	if published() {
		t.Fatal("published before the landing's server applied its half")
	}
	before := f.invalidations
	f.syncAs(t, fromExit, receipt(exit, exitResponse, nodeprotocol.ObjectApplied, string(in.Key)))
	if !published() {
		t.Fatal("not published with the listener applied and both halves confirmed")
	}
	if f.invalidations == before {
		t.Fatal("publishing the line did not refresh subscriptions")
	}
	// The landing's server rejects its half: the line is withheld, not replaced.
	f.syncAs(t, fromExit, receipt(exit, exitResponse, nodeprotocol.ObjectRejected, string(in.Key)))
	if published() {
		t.Fatal("a rejected tunnel kept its line published")
	}
}

// The source-address limit is the person's: however many tunnel lines (rows) a
// user has on a server, the node is told one limit for the one person, so lines
// never multiply it, and PSP judges the addresses of all of a person's rows as
// one set (see the traffic service's tunnel rows).
func TestAUsersTunnelRowsShareOneIPLimit(t *testing.T) {
	snapshot, first := tunnelSnapshot(1000)
	second := &domain.PSPClient{ID: 12, UserID: first.UserID, PanelID: first.PanelID, NodeID: 4, LineID: "u", Email: "u7-lu@x", DesiredEnable: true, UUID: "22222222-2222-4222-8222-222222222222"}
	snapshot.Clients = append(snapshot.Clients, ports.NativeDesiredClient{Client: second, Inbounds: []domain.PSPClientInbound{{ClientID: 12, NodeID: 20}}})
	limit := 3
	user := &domain.User{ID: first.UserID, TrafficLimitBytes: 3000, TrafficResetPeriod: domain.ResetMonthly, Limits: domain.LimitOverrides{IPLimit: &limit}}
	agent := &domain.NodeAgent{AgentID: "agt_tunnel", PanelID: first.PanelID}
	service := &Service{
		users:    emergencyDirectiveUsers{user: user},
		clients:  emergencyDirectiveClients{client: first},
		agents:   emergencyDirectiveAgents{agent: agent},
		settings: emergencyDirectiveSettings{},
		reports:  make(map[string]receivedFullReport),
		anchors:  make(map[int64]nodeprotocol.ClientCounters),
		grants:   make(map[string]map[nodeprotocol.ClientKey]int64),
	}
	body, _, err := service.buildDirectives(t.Context(), agent, snapshot, nodeprotocol.NodeReport{}, nodeprotocol.Version{Epoch: 1, Version: 1}, time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(body.IPShadow) != 1 || body.IPShadow[0].Subject != nodeprotocol.NewSubjectKey(first.UserID) || body.IPShadow[0].IPLimit != user.IPLimit {
		t.Fatalf("ip shadow = %+v, want one entry for the person with the person's limit", body.IPShadow)
	}
}

type mapNodes struct {
	ports.NodeRepo
	nodes map[int64]*domain.Node
}

func (m mapNodes) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	if n, ok := m.nodes[id]; ok {
		return n, nil
	}
	return nil, domain.ErrNotFound
}

// A hidden listener whose line is gone, and one whose copy of its line's factor
// is out of date, are reported as issues of their server; a healthy one is not.
func TestOrphanAndStaleTunnelListenersAreReportedAsIssues(t *testing.T) {
	two := 2000
	landing := &domain.Node{ID: 3, PanelID: 1, Relays: []domain.RelayLine{
		{ID: "ok", Enabled: true, Mode: domain.RelayModeTunnel, TunnelNodeID: 20},
		{ID: "stale", Enabled: true, Mode: domain.RelayModeTunnel, TunnelNodeID: 21, TrafficFactorMilli: &two},
	}}
	snapshot := &ports.NativeDesiredSnapshot{Nodes: []*domain.Node{
		{ID: 20, PanelID: 9, ParentNodeID: 3, ParentLineID: "ok"},
		{ID: 21, PanelID: 9, ParentNodeID: 3, ParentLineID: "stale"},
		{ID: 22, PanelID: 9, ParentNodeID: 3, ParentLineID: "gone", DisplayName: "left behind"},
		{ID: 23, PanelID: 9, ParentNodeID: 99, ParentLineID: "x", DisplayName: "landing deleted"},
		{ID: 24, PanelID: 9, DisplayName: "an ordinary node"},
	}}
	service := &Service{nodes: mapNodes{nodes: map[int64]*domain.Node{3: landing}}}
	// A listener is judged an orphan only once two syncs in a row have found it
	// so: a save holding the locks may be making it and pointing its line at it.
	if first := service.orphanTunnelIssues(t.Context(), snapshot); len(first) != 1 || first[0].Code != IssueTunnelFactorStale {
		t.Fatalf("the first sync reported %+v, want only the stale factor", first)
	}
	got := map[string]string{}
	for _, issue := range service.orphanTunnelIssues(t.Context(), snapshot) {
		got[issue.Key] = issue.Code
	}
	want := map[string]string{
		string(nodeprotocol.NewListenerKey(21)): IssueTunnelFactorStale,
		string(nodeprotocol.NewListenerKey(22)): IssueTunnelListenerOrphan,
		string(nodeprotocol.NewListenerKey(23)): IssueTunnelListenerOrphan,
	}
	if len(got) != len(want) {
		t.Fatalf("issues = %v, want %v", got, want)
	}
	for key, code := range want {
		if got[key] != code {
			t.Fatalf("issues = %v, want %v", got, want)
		}
	}
}

// The count of an orphan that has since been removed is dropped.
func TestTheOrphanCountOfARemovedListenerIsPruned(t *testing.T) {
	service := &Service{nodes: mapNodes{nodes: map[int64]*domain.Node{}}}
	snapshot := &ports.NativeDesiredSnapshot{Nodes: []*domain.Node{{ID: 22, PanelID: 9, ParentNodeID: 3, ParentLineID: "gone"}}}
	service.orphanTunnelIssues(t.Context(), snapshot)
	if len(service.orphanSeen) != 1 {
		t.Fatalf("counts = %v", service.orphanSeen)
	}
	// The listener is deleted: the next sync (of any server) forgets it.
	service.orphanTunnelIssues(t.Context(), &ports.NativeDesiredSnapshot{})
	if len(service.orphanSeen) != 0 {
		t.Fatalf("a removed listener's count stayed: %v", service.orphanSeen)
	}
}
