package front

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// twoListeners is a server whose shared port 443 has node 1 behind it, with node
// 2 (also REALITY, its own name) about to land on the same port.
func twoListeners(t *testing.T) (*env, *domain.Node) {
	t.Helper()
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), realityNode(2, "us", 8443, "b.example.com"))
	if _, err := e.svc.Create(t.Context(), panelID, simpleInput()); err != nil {
		t.Fatal(err)
	}
	return e, e.nodes[1]
}

func TestASecondListenerJoinsASharedPortOnConfirmationAndIsRoutedByItsOwnName(t *testing.T) {
	e, second := twoListeners(t)
	second.DesiredPort = 443 // the save that moves it onto the shared port
	offer, err := e.svc.JoinOffer(t.Context(), second)
	if err != nil || offer == nil {
		t.Fatalf("offer = %+v err = %v", offer, err)
	}
	if offer.Port != 443 || len(offer.Names) != 1 || offer.Names[0] != "b.example.com" || offer.Loopback < 30000 {
		t.Fatalf("offer = %+v", offer)
	}
	refusal := offer.JoinRefusal(second)
	if refusal.Code != CodeConfirmRequired || !errors.Is(refusal, domain.ErrConflict) ||
		refusal.Params["front_id"] != offer.FrontID || refusal.Params["loopback_port"] != offer.Loopback {
		t.Fatalf("the confirmation must name the front, the SNI and the loopback port: %+v", refusal)
	}
	if err := e.svc.JoinCheck(t.Context(), second, offer, 0); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(e.fronts.rows[0].Listeners) != 1 {
		t.Fatal("the dry run wrote")
	}
	joined, err := e.svc.Join(t.Context(), second, offer, 30055)
	if err != nil || joined.LoopbackPort != 30055 || joined.NodeID != 2 {
		t.Fatalf("join = %+v err = %v", joined, err)
	}
	stored := e.fronts.rows[0]
	if len(stored.Listeners) != 2 || len(stored.Routes) != 2 {
		t.Fatalf("stored = %+v", stored)
	}
	// And the wire carries both, each routed by its own name.
	plan, err := e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1, 2})
	if err != nil || len(plan.Fronts) != 1 {
		t.Fatalf("plan = %+v err = %v", plan, err)
	}
	wire := plan.Fronts[0]
	if len(wire.Listeners) != 2 || len(wire.Routes) != 2 {
		t.Fatalf("wire = %+v", wire)
	}
	byName := map[string]nodeprotocol.ListenerKey{}
	for _, r := range wire.Routes {
		byName[r.ServerNames[0]] = r.Target.Listener
	}
	if byName["a.example.com"] != nodeprotocol.NewListenerKey(1) || byName["b.example.com"] != nodeprotocol.NewListenerKey(2) {
		t.Fatalf("routes = %v", byName)
	}
	body := nodeprotocol.Transit{Fronts: plan.Fronts}
	if err := nodeprotocol.ValidateTransit(&body); err != nil {
		t.Fatalf("contract: %v", err)
	}
}

func TestAListenerThatCannotJoinIsRefusedNamingTheFront(t *testing.T) {
	e, second := twoListeners(t)
	second.DesiredPort = 443
	second.StreamSettings = `{"network":"ws","security":"tls","tlsSettings":{"serverName":"b.example.com"}}`
	if _, err := e.svc.JoinOffer(t.Context(), second); refusalCode(t, err) != CodeNotFrontable {
		t.Fatalf("websocket: %v", err)
	}
	second.StreamSettings = `{"network":"tcp","security":"tls","tlsSettings":{"serverName":""}}`
	if _, err := e.svc.JoinOffer(t.Context(), second); refusalCode(t, err) != CodeNotFrontable {
		t.Fatalf("no name: %v", err)
	}
	// A name the front already routes is a conflict that names its owner.
	second.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["a.example.com"]}}`
	offer, err := e.svc.JoinOffer(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	err = e.svc.JoinCheck(t.Context(), second, offer, 0)
	var r *Refusal
	if !errors.As(err, &r) || r.Code != CodeDuplicateName || r.Params["owner"] == "" {
		t.Fatalf("duplicate name: %v", err)
	}
	// Not on the shared port: no offer.
	second.DesiredPort = 9443
	if offer, err := e.svc.JoinOffer(t.Context(), second); offer != nil || err != nil {
		t.Fatalf("a listener elsewhere was offered a front: %+v %v", offer, err)
	}
}

func TestADisabledListenerIsNotSentAndTheOthersKeepWorking(t *testing.T) {
	e, second := twoListeners(t)
	second.DesiredPort = 443
	offer, _ := e.svc.JoinOffer(t.Context(), second)
	if _, err := e.svc.Join(t.Context(), second, offer, 0); err != nil {
		t.Fatal(err)
	}
	// Disabling node 2 asks first and names the names that fall to the default.
	refusal, err := e.svc.DisableImpact(t.Context(), second)
	if err != nil || refusal == nil || refusal.Code != CodeConfirmRequired {
		t.Fatalf("disable impact = %+v %v", refusal, err)
	}
	if got := refusal.Params["routes"].([]string); len(got) != 1 || got[0] != "b.example.com" {
		t.Fatalf("routes = %v", refusal.Params)
	}
	second.Enabled = false
	plan, err := e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1, 2})
	if err != nil || len(plan.Fronts) != 1 {
		t.Fatalf("plan = %+v err = %v", plan, err)
	}
	wire := plan.Fronts[0]
	if len(wire.Listeners) != 1 || wire.Listeners[0].Listener != nodeprotocol.NewListenerKey(1) || len(wire.Routes) != 1 {
		t.Fatalf("the disabled listener must not be fronted and its route must go: %+v", wire)
	}
	if err := nodeprotocol.ValidateTransit(&nodeprotocol.Transit{Fronts: plan.Fronts}); err != nil {
		t.Fatal(err)
	}
	// With every listener disabled nothing is left to route: the front is
	// withheld and says why.
	e.nodes[0].Enabled = false
	plan, _ = e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1, 2})
	if len(plan.Fronts) != 0 || len(plan.Withheld) != 1 || plan.Withheld[0].Reason != WithheldNoRoutes {
		t.Fatalf("plan = %+v", plan)
	}
	// Enabling is never confirmed.
	e.nodes[0].Enabled = true
	if refusal, err := e.svc.DisableImpact(t.Context(), realityNode(9, "free", 7000, "z.example.com")); refusal != nil || err != nil {
		t.Fatalf("a node behind no front: %+v %v", refusal, err)
	}
}

func TestAnEnabledListenerOnTheSharedPortThatIsNotBehindItWithholdsTheFront(t *testing.T) {
	e, second := twoListeners(t)
	second.DesiredPort = 443 // stored on the port without having joined
	plan, err := e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1, 2})
	if err != nil || len(plan.Fronts) != 0 || len(plan.Withheld) != 1 || plan.Withheld[0].Reason != WithheldListenerOnPort {
		t.Fatalf("plan = %+v err = %v", plan, err)
	}
	d, err := e.svc.Diagnose(t.Context(), panelID)
	if err != nil || len(d.Withheld) != 1 || d.Inactive != "" {
		t.Fatalf("diagnosis = %+v err = %v", d, err)
	}
	// A listener Node cannot move withholds it too.
	e, _ = twoListeners(t)
	e.nodes[0].StreamSettings = `{"network":"ws","security":"tls"}`
	plan, _ = e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1, 2})
	if len(plan.Fronts) != 0 || plan.Withheld[0].Reason != WithheldListenerUnfit {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestRouteNamesMustBeWhatTheTargetsClientsSend(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), forwardLanding(443))
	in := simpleInput()
	in.Routes[0].ServerNames = []string{"other.example.com"}
	err := mustFail(e.svc.Create(t.Context(), panelID, in))
	var r *Refusal
	if !errors.As(err, &r) || r.Code != CodeNameNotServed || r.Params["name"] != "other.example.com" {
		t.Fatalf("listener name: %v", err)
	}
	in = simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"not-the-landing.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "a"}})
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeNameNotServed {
		t.Fatalf("landing name: %s", code)
	}
}

func TestOnlyATLSOverTCPLandingCanBeRouted(t *testing.T) {
	landing := forwardLanding(443)
	landing.DesiredProtocol = "hysteria2"
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), landing)
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "a"}})
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeLandingUnfit {
		t.Fatalf("udp landing: %s", code)
	}
	landing.DesiredProtocol = "shadowsocks"
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeLandingUnfit {
		t.Fatalf("ss landing: %s", code)
	}
	// And one that became unfit after saving is dropped, never forced to TCP.
	landing.DesiredProtocol = "vless"
	e = newEnv(realityNode(1, "hk", 443, "a.example.com"), landing)
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatal(err)
	}
	landing.DesiredProtocol = "tuic"
	plan, _ := e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1})
	if len(plan.Forwards) != 0 || len(plan.Fronts) != 1 || len(plan.Fronts[0].Routes) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestNodeReservedPortsAreNeverALoopbackOrASharedPort(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	in := simpleInput()
	in.Listeners[0].LoopbackPort = 10085
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeLoopbackBad {
		t.Fatalf("loopback: %s", code)
	}
	in = simpleInput()
	in.Port = 10085
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeBadPort {
		t.Fatalf("shared: %s", code)
	}
	usage, _ := e.svc.Usage(t.Context(), panelID)
	found := false
	for _, u := range usage {
		found = found || (u.Kind == KindReserved && u.Port == 10085)
	}
	if !found {
		t.Fatal("the port-usage view must list Node's core API port")
	}
}

func TestFrontsNeedTheAgentToReportXrayRunning(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	e.agent.ObservedCoreEngine = domain.NodeCoreSingBox
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, simpleInput()))); code != CodeNotRunningXray {
		t.Fatalf("code = %s", code)
	}
	e.agent.ObservedCoreEngine = domain.NodeCoreXray
	if _, err := e.svc.Create(t.Context(), panelID, simpleInput()); err != nil {
		t.Fatal(err)
	}
	// Saved fronts show as inactive, with the reason, once the engine is not Xray.
	e.agent.DesiredCoreEngine = domain.NodeCoreSingBox
	d, _ := e.svc.Diagnose(t.Context(), panelID)
	if d.Inactive != CodeNotXray {
		t.Fatalf("diagnosis = %+v", d)
	}
	usage, _ := e.svc.Usage(t.Context(), panelID)
	for _, u := range usage {
		if u.Kind == KindFront && u.Inactive != CodeNotXray {
			t.Fatalf("front entry = %+v", u)
		}
	}
	impact, err := e.svc.EngineImpact(t.Context(), panelID)
	if err != nil || impact == nil || len(impact.Listeners) != 1 || impact.Ports[0] != 443 {
		t.Fatalf("impact = %+v %v", impact, err)
	}
	refusal, _ := e.svc.EngineChangeRefusal(t.Context(), panelID)
	if refusal == nil || refusal.Code != CodeConfirmRequired {
		t.Fatalf("refusal = %+v", refusal)
	}
}

func TestChangingThePortOrDroppingARouteAsksFirst(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), forwardLanding(443))
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "a"}})
	saved, err := e.svc.Create(t.Context(), panelID, in)
	if err != nil {
		t.Fatal(err)
	}
	// Dropping the relay line's route stops serving the line.
	drop := in
	drop.ConfirmMoveListeners = false
	drop.Routes = in.Routes[:1]
	err = mustFail(e.svc.Update(t.Context(), panelID, saved.Front.ID, drop))
	var r *Refusal
	if !errors.As(err, &r) || r.Code != CodeConfirmRequired || len(r.Params["leaving_lines"].([]string)) != 1 {
		t.Fatalf("drop: %v", err)
	}
	drop.ConfirmMoveListeners = true
	if _, err := e.svc.Update(t.Context(), panelID, saved.Front.ID, drop); err != nil {
		t.Fatalf("confirmed drop: %v", err)
	}
}

func TestADefaultNamedLikeTheServerIsRefusedEvenWhenDNSFails(t *testing.T) {
	n := realityNode(1, "hk", 443, "a.example.com")
	n.ServerAddress = "Node.Example.Org."
	e := newEnv(n)
	in := simpleInput()
	in.Default = external("node.example.org", 443) // not in the resolver: DNS says nothing
	err := mustFail(e.svc.Create(t.Context(), panelID, in))
	if code := refusalCode(t, err); code != CodeDefaultIsSelf {
		t.Fatalf("code = %s", code)
	}
	in.Default = external("node.example.org", 8443)
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatalf("another port: %v", err)
	}
}

type slowResolver struct{ calls atomic.Int32 }

func (r *slowResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	r.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDNSIsResolvedInParallelUnderOneBudget(t *testing.T) {
	old := dnsBudget
	dnsBudget = 150 * time.Millisecond
	defer func() { dnsBudget = old }()
	r := &slowResolver{}
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"))
	e.svc.resolver = r
	names := []string{"a1.example.com", "a2.example.com", "a3.example.com", "a4.example.com", "a5.example.com"}
	started := time.Now()
	out := e.svc.resolveAll(t.Context(), names)
	if elapsed := time.Since(started); elapsed > 600*time.Millisecond {
		t.Fatalf("five names took %s: they were resolved one after another", elapsed)
	}
	if len(out) != 0 || r.calls.Load() != 5 {
		t.Fatalf("out = %v calls = %d", out, r.calls.Load())
	}
}

func TestARefusedSaveWritesNothingNotEvenForwardRows(t *testing.T) {
	e := newEnv(realityNode(1, "hk", 443, "a.example.com"), forwardLanding(443))
	in := simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "a"}})
	in.Default = external("198.51.100.10", 443) // refused: this server itself
	if err := mustFail(e.svc.Create(t.Context(), panelID, in)); err == nil {
		t.Fatal("expected a refusal")
	}
	in.Default = external("camouflage.example", 443)
	in.Listeners[0].LoopbackPort = 31500 // refused later than the routes
	_ = mustFail(e.svc.Create(t.Context(), panelID, in))
	if e.ensured != 0 || len(e.fronts.rows) != 0 {
		t.Fatalf("a refused save wrote: forward rows %d, fronts %d", e.ensured, len(e.fronts.rows))
	}
	in.Listeners[0].LoopbackPort = 30001
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatal(err)
	}
	if e.ensured != 0 {
		t.Fatalf("even an accepted save mints no forward row (the sync does): %d", e.ensured)
	}
	if _, err := e.svc.PlanFor(t.Context(), panelID, sniCaps, domain.NodeCoreXray, domain.NodeCoreXray, []int64{1}); err != nil || e.ensured != 1 {
		t.Fatalf("the sync mints it: %d %v", e.ensured, err)
	}
}

func TestSavesOfOneServerAreSerialized(t *testing.T) {
	e := newEnv()
	unlock := e.svc.Lock(panelID)
	var got atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer e.svc.Lock(panelID)()
		got.Store(true)
	}()
	time.Sleep(50 * time.Millisecond)
	if got.Load() {
		t.Fatal("a second save of the same server ran while the first held the lock")
	}
	other := e.svc.Lock(panelID + 1) // another server never waits
	other()
	unlock()
	<-done
	if !got.Load() {
		t.Fatal("the waiting save never ran")
	}
}

// threeBehind: two listeners behind the shared port 443, joined as an admin would.
func twoBehind(t *testing.T) *env {
	t.Helper()
	e, second := twoListeners(t)
	second.DesiredPort = 443
	offer, err := e.svc.JoinOffer(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Join(t.Context(), second, offer, 0); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAFrontWithTwoListenersCannotBeDeletedMovedOrLeftBehindWithXray(t *testing.T) {
	e := twoBehind(t)
	id := e.fronts.rows[0].ID
	check := func(what string, err error) {
		t.Helper()
		var r *Refusal
		if !errors.As(err, &r) || r.Code != CodeListenersShare || errors.Is(err, domain.ErrConflict) {
			t.Fatalf("%s: want a refusal (not a confirmation) naming the listeners, got %v", what, err)
		}
		if got := r.Params["nodes"].([]string); len(got) != 2 {
			t.Fatalf("%s: the refusal must name both listeners: %v", what, got)
		}
	}
	_, err := e.svc.Delete(t.Context(), panelID, id, true)
	check("delete", err)
	in := simpleInput()
	in.Listeners = append(in.Listeners, domain.FrontListener{NodeID: 2, LoopbackPort: 30002})
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"b.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 2}})
	in.Port = 8443
	_, err = e.svc.Update(t.Context(), panelID, id, in)
	check("port change", err)
	e.agent.DesiredCoreEngine = domain.NodeCoreSingBox
	refusal, err := e.svc.EngineChangeRefusal(t.Context(), panelID)
	if err != nil || refusal == nil || refusal.Code != CodeListenersShare {
		t.Fatalf("leaving Xray: %+v %v", refusal, err)
	}
}

func TestAListenerCannotBeTakenOutOfAFrontWhileItStillSitsOnItsPort(t *testing.T) {
	e := twoBehind(t)
	in := simpleInput() // only node 1 behind it; node 2 is still on 443
	in.ConfirmMoveListeners = true
	err := mustFail(e.svc.Update(t.Context(), panelID, e.fronts.rows[0].ID, in))
	var r *Refusal
	if !errors.As(err, &r) || r.Code != CodeListenerStillOnPort {
		t.Fatalf("err = %v", err)
	}
}

func TestAListenerLeavesAFrontWhenItsPortChangesAndTheRoutesGo(t *testing.T) {
	e := twoBehind(t)
	second := e.nodes[1]
	second.DesiredPort = 8443 // the save that moves it off the shared port
	offer, err := e.svc.LeaveOffer(t.Context(), second, false)
	if err != nil || offer == nil || len(offer.Routes) != 1 || offer.Routes[0] != "b.example.com" {
		t.Fatalf("offer = %+v err = %v", offer, err)
	}
	if refusal := offer.LeaveRefusal(second, false); refusal.Code != CodeConfirmRequired || refusal.Params["action"] != "leave" {
		t.Fatalf("refusal = %+v", refusal)
	}
	if err := e.svc.LeaveCheck(t.Context(), second, offer, false); err != nil {
		t.Fatal(err)
	}
	if len(e.fronts.rows[0].Listeners) != 2 {
		t.Fatal("the check wrote")
	}
	left, err := e.svc.Leave(t.Context(), second, offer, false)
	if err != nil || left.NodeID != 2 {
		t.Fatalf("left = %+v err = %v", left, err)
	}
	row := e.fronts.rows[0]
	if len(row.Listeners) != 1 || len(row.Routes) != 1 || row.Routes[0].ServerNames[0] != "a.example.com" {
		t.Fatalf("row = %+v", row)
	}
	// A node that is only deleted leaves too, through the removed form, and the last
	// thing behind a front cannot leave it empty.
	e = twoBehind(t)
	gone := e.nodes[1]
	offer, err = e.svc.LeaveOffer(t.Context(), gone, true)
	if err != nil || offer == nil {
		t.Fatalf("delete offer = %+v err = %v", offer, err)
	}
	if _, err := e.svc.Leave(t.Context(), gone, offer, true); err != nil || len(e.fronts.rows[0].Listeners) != 1 {
		t.Fatalf("leave on delete: %v %+v", err, e.fronts.rows[0])
	}
	only := e.nodes[0]
	if _, err := e.svc.LeaveOffer(t.Context(), only, true); refusalCode(t, err) != CodeLeaveEmptiesFront {
		t.Fatalf("emptying a front: %v", err)
	}
}

func TestRoutesMustIncludeTheSNIASubscriptionRenders(t *testing.T) {
	// The subscription renders the FIRST serverName as written, not the least.
	node := realityNode(1, "hk", 443, "z.example.com", "a.example.com")
	e := newEnv(node)
	in := simpleInput()
	in.Routes[0].ServerNames = []string{"a.example.com"} // served, but not what clients send
	err := mustFail(e.svc.Create(t.Context(), panelID, in))
	var r *Refusal
	if !errors.As(err, &r) || r.Code != CodeRenderedNameMissing || r.Params["name"] != "z.example.com" {
		t.Fatalf("err = %v", err)
	}
	in.Routes[0].ServerNames = []string{"z.example.com", "a.example.com"} // extra names are fine
	if _, err := e.svc.Create(t.Context(), panelID, in); err != nil {
		t.Fatalf("with the rendered name and an extra: %v", err)
	}
	// A relay line's route must include the landing's rendered name too.
	landing := forwardLanding(443)
	landing.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["l2.example.com","land.example.com"]}}`
	e = newEnv(realityNode(1, "hk", 443, "a.example.com"), landing)
	in = simpleInput()
	in.Routes = append(in.Routes, domain.FrontRoute{ServerNames: []string{"land.example.com"},
		Target: domain.FrontTarget{Kind: domain.FrontTargetForward, NodeID: 5, LineID: "a"}})
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, in))); code != CodeRenderedNameMissing {
		t.Fatalf("landing: %s", code)
	}
}

func TestStreamSettingsAreReadWithExactKeys(t *testing.T) {
	// A case-variant duplicate key is a different key to Node; reading it as the
	// same would make the panel accept what Node refuses.
	n := &domain.Node{DesiredProtocol: "vless", StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["a.example.com"]},"sockopt":{"AcceptProxyProtocol":true}}`}
	if f := domain.FrontableOf(n); !f.OK {
		t.Fatalf("a different key is no setting: %+v", f)
	}
	n.StreamSettings = `{"network":"tcp","security":"reality","RealitySettings":{"serverNames":["a.example.com"]}}`
	if f := domain.FrontableOf(n); f.OK || f.Reason != domain.FrontRefusalNoName {
		t.Fatalf("a mis-cased settings object has no names: %+v", f)
	}
	n.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["a.example.com"]},"sockopt":{"acceptProxyProtocol":true}}`
	if f := domain.FrontableOf(n); f.OK || f.Reason != domain.FrontRefusalProxyProtocol {
		t.Fatalf("PROXY: %+v", f)
	}
}

func TestAJoinChecksItsDefaultNoSecondTime(t *testing.T) {
	e, second := twoListeners(t)
	second.DesiredPort = 443
	offer, _ := e.svc.JoinOffer(t.Context(), second)
	var lookups atomic.Int32
	e.svc.resolver = countingResolver{fakeResolver{"camouflage.example": {"203.0.113.50"}}, &lookups}
	if err := e.svc.JoinCheck(t.Context(), second, offer, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Join(t.Context(), second, offer, 0); err != nil {
		t.Fatal(err)
	}
	if lookups.Load() != 0 {
		t.Fatalf("the join resolved names %d times: the stored default is not checked again", lookups.Load())
	}
}

type countingResolver struct {
	inner fakeResolver
	n     *atomic.Int32
}

func (c countingResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	c.n.Add(1)
	return c.inner.LookupHost(ctx, host)
}

func TestShrinkingStillWorksOnASingBoxServerButAddingDoesNot(t *testing.T) {
	e := twoBehind(t)
	e.agent.DesiredCoreEngine = domain.NodeCoreSingBox
	e.agent.ObservedCoreEngine = domain.NodeCoreSingBox
	second := e.nodes[1]
	second.DesiredPort = 8443
	offer, err := e.svc.LeaveOffer(t.Context(), second, false)
	if err != nil || offer == nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Leave(t.Context(), second, offer, false); err != nil {
		t.Fatalf("leave on sing-box: %v", err)
	}
	// An edit that only removes routes goes through; one that adds does not.
	in := simpleInput()
	in.Routes[0].ServerNames = []string{"a.example.com"}
	in.ConfirmMoveListeners = true
	if _, err := e.svc.Update(t.Context(), panelID, e.fronts.rows[0].ID, in); err != nil {
		t.Fatalf("an edit that changes nothing: %v", err)
	}
	in.Default = external("other.example.com", 443)
	if code := refusalCode(t, mustFail(e.svc.Update(t.Context(), panelID, e.fronts.rows[0].ID, in))); code != CodeNotXray {
		t.Fatalf("changing the default is not shrinking: %s", code)
	}
	if code := refusalCode(t, mustFail(e.svc.Create(t.Context(), panelID, simpleInput()))); code != CodeNotXray {
		t.Fatalf("create: %s", code)
	}
}

func TestEnablingAMemberOfAFrontThatIsNotSentAsksFirst(t *testing.T) {
	e := twoBehind(t)
	first := e.nodes[0]
	first.Enabled = false
	// The front is being sent: enabling is just enabling.
	if refusal, err := e.svc.EnableRefusal(t.Context(), first); refusal != nil || err != nil {
		t.Fatalf("a sent front: %+v %v", refusal, err)
	}
	e.agent.DesiredCoreEngine = domain.NodeCoreSingBox
	refusal, err := e.svc.EnableRefusal(t.Context(), first)
	if err != nil || refusal == nil || refusal.Code != CodeConfirmRequired || refusal.Params["action"] != "enable" ||
		refusal.Params["reason"] != CodeNotXray || len(refusal.Params["nodes"].([]string)) != 2 {
		t.Fatalf("refusal = %+v err = %v", refusal, err)
	}
	// Alone on the port, enabling it is harmless.
	e.nodes[1].Enabled = false
	if refusal, _ := e.svc.EnableRefusal(t.Context(), first); refusal != nil {
		t.Fatalf("alone: %+v", refusal)
	}
}

func TestMoveRefusalNamesBothHalves(t *testing.T) {
	n := realityNode(3, "n3", 8443, "b.example.com")
	r := MoveRefusal(n, &LeaveOffer{FrontID: 1, Port: 443, Routes: []string{"b.example.com"}}, &JoinOffer{FrontID: 2, Port: 8443, Names: []string{"b.example.com"}, Loopback: 30010})
	if r.Code != CodeConfirmRequired || r.Params["action"] != "move" || r.Params["leave_port"] != 443 || r.Params["loopback_port"] != 30010 {
		t.Fatalf("%+v", r)
	}
}
