package destpolicy

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

// The compiler is tested over a real SQLite store, the real list service (so
// expansion is the production one) and a clock the test moves. The store stamps
// its own writes with the wall clock, so the test clock is wall time plus an
// offset: advancing it moves "now" relative to those stamps.

type clock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset += d
}

type fakeLocator struct{ path string }

func (l *fakeLocator) ActiveMMDBPath(context.Context) string { return l.path }

type fakeSettings struct {
	mu  sync.Mutex
	set ports.UISettings
}

func (f *fakeSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.set, nil
}

// counting wraps the stores to count the reads a compile makes.
type reads struct {
	defsReads, groupReads, stateReads, snapshotReads, metaReads, bodyReads, patches int
}

type counting struct {
	sync.Mutex
	reads
	// failBody makes AppliedBody fail, as a transient database error would.
	failBody bool
}

type countedDefs struct {
	Definitions
	c *counting
	// afterRead runs once, right after ReadDefinitions returned (the window
	// between the read and the compare-and-set of the publish).
	afterRead func()
	// modes are the allowlist groups the definitions read carries. Where they
	// are stored is not this package's concern, so the tests supply them here.
	modes []*domain.DestGroupMode
}

func (d *countedDefs) ReadDefinitions(ctx context.Context) (*domain.DestDefinitions, error) {
	d.c.Lock()
	d.c.defsReads++
	d.c.Unlock()
	defs, err := d.Definitions.ReadDefinitions(ctx)
	if err == nil && d.modes != nil {
		defs.GroupModes = slices.Clone(d.modes)
	}
	if d.afterRead != nil {
		f := d.afterRead
		d.afterRead = nil
		f()
	}
	return defs, err
}

func (d *countedDefs) GroupIDsByUserIDs(ctx context.Context, ids []int64) (map[int64]int64, error) {
	d.c.Lock()
	d.c.groupReads++
	d.c.Unlock()
	return d.Definitions.GroupIDsByUserIDs(ctx, ids)
}

type countedState struct {
	StateStore
	c *counting
}

func (s *countedState) Get(ctx context.Context) (*domain.DestPolicyState, error) {
	s.c.Lock()
	s.c.stateReads++
	s.c.Unlock()
	return s.StateStore.Get(ctx)
}

func (s *countedState) Snapshot(ctx context.Context, gen int64) (*domain.DestPolicySnapshot, error) {
	s.c.Lock()
	s.c.snapshotReads++
	s.c.Unlock()
	return s.StateStore.Snapshot(ctx, gen)
}

type countedAgents struct {
	AgentStore
	c *counting
}

func (a *countedAgents) GetMeta(ctx context.Context, id string) (*domain.DestAgentPolicy, error) {
	a.c.Lock()
	a.c.metaReads++
	a.c.Unlock()
	return a.AgentStore.GetMeta(ctx, id)
}

func (a *countedAgents) AppliedBody(ctx context.Context, id string) ([]byte, error) {
	a.c.Lock()
	a.c.bodyReads++
	fail := a.c.failBody
	a.c.Unlock()
	if fail {
		return nil, errors.New("transient database error")
	}
	return a.AgentStore.AppliedBody(ctx, id)
}

func (a *countedAgents) Patch(ctx context.Context, id string, p domain.DestAgentPolicyPatch) error {
	a.c.Lock()
	a.c.patches++
	a.c.Unlock()
	return a.AgentStore.Patch(ctx, id, p)
}

type recorded struct {
	sync.Mutex
	compile  []string
	publish  []string
	rejected int
}

func (r *recorded) count(label string) int {
	r.Lock()
	defer r.Unlock()
	n := 0
	for _, c := range r.compile {
		if c == label {
			n++
		}
	}
	return n
}

type harness struct {
	db       *gorm.DB
	t        *testing.T
	ctx      context.Context
	repos    *sqlstore.DestRepos
	lists    *destlist.Service
	svc      *Service
	defs     *countedDefs
	counts   *counting
	rec      *recorded
	clock    *clock
	settings *fakeSettings
	locator  *fakeLocator
	dir      string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := sqlstore.OpenQuiet("sqlite", filepath.Join(t.TempDir(), "dest.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	h := &harness{
		db: db, t: t, ctx: context.Background(), repos: sqlstore.NewDestRepos(db), counts: &counting{}, rec: &recorded{},
		clock: &clock{}, settings: &fakeSettings{}, locator: &fakeLocator{}, dir: t.TempDir(),
	}
	h.lists = destlist.New(destlist.Deps{
		Lists: h.repos.Lists, Policies: h.repos.Policies, SourceRules: h.repos.SourceRules, State: h.repos.State,
		Settings: h.settings, Locator: h.locator, DataDir: h.dir,
	})
	h.defs = &countedDefs{Definitions: h.repos, c: h.counts}
	svc, err := New(Deps{
		Definitions: h.defs, State: &countedState{StateStore: h.repos.State, c: h.counts},
		Agents: &countedAgents{AgentStore: h.repos.Agents, c: h.counts},
		Lists:  h.lists, Countries: h.lists.Countries, Settings: h.settings,
		Metrics: Metrics{
			Compile: func(r string) { h.rec.Lock(); h.rec.compile = append(h.rec.compile, r); h.rec.Unlock() },
			Publish: func(r string) { h.rec.Lock(); h.rec.publish = append(h.rec.publish, r); h.rec.Unlock() },
			PublishRejected: func() {
				h.rec.Lock()
				h.rec.rejected++
				h.rec.Unlock()
			},
		},
		Now: h.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	return h
}

// restart is "the process restarted": the service is rebuilt over the same
// stores, with empty caches.
func (h *harness) restart() {
	h.t.Helper()
	svc, err := New(Deps{
		Definitions: h.defs, State: &countedState{StateStore: h.repos.State, c: h.counts},
		Agents: &countedAgents{AgentStore: h.repos.Agents, c: h.counts},
		Lists:  h.lists, Countries: h.lists.Countries, Settings: h.settings, Now: h.clock.Now,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.svc = svc
}

func (h *harness) list(name, entries string) *domain.DestList {
	h.t.Helper()
	l, _, err := h.lists.CreateCustom(h.ctx, name, domain.DestSideDest, []byte(entries))
	if err != nil {
		h.t.Fatal(err)
	}
	return l
}

func (h *harness) policy(name string, action domain.DestAction, state domain.DestState, mut func(*domain.DestPolicy)) *domain.DestPolicy {
	h.t.Helper()
	p := &domain.DestPolicy{Name: name, Action: action, State: state, Scope: domain.DestScopeAll}
	if mut != nil {
		mut(p)
	}
	if err := h.repos.Policies.Create(h.ctx, p); err != nil {
		h.t.Fatal(err)
	}
	return p
}

// publishNow makes every pending write visible to Compile.
func (h *harness) publishNow() {
	h.t.Helper()
	if _, err := h.svc.Publish(h.ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) mmdb(nets map[string]string) {
	h.t.Helper()
	w, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "Test-Country", IncludeReservedNetworks: true, RecordSize: 24})
	if err != nil {
		h.t.Fatal(err)
	}
	for cidr, cc := range nets {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			h.t.Fatal(err)
		}
		if err := w.Insert(n, mmdbtype.Map{"country": mmdbtype.Map{"iso_code": mmdbtype.String(cc)}}); err != nil {
			h.t.Fatal(err)
		}
	}
	path := filepath.Join(h.dir, "country.mmdb")
	f, err := os.Create(path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if _, err := w.WriteTo(f); err != nil {
		h.t.Fatal(err)
	}
	h.locator.path = path
}

const policyCap = protocol.CapabilityDestinationPolicy

// goodSniffing is an inbound whose sniffing supplies domains.
const goodSniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":false}`

type fleet struct {
	panelID int64
	nodes   []*domain.Node
	users   []int64
}

func (f fleet) snapshot() *ports.NativeDesiredSnapshot {
	snap := &ports.NativeDesiredSnapshot{Nodes: f.nodes}
	for i, u := range f.users {
		snap.Clients = append(snap.Clients, ports.NativeDesiredClient{Client: &domain.PSPClient{ID: int64(i + 1), UserID: u}})
	}
	return snap
}

func node(id int64, sniffing string) *domain.Node {
	return &domain.Node{ID: id, PanelID: 9, DisplayName: "n" + string(rune('0'+id)), Enabled: true, Sniffing: sniffing}
}

func (h *harness) compile(f fleet, caps ...string) Result {
	h.t.Helper()
	return h.compileEngine(f, "", caps...)
}

func (h *harness) compileEngine(f fleet, engine string, caps ...string) Result {
	h.t.Helper()
	if len(caps) == 0 {
		caps = []string{policyCap}
	}
	res, err := h.svc.Compile(h.ctx, CompileInput{
		PanelID: 9, AgentID: "agt_9", Capabilities: caps, Snapshot: f.snapshot(),
		Config: protocol.ConfigBody{Core: protocol.CoreSelection{Engine: engine}},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func ruleIDs(p *protocol.DestinationPolicy) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, r := range p.Rules {
		out = append(out, r.ID)
	}
	return out
}

func joinIDs(ids []string) string { return strings.Join(ids, ",") }

// backdate says the last write was lastAgo ago and the first unpublished one
// firstAgo ago. The store stamps wall-clock time itself, so the debounce is
// tested by moving those stamps rather than the clock.
func (h *harness) backdate(lastAgo, firstAgo time.Duration) {
	h.t.Helper()
	now := time.Now().UTC()
	if err := h.db.Exec("UPDATE dest_policy_state SET last_write_at = ?, first_unpublished_at = ? WHERE id = 1",
		now.Add(-lastAgo), now.Add(-firstAgo)).Error; err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) state() *domain.DestPolicyState {
	h.t.Helper()
	st, err := h.repos.State.Get(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func (h *harness) publishes() []string {
	h.rec.Lock()
	defer h.rec.Unlock()
	return slices.Clone(h.rec.publish)
}
