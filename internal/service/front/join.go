package front

import (
	"context"
	"fmt"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Impact is what a change does to the people and lines on a server, named so a
// confirmation says it.
type Impact struct {
	// Ports are the shared ports involved.
	Ports []int
	// Moved are listeners about to be moved behind a front.
	Moved []string
	// Listeners are listeners that go back to their own public port.
	Listeners []string
	// Lines are relay lines that stop being served.
	Lines []string
	// Tunnels change their public port or stop until the front is restored.
	Tunnels []string
	// Routes are the names that fall to the default site.
	Routes []string
}

// Empty reports whether nothing is affected.
func (i Impact) Empty() bool {
	return len(i.Moved)+len(i.Listeners)+len(i.Lines)+len(i.Tunnels)+len(i.Routes) == 0
}

func (i Impact) params() map[string]any {
	out := map[string]any{"ports": i.Ports}
	if len(i.Ports) > 0 {
		out["port"] = i.Ports[0]
	}
	for key, values := range map[string][]string{"nodes": i.Moved, "leaving_listeners": i.Listeners, "leaving_lines": i.Lines, "tunnels": i.Tunnels, "routes": i.Routes} {
		if len(values) > 0 {
			out[key] = values
		}
	}
	return out
}

func (i Impact) text() string {
	var parts []string
	if len(i.Moved) > 0 {
		parts = append(parts, "moves listeners "+strings.Join(i.Moved, ", ")+" to loopback ports")
	}
	if len(i.Listeners) > 0 {
		parts = append(parts, "returns listeners "+strings.Join(i.Listeners, ", ")+" to their own ports")
	}
	if len(i.Lines) > 0 {
		parts = append(parts, "stops serving relay lines "+strings.Join(i.Lines, ", "))
	}
	if len(i.Tunnels) > 0 {
		parts = append(parts, "changes the availability or port of "+strings.Join(i.Tunnels, ", "))
	}
	if len(i.Routes) > 0 {
		parts = append(parts, "sends the server names "+strings.Join(i.Routes, ", ")+" to the default site")
	}
	return strings.Join(parts, "; ")
}

// confirmRefusal is the 409 that asks for an explicit go-ahead and names what
// the change affects. what says what is being done ("deleting the shared port
// %d"); the first port in the impact fills it.
func confirmRefusal(what string, impact Impact, action string) *Refusal {
	port := 0
	if len(impact.Ports) > 0 {
		port = impact.Ports[0]
	}
	params := impact.params()
	params["action"] = action
	return refuse(CodeConfirmRequired, what+" "+impact.text()+"; the users of those listeners reconnect once. Confirm to continue.",
		params, port)
}

// impactOf is what removing a front does to the listeners and lines behind it.
func impactOf(f domain.TransitFront, nodes []*domain.Node) Impact {
	byID := map[int64]*domain.Node{}
	for _, n := range nodes {
		if n != nil {
			byID[n.ID] = n
		}
	}
	impact := Impact{Ports: []int{f.Port}}
	for _, item := range f.Listeners {
		impact.Listeners = append(impact.Listeners, nodeLabel(byID[item.NodeID]))
	}
	for _, target := range f.Targets() {
		if target.Kind == domain.FrontTargetForward {
			impact.Lines = append(impact.Lines, lineLabel(byID[target.NodeID], target.LineID))
		} else if target.Kind == domain.FrontTargetLink {
			impact.Tunnels = append(impact.Tunnels, TargetLabel(target, byID))
		}
	}
	return impact
}

// EngineImpact is what moving a server off Xray does to its saved fronts: they
// stay saved but stop being sent, so their listeners go back to their own ports
// and their relay lines stop being served. nil when it has none.
func (s *Service) EngineImpact(ctx context.Context, panelID int64) (*Impact, error) {
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil || len(fronts) == 0 {
		return nil, err
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	var total Impact
	for _, f := range fronts {
		part := impactOf(f, nodes)
		total.Ports = append(total.Ports, f.Port)
		total.Listeners = append(total.Listeners, part.Listeners...)
		total.Lines = append(total.Lines, part.Lines...)
		total.Tunnels = append(total.Tunnels, part.Tunnels...)
	}
	return &total, nil
}

// EngineChangeRefusal is the 409 for leaving Xray with fronts saved, or nil.
func (s *Service) EngineChangeRefusal(ctx context.Context, panelID int64) (*Refusal, error) {
	impact, err := s.EngineImpact(ctx, panelID)
	if err != nil || impact == nil {
		return nil, err
	}
	// Fronts that are not sent send their listeners to their public port; two on
	// one port would be refused by Node, so that is not a confirmation but a
	// refusal until they are apart.
	fronts, err := s.fronts.ListByPanel(ctx, panelID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range fronts {
		if sharing := sharingListeners(f, nodes); len(sharing) >= 2 {
			return shareRefusal(f.Port, sharing, "leave Xray with"), nil
		}
	}
	return confirmRefusal(fmt.Sprintf("leaving Xray stops sending the shared ports %v:", impact.Ports), *impact, "engine"), nil
}

// DisableImpact is what disabling a fronted listener does: the names routed to
// it fall to the default site. nil when the node is not behind a front.
func (s *Service) DisableImpact(ctx context.Context, node *domain.Node) (*Refusal, error) {
	fronts, err := s.fronts.ListByPanel(ctx, node.PanelID)
	if err != nil {
		return nil, err
	}
	var impact Impact
	for _, f := range fronts {
		if !f.HasListener(node.ID) {
			continue
		}
		impact.Ports = append(impact.Ports, f.Port)
		for _, route := range f.Routes {
			if route.Target.Kind == domain.FrontTargetListener && route.Target.NodeID == node.ID {
				impact.Routes = append(impact.Routes, route.ServerNames...)
			}
		}
	}
	if len(impact.Ports) == 0 {
		return nil, nil
	}
	return confirmRefusal(fmt.Sprintf("disabling node %s, which is behind the shared port %d,", nodeLabel(node), impact.Ports[0]), impact, "disable"), nil
}

// ---- Joining a front ----

// Confirmation is what a request carries to say the administrator has seen what
// a change does and wants it, and a place for the service to report what it did.
type Confirmation struct {
	// Confirmed is the go-ahead.
	Confirmed bool
	// LoopbackPort is the loopback port to give a listener that joins a front;
	// 0 uses the one the offer suggested.
	LoopbackPort int
	// Joined is filled in when a listener was added to a front, Left when one was
	// taken out, for the audit.
	Joined *Joined
	Left   *Joined
}

// Joined is a listener that was added to a front.
type Joined struct {
	PanelID      int64
	FrontID      int64
	Port         int
	NodeID       int64
	ServerNames  []string
	LoopbackPort int
}

type confirmationKey struct{}

// WithConfirmation puts a request's confirmation in its context.
func WithConfirmation(ctx context.Context, c *Confirmation) context.Context {
	return context.WithValue(ctx, confirmationKey{}, c)
}

// ConfirmationFrom reads it; a request without one has not confirmed anything.
func ConfirmationFrom(ctx context.Context) *Confirmation {
	if c, _ := ctx.Value(confirmationKey{}).(*Confirmation); c != nil {
		return c
	}
	return &Confirmation{}
}

// JoinOffer is a front a listener would be added to, with what the addition
// would be: the listener's own server names as the route, and a loopback port.
type JoinOffer struct {
	FrontID  int64
	Port     int
	Names    []string
	Loopback int
}

// JoinOffer finds the front a node's listener would have to join: one on its
// server whose port is the node's port and that does not hold it yet. nil when
// there is none. A listener that cannot join (no SNI, or a transport a front
// cannot carry) is refused naming the front.
func (s *Service) JoinOffer(ctx context.Context, node *domain.Node) (*JoinOffer, error) {
	if node == nil || node.PanelID == 0 || node.DesiredPort <= 0 || node.IsSeparator() {
		return nil, nil
	}
	if _, err := s.panels.GetByID(ctx, node.PanelID); err != nil {
		return nil, nil // the server is gone: nothing shares anything there
	}
	fronts, err := s.fronts.ListByPanel(ctx, node.PanelID)
	if err != nil {
		return nil, err
	}
	for _, f := range fronts {
		if f.Port != node.DesiredPort || (node.ID != 0 && f.HasListener(node.ID)) {
			continue
		}
		fr := domain.FrontableOf(node)
		if !fr.OK {
			return nil, refuse(CodeNotFrontable,
				"port %d is shared: node %s cannot join it, because only a TCP TLS or REALITY listener without a PROXY header of its own can (reason: %s)",
				map[string]any{"node": node.DisplayName, "port": f.Port, "reason": fr.Reason, "protocol": node.DesiredProtocol}, f.Port, nodeLabel(node), fr.Reason)
		}
		loopback, err := s.SuggestLoopbackPort(ctx, node.PanelID)
		if err != nil {
			return nil, err
		}
		return &JoinOffer{FrontID: f.ID, Port: f.Port, Names: fr.ServerNames, Loopback: loopback}, nil
	}
	return nil, nil
}

// JoinRefusal is the 409 that asks before a listener joins a shared port,
// naming the front and the SNI it will be routed by.
func (o *JoinOffer) JoinRefusal(node *domain.Node) *Refusal {
	return refuse(CodeConfirmRequired,
		"port %d is a shared port: node %s would join it, moved to loopback port %d and routed by %s; its users reconnect once. Confirm to continue.",
		map[string]any{"action": "join", "port": o.Port, "front_id": o.FrontID, "node": node.DisplayName, "nodes": []string{nodeLabel(node)},
			"names": o.Names, "loopback_port": o.Loopback},
		o.Port, nodeLabel(node), o.Loopback, strings.Join(o.Names, ", "))
}

// JoinCheck validates a join for a node that is about to be saved, writing
// nothing. The caller holds Lock(panelID).
func (s *Service) JoinCheck(ctx context.Context, node *domain.Node, offer *JoinOffer, loopback int) error {
	_, err := s.joinLocked(ctx, node, offer, loopback, true)
	return err
}

// Join adds the listener to the front, routed by its own server names: the
// same operation as saving the front by hand, audited by the caller from the
// returned Joined. The node must be stored. The caller holds Lock(panelID).
func (s *Service) Join(ctx context.Context, node *domain.Node, offer *JoinOffer, loopback int) (*Joined, error) {
	return s.joinLocked(ctx, node, offer, loopback, false)
}

func (s *Service) joinLocked(ctx context.Context, node *domain.Node, offer *JoinOffer, loopback int, dry bool) (*Joined, error) {
	existing, err := s.fronts.Get(ctx, offer.FrontID)
	if err != nil {
		return nil, err
	}
	if loopback == 0 {
		loopback = offer.Loopback
	}
	id := node.ID
	if id == 0 {
		id = pendingNodeID
	}
	in := Input{
		Port: existing.Port, Default: existing.Default, ConfirmMoveListeners: true,
		Listeners: append(append([]domain.FrontListener(nil), existing.Listeners...), domain.FrontListener{NodeID: id, LoopbackPort: loopback}),
		Routes: append(append([]domain.FrontRoute(nil), existing.Routes...), domain.FrontRoute{
			ServerNames: offer.Names, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: id},
		}),
	}
	if _, err := s.save(ctx, node.PanelID, &existing, in, saveOpts{dry: dry, candidate: node, keepDefault: true}); err != nil {
		return nil, err
	}
	return &Joined{PanelID: node.PanelID, FrontID: existing.ID, Port: existing.Port, NodeID: node.ID, ServerNames: offer.Names, LoopbackPort: loopback}, nil
}

// ---- Leaving a front ----

// LeaveOffer is the front a listener is taken out of, with the names that then
// go to the default site.
type LeaveOffer struct {
	FrontID int64
	Port    int
	Routes  []string
}

// LeaveOffer finds the front a node's listener would leave: one that holds it
// while the node's own port is no longer the front's (or the node is going
// away). nil when it is behind none. Taking out the last route is refused: the
// front would share nothing.
func (s *Service) LeaveOffer(ctx context.Context, node *domain.Node, removed bool) (*LeaveOffer, error) {
	if node == nil || node.ID == 0 || node.PanelID == 0 {
		return nil, nil
	}
	if _, err := s.panels.GetByID(ctx, node.PanelID); err != nil {
		return nil, nil
	}
	fronts, err := s.fronts.ListByPanel(ctx, node.PanelID)
	if err != nil {
		return nil, err
	}
	for _, f := range fronts {
		if !f.HasListener(node.ID) || (!removed && node.DesiredPort == f.Port) {
			continue
		}
		offer := &LeaveOffer{FrontID: f.ID, Port: f.Port}
		remaining := 0
		for _, route := range f.Routes {
			if route.Target.Kind == domain.FrontTargetListener && route.Target.NodeID == node.ID {
				offer.Routes = append(offer.Routes, route.ServerNames...)
			} else {
				remaining++
			}
		}
		if remaining == 0 {
			return nil, refuse(CodeLeaveEmptiesFront,
				"node %s is the only thing behind the shared port %d: delete that shared port first",
				map[string]any{"node": node.DisplayName, "port": f.Port}, nodeLabel(node), f.Port)
		}
		return offer, nil
	}
	return nil, nil
}

// LeaveRefusal is the 409 that asks before a listener leaves a shared port.
func (o *LeaveOffer) LeaveRefusal(node *domain.Node, removed bool) *Refusal {
	why := "moving to its own port"
	if removed {
		why = "being deleted"
	}
	return refuse(CodeConfirmRequired,
		"node %s is behind the shared port %d and is %s: it leaves that front, and %s go to the default site. Confirm to continue.",
		map[string]any{"action": "leave", "port": o.Port, "front_id": o.FrontID, "node": node.DisplayName, "nodes": []string{nodeLabel(node)}, "routes": o.Routes},
		nodeLabel(node), o.Port, why, strings.Join(o.Routes, ", "))
}

// LeaveCheck validates a leave without writing. The caller holds Lock.
func (s *Service) LeaveCheck(ctx context.Context, node *domain.Node, offer *LeaveOffer, removed bool) error {
	_, err := s.leaveLocked(ctx, node, offer, removed, true)
	return err
}

// Leave takes the listener out of the front, with its routes, in the same
// operation as the save or delete that makes it leave. The caller holds Lock.
func (s *Service) Leave(ctx context.Context, node *domain.Node, offer *LeaveOffer, removed bool) (*Joined, error) {
	return s.leaveLocked(ctx, node, offer, removed, false)
}

func (s *Service) leaveLocked(ctx context.Context, node *domain.Node, offer *LeaveOffer, removed, dry bool) (*Joined, error) {
	existing, err := s.fronts.Get(ctx, offer.FrontID)
	if err != nil {
		return nil, err
	}
	in := Input{Port: existing.Port, Default: existing.Default, ConfirmMoveListeners: true}
	for _, item := range existing.Listeners {
		if item.NodeID != node.ID {
			in.Listeners = append(in.Listeners, item)
		}
	}
	for _, route := range existing.Routes {
		if route.Target.Kind == domain.FrontTargetListener && route.Target.NodeID == node.ID {
			continue
		}
		in.Routes = append(in.Routes, route)
	}
	opts := saveOpts{dry: dry, keepDefault: true, shrink: true}
	if removed {
		opts.omit = node.ID
	} else {
		opts.candidate = node
	}
	if _, err := s.save(ctx, node.PanelID, &existing, in, opts); err != nil {
		return nil, err
	}
	return &Joined{PanelID: node.PanelID, FrontID: existing.ID, Port: existing.Port, NodeID: node.ID, ServerNames: offer.Routes}, nil
}

// MoveRefusal asks once for a listener that moves from one shared port onto
// another: it leaves the first front (its routes go to that default) and joins
// the second (its SNI and loopback port). With only one of the two it is that
// one's refusal.
func MoveRefusal(node *domain.Node, leave *LeaveOffer, join *JoinOffer) *Refusal {
	switch {
	case leave == nil:
		return join.JoinRefusal(node)
	case join == nil:
		return leave.LeaveRefusal(node, false)
	}
	return refuse(CodeConfirmRequired,
		"node %s leaves the shared port %d (%s then go to that port's default site) and joins the shared port %d, moved to loopback port %d and routed by %s; its users reconnect once. Confirm to continue.",
		map[string]any{"action": "move", "node": node.DisplayName, "nodes": []string{nodeLabel(node)},
			"leave_port": leave.Port, "leave_front_id": leave.FrontID, "routes": leave.Routes,
			"port": join.Port, "front_id": join.FrontID, "names": join.Names, "loopback_port": join.Loopback},
		nodeLabel(node), leave.Port, strings.Join(leave.Routes, ", "), join.Port, join.Loopback, strings.Join(join.Names, ", "))
}

// EnableRefusal asks before a disabled member of a front is enabled while the
// front is not being sent and another enabled listener is on its port: both
// would stay down (Node refuses two listeners on one port). nil when enabling
// changes nothing of that kind.
func (s *Service) EnableRefusal(ctx context.Context, node *domain.Node) (*Refusal, error) {
	fronts, err := s.fronts.ListByPanel(ctx, node.PanelID)
	if err != nil || len(fronts) == 0 {
		return nil, err
	}
	var front *domain.TransitFront
	for i := range fronts {
		if fronts[i].HasListener(node.ID) && fronts[i].Port == node.DesiredPort {
			front = &fronts[i]
		}
	}
	if front == nil {
		return nil, nil
	}
	diagnosis, err := s.Diagnose(ctx, node.PanelID)
	if err != nil {
		return nil, err
	}
	reason := diagnosis.Inactive
	for _, w := range diagnosis.Withheld {
		if w.FrontID == front.ID && reason == "" {
			reason = w.Reason
		}
	}
	if reason == "" {
		return nil, nil // the front is being sent: enabling is just enabling
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	var others []string
	for _, n := range nodes {
		if n != nil && n.ID != node.ID && n.PanelID == node.PanelID && !n.IsSeparator() && n.Enabled && n.DesiredPort == front.Port {
			others = append(others, nodeLabel(n))
		}
	}
	if len(others) == 0 {
		return nil, nil
	}
	return refuse(CodeConfirmRequired,
		"the shared port %d is not being sent (%s): enabling node %s puts it on the same port as %s, so all of them stay down until that is fixed. Confirm to continue.",
		map[string]any{"action": "enable", "port": front.Port, "reason": reason, "node": node.DisplayName, "nodes": append([]string{nodeLabel(node)}, others...)},
		front.Port, reason, nodeLabel(node), strings.Join(others, ", ")), nil
}
