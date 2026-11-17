package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
)

type joinFronts struct {
	fixedFronts
}

func (j *joinFronts) Get(_ context.Context, id int64) (domain.TransitFront, error) {
	for _, row := range j.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return domain.TransitFront{}, domain.ErrNotFound
}
func (j *joinFronts) Update(_ context.Context, f domain.TransitFront) (domain.TransitFront, error) {
	for i := range j.rows {
		if j.rows[i].ID == f.ID {
			j.rows[i] = f
			return f, nil
		}
	}
	return domain.TransitFront{}, domain.ErrNotFound
}

type nodeList []*domain.Node

func (n nodeList) List(context.Context) ([]*domain.Node, error) { return n, nil }

type panelOK struct{}

func (panelOK) GetByID(_ context.Context, id int64) (*domain.XUIPanel, error) {
	return &domain.XUIPanel{ID: id, Kind: domain.PanelKindPSP}, nil
}

type agentOK struct{ engine domain.NodeCoreEngine }

func (a agentOK) GetByPanelID(_ context.Context, id int64) (*domain.NodeAgent, error) {
	engine := a.engine
	if engine == "" {
		engine = domain.NodeCoreXray
	}
	return &domain.NodeAgent{PanelID: id, DesiredCoreEngine: engine, ObservedCoreEngine: engine,
		ObservedCapabilities: []string{nodeprotocol.CapabilityTransitSNI}, ObservedAddress: "198.51.100.10"}, nil
}

type noForwards struct{}

func (noForwards) Ensure(context.Context, int64, string) (domain.TransitForward, error) {
	return domain.TransitForward{}, nil
}

func joinService(t *testing.T) (*Service, *joinFronts, *domain.Node) {
	t.Helper()
	first := fronted(domain.Node{ID: 2, PanelID: 9, DisplayName: "behind", DesiredPort: 443, Enabled: true})
	second := fronted(domain.Node{ID: 3, PanelID: 9, DisplayName: "second", DesiredPort: 443, Enabled: true})
	second.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["b.example.com"]}}`
	fronts := &joinFronts{fixedFronts{rows: []domain.TransitFront{{
		ID: 1, PanelID: 9, Port: 443,
		Listeners: []domain.FrontListener{{NodeID: 2, LoopbackPort: 30001}},
		Routes:    []domain.FrontRoute{{ServerNames: []string{"a.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 2}}},
		Default:   domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}}}}
	other := second
	other.DesiredPort = 8443 // stored before the save that moves it
	svc := front.New(fronts, nodeList{&first, &other}, panelOK{}, agentOK{}, noForwards{}, nil, nil)
	pool := panelListPool{panels: []*domain.Panel{{ID: 9, Kind: domain.PanelKindPSP}}}
	return &Service{pool: pool, fronts: fronts, frontSvc: svc}, fronts, &second
}

func TestAListenerLandingOnASharedPortIsOfferedTheFrontAndJoinsOnConfirmation(t *testing.T) {
	svc, fronts, second := joinService(t)

	// Unconfirmed: a 409 naming the front, the SNI and the loopback port.
	_, err := svc.checkInboundAgainstFronts(context.Background(), second)
	var refusal *front.Refusal
	if !errors.As(err, &refusal) || refusal.Code != front.CodeConfirmRequired || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if refusal.Params["port"] != 443 || refusal.Params["names"].([]string)[0] != "b.example.com" || refusal.Params["loopback_port"] == nil {
		t.Fatalf("params = %v", refusal.Params)
	}
	if len(fronts.rows[0].Listeners) != 1 {
		t.Fatal("nothing may change before the confirmation")
	}

	// Confirmed: checked now, joined once the node is stored.
	confirmation := &front.Confirmation{Confirmed: true, LoopbackPort: 30077}
	ctx := front.WithConfirmation(context.Background(), confirmation)
	offer, err := svc.checkInboundAgainstFronts(ctx, second)
	if err != nil || offer == nil {
		t.Fatalf("offer = %+v err = %v", offer, err)
	}
	if len(fronts.rows[0].Listeners) != 1 {
		t.Fatal("the check wrote")
	}
	stored := *second
	if err := svc.finishFront(ctx, &stored, offer); err != nil {
		t.Fatal(err)
	}
	row := fronts.rows[0]
	if len(row.Listeners) != 2 || len(row.Routes) != 2 || confirmation.Joined == nil || confirmation.Joined.LoopbackPort != 30077 || confirmation.Joined.PanelID != 9 {
		t.Fatalf("row = %+v joined = %+v", row, confirmation.Joined)
	}
}

func TestAListenerThatCannotJoinASharedPortIsRefusedNamingIt(t *testing.T) {
	svc, _, second := joinService(t)
	second.StreamSettings = `{"network":"grpc","security":"tls","tlsSettings":{"serverName":"b.example.com"}}`
	ctx := front.WithConfirmation(context.Background(), &front.Confirmation{Confirmed: true})
	_, err := svc.checkInboundAgainstFronts(ctx, second)
	if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "port 443 is shared") {
		t.Fatalf("err = %v", err)
	}
}

func TestDisablingAFrontedListenerAsksAndNamesTheRoutes(t *testing.T) {
	svc, _, _ := joinService(t)
	repo := &fleetNodeRepo{labelNodeRepo: labelNodeRepo{stored: domain.Node{ID: 2, PanelID: 9, DisplayName: "behind", Enabled: true}}}
	svc.nodes = repo
	err := svc.SetEnabled(context.Background(), 2, false)
	var refusal *front.Refusal
	if !errors.As(err, &refusal) || refusal.Code != front.CodeConfirmRequired || refusal.Params["routes"].([]string)[0] != "a.example.com" {
		t.Fatalf("err = %v", err)
	}
	// Enabling a node never asks (this stops at the pool, which the test has none of).
	defer func() { _ = recover() }()
	err = svc.SetEnabled(context.Background(), 2, true)
	if errors.As(err, &refusal) {
		t.Fatalf("enabling asked: %v", err)
	}
}

func TestAnOrphanFrontNeverBlocksAnEdit(t *testing.T) {
	// The front belongs to server 9; the pool no longer has it.
	pool := panelListPool{panels: []*domain.Panel{{ID: 7, Kind: domain.PanelKindPSP}}}
	stored := domain.Node{ID: 1, PanelID: 7, DisplayName: "landing", DesiredPort: 443, Enabled: true, Relays: []domain.RelayLine{
		{ID: "a", Address: "a.example", Port: 20443, Enabled: true, EntryPanelID: 7, Tags: []string{}},
	}}
	svc := &Service{pool: pool, fronts: fixedFronts{rows: []domain.TransitFront{guardFront()}}}
	// The orphan routes node 1's line "a"; moving the line would be refused if it counted.
	landing := stored
	landing.Relays = nil
	if err := svc.checkRelaysAgainstFronts(context.Background(), &landing); err != nil {
		t.Fatalf("an orphan front blocked an edit: %v", err)
	}
	removed := domain.Node{ID: 2, PanelID: 9, DisplayName: "behind"}
	if _, err := svc.checkRemovalAgainstFronts(context.Background(), &removed); err != nil {
		t.Fatalf("an orphan front blocked a delete: %v", err)
	}
}

// bothBehind joins the second listener to the front, as the save does.
func bothBehind(t *testing.T) (*Service, *joinFronts, *domain.Node) {
	t.Helper()
	svc, fronts, second := joinService(t)
	ctx := front.WithConfirmation(context.Background(), &front.Confirmation{Confirmed: true})
	change, err := svc.checkInboundAgainstFronts(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	stored := *second
	if err := svc.finishFront(ctx, &stored, change); err != nil {
		t.Fatal(err)
	}
	if len(fronts.rows[0].Listeners) != 2 {
		t.Fatalf("setup: %+v", fronts.rows[0])
	}
	return svc, fronts, second
}

func TestAFrontedListenerLeavesTheFrontWhenItsPortChangesAfterConfirmation(t *testing.T) {
	svc, fronts, second := bothBehind(t)
	moved := *second
	moved.DesiredPort = 8443
	_, err := svc.checkInboundAgainstFronts(context.Background(), &moved)
	var refusal *front.Refusal
	if !errors.As(err, &refusal) || refusal.Code != front.CodeConfirmRequired || refusal.Params["action"] != "leave" ||
		refusal.Params["routes"].([]string)[0] != "b.example.com" {
		t.Fatalf("err = %v", err)
	}
	if len(fronts.rows[0].Listeners) != 2 {
		t.Fatal("nothing may change before the confirmation")
	}
	confirmation := &front.Confirmation{Confirmed: true}
	ctx := front.WithConfirmation(context.Background(), confirmation)
	change, err := svc.checkInboundAgainstFronts(ctx, &moved)
	if err != nil || change == nil || change.leave == nil {
		t.Fatalf("change = %+v err = %v", change, err)
	}
	if err := svc.finishFront(ctx, &moved, change); err != nil {
		t.Fatal(err)
	}
	if row := fronts.rows[0]; len(row.Listeners) != 1 || len(row.Routes) != 1 || confirmation.Left == nil || confirmation.Left.NodeID != 3 {
		t.Fatalf("row = %+v left = %+v", row, confirmation.Left)
	}
}

func TestDeletingAFrontedListenerAsksAndTakesItOutOfTheFront(t *testing.T) {
	svc, fronts, second := bothBehind(t)
	_, err := svc.checkRemovalAgainstFronts(context.Background(), second)
	var refusal *front.Refusal
	if !errors.As(err, &refusal) || refusal.Code != front.CodeConfirmRequired || refusal.Params["action"] != "leave" {
		t.Fatalf("err = %v", err)
	}
	confirmation := &front.Confirmation{Confirmed: true}
	ctx := front.WithConfirmation(context.Background(), confirmation)
	change, err := svc.checkRemovalAgainstFronts(ctx, second)
	if err != nil || change == nil || !change.removed {
		t.Fatalf("change = %+v err = %v", change, err)
	}
	if err := svc.finishFront(ctx, second, change); err != nil {
		t.Fatal(err)
	}
	if row := fronts.rows[0]; len(row.Listeners) != 1 || len(row.Routes) != 1 {
		t.Fatalf("row = %+v", row)
	}
	// The last listener cannot go while it is all the front has.
	first := fronted(domain.Node{ID: 2, PanelID: 9, DisplayName: "behind", DesiredPort: 443, Enabled: true})
	if _, err := svc.checkRemovalAgainstFronts(ctx, &first); err == nil || !strings.Contains(err.Error(), "delete that shared port first") {
		t.Fatalf("err = %v", err)
	}
}

type downPool struct{ panelListPool }

func (downPool) Get(int64) (ports.XUIClient, error) {
	return nil, errors.New("panel unreachable")
}

// A create that joins a front is never queued for later: a queued create would
// run without the join and land the node on the shared port beside the front.
func TestACreateThatJoinsAFrontIsNotQueued(t *testing.T) {
	svc, fronts, _ := joinService(t)
	svc.pool = downPool{panelListPool{panels: []*domain.Panel{{ID: 9, Kind: domain.PanelKindPSP}}}}
	svc.nodes = &fleetNodeRepo{}
	ctx := front.WithConfirmation(context.Background(), &front.Confirmation{Confirmed: true})
	n := &domain.Node{PanelID: 9, DisplayName: "new", Region: "JP", DesiredProtocol: "vless"}
	spec := ports.InboundSpec{Port: 443, Protocol: "vless",
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["c.example.com"]}}`}
	// svc.tasks is nil: queueing would panic, so reaching the error proves it did not.
	err := svc.CreateInbound(ctx, n, spec)
	if err == nil || !strings.Contains(err.Error(), "panel unreachable") {
		t.Fatalf("err = %v", err)
	}
	if len(fronts.rows[0].Listeners) != 1 {
		t.Fatal("the front changed although nothing was created")
	}
}

// twoFronts is A on 443 (nodes 2 and 3) and B on 8443 (node 4), all REALITY with
// their own names.
func twoFronts(t *testing.T, engine domain.NodeCoreEngine) (*Service, *joinFronts, []*domain.Node) {
	t.Helper()
	mk := func(id int64, name string, port int, sni string) *domain.Node {
		n := fronted(domain.Node{ID: id, PanelID: 9, DisplayName: name, DesiredPort: port, Enabled: true})
		n.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["` + sni + `"]}}`
		return &n
	}
	nodes := []*domain.Node{mk(2, "n2", 443, "a.example.com"), mk(3, "n3", 443, "b.example.com"), mk(4, "n4", 8443, "c.example.com")}
	ext := domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443}
	listener := func(id int64) domain.FrontTarget {
		return domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: id}
	}
	fronts := &joinFronts{fixedFronts{rows: []domain.TransitFront{
		{ID: 1, PanelID: 9, Port: 443, Default: ext,
			Listeners: []domain.FrontListener{{NodeID: 2, LoopbackPort: 30001}, {NodeID: 3, LoopbackPort: 30002}},
			Routes:    []domain.FrontRoute{{ServerNames: []string{"a.example.com"}, Target: listener(2)}, {ServerNames: []string{"b.example.com"}, Target: listener(3)}}},
		{ID: 2, PanelID: 9, Port: 8443, Default: ext,
			Listeners: []domain.FrontListener{{NodeID: 4, LoopbackPort: 30003}},
			Routes:    []domain.FrontRoute{{ServerNames: []string{"c.example.com"}, Target: listener(4)}}},
	}}}
	svc := front.New(fronts, nodeList(nodes), panelOK{}, agentOK{engine: engine}, noForwards{}, nil, nil)
	pool := panelListPool{panels: []*domain.Panel{{ID: 9, Kind: domain.PanelKindPSP}}}
	return &Service{pool: pool, fronts: fronts, frontSvc: svc}, fronts, nodes
}

func TestAListenerMovingFromOneSharedPortOntoAnotherLeavesAndJoinsInOneConfirmation(t *testing.T) {
	svc, fronts, nodes := twoFronts(t, "")
	moved := *nodes[1]
	moved.DesiredPort = 8443
	_, err := svc.checkInboundAgainstFronts(context.Background(), &moved)
	var refusal *front.Refusal
	if !errors.As(err, &refusal) || refusal.Code != front.CodeConfirmRequired || refusal.Params["action"] != "move" {
		t.Fatalf("err = %v", err)
	}
	// One confirmation names both halves.
	if refusal.Params["leave_port"] != 443 || refusal.Params["port"] != 8443 ||
		refusal.Params["routes"].([]string)[0] != "b.example.com" || refusal.Params["names"].([]string)[0] != "b.example.com" || refusal.Params["loopback_port"] == nil {
		t.Fatalf("params = %v", refusal.Params)
	}
	confirmation := &front.Confirmation{Confirmed: true}
	ctx := front.WithConfirmation(context.Background(), confirmation)
	change, err := svc.checkInboundAgainstFronts(ctx, &moved)
	if err != nil || change == nil || change.leave == nil || change.join == nil {
		t.Fatalf("change = %+v err = %v", change, err)
	}
	// The node is stored on its new port, then both are done.
	nodes[1].DesiredPort = 8443
	if err := svc.finishFront(ctx, nodes[1], change); err != nil {
		t.Fatal(err)
	}
	if confirmation.Left == nil || confirmation.Joined == nil || confirmation.Left.Port != 443 || confirmation.Joined.Port != 8443 {
		t.Fatalf("left = %+v joined = %+v", confirmation.Left, confirmation.Joined)
	}
	if a, b := fronts.rows[0], fronts.rows[1]; len(a.Listeners) != 1 || len(b.Listeners) != 2 || len(a.Routes) != 1 || len(b.Routes) != 2 {
		t.Fatalf("A = %+v B = %+v", a, b)
	}
	// Both fronts are sent, and no listener is dropped.
	plan, err := svc.frontSvc.PlanFor(context.Background(), 9, []string{"transit.sni.v1"}, domain.NodeCoreXray, domain.NodeCoreXray, []int64{2, 3, 4})
	if err != nil || len(plan.Fronts) != 2 || len(plan.Withheld) != 0 {
		t.Fatalf("plan = %+v err = %v", plan, err)
	}
	total := 0
	for _, f := range plan.Fronts {
		total += len(f.Listeners)
	}
	if total != 3 {
		t.Fatalf("listeners sent = %d, want all three", total)
	}
	unserved, err := svc.frontSvc.UnservedListeners(context.Background(), 9, plan, []int64{2, 3, 4})
	if err != nil || len(unserved) != 0 {
		t.Fatalf("unserved = %+v err = %v", unserved, err)
	}
}

// Only-shrinking operations stay possible when the server can no longer run
// fronts: otherwise a front with several listeners could never be emptied.
func TestShrinkingAFrontStillWorksWhenTheServerIsOnSingBox(t *testing.T) {
	// A listener leaves by a port change.
	svc, fronts, nodes := twoFronts(t, domain.NodeCoreSingBox)
	moved := *nodes[1]
	moved.DesiredPort = 7000
	ctx := front.WithConfirmation(context.Background(), &front.Confirmation{Confirmed: true})
	change, err := svc.checkInboundAgainstFronts(ctx, &moved)
	if err != nil || change == nil || change.leave == nil {
		t.Fatalf("port change: %+v %v", change, err)
	}
	if err := svc.finishFront(ctx, &moved, change); err != nil || len(fronts.rows[0].Listeners) != 1 {
		t.Fatalf("leave: %v %+v", err, fronts.rows[0])
	}
	// A node is deleted or detached (the same check and the same leave).
	svc, fronts, nodes = twoFronts(t, domain.NodeCoreSingBox)
	gone := nodes[1]
	change, err = svc.checkRemovalAgainstFronts(ctx, gone)
	if err != nil || change == nil || !change.removed {
		t.Fatalf("remove: %+v %v", change, err)
	}
	if err := svc.finishFront(ctx, gone, change); err != nil || len(fronts.rows[0].Listeners) != 1 || len(fronts.rows[0].Routes) != 1 {
		t.Fatalf("remove: %v %+v", err, fronts.rows[0])
	}
	// What is left behind it cannot be removed without deleting the front.
	if _, err := svc.checkRemovalAgainstFronts(ctx, nodes[0]); err == nil || !strings.Contains(err.Error(), "delete that shared port first") {
		t.Fatalf("emptying: %v", err)
	}
}
