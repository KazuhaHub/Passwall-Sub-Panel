package route

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// world is an in-memory fleet the service reads, with the fakes of everything
// it talks to.
type world struct {
	mu      sync.Mutex
	nodes   map[int64]*domain.Node
	panels  map[int64]*domain.Panel
	agents  map[int64]*domain.NodeAgent
	routes  map[int64]*domain.NodeRoutes
	links   map[[2]int64]domain.TransitLink
	address map[int64]string
	lists   map[int64]*domain.DestList
	expand  map[int64]*destlist.Expanded
	plan    destpolicy.Plan
	plans   int
	snap    *ports.NativeDesiredSnapshot
	probes  []domain.ProbeState
	nextLnk int64
	good    map[int64]goodList
	// expandErr makes a list fail to expand.
	expandErr map[int64]error
	// ensureErr makes the tunnel to an exit server fail.
	ensureErr map[int64]error
}

func newWorld() *world {
	return &world{
		nodes: map[int64]*domain.Node{}, panels: map[int64]*domain.Panel{}, agents: map[int64]*domain.NodeAgent{},
		routes: map[int64]*domain.NodeRoutes{}, links: map[[2]int64]domain.TransitLink{}, address: map[int64]string{},
		lists: map[int64]*domain.DestList{}, expand: map[int64]*destlist.Expanded{}, nextLnk: 10, good: map[int64]goodList{}, expandErr: map[int64]error{}, ensureErr: map[int64]error{},
	}
}

// server adds a Passwall Node server that has synced, with the capabilities of a
// current agent.
func (w *world) server(id int64, name string, caps ...string) {
	w.panels[id] = &domain.Panel{ID: id, Name: name, Kind: domain.PanelKindPSP}
	if len(caps) == 0 {
		caps = []string{nodeprotocol.CapabilityTransitLink, nodeprotocol.CapabilityTransitRoute}
	}
	w.agents[id] = &domain.NodeAgent{AgentID: fmt.Sprintf("agent-%d", id), PanelID: id, ObservedCapabilities: caps, DesiredCoreEngine: domain.NodeCoreXray, ObservedAddress: fmt.Sprintf("192.0.2.%d", id)}
	w.address[id] = fmt.Sprintf("192.0.2.%d", id)
}

func (w *world) node(id, panel int64, name string) *domain.Node {
	n := &domain.Node{ID: id, PanelID: panel, DisplayName: name, Enabled: true, DesiredProtocol: "vless", DesiredPort: int(4000 + id)}
	w.nodes[id] = n
	return n
}

func (w *world) list(id int64, name string, ready bool, domains []string, cidrs []string) {
	now := time.Now()
	l := &domain.DestList{ID: id, Name: name, Kind: domain.DestListCustom, Side: domain.DestSideDest, ContentSHA256: fmt.Sprintf("sha-%d", id), EntryCount: 1}
	if !ready {
		l.Kind, l.LastFetchedAt, l.EntryCount = domain.DestListRemote, nil, 0
	} else {
		l.LastFetchedAt = &now
	}
	w.lists[id] = l
	w.expand[id] = &destlist.Expanded{Domains: domains, CIDRs: cidrs}
}

// --- fakes ---

type fakeNodes struct{ w *world }

func (f fakeNodes) List(context.Context) ([]*domain.Node, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	var out []*domain.Node
	for _, n := range f.w.nodes {
		copy := *n
		if r, ok := f.w.routes[n.ID]; ok {
			copy.DestRoutes, copy.RoutesSent, copy.RoutesApplied = r.Routes, r.Sent, r.Applied
		}
		out = append(out, &copy)
	}
	slices.SortFunc(out, func(a, b *domain.Node) int { return int(a.ID - b.ID) })
	return out, nil
}

func (f fakeNodes) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	all, _ := f.List(ctx)
	for _, n := range all {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, domain.ErrNotFound
}

type fakePanels struct{ w *world }

func (f fakePanels) GetByID(_ context.Context, id int64) (*domain.Panel, error) {
	if p, ok := f.w.panels[id]; ok {
		return p, nil
	}
	return nil, domain.ErrNotFound
}

type fakeAgents struct{ w *world }

func (f fakeAgents) GetByPanelID(_ context.Context, id int64) (*domain.NodeAgent, error) {
	if a, ok := f.w.agents[id]; ok {
		return a, nil
	}
	return nil, domain.ErrNotFound
}

type fakeLinks struct{ w *world }

func (f fakeLinks) EnsureLink(_ context.Context, entry, exit int64) (domain.TransitLink, error) {
	if err := f.w.ensureErr[exit]; err != nil {
		return domain.TransitLink{}, err
	}
	key := [2]int64{entry, exit}
	if l, ok := f.w.links[key]; ok {
		return l, nil
	}
	f.w.nextLnk++
	l := domain.TransitLink{ID: f.w.nextLnk, EntryPanelID: entry, ExitPanelID: exit}
	f.w.links[key] = l
	return l, nil
}

func (f fakeLinks) LinkRow(_ context.Context, entry, exit int64) (domain.TransitLink, error) {
	if l, ok := f.w.links[[2]int64{entry, exit}]; ok {
		return l, nil
	}
	return domain.TransitLink{}, domain.ErrNotFound
}

func (f fakeLinks) OutUsable(_ context.Context, _, exit int64) (bool, error) {
	l, ok := f.w.links[[2]int64{1, exit}]
	return f.w.address[exit] != "" || (ok && l.ExitAddress != ""), nil
}

func (f fakeLinks) RemoveLink(_ context.Context, entry, exit int64) error {
	delete(f.w.links, [2]int64{entry, exit})
	return nil
}

func (f fakeLinks) ServerAddress(_ context.Context, panel int64) (string, error) {
	return f.w.address[panel], nil
}

type fakeLists struct{ w *world }

func (f fakeLists) Lists(context.Context) ([]*domain.DestList, error) {
	var out []*domain.DestList
	for _, l := range f.w.lists {
		out = append(out, l)
	}
	return out, nil
}

func (f fakeLists) GetList(_ context.Context, id int64) (*domain.DestList, error) {
	if l, ok := f.w.lists[id]; ok {
		return l, nil
	}
	return nil, domain.ErrNotFound
}

func (f fakeLists) Expand(_ context.Context, l *domain.DestList) (*destlist.Expanded, error) {
	if err := f.w.expandErr[l.ID]; err != nil {
		return nil, err
	}
	return f.w.expand[l.ID], nil
}

type fakeRoutes struct{ w *world }

func (f fakeRoutes) Get(_ context.Context, id int64) (domain.NodeRoutes, error) {
	if r, ok := f.w.routes[id]; ok {
		return *r, nil
	}
	return domain.NodeRoutes{NodeID: id}, nil
}

func (f fakeRoutes) List(context.Context) ([]domain.NodeRoutes, error) {
	var out []domain.NodeRoutes
	for _, r := range f.w.routes {
		if len(r.Routes) > 0 {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b domain.NodeRoutes) int { return int(a.NodeID - b.NodeID) })
	return out, nil
}

func (f fakeRoutes) Replace(_ context.Context, id int64, routes []domain.DestRoute) error {
	r := f.w.routes[id]
	if r == nil {
		r = &domain.NodeRoutes{NodeID: id}
		f.w.routes[id] = r
	}
	r.Routes = routes
	return nil
}

func (f fakeRoutes) RecordSent(_ context.Context, id int64, digest, withheld string, at time.Time) (bool, error) {
	r := f.w.routes[id]
	if r == nil || (r.Sent == digest && r.Withheld == withheld) {
		return false, nil
	}
	r.Sent, r.Withheld = digest, withheld
	if digest == "" {
		r.Applied = ""
	}
	return true, nil
}

func (f fakeRoutes) SetApplied(_ context.Context, id int64, digest string, at time.Time) (bool, error) {
	r := f.w.routes[id]
	if r == nil || r.Applied == digest {
		return false, nil
	}
	r.Applied = digest
	return true, nil
}

func (f fakeRoutes) GoodList(_ context.Context, id int64) ([]string, []string, bool, error) {
	g, ok := f.w.good[id]
	return g.domains, g.cidrs, ok, nil
}

func (f fakeRoutes) SaveGoodList(_ context.Context, id int64, _ string, domains, cidrs []string) error {
	f.w.good[id] = goodList{domains, cidrs}
	return nil
}

func (f fakeRoutes) ForgetGoodList(_ context.Context, id int64) error {
	delete(f.w.good, id)
	return nil
}

type goodList struct{ domains, cidrs []string }

type fakePolicies struct{ w *world }

func (f fakePolicies) PlanPolicy(context.Context, destpolicy.CompileInput) (destpolicy.Plan, error) {
	f.w.plans++
	return f.w.plan, nil
}

type fakeDesired struct{ w *world }

func (f fakeDesired) Load(_ context.Context, panel int64) (*ports.NativeDesiredSnapshot, error) {
	if f.w.snap != nil {
		return f.w.snap, nil
	}
	snap := &ports.NativeDesiredSnapshot{}
	ids := make([]int64, 0)
	for id, n := range f.w.nodes {
		if n.PanelID == panel {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		n := *f.w.nodes[id]
		snap.Nodes = append(snap.Nodes, &n)
	}
	return snap, nil
}

type fakeProbes struct{ w *world }

func (f fakeProbes) List(context.Context) ([]domain.ProbeState, error) { return f.w.probes, nil }

func (w *world) service() *Service {
	return New(Deps{
		Routes: fakeRoutes{w}, Nodes: fakeNodes{w}, Panels: fakePanels{w}, Agents: fakeAgents{w}, Links: fakeLinks{w},
		Lists: fakeLists{w}, Policies: fakePolicies{w}, Desired: fakeDesired{w}, Probes: fakeProbes{w},
		Now: func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
	})
}

func serverRoute(id string, panel int64, entries ...string) domain.DestRoute {
	return domain.DestRoute{ID: id, Target: domain.DestRouteTarget{Kind: domain.DestRouteServer, PanelID: panel}, Domains: entries}
}

func localRoute(id string, entries ...string) domain.DestRoute {
	return domain.DestRoute{ID: id, Target: domain.DestRouteTarget{Kind: domain.DestRouteLocal}, Domains: entries}
}

func generated(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("domain:%s%d.example", prefix, i)
	}
	slices.Sort(out)
	return out
}
