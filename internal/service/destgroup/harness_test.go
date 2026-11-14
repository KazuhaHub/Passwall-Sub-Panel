package destgroup

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// ---- fakes ----

// events records the order of what the service tells the rest of the system.
type events struct {
	mu  sync.Mutex
	log []string
}

func (e *events) add(s string) { e.mu.Lock(); e.log = append(e.log, s); e.mu.Unlock() }
func (e *events) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.log)
}

type fakeEligibility struct {
	ev      *events
	panels  []int64
	reasons map[int64]string
	err     error
}

func (f *fakeEligibility) PanelsFor(context.Context, *domain.Group) ([]int64, error) {
	return f.panels, f.err
}
func (f *fakeEligibility) PanelEnforceReason(_ context.Context, id int64) (string, error) {
	return f.reasons[id], f.err
}
func (f *fakeEligibility) InvalidateEligibility(id int64) { f.ev.add("invalidate:" + itoa(id)) }

type fakeResync struct{ ev *events }

func (f fakeResync) ResyncGroupMembersInBackground(gid int64) { f.ev.add("resync:" + itoa(gid)) }

type fakePanels map[int64]*domain.XUIPanel

func (f fakePanels) GetByID(_ context.Context, id int64) (*domain.XUIPanel, error) {
	if p, ok := f[id]; ok {
		return p, nil
	}
	return nil, domain.ErrNotFound
}

type fakeAgents map[int64]*domain.NodeAgent

func (f fakeAgents) GetByPanelID(_ context.Context, id int64) (*domain.NodeAgent, error) {
	if a, ok := f[id]; ok {
		return a, nil
	}
	return nil, domain.ErrNotFound
}

type fakePolicies map[string]*domain.DestAgentPolicy

func (f fakePolicies) GetMeta(_ context.Context, id string) (*domain.DestAgentPolicy, error) {
	if m, ok := f[id]; ok {
		return m, nil
	}
	return nil, domain.ErrNotFound
}

// memberGroups is the real group repo with the member counts the test sets.
type memberGroups struct {
	ports.GroupRepo
	members map[int64]int64
}

func (m memberGroups) CountMembersByGroups(_ context.Context, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for _, id := range ids {
		out[id] = m.members[id]
	}
	return out, nil
}

type fakeBudget struct {
	err error
	got []destpolicy.GroupHypo
}

func (f *fakeBudget) CheckGroupBudget(_ context.Context, h destpolicy.GroupHypo) error {
	f.got = append(f.got, h)
	return f.err
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// ---- the harness ----

const (
	nativeAgent  = "agt_1"
	nativeAgent2 = "agt_2"
)

type harness struct {
	t      *testing.T
	ctx    context.Context
	dest   *sqlstore.DestRepos
	groups memberGroups
	lists  *destlist.Service
	svc    *Service
	ev     *events
	elig   *fakeEligibility
	budget *fakeBudget
	panels fakePanels
	agents fakeAgents
	meta   fakePolicies
	now    time.Time
	hosts  []string
	// hostsErr makes the base-host suggestion fail.
	hostsErr error
	batch    int
}

// newHarness is the real stores over SQLite and fakes for what group.Service
// knows: two native panels (1 healthy, 2 not) and a 3X-UI one (3).
func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := sqlstore.OpenQuiet("sqlite", filepath.Join(t.TempDir(), "destgroup.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ctx: context.Background(), dest: sqlstore.NewDestRepos(db), groups: memberGroups{GroupRepo: sqlstore.NewRepos(db).Group, members: map[int64]int64{}}, ev: &events{}, budget: &fakeBudget{},
		now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), hosts: []string{"dns.alidns.com", "doh.example"}}
	h.lists = destlist.New(destlist.Deps{
		Lists: h.dest.Lists, Policies: h.dest.Policies, Groups: h.dest.GroupAllowlists, DataDir: t.TempDir(),
	})
	h.elig = &fakeEligibility{ev: h.ev, panels: []int64{1, 2, 3}, reasons: map[int64]string{2: "capability", 3: "xui"}}
	caps := []string{protocol.CapabilityDestinationPolicy, protocol.CapabilityDestinationGroups, protocol.CapabilityAuditHits}
	h.panels = fakePanels{
		1: {ID: 1, Name: "tokyo", Kind: domain.PanelKindPSP},
		2: {ID: 2, Name: "osaka", Kind: domain.PanelKindPSP},
		3: {ID: 3, Name: "legacy", Kind: domain.PanelKind3XUI},
	}
	h.agents = fakeAgents{
		1: {AgentID: nativeAgent, PanelID: 1, ObservedCapabilities: caps, ObservedCoreEngine: domain.NodeCoreXray, DesiredCoreEngine: domain.NodeCoreXray},
		2: {AgentID: nativeAgent2, PanelID: 2, ObservedCapabilities: caps[:1], ObservedCoreEngine: domain.NodeCoreXray, DesiredCoreEngine: domain.NodeCoreXray},
	}
	h.meta = fakePolicies{}
	h.rebuild()
	return h
}

func (h *harness) rebuild() {
	svc, err := New(Deps{
		Allowlists: h.dest.GroupAllowlists, Groups: h.groups, Lists: h.dest.Lists, Editor: h.lists, Budget: h.budget,
		Eligibility: h.elig, Resync: fakeResync{h.ev}, Panels: h.panels, Agents: h.agents, Policies: h.meta, Hits: h.dest.Hits,
		BaseHosts: func(context.Context) ([]string, error) { return h.hosts, h.hostsErr },
		Changed:   func(context.Context) { h.ev.add("changed") },
		Now:       func() time.Time { return h.now },
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.svc = svc
}

func (h *harness) group(name string, members int64) *domain.Group {
	h.t.Helper()
	g := &domain.Group{Slug: strings.ToLower(name), Name: name, TagFilter: domain.TagFilter{All: true}}
	if err := h.groups.Create(h.ctx, g); err != nil {
		h.t.Fatal(err)
	}
	h.groups.members[g.ID] = members
	return g
}

func (h *harness) list(name, text string) *domain.DestList {
	h.t.Helper()
	l, _, err := h.lists.CreateCustom(h.ctx, name, domain.DestSideDest, []byte(text))
	if err != nil {
		h.t.Fatal(err)
	}
	return l
}

// remoteList is a remote list that never fetched: it is not ready.
func (h *harness) remoteList(name string) *domain.DestList {
	h.t.Helper()
	l := &domain.DestList{Name: name, Kind: domain.DestListRemote, Side: domain.DestSideDest, SourceURL: "https://lists.example.test/" + name}
	if err := h.dest.Lists.Create(h.ctx, l); err != nil {
		h.t.Fatal(err)
	}
	return l
}

func (h *harness) generation() int64 {
	h.t.Helper()
	st, err := h.dest.State.Get(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return st.Generation
}

func (h *harness) put(gid int64, in PutInput) (*View, error) {
	h.t.Helper()
	return h.svc.Put(h.ctx, gid, in)
}

func hosts(h ...string) *[]string { return &h }

// hit stores one hit row of the hour daysAgo days before the harness clock.
func (h *harness) hit(daysAgo int, source, action, dest string, count, user int64) {
	h.t.Helper()
	hour := h.now.Add(-time.Duration(daysAgo) * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	row := domain.DestHit{HourMS: hour, PanelID: 1, UserID: user, Source: source, Action: action, Dest: dest, Count: count, FirstMS: hour + 1000, LastMS: hour + 2000}
	h.batch++
	if _, err := h.dest.Hits.StoreBatch(h.ctx, nativeAgent, "b"+itoa(int64(h.batch)), h.now, []domain.DestHit{row}); err != nil {
		h.t.Fatal(err)
	}
}

// drop records overflow of the hour of the harness clock.
func (h *harness) drop(daysAgo int, kind string, n int64) {
	h.t.Helper()
	hour := h.now.Add(-time.Duration(daysAgo) * 24 * time.Hour).Truncate(time.Hour).UnixMilli()
	if err := h.dest.Hits.AddDrops(h.ctx, hour, 1, kind, n); err != nil {
		h.t.Fatal(err)
	}
}
