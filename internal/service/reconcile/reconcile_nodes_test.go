package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- minimal fakes (embed the interface, override only what checkNodes uses) ----

type recNodeRepo struct {
	ports.NodeRepo
	nodes []*domain.Node
	// Separate slices so tests can pin which writer path the service took.
	// updates = full-row Save (Update); updatesCfg = column-scoped snapshot
	// writer (UpdateInboundConfig — the v3.5 path snapshot writes must use).
	updates     []*domain.Node
	updatesCfg  []*domain.Node
	enabledSets []bool // column-scoped UpdateEnabled calls
	getOverride func(int64) (*domain.Node, error)
}

func (r *recNodeRepo) List(context.Context) ([]*domain.Node, error) { return r.nodes, nil }
func (r *recNodeRepo) Update(_ context.Context, n *domain.Node) error {
	cp := *n
	r.updates = append(r.updates, &cp)
	return nil
}

// UpdateInboundConfig is the column-scoped v3.5 snapshot writer. Recording
// separately from Update lets tests catch regressions where a snapshot write
// accidentally takes the full-row Save path (which would race with the health
// pass).
func (r *recNodeRepo) UpdateInboundConfig(_ context.Context, n *domain.Node) error {
	cp := *n
	r.updatesCfg = append(r.updatesCfg, &cp)
	return nil
}

// UpdateEnabled is the column-scoped enabled writer. Recorded separately from
// Update so the disappeared-inbound branch can be pinned to the narrow writer.
func (r *recNodeRepo) UpdateEnabled(_ context.Context, _ int64, enabled bool) error {
	r.enabledSets = append(r.enabledSets, enabled)
	return nil
}

// GetByID returns whatever the test currently has staged for that id. Tests
// that want to simulate "admin wrote a fresh row mid-cycle" can mutate the
// node pointer in r.nodes between checkNodes invocations or override this
// behaviour via getOverride.
func (r *recNodeRepo) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	if r.getOverride != nil {
		return r.getOverride(id)
	}
	for _, n := range r.nodes {
		if n.ID == id {
			cp := *n
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

type recPool struct{ c ports.XUIClient }

func (p recPool) Get(int64) (ports.XUIClient, error) { return p.c, nil }
func (recPool) List() []*domain.XUIPanel             { return nil }
func (recPool) Add(*domain.XUIPanel) error           { return nil }
func (recPool) Remove(int64) error                   { return nil }

type recClient struct {
	ports.XUIClient
	inbounds  []ports.Inbound
	getResp   *ports.Inbound
	updated   []ports.InboundSpec
	listCalls int
	updateErr error // when set, UpdateInbound returns it (drift-push fail)
	getErr    error // when set, GetInbound returns it (recapture fail)
}

func (c *recClient) ListInbounds(context.Context) ([]ports.Inbound, error) {
	c.listCalls++
	return c.inbounds, nil
}
func (c *recClient) GetInbound(_ context.Context, id int) (*ports.Inbound, error) {
	if c.getErr != nil {
		return nil, c.getErr
	}
	if c.getResp != nil {
		return c.getResp, nil
	}
	for i := range c.inbounds {
		if c.inbounds[i].ID == id {
			return &c.inbounds[i], nil
		}
	}
	return nil, domain.ErrNotFound
}
func (c *recClient) UpdateInbound(_ context.Context, _ int, spec ports.InboundSpec) error {
	c.updated = append(c.updated, spec)
	return c.updateErr
}

// recAudit captures AuditEntry inserts so observability tests can assert that
// reconcile axis-A fires per-inbound rows (alongside the cycle-wide aggregate
// RunOnce writes). Embeds AuditRepo so the methods we don't drive panic if
// accidentally called — the same idiom the other fakes use.
type recAudit struct {
	ports.AuditRepo
	entries []*domain.AuditEntry
}

func (a *recAudit) Insert(_ context.Context, e *domain.AuditEntry) error {
	cp := *e
	a.entries = append(a.entries, &cp)
	return nil
}

// findAuditByAction returns the first captured audit entry whose Action matches.
func (a *recAudit) findAuditByAction(action string) *domain.AuditEntry {
	for _, e := range a.entries {
		if e.Action == action {
			return e
		}
	}
	return nil
}

type recUserRepo struct {
	ports.UserRepo
	users []*domain.User
}

func (r *recUserRepo) List(context.Context, ports.UserFilter) ([]*domain.User, int64, error) {
	return r.users, int64(len(r.users)), nil
}

type recOwnershipRepo struct {
	ports.OwnershipRepo
	byUser map[int64][]*domain.XUIClientEntry
}

func (r *recOwnershipRepo) ListByUsers(_ context.Context, userIDs []int64) (map[int64][]*domain.XUIClientEntry, error) {
	out := map[int64][]*domain.XUIClientEntry{}
	for _, id := range userIDs {
		if entries, ok := r.byUser[id]; ok {
			out[id] = entries
		}
	}
	return out, nil
}

func (r *recOwnershipRepo) ListByUser(_ context.Context, userID int64) ([]*domain.XUIClientEntry, error) {
	return r.byUser[userID], nil
}

type recGroupRepo struct {
	ports.GroupRepo
	groups []*domain.Group
}

func (r *recGroupRepo) List(context.Context) ([]*domain.Group, error) { return r.groups, nil }
func (r *recGroupRepo) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	for _, g := range r.groups {
		if g.ID == id {
			return g, nil
		}
	}
	return nil, domain.ErrNotFound
}

type recSettings struct{ ports.SettingsRepo }

func (recSettings) Load(_ context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	return defaults, nil
}

type recSyncer struct {
	addCalls    int
	enableCalls int
	rotateCalls int
}

func (s *recSyncer) AddClientToInbound(context.Context, int64, int64, int, domain.Protocol, string, string, string, string, int64, int64) error {
	s.addCalls++
	return nil
}

func (s *recSyncer) SetOwnedClientEnable(context.Context, int64, int, string, domain.Protocol, string, string, string, bool, int64, int64) error {
	s.enableCalls++
	return nil
}

func (s *recSyncer) RotateClientUUID(context.Context, int64, int, string, domain.Protocol, string, string, string, string, bool, int64, int64) error {
	s.rotateCalls++
	return nil
}

// cacheFromInbounds builds an inboundCacheKey→entry map identical to what
// prefetchInbounds would have populated at the top of RunOnce. Each test
// supplies its own live inbounds; this helper does the same shape conversion
// the prefetch does so checkNodes sees the test's intended live state.
func cacheFromInbounds(panelID int64, inbs []ports.Inbound) map[inboundCacheKey]*inboundCacheEntry {
	out := map[inboundCacheKey]*inboundCacheEntry{}
	for i := range inbs {
		out[inboundCacheKey{panelID: panelID, inboundID: inbs[i].ID}] = &inboundCacheEntry{
			inbound: &inbs[i],
		}
	}
	return out
}

// Node with no captured config + a live inbound → reconcile should pull the
// config into the node (backfill) and NOT push anything.
func TestRunOnce_IgnoresDisabledOwnershipRows(t *testing.T) {
	node := &domain.Node{ID: 1, PanelID: 1, InboundID: 3, Enabled: false}
	user := &domain.User{ID: 7, UUID: "uuid-7", GroupID: 9, Enabled: true}
	owner := &domain.XUIClientEntry{
		ID: 21, UserID: user.ID, PanelID: node.PanelID, InboundID: node.InboundID,
		ClientEmail: "u7-n1@psp.local", ClientUUID: user.UUID,
	}
	live := ports.Inbound{
		ID: node.InboundID, Protocol: "vless", Port: 443,
		Settings: `{"decryption":"none","clients":[]}`,
	}
	client := &recClient{inbounds: []ports.Inbound{live}}
	syncer := &recSyncer{}
	svc := &Service{
		users:            &recUserRepo{users: []*domain.User{user}},
		ownership:        &recOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{user.ID: {owner}}},
		nodes:            &recNodeRepo{nodes: []*domain.Node{node}},
		groups:           &recGroupRepo{groups: []*domain.Group{{ID: user.GroupID, TagFilter: domain.TagFilter{All: true}}}},
		settings:         recSettings{},
		audit:            &recAudit{},
		pool:             recPool{c: client},
		syncer:           syncer,
		axisAReversePush: true,
	}

	report, err := svc.RunOnce(context.Background(), LevelFull)
	if err != nil {
		t.Fatal(err)
	}
	if client.listCalls != 0 {
		t.Fatalf("disabled nodes must not be prefetched by reconcile, ListInbounds calls=%d", client.listCalls)
	}
	if syncer.addCalls != 0 || syncer.enableCalls != 0 || syncer.rotateCalls != 0 {
		t.Fatalf("disabled ownership rows must not be healed: add=%d enable=%d rotate=%d",
			syncer.addCalls, syncer.enableCalls, syncer.rotateCalls)
	}
	if report.Scanned != 0 || report.Fixed != 0 || len(report.Issues) != 0 {
		t.Fatalf("disabled ownership rows should be invisible to reconcile, report=%+v", report)
	}
}

func TestCheckNodes_BackfillsMissingConfig(t *testing.T) {
	node := &domain.Node{ID: 1, PanelID: 1, InboundID: 3, Enabled: true} // ConfigSyncedAt nil
	live := []ports.Inbound{{
		ID: 3, Protocol: "vless", Port: 443,
		StreamSettings: `{"network":"tcp","security":"reality"}`,
		Settings:       `{"decryption":"none","clients":[{"id":"x"}]}`,
	}}
	client := &recClient{inbounds: live}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	svc := &Service{nodes: repo, pool: recPool{c: client}, axisAReversePush: true}

	report := &Report{}
	svc.checkNodes(context.Background(), report, cacheFromInbounds(1, live), nil)

	// Snapshot writes MUST take the column-scoped path (UpdateInboundConfig)
	// to coexist with the concurrent health-pass writer. A full-row Save
	// here would race against UpdateHealth — that bug shipped in beta.1.
	if len(repo.updates) != 0 {
		t.Fatalf("backfill must use UpdateInboundConfig, not Update; got %d full-row writes", len(repo.updates))
	}
	if len(repo.updatesCfg) != 1 {
		t.Fatalf("want 1 column-scoped snapshot write (backfill), got %d", len(repo.updatesCfg))
	}
	got := repo.updatesCfg[0]
	if got.ConfigSyncedAt == nil || got.StreamSettings != `{"network":"tcp","security":"reality"}` {
		t.Fatalf("config not captured into node: %+v", got)
	}
	if len(client.updated) != 0 {
		t.Fatalf("backfill must not push to 3X-UI, got %d pushes", len(client.updated))
	}
	if report.Fixed != 1 {
		t.Fatalf("want report.Fixed=1, got %d", report.Fixed)
	}
}

// resolveFlow must be the single source of truth shared by axis A, axis B, and
// the flow healer. The critical case: a VLESS+Reality inbound (detected flow
// xtls-rprx-vision) whose Node.Flow is blank must still resolve to vision — the
// axis-B bug recreated such clients with an empty flow that never self-healed.
func TestResolveFlow(t *testing.T) {
	vision := "xtls-rprx-vision"
	cases := []struct {
		name     string
		protocol domain.Protocol
		nodeFlow string
		ceFlow   string
		want     string
	}{
		{"reality blank node flow falls back to inbound flow", domain.ProtoVLESS, "", vision, vision},
		{"node flow overrides", domain.ProtoVLESS, "xtls-rprx-direct", vision, "xtls-rprx-direct"},
		{"plain vless no flow", domain.ProtoVLESS, "", "", ""},
		{"non-vless carries no flow", domain.ProtoTrojan, "ignored", vision, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &domain.Node{Flow: tc.nodeFlow}
			ce := &inboundCacheEntry{flow: tc.ceFlow}
			if got := resolveFlow(tc.protocol, n, ce); got != tc.want {
				t.Fatalf("resolveFlow(%s, node.Flow=%q, ce.flow=%q) = %q, want %q",
					tc.protocol, tc.nodeFlow, tc.ceFlow, got, tc.want)
			}
		})
	}
}

// flowRenderDiverges is the detector for the one state in which every reconcile
// check reports green while PSP's link and the panel's server config disagree.
// It is the mirror of resolveFlow above; the two must stay consistent, so they
// are tested against the same matrix.
func TestFlowRenderDiverges(t *testing.T) {
	vision := "xtls-rprx-vision"
	cases := []struct {
		name       string
		protocol   domain.Protocol
		nodeFlow   string
		storedFlow string
		want       bool
	}{
		{"blank node flow with a stored flow is the divergence", domain.ProtoVLESS, "", vision, true},
		{"node flow set: PSP owns it, healer converges the panel", domain.ProtoVLESS, "xtls-rprx-direct", vision, false},
		{"node flow set and matching", domain.ProtoVLESS, vision, vision, false},
		{"neither side has a flow", domain.ProtoVLESS, "", "", false},
		{"non-vless never carries flow", domain.ProtoTrojan, "", vision, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &domain.Node{Flow: tc.nodeFlow}
			if got := flowRenderDiverges(tc.protocol, n, tc.storedFlow); got != tc.want {
				t.Fatalf("flowRenderDiverges(%s, node.Flow=%q, stored=%q) = %v, want %v",
					tc.protocol, tc.nodeFlow, tc.storedFlow, got, tc.want)
			}
		})
	}

	// A nil node is the same case as a blank flow: nothing tells render what to
	// emit, so a stored flow still diverges.
	if !flowRenderDiverges(domain.ProtoVLESS, nil, vision) {
		t.Fatal("nil node with a stored flow must report divergence")
	}
}

// The detector must agree with resolveFlow: whenever it fires, resolveFlow has
// adopted the panel's flow, which is exactly why the healer stays quiet and the
// report reads green. If a change to either breaks that, this fails.
func TestFlowRenderDiverges_AgreesWithResolveFlow(t *testing.T) {
	vision := "xtls-rprx-vision"
	n := &domain.Node{Flow: ""}
	ce := &inboundCacheEntry{flow: vision}

	if !flowRenderDiverges(domain.ProtoVLESS, n, vision) {
		t.Fatal("precondition: this is the divergent state")
	}
	if got := resolveFlow(domain.ProtoVLESS, n, ce); got != vision {
		t.Fatalf("resolveFlow adopted %q, want the panel's %q — the healer would fire and this would not be a silent divergence", got, vision)
	}
}

// Regression: when an inbound has disappeared upstream (operator deleted it
// directly in 3X-UI) the node is disabled — but via the column-scoped
// UpdateEnabled, NOT a full-row Save of the cycle-start snapshot, which would
// revert the health/traffic/config columns the concurrent loops have written.
func TestCheckNodes_DisappearedInboundDisablesViaColumnWriter(t *testing.T) {
	node := &domain.Node{ID: 1, PanelID: 1, InboundID: 3, Enabled: true}
	// Panel reachable (another inbound is present in the cache) but the node's
	// own inbound (id 3) is absent → the "disappeared" branch.
	other := []ports.Inbound{{ID: 99, Protocol: "vless", Port: 8443}}
	client := &recClient{inbounds: other}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	svc := &Service{nodes: repo, pool: recPool{c: client}, axisAReversePush: true}

	report := &Report{}
	svc.checkNodes(context.Background(), report, cacheFromInbounds(1, other), nil)

	if len(repo.updates) != 0 {
		t.Fatalf("disable must use the column-scoped UpdateEnabled, not a full-row Save; got %d full-row writes", len(repo.updates))
	}
	if len(repo.enabledSets) != 1 || repo.enabledSets[0] != false {
		t.Fatalf("want exactly one UpdateEnabled(false), got %v", repo.enabledSets)
	}
	if report.Fixed != 1 {
		t.Fatalf("want report.Fixed=1, got %d", report.Fixed)
	}
}

// Node with a captured snapshot that differs from the live inbound → reconcile
// pushes PSP's config back, then re-captures the live config.
func TestCheckNodes_DisabledNodeSkipsConfigDrift(t *testing.T) {
	now := time.Now()
	node := &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3, Enabled: false,
		Protocol:        "vless",
		Port:            443,
		StreamSettings:  `{"network":"ws"}`,
		InboundSettings: `{"decryption":"none"}`,
		ConfigSyncedAt:  &now,
	}
	live := ports.Inbound{
		ID: 3, Protocol: "vless", Port: 443,
		StreamSettings: `{"network":"tcp"}`,
		Settings:       `{"decryption":"none","clients":[]}`,
	}
	client := &recClient{inbounds: []ports.Inbound{live}, getResp: &live}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	svc := &Service{nodes: repo, pool: recPool{c: client}, axisAReversePush: true}

	report := &Report{}
	svc.checkNodes(context.Background(), report, cacheFromInbounds(1, []ports.Inbound{live}), nil)

	if len(client.updated) != 0 {
		t.Fatalf("disabled nodes must not have config drift pushed, got %d UpdateInbound calls", len(client.updated))
	}
	if len(repo.updates)+len(repo.updatesCfg)+len(repo.enabledSets) != 0 {
		t.Fatalf("disabled nodes must not be written by checkNodes: full=%d cfg=%d enabled=%v",
			len(repo.updates), len(repo.updatesCfg), repo.enabledSets)
	}
	if report.Fixed != 0 || len(report.Issues) != 0 {
		t.Fatalf("disabled nodes should not appear in node reconcile report: %+v", report)
	}
}

func TestCheckNodes_DriftPushed(t *testing.T) {
	now := time.Now()
	node := &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3, Enabled: true,
		Protocol:        "vless",
		Port:            443,
		StreamSettings:  `{"network":"ws","security":"tls"}`, // PSP's truth
		InboundSettings: `{"decryption":"none"}`,
		InboundRemark:   "psp-stored", // stale vs the operator's live rename below
		ConfigSyncedAt:  &now,
	}
	live := ports.Inbound{
		ID: 3, Protocol: "vless", Port: 443,
		StreamSettings: `{"network":"tcp","security":"none"}`, // drifted on 3X-UI
		Settings:       `{"decryption":"none","clients":[{"id":"x"}]}`,
		Remark:         "operator-renamed", // renamed directly in 3X-UI
	}
	client := &recClient{inbounds: []ports.Inbound{live}, getResp: &live}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	svc := &Service{nodes: repo, pool: recPool{c: client}, axisAReversePush: true}

	report := &Report{}
	svc.checkNodes(context.Background(), report, cacheFromInbounds(1, []ports.Inbound{live}), nil)

	if len(client.updated) != 1 {
		t.Fatalf("want 1 push to 3X-UI on drift, got %d", len(client.updated))
	}
	if client.updated[0].StreamSettings != `{"network":"ws","security":"tls"}` {
		t.Fatalf("push must carry PSP's config, got %s", client.updated[0].StreamSettings)
	}
	// remark is operator-owned: the drift push must carry the LIVE remark, not
	// PSP's stale stored one, so a direct-in-3X-UI rename survives the push.
	if client.updated[0].Remark != "operator-renamed" {
		t.Fatalf("drift push must preserve the operator's live remark, got %q", client.updated[0].Remark)
	}
	if report.Fixed != 1 {
		t.Fatalf("want report.Fixed=1, got %d", report.Fixed)
	}
}

// Exercises the adopt fallback (axisAReversePush kill-switch off). P2 verified
// reverse-push is safe on 3.2.0 so New() enables it by default; when the
// kill-switch is off, a drifted captured inbound is ADOPTED instead — PSP
// captures the live config into the snapshot and writes NOTHING to the inbound.
// The svc here deliberately omits axisAReversePush (defaults false) to drive
// that fallback — the inverse of the *_DriftPushed tests above.
func TestCheckNodes_DriftAdoptedWhenReversePushDisabled(t *testing.T) {
	now := time.Now()
	node := &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3, Enabled: true,
		Protocol: "vless", Port: 443,
		StreamSettings:  `{"network":"ws","security":"tls"}`, // PSP's stored truth
		InboundSettings: `{"decryption":"none"}`,
		ConfigSyncedAt:  &now, ConfigSyncState: "synced",
	}
	live := ports.Inbound{
		ID: 3, Protocol: "vless", Port: 443,
		StreamSettings: `{"network":"tcp","security":"none"}`, // drifted on 3X-UI
		Settings:       `{"decryption":"none","clients":[{"id":"x"}]}`,
	}
	client := &recClient{inbounds: []ports.Inbound{live}, getResp: &live}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	audit := &recAudit{}
	svc := &Service{nodes: repo, pool: recPool{c: client}, audit: audit} // axisAReversePush defaults false

	report := &Report{}
	svc.checkNodes(context.Background(), report, cacheFromInbounds(1, []ports.Inbound{live}), nil)

	if len(client.updated) != 0 {
		t.Fatalf("reverse-push disabled: must NOT write to the 3X-UI inbound, got %d UpdateInbound calls", len(client.updated))
	}
	if got := audit.findAuditByAction("inbound_config_drift_adopted"); got == nil {
		t.Fatalf("expected drift-adopted audit, got entries=%+v", audit.entries)
	}
	if len(repo.updatesCfg) == 0 {
		t.Fatalf("adopt must write the snapshot via UpdateInboundConfig")
	}
	last := repo.updatesCfg[len(repo.updatesCfg)-1]
	if last.StreamSettings != `{"network":"tcp","security":"none"}` {
		t.Fatalf("adopt must capture the LIVE config into the snapshot, got %q", last.StreamSettings)
	}
	if last.ConfigSyncState != "synced" {
		t.Fatalf("adopt must converge the snapshot to synced, got %q", last.ConfigSyncState)
	}
	if report.Fixed != 1 {
		t.Fatalf("want report.Fixed=1, got %d", report.Fixed)
	}
}

// Regression: admin write that lands between List() and the per-node push
// must NOT have its edit reverted by reconcile. The fresh row's
// ConfigSyncedAt differs from the cached row's stamp; reconcile must skip.
func TestCheckNodes_StaleReadDoesNotRevertAdminEdit(t *testing.T) {
	cachedStamp := time.Now().Add(-1 * time.Hour) // what reconcile pulled at top of cycle
	freshStamp := time.Now()                      // what admin just wrote
	cached := &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3, Enabled: true,
		Protocol:        "vless",
		Port:            443,
		StreamSettings:  `{"network":"ws","security":"tls"}`, // PSP's *old* truth
		InboundSettings: `{"decryption":"none"}`,
		ConfigSyncedAt:  &cachedStamp,
	}
	live := ports.Inbound{
		ID: 3, Protocol: "vless", Port: 443,
		StreamSettings: `{"network":"tcp","security":"reality"}`, // admin just pushed this
		Settings:       `{"decryption":"none","clients":[{"id":"x"}]}`,
	}
	client := &recClient{inbounds: []ports.Inbound{live}, getResp: &live}
	repo := &recNodeRepo{nodes: []*domain.Node{cached}}
	// Simulate the admin write: GetByID returns the freshly-stamped row
	// while the in-flight reconcile iteration still holds `cached`.
	repo.getOverride = func(id int64) (*domain.Node, error) {
		fresh := *cached
		fresh.StreamSettings = `{"network":"tcp","security":"reality"}`
		fresh.ConfigSyncedAt = &freshStamp
		return &fresh, nil
	}
	svc := &Service{nodes: repo, pool: recPool{c: client}, axisAReversePush: true}

	report := &Report{}
	svc.checkNodes(context.Background(), report, cacheFromInbounds(1, []ports.Inbound{live}), nil)

	if len(client.updated) != 0 {
		t.Fatalf("stale reconcile must NOT push to 3X-UI when admin row advanced; got %d pushes", len(client.updated))
	}
	if len(repo.updates)+len(repo.updatesCfg) != 0 {
		t.Fatalf("stale reconcile must NOT write back to DB; got %d full-row + %d snapshot updates",
			len(repo.updates), len(repo.updatesCfg))
	}
	if report.Fixed != 0 {
		t.Fatalf("stale reconcile must not mark Fixed; got %d", report.Fixed)
	}
}

// Node whose snapshot already matches the live inbound (modulo clients[] and
// key ordering) → no push, no node write.
func TestCheckNodes_InSync_NoOp(t *testing.T) {
	now := time.Now()
	node := &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3, Enabled: true,
		Protocol:        "vless",
		Port:            443,
		StreamSettings:  `{"security":"tls","network":"ws"}`, // key order differs only
		InboundSettings: `{"decryption":"none"}`,
		ConfigSyncedAt:  &now,
	}
	live := ports.Inbound{
		ID: 3, Protocol: "vless", Port: 443,
		StreamSettings: `{"network":"ws","security":"tls"}`,
		Settings:       `{"decryption":"none","clients":[{"id":"x"}]}`,
	}
	client := &recClient{inbounds: []ports.Inbound{live}}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	svc := &Service{nodes: repo, pool: recPool{c: client}, axisAReversePush: true}

	report := &Report{}
	svc.checkNodes(context.Background(), report, cacheFromInbounds(1, []ports.Inbound{live}), nil)

	if len(client.updated) != 0 || len(repo.updates)+len(repo.updatesCfg) != 0 {
		t.Fatalf("in-sync node must be a no-op: pushes=%d full-row=%d snapshot=%d",
			len(client.updated), len(repo.updates), len(repo.updatesCfg))
	}
	if report.Fixed != 0 {
		t.Fatalf("want report.Fixed=0, got %d", report.Fixed)
	}
}

// ---- A: per-event audit + ConfigSyncState pending on failure ----

// Backfill emits an audit row so the admin / dashboard can see which nodes were
// captured in this cycle (in addition to the cycle-aggregate row).
func TestCheckNodes_BackfillEmitsAudit(t *testing.T) {
	node := &domain.Node{ID: 1, PanelID: 1, InboundID: 3, Enabled: true} // never captured
	live := []ports.Inbound{{ID: 3, Protocol: "vless", Port: 443, StreamSettings: `{"network":"tcp"}`, Settings: `{"decryption":"none","clients":[{"id":"x"}]}`}}
	client := &recClient{inbounds: live}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	audit := &recAudit{}
	svc := &Service{nodes: repo, pool: recPool{c: client}, audit: audit, axisAReversePush: true}

	svc.checkNodes(context.Background(), &Report{}, cacheFromInbounds(1, live), nil)

	if got := audit.findAuditByAction("inbound_config_backfilled"); got == nil {
		t.Fatalf("expected per-inbound backfill audit, got entries=%+v", audit.entries)
	}
}

// Drift-push emits an audit row and leaves the snapshot in synced (Capture sets
// it from the post-push re-capture).
func TestCheckNodes_DriftPushedEmitsAudit(t *testing.T) {
	now := time.Now()
	node := &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3, Enabled: true,
		Protocol: "vless", Port: 443,
		StreamSettings:  `{"network":"ws"}`,
		InboundSettings: `{"decryption":"none"}`,
		ConfigSyncedAt:  &now, ConfigSyncState: "synced",
	}
	live := ports.Inbound{ID: 3, Protocol: "vless", Port: 443, StreamSettings: `{"network":"tcp"}`, Settings: `{"decryption":"none","clients":[]}`}
	client := &recClient{inbounds: []ports.Inbound{live}, getResp: &live}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	audit := &recAudit{}
	svc := &Service{nodes: repo, pool: recPool{c: client}, audit: audit, axisAReversePush: true}

	svc.checkNodes(context.Background(), &Report{}, cacheFromInbounds(1, []ports.Inbound{live}), nil)

	if got := audit.findAuditByAction("inbound_config_drift_pushed"); got == nil {
		t.Fatalf("expected per-inbound drift-push audit, got entries=%+v", audit.entries)
	}
	// Post-push Capture must leave the snapshot synced (not pending).
	last := repo.updatesCfg[len(repo.updatesCfg)-1]
	if last.ConfigSyncState != "synced" {
		t.Fatalf("after successful drift push the snapshot must read synced, got %q", last.ConfigSyncState)
	}
}

// Push failure: snapshot flips to pending so the UI can surface "PSP wants
// this config but couldn't deliver it", and the failure is auditable.
func TestCheckNodes_PushFailMarksPendingAndAudits(t *testing.T) {
	now := time.Now()
	node := &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3, Enabled: true,
		Protocol: "vless", Port: 443,
		StreamSettings:  `{"network":"ws"}`,
		InboundSettings: `{"decryption":"none"}`,
		ConfigSyncedAt:  &now, ConfigSyncState: "synced",
	}
	live := ports.Inbound{ID: 3, Protocol: "vless", Port: 443, StreamSettings: `{"network":"tcp"}`, Settings: `{"decryption":"none","clients":[]}`}
	client := &recClient{inbounds: []ports.Inbound{live}, getResp: &live, updateErr: errPushFail{}}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	audit := &recAudit{}
	svc := &Service{nodes: repo, pool: recPool{c: client}, audit: audit, axisAReversePush: true}

	svc.checkNodes(context.Background(), &Report{}, cacheFromInbounds(1, []ports.Inbound{live}), nil)

	if got := audit.findAuditByAction("inbound_config_push_failed"); got == nil {
		t.Fatalf("expected push-failed audit, got entries=%+v", audit.entries)
	}
	// markConfigSyncStatePending must write through to the snapshot column.
	var sawPending bool
	for _, u := range repo.updatesCfg {
		if u.ConfigSyncState == "pending" {
			sawPending = true
			break
		}
	}
	if !sawPending {
		t.Fatalf("push fail must flip ConfigSyncState to pending; snapshot writes=%+v", repo.updatesCfg)
	}
}

// Re-capture failure: push went through but the converging GetInbound failed.
// Same pending-state + audit treatment (and we don't loop on the next cycle —
// the in-sync compare on stale data still triggers another push, but it's
// idempotent; the user sees the pending state until 3X-UI is reachable again).
func TestCheckNodes_RecaptureFailMarksPendingAndAudits(t *testing.T) {
	now := time.Now()
	node := &domain.Node{
		ID: 1, PanelID: 1, InboundID: 3, Enabled: true,
		Protocol: "vless", Port: 443,
		StreamSettings:  `{"network":"ws"}`,
		InboundSettings: `{"decryption":"none"}`,
		ConfigSyncedAt:  &now, ConfigSyncState: "synced",
	}
	live := ports.Inbound{ID: 3, Protocol: "vless", Port: 443, StreamSettings: `{"network":"tcp"}`, Settings: `{"decryption":"none","clients":[]}`}
	client := &recClient{inbounds: []ports.Inbound{live}, getErr: errRecaptureFail{}}
	repo := &recNodeRepo{nodes: []*domain.Node{node}}
	audit := &recAudit{}
	svc := &Service{nodes: repo, pool: recPool{c: client}, audit: audit, axisAReversePush: true}

	svc.checkNodes(context.Background(), &Report{}, cacheFromInbounds(1, []ports.Inbound{live}), nil)

	if got := audit.findAuditByAction("inbound_config_recapture_failed"); got == nil {
		t.Fatalf("expected recapture-failed audit, got entries=%+v", audit.entries)
	}
	var sawPending bool
	for _, u := range repo.updatesCfg {
		if u.ConfigSyncState == "pending" {
			sawPending = true
			break
		}
	}
	if !sawPending {
		t.Fatalf("recapture fail must flip ConfigSyncState to pending; snapshot writes=%+v", repo.updatesCfg)
	}
}

type errPushFail struct{}

func (errPushFail) Error() string { return "panel returned 5xx" }

type errRecaptureFail struct{}

func (errRecaptureFail) Error() string { return "panel went away post-push" }
