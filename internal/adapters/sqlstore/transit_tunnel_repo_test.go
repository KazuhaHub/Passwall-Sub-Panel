package sqlstore

import (
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/clientprov"
)

// tunnelLanding stores a landing on server 7 with one tunnel line entering on
// server 9, and the hidden listener that line is served by.
func tunnelLanding(t *testing.T, repo *nodeRepo) (landing, child *domain.Node) {
	t.Helper()
	ctx := t.Context()
	landing = &domain.Node{
		PanelID: 7, InboundID: 1, DisplayName: "JP", ServerAddress: "land.example.com",
		DesiredProtocol: "vless", DesiredPort: 443, Region: "JP", Enabled: true,
	}
	if err := repo.Create(ctx, landing); err != nil {
		t.Fatal(err)
	}
	child = &domain.Node{
		PanelID: 9, InboundID: 1, DisplayName: "JP · hk", ServerAddress: "hk.example.com",
		DesiredProtocol: "vless", DesiredPort: 24443, Region: "JP", Enabled: true,
		ParentNodeID: landing.ID, ParentLineID: "line-1", EgressPanelID: 7,
		ConfigSyncState: domain.ConfigSyncSynced,
	}
	if err := repo.Create(ctx, child); err != nil {
		t.Fatal(err)
	}
	landing.Relays = []domain.RelayLine{
		{ID: "line-1", Name: "hk", Address: "hk.example.com", Enabled: true, EntryPanelID: 9, Mode: domain.RelayModeTunnel, TunnelNodeID: child.ID, Tags: []string{}},
	}
	if err := repo.UpdateMetadata(ctx, landing); err != nil {
		t.Fatal(err)
	}
	return landing, child
}

// readyLink makes the link from server 9 to 7 hold what was sent on both sides,
// with the listener bound in the entry's half.
func readyLink(t *testing.T, repo *nodeRepo, child *domain.Node) {
	t.Helper()
	applyLinkSide(t, repo, child, domain.LinkSideIn, "in-1", 30001)
	applyLinkSide(t, repo, child, domain.LinkSideOut, "out-1", 30001)
}

// applyLinkSide records one side of the link from server 9 to 7 as sent on
// port and confirmed by that side.
func applyLinkSide(t *testing.T, repo *nodeRepo, child *domain.Node, side domain.LinkSide, digest string, port int) {
	t.Helper()
	ctx := t.Context()
	links := &transitLinkRepo{db: repo.db}
	link, err := links.EnsureLink(ctx, 9, 7, func() (domain.TransitLink, error) {
		return domain.TransitLink{EntryPanelID: 9, ExitPanelID: 7, Protocol: "ss2022", Port: 30001}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var bound []string
	if side == domain.LinkSideOut {
		bound = []string{fmt.Sprintf("lst_%d", child.ID)}
	}
	if _, err := links.RecordLinkSent(ctx, link.ID, side, digest, port, bound); err != nil {
		t.Fatal(err)
	}
	if _, err := links.SetLinkApplied(ctx, link.ID, side, digest); err != nil {
		t.Fatal(err)
	}
}

func TestATunnelLineRoundTripsAndIsLoadedWithItsListener(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	landing, child := tunnelLanding(t, nodes)

	got, err := nodes.GetByID(ctx, landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := got.Relays[0]
	if !line.Tunnel() || line.Managed() || line.TunnelNodeID != child.ID || line.EntryPanelID != 9 {
		t.Fatalf("stored line = %+v", line)
	}
	if line.TunnelNode == nil || line.TunnelNode.ID != child.ID || line.TunnelNode.ParentNodeID != landing.ID || line.TunnelNode.ParentLineID != "line-1" {
		t.Fatalf("the listener was not loaded with its line: %+v", line.TunnelNode)
	}
	stored, err := nodes.GetByID(ctx, child.ID)
	if err != nil || !stored.IsTunnelChild() || stored.ParentLineID != "line-1" || stored.EgressPanelID != 7 {
		t.Fatalf("listener = %+v (%v)", stored, err)
	}
}

func TestATunnelLineIsReadyOnlyWhenItsListenerIsAppliedAndItsLinkIsReadyOnBothSides(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	landing, child := tunnelLanding(t, nodes)

	running := func() bool {
		got, err := nodes.GetByID(ctx, landing.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Relays[0].ManagedReady != got.Relays[0].Publishable() {
			t.Fatalf("ManagedReady %v disagrees with Publishable %v", got.Relays[0].ManagedReady, got.Relays[0].Publishable())
		}
		return got.Relays[0].Publishable()
	}
	if running() {
		t.Fatal("a line whose link was never made is published: it fails closed")
	}
	readyLink(t, nodes, child)
	if !running() {
		t.Fatal("a line with its listener applied and its link ready is withheld")
	}
	// The listener is edited and has not been applied again: withheld, not
	// replaced by a direct line.
	child.ConfigSyncState = domain.ConfigSyncPending
	if err := nodes.UpdateInboundConfig(ctx, child); err != nil {
		t.Fatal(err)
	}
	if running() {
		t.Fatal("a line is published while its listener's new settings are not applied")
	}
	child.ConfigSyncState = domain.ConfigSyncSynced
	if err := nodes.UpdateInboundConfig(ctx, child); err != nil {
		t.Fatal(err)
	}
	if !running() {
		t.Fatal("a line is withheld after its listener was applied")
	}
	// The tunnel moves to a shared port and the entry applies its half first:
	// it dials 8443 while the exit still listens on 30001. Each side holds what
	// it was sent, but the line is withheld until both agree on the port.
	applyLinkSide(t, nodes, child, domain.LinkSideOut, "out-shared", 8443)
	if running() {
		t.Fatal("a line is published while its entry dials a port its exit does not listen on")
	}
	if link, err := (&transitLinkRepo{db: nodes.db}).GetLink(ctx, 9, 7); err != nil || link.Ready() {
		t.Fatalf("the link reads ready with its ports apart: %+v %v", link, err)
	}
	applyLinkSide(t, nodes, child, domain.LinkSideIn, "in-shared", 8443)
	if !running() {
		t.Fatal("a line is withheld after both sides moved to the shared port")
	}
	// Disabling the line (or the landing) withdraws it.
	if err := nodes.UpdateEnabled(ctx, landing.ID, false); err != nil {
		t.Fatal(err)
	}
	if running() {
		t.Fatal("a line of a disabled landing is published")
	}
}

func TestATunnelListenerIsHiddenFromNodeListsAndGroups(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	landing, child := tunnelLanding(t, nodes)

	enabled, err := nodes.ListEnabled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].ID != landing.ID {
		t.Fatalf("ListEnabled = %v nodes, want only the landing: groups must never hold the listener", len(enabled))
	}
	if enabled[0].Relays[0].TunnelNode == nil || enabled[0].Relays[0].TunnelNode.ID != child.ID {
		t.Fatal("the landing from a group read did not carry its line's listener")
	}
	paged, total, err := nodes.ListPaged(ctx, ports.Pagination{Page: 1, PageSize: 50})
	if err != nil || total != 1 || len(paged) != 1 || paged[0].ID != landing.ID {
		t.Fatalf("ListPaged = %d/%d (%v), want only the landing", len(paged), total, err)
	}
	// The services that plan servers, ports and tunnels see every listener.
	all, err := nodes.List(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("List = %d (%v), want both", len(all), err)
	}
}

func TestTheProbesOfATunnelsLinkWithholdTheLineAndFeedItsQuality(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	landing, child := tunnelLanding(t, nodes)
	readyLink(t, nodes, child)
	links := &transitLinkRepo{db: nodes.db}
	link, err := links.GetLink(ctx, 9, 7)
	if err != nil {
		t.Fatal(err)
	}
	probes := &transitProbeRepo{db: nodes.db}
	row := domain.ProbeState{PanelID: 9, ObjectKey: fmt.Sprintf("lnk_%d", link.ID), Kind: domain.ProbeKindE2E, State: domain.ProbeDown, Withheld: true,
		StateSince: time.Now().Add(-time.Hour), ReportedAt: time.Now().Add(-time.Minute), ValidUntil: time.Now().Add(time.Hour)}
	if _, _, err := probes.Apply(ctx, row.PanelID, row.ObjectKey, row.Kind, func(*domain.ProbeState) (domain.ProbeState, bool, error) { return row, true, nil }); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"withhold": "1", "off": "0"} {
		if err := nodes.db.Create(&settingRow{Type: "transit_probe", Name: name, Value: value}).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := nodes.GetByID(ctx, landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := got.Relays[0]
	if !line.ProbeWithheld || line.Probe == nil || line.Probe.State != domain.ProbeDown || line.Publishable() {
		t.Fatalf("a line whose tunnel is down was not withheld: withheld %v probe %+v publishable %v", line.ProbeWithheld, line.Probe, line.Publishable())
	}
	// With withholding off the quality is still shown, and the line is published.
	if err := nodes.db.Model(&settingRow{}).Where("type = ? AND name = ?", "transit_probe", "withhold").Update("value", "0").Error; err != nil {
		t.Fatal(err)
	}
	got, err = nodes.GetByID(ctx, landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if line = got.Relays[0]; line.ProbeWithheld || line.Probe == nil || !line.Publishable() {
		t.Fatalf("with withholding off: withheld %v probe %+v publishable %v", line.ProbeWithheld, line.Probe, line.Publishable())
	}
}

// A database written before tunnel lines has no parent columns and lines
// without a mode: the upgrade adds the columns, and every old node and line
// reads exactly as before.
func TestUpgradeAddsTheTunnelColumnsAndLeavesEveryOldLineAForward(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	m := nodes.db.Migrator()
	for _, column := range []string{"parent_node_id", "parent_line_id"} {
		if m.HasColumn(&nodeRow{}, column) {
			if err := m.DropColumn(&nodeRow{}, column); err != nil {
				t.Skipf("this database cannot drop %s: %v", column, err)
			}
		}
	}
	if err := nodes.db.Exec(`INSERT INTO nodes (panel_id, inbound_id, display_name, region, enabled, kind, relays, traffic_factor_milli, egress_panel_id)
		VALUES (7, 3, 'old', 'JP', ?, 'real', ?, 1000, 0)`, true,
		`[{"ID":"old","Name":"via A","Address":"a.example.com","Port":20443,"Enabled":true,"EntryPanelID":9,"Region":"JP","Tags":[]}]`).Error; err != nil {
		t.Fatal(err)
	}
	if err := autoMigrateCurrentSchema(nodes.db); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"parent_node_id", "parent_line_id"} {
		if !m.HasColumn(&nodeRow{}, column) {
			t.Fatalf("the upgrade did not add %s", column)
		}
	}
	got, err := nodes.List(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %d (%v)", len(got), err)
	}
	if got[0].IsTunnelChild() || got[0].ParentLineID != "" {
		t.Fatalf("an old node became a tunnel listener: %+v", got[0])
	}
	line := got[0].Relays[0]
	if line.Tunnel() || !line.Managed() || line.TunnelNodeID != 0 || line.Port != 20443 {
		t.Fatalf("an old line is not a direct forward any more: %+v", line)
	}
}

// A line's identity moves to the entry server when the line becomes a tunnel
// and back when it returns to a forward: the unique key holds the server, so
// the new row never collides with the old server's detached one, and going back
// reuses the old row (its counters with it).
func TestSwitchingALineForwardToTunnelAndBackKeepsEveryMembersIdentity(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	clients := &pspClientRepo{db: nodes.db}
	svc := clientprov.New(clients)
	rules := domain.EmailRules{Domain: "psp.local"}
	child := &domain.Node{ID: 20, PanelID: 9, DesiredProtocol: "vless", ParentNodeID: 1, ParentLineID: "a"}
	landing := func(mode string) []*domain.Node {
		line := domain.RelayLine{ID: "a", Enabled: true, EntryPanelID: 9, Port: 20001}
		if mode == domain.RelayModeTunnel {
			line.Mode, line.Port, line.TunnelNodeID, line.TunnelNode = mode, 0, 20, child
		}
		return []*domain.Node{{ID: 1, PanelID: 7, DesiredProtocol: "vless", Relays: []domain.RelayLine{line}}}
	}
	row := func(panel int64) *domain.PSPClient {
		all, err := clients.ListByUser(ctx, 5)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range all {
			if c.PanelID == panel && c.LineID == "a" {
				return c
			}
		}
		return nil
	}
	attachedTo := func(c *domain.PSPClient) int {
		in, err := clients.ListInbounds(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(in)
	}
	for step, mode := range []string{"", domain.RelayModeTunnel, "", domain.RelayModeTunnel} {
		if _, err := svc.SyncUser(ctx, 5, "11111111-2222-4333-8444-555555555555", rules, landing(mode)); err != nil {
			t.Fatalf("step %d (mode %q): %v", step, mode, err)
		}
		on7, on9 := row(7), row(9)
		if mode == "" && (on7 == nil || attachedTo(on7) != 1 || (on9 != nil && attachedTo(on9) != 0)) {
			t.Fatalf("step %d: forward line must live on server 7 only: %+v / %+v", step, on7, on9)
		}
		if mode != "" && (on9 == nil || attachedTo(on9) != 1 || (on7 != nil && attachedTo(on7) != 0)) {
			t.Fatalf("step %d: tunnel line must live on server 9 only: %+v / %+v", step, on7, on9)
		}
	}
	// The second forward step reused the first forward row.
	if all, _ := clients.ListByUser(ctx, 5); len(all) != 3 {
		t.Fatalf("rows = %d, want the direct line and one row per server for the line", len(all))
	}
}

// A database with the old key (user, node, line) and rows in it is upgraded to
// the key that holds the server, keeping the rows.
func TestUpgradeWidensTheClientLineKeyKeepingExistingRows(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	db := nodes.db
	m := db.Migrator()
	if m.HasIndex("psp_clients", "uk_psp_client_line_panel") {
		if err := m.DropIndex("psp_clients", "uk_psp_client_line_panel"); err != nil {
			t.Fatal(err)
		}
	}
	if !m.HasIndex("psp_clients", "uk_psp_client_line") {
		if err := db.Exec("CREATE UNIQUE INDEX uk_psp_client_line ON psp_clients (user_id, node_id, line_id)").Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := &pspClientRepo{db: db}
	old := &domain.PSPClient{UserID: 5, PanelID: 7, NodeID: 1, LineID: "a", Email: "u5-la@x", UUID: "u"}
	if _, err := repo.Create(ctx, old); err != nil {
		t.Fatal(err)
	}
	moved := &domain.PSPClient{UserID: 5, PanelID: 9, NodeID: 1, LineID: "a", Email: "u5-la@x", UUID: "v"}
	if _, err := repo.Create(ctx, moved); err == nil {
		t.Fatal("the old key accepted two servers' rows of one line: the test does not start from the old schema")
	}
	if err := autoMigrateCurrentSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := migrateV4_3(db); err != nil {
		t.Fatal(err)
	}
	if m.HasIndex("psp_clients", "uk_psp_client_line") || !m.HasIndex("psp_clients", "uk_psp_client_line_panel") {
		t.Fatal("the line key was not replaced")
	}
	if _, err := repo.Create(ctx, moved); err != nil {
		t.Fatalf("a line's row on another server is still refused: %v", err)
	}
	if _, err := repo.Create(ctx, &domain.PSPClient{UserID: 5, PanelID: 9, NodeID: 1, LineID: "a", Email: "dup@x", UUID: "w"}); err == nil {
		t.Fatal("two rows of one line on one server were accepted")
	}
	if all, err := repo.ListByUser(ctx, 5); err != nil || len(all) != 2 {
		t.Fatalf("rows = %d (%v)", len(all), err)
	}
}
