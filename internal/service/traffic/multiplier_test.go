package traffic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// lineNodes is a node repository whose nodes carry line factors; err makes
// the per-cycle factor read fail.
type lineNodes struct {
	*fakeNodeRepo
	err error
}

func (r *lineNodes) List(ctx context.Context) ([]*domain.Node, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.fakeNodeRepo.List(ctx)
}

// factorNodes builds the node of panel 10 / inbound 20 (id 5) whose direct
// line costs direct and whose relay line "r1" costs relay (nil = default).
func factorNodes(direct, relay *int) *lineNodes {
	n := &domain.Node{ID: 5, PanelID: 10, InboundID: 20, TrafficFactorMilli: direct,
		Relays: []domain.RelayLine{{ID: "r1", Enabled: true, TrafficFactorMilli: relay}}}
	return &lineNodes{fakeNodeRepo: &fakeNodeRepo{nodes: map[int64]*domain.Node{5: n}, byMatch: map[fakeNodeKey]int64{{panelID: 10, inboundID: 20}: 5}}}
}

func milli(v int) *int { return &v }

// sharedPoll drives one PollOnce for a migrated user (shared client only) on
// panel 10 whose counter moves from 100/100 to up/down: a delta of
// (up-100) + (down-100).
type sharedPoll struct {
	users    *fakeUserRepo
	client   *domain.PSPClient
	psp      *fakePSPClientRepo
	stats    *fakeXUIClient
	disabler *suspendCapture
	svc      *Service
}

func newSharedPoll(t *testing.T, limit int64, nodes *lineNodes) *sharedPoll {
	t.Helper()
	p := &sharedPoll{
		users: &fakeUserRepo{users: map[int64]*domain.User{
			1: {ID: 1, Enabled: true, TrafficLimitBytes: limit},
		}},
		client: &domain.PSPClient{ID: 1, UserID: 1, PanelID: 10, NodeID: 5, LineID: domain.DirectLineID, Email: "u1@psp.local",
			LastRawUpBytes: 100, LastRawDownBytes: 100, LastRawTotalBytes: 200},
		stats:    &fakeXUIClient{},
		disabler: &suspendCapture{},
	}
	p.psp = &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{1: {p.client}}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{10: p.stats}}
	var repo ports.NodeRepo
	if nodes != nil {
		repo = nodes
	}
	p.svc = New(p.users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, &fakeTrafficRepo{}, repo, nil, pool, p.disabler)
	p.svc.SetPSPClientRepo(p.psp)
	return p
}

func (p *sharedPoll) poll(t *testing.T, up, down int64) {
	t.Helper()
	p.stats.inbounds = []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{{Email: p.client.Email, Up: up, Down: down}}}}
	if err := p.svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
}

func (p *sharedPoll) user() *domain.User { return p.users.users[1] }

// At the default factor the multiplier must not change a single figure, with
// or without a factor reader wired.
func TestPollOnce_DefaultFactorChangesNothing(t *testing.T) {
	for name, nodes := range map[string]*lineNodes{
		"no node repo":   nil,
		"explicit 1000":  factorNodes(milli(1000), nil),
		"factors absent": factorNodes(nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			p := newSharedPoll(t, 0, nodes)
			p.poll(t, 700, 500) // delta 600 up + 400 down
			u := p.user()
			if u.LifetimeUpBytes != 600 || u.LifetimeDownBytes != 400 || u.LifetimeTotalBytes != 1000 {
				t.Fatalf("charged ledger = %d/%d/%d, want 600/400/1000", u.LifetimeUpBytes, u.LifetimeDownBytes, u.LifetimeTotalBytes)
			}
			if u.RawLifetimeUpBytes != 600 || u.RawLifetimeDownBytes != 400 || u.RawLifetimeTotalBytes != 1000 {
				t.Fatalf("raw ledger = %d/%d/%d, want 600/400/1000", u.RawLifetimeUpBytes, u.RawLifetimeDownBytes, u.RawLifetimeTotalBytes)
			}
			if p.client.LifetimeTotalBytes != 1000 || p.client.ChargedLifetimeBytes != 1000 {
				t.Fatalf("row lifetime raw=%d charged=%d, want 1000/1000", p.client.LifetimeTotalBytes, p.client.ChargedLifetimeBytes)
			}
		})
	}
}

// A ×1.5 line charges the user 1.5× and keeps the raw figure beside it.
// This is also the mutation check for the booking line: booking the RAW delta
// into the user's quota ledger fails the first assertion.
func TestPollOnce_WeightedServerChargesTheUser(t *testing.T) {
	p := newSharedPoll(t, 1400, factorNodes(milli(1500), nil))
	p.poll(t, 700, 500) // raw 600 + 400 = 1000

	u := p.user()
	if u.LifetimeUpBytes != 900 || u.LifetimeDownBytes != 600 || u.LifetimeTotalBytes != 1500 {
		t.Fatalf("charged ledger = %d/%d/%d, want 900/600/1500", u.LifetimeUpBytes, u.LifetimeDownBytes, u.LifetimeTotalBytes)
	}
	if u.RawLifetimeTotalBytes != 1000 {
		t.Fatalf("raw lifetime = %d, want 1000", u.RawLifetimeTotalBytes)
	}
	// The per-row ledger stays raw and gains the charged figure.
	if p.client.LifetimeTotalBytes != 1000 || p.client.ChargedLifetimeBytes != 1500 {
		t.Fatalf("row raw=%d charged=%d, want 1000/1500", p.client.LifetimeTotalBytes, p.client.ChargedLifetimeBytes)
	}
	// The charged figure is what persists: the client rides the counter flush.
	if len(p.psp.batchUpdated) != 1 || p.psp.batchUpdated[0].ChargedLifetimeBytes != 1500 || p.psp.batchUpdated[0].LastFactor() != 1500 {
		t.Fatalf("flushed rows = %+v, want the client with charged 1500", p.psp.batchUpdated)
	}
	// 1000 raw bytes < 1400 limit, but 1500 charged bytes are over it.
	if p.disabler.suspendedUser != 1 || p.disabler.suspendedReason != domain.DisabledTrafficExceeded {
		t.Fatalf("want user suspended on the charged figure, got user=%d reason=%q", p.disabler.suspendedUser, p.disabler.suspendedReason)
	}
}

// A free line books raw traffic and charges nothing.
func TestPollOnce_FreeLineChargesNothing(t *testing.T) {
	p := newSharedPoll(t, 500, factorNodes(milli(0), nil))
	p.poll(t, 700, 500)

	u := p.user()
	if u.LifetimeTotalBytes != 0 || u.PeriodUsed() != 0 {
		t.Fatalf("charged lifetime = %d, period = %d, want 0 on a free server", u.LifetimeTotalBytes, u.PeriodUsed())
	}
	if u.RawLifetimeTotalBytes != 1000 || p.client.LifetimeTotalBytes != 1000 || p.client.ChargedLifetimeBytes != 0 {
		t.Fatalf("raw user=%d row raw=%d row charged=%d, want 1000/1000/0", u.RawLifetimeTotalBytes, p.client.LifetimeTotalBytes, p.client.ChargedLifetimeBytes)
	}
	if p.disabler.suspendedUser != 0 {
		t.Fatalf("free traffic over the raw limit suspended user %d", p.disabler.suspendedUser)
	}
}

// Changing a factor re-prices only traffic booked afterwards.
func TestPollOnce_FactorChangeAffectsOnlyLaterTraffic(t *testing.T) {
	nodes := factorNodes(milli(1000), nil)
	p := newSharedPoll(t, 0, nodes)
	p.poll(t, 700, 500) // 1000 raw at ×1
	nodes.nodes[5].TrafficFactorMilli = milli(2000)
	p.poll(t, 900, 800) // 200 + 300 = 500 raw at ×2

	u := p.user()
	if u.LifetimeTotalBytes != 1000+1000 {
		t.Fatalf("charged lifetime = %d, want 2000 (1000 at ×1, then 500 at ×2)", u.LifetimeTotalBytes)
	}
	if u.RawLifetimeTotalBytes != 1500 || p.client.ChargedLifetimeBytes != 2000 {
		t.Fatalf("raw=%d row charged=%d, want 1500/2000", u.RawLifetimeTotalBytes, p.client.ChargedLifetimeBytes)
	}
}

// A factor read failure abandons the cycle before anything is booked, so the
// next cycle books the whole delta at the right price.
func TestPollOnce_FactorReadFailureBooksNothing(t *testing.T) {
	nodes := factorNodes(milli(1500), nil)
	nodes.err = errors.New("db down")
	p := newSharedPoll(t, 0, nodes)
	p.stats.inbounds = []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{{Email: p.client.Email, Up: 700, Down: 500}}}}
	if err := p.svc.PollOnce(context.Background()); err == nil {
		t.Fatal("PollOnce succeeded without traffic factors")
	}
	if p.user().LifetimeTotalBytes != 0 || p.client.LastRawTotalBytes != 200 {
		t.Fatalf("booked through a failed factor read: user=%d last raw=%d", p.user().LifetimeTotalBytes, p.client.LastRawTotalBytes)
	}
	nodes.err = nil
	p.poll(t, 700, 500)
	if p.user().LifetimeTotalBytes != 1500 {
		t.Fatalf("charged lifetime after recovery = %d, want 1500", p.user().LifetimeTotalBytes)
	}
}

// A legacy per-node ownership row is its node's direct line, so it is
// weighted by that line, and keeps its per-row counters raw.
func TestPollOnce_LegacyOwnershipIsWeightedByItsDirectLine(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour)
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, LifetimeBaselineAt: &old},
	}}
	cheap := &domain.XUIClientEntry{ID: 1, UserID: 1, PanelID: 10, InboundID: 20, ClientEmail: "a@x",
		LastRawUpBytes: 1, LastRawDownBytes: 1, LastRawTotalBytes: 2, CreatedAt: old}
	dear := &domain.XUIClientEntry{ID: 2, UserID: 1, PanelID: 11, InboundID: 30, ClientEmail: "b@x",
		LastRawUpBytes: 1, LastRawDownBytes: 1, LastRawTotalBytes: 2, CreatedAt: old}
	ownership := &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{1: {cheap, dear}}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{
		10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{{Email: "a@x", Up: 101, Down: 101}}}}},
		11: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 30, ClientStats: []ports.ClientTraffic{{Email: "b@x", Up: 101, Down: 101}}}}},
	}}
	nodes := &fakeNodeRepo{nodes: map[int64]*domain.Node{
		1: {ID: 1, PanelID: 10, InboundID: 20, TrafficFactorMilli: milli(500)},
		2: {ID: 2, PanelID: 11, InboundID: 30, TrafficFactorMilli: milli(3000)},
	}, byMatch: map[fakeNodeKey]int64{{panelID: 10, inboundID: 20}: 1, {panelID: 11, inboundID: 30}: 2}}
	svc := New(users, ownership, &fakeTrafficRepo{}, nodes, nil, pool, &fakeDisabler{})
	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	u := users.users[1]
	// 200 raw at ×0.5 + 200 raw at ×3.
	if u.LifetimeTotalBytes != 100+600 || u.RawLifetimeTotalBytes != 400 {
		t.Fatalf("user charged=%d raw=%d, want 700/400", u.LifetimeTotalBytes, u.RawLifetimeTotalBytes)
	}
	// Read back what the batched counter flush persisted.
	cheap, dear = ownership.byUser[1][0], ownership.byUser[1][1]
	if cheap.LifetimeTotalBytes != 200 || cheap.ChargedLifetimeBytes != 100 ||
		dear.LifetimeTotalBytes != 200 || dear.ChargedLifetimeBytes != 600 {
		t.Fatalf("rows: cheap %d/%d dear %d/%d, want 200/100 and 200/600",
			cheap.LifetimeTotalBytes, cheap.ChargedLifetimeBytes, dear.LifetimeTotalBytes, dear.ChargedLifetimeBytes)
	}
}

// The usage reseed takes its fraction against the CHARGED per-row ledgers,
// because the override it distributes is charged.
func TestSetPeriodUsage_ReseedUsesChargedLedgers(t *testing.T) {
	users := &fakeUserRepo{users: map[int64]*domain.User{
		1: {ID: 1, Enabled: true, LifetimeTotalBytes: 3000, RawLifetimeTotalBytes: 2000},
	}}
	// Raw 1000 on each row; the first was charged ×2, the second ×1.
	a := &domain.PSPClient{ID: 1, UserID: 1, PanelID: 10, LifetimeUpBytes: 400, LifetimeDownBytes: 600, LifetimeTotalBytes: 1000, ChargedLifetimeBytes: 2000}
	b := &domain.PSPClient{ID: 2, UserID: 1, PanelID: 11, LifetimeUpBytes: 400, LifetimeDownBytes: 600, LifetimeTotalBytes: 1000, ChargedLifetimeBytes: 1000}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{1: {a, b}}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, &fakeTrafficRepo{}, nil, nil, nil, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)

	if err := svc.SetPeriodUsage(context.Background(), 1, 1500); err != nil {
		t.Fatalf("SetPeriodUsage: %v", err)
	}
	// f = (3000 charged − 1500 used) / 3000 = 0.5 of each row's raw lifetime.
	// A raw fraction would be (2000 − 1500) / 2000 = 0.25.
	for _, c := range []*domain.PSPClient{a, b} {
		if c.PeriodBaselineTotalBytes != 500 || c.PeriodBaselineUpBytes != 200 || c.PeriodBaselineDownBytes != 300 {
			t.Fatalf("client %d baselines = %d/%d/%d, want 200/300/500", c.ID,
				c.PeriodBaselineUpBytes, c.PeriodBaselineDownBytes, c.PeriodBaselineTotalBytes)
		}
	}
}

// Two lines on one server have their own counters and their own prices: the
// direct line at ×1 and a relay line at ×3, booked separately.
func TestPollOnce_LinesOnOneServerAreBookedAtTheirOwnFactor(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour)
	users := &fakeUserRepo{users: map[int64]*domain.User{1: {ID: 1, Enabled: true, LifetimeBaselineAt: &old}}}
	direct := &domain.PSPClient{ID: 1, UserID: 1, PanelID: 10, NodeID: 5, LineID: domain.DirectLineID, Email: "u1-ld@x",
		LastRawUpBytes: 1, LastRawDownBytes: 1, LastRawTotalBytes: 2}
	viaRelay := &domain.PSPClient{ID: 2, UserID: 1, PanelID: 10, NodeID: 5, LineID: "r1", Email: "u1-lr@x",
		LastRawUpBytes: 1, LastRawDownBytes: 1, LastRawTotalBytes: 2}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{1: {direct, viaRelay}}}
	pool := &fakeXUIPool{clients: map[int64]ports.XUIClient{10: &fakeXUIClient{inbounds: []ports.Inbound{{ID: 20, ClientStats: []ports.ClientTraffic{
		{Email: "u1-ld@x", Up: 101, Down: 101}, {Email: "u1-lr@x", Up: 51, Down: 51},
	}}}}}}
	svc := New(users, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, &fakeTrafficRepo{}, factorNodes(nil, milli(3000)), nil, pool, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)
	if err := svc.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	u := users.users[1]
	if u.LifetimeTotalBytes != 200+300 || u.RawLifetimeTotalBytes != 300 {
		t.Fatalf("user charged=%d raw=%d, want 500/300", u.LifetimeTotalBytes, u.RawLifetimeTotalBytes)
	}
	if direct.ChargedLifetimeBytes != 200 || viaRelay.ChargedLifetimeBytes != 300 || viaRelay.LastFactor() != 3000 {
		t.Fatalf("rows direct=%d relay=%d (factor %d)", direct.ChargedLifetimeBytes, viaRelay.ChargedLifetimeBytes, viaRelay.LastFactor())
	}
}

// A row whose line was deleted is still booked, at the price its line last had.
func TestPollOnce_DeletedLineBooksAtItsLastFactor(t *testing.T) {
	p := newSharedPoll(t, 0, factorNodes(nil, nil))
	p.client.LineID = "gone"
	p.client.LastFactorMilli = milli(2000)
	p.poll(t, 700, 500)
	if p.user().LifetimeTotalBytes != 2000 {
		t.Fatalf("charged = %d, want 1000 raw at the last ×2", p.user().LifetimeTotalBytes)
	}
}

// Usage pages report raw and charged per server, with the factor while every
// line there agrees, and the period's charge is the row's lifetime price
// applied to its raw period.
func TestUserServerUsage_ReportsRawChargedAndFactor(t *testing.T) {
	a := &domain.PSPClient{ID: 1, UserID: 1, PanelID: 10, NodeID: 5, LineID: domain.DirectLineID,
		LifetimeTotalBytes: 1000, ChargedLifetimeBytes: 2000, PeriodBaselineTotalBytes: 600}
	psp := &fakePSPClientRepo{byUser: map[int64][]*domain.PSPClient{1: {a}}}
	svc := New(&fakeUserRepo{users: map[int64]*domain.User{}}, &fakeOwnershipRepo{byUser: map[int64][]*domain.XUIClientEntry{}}, &fakeTrafficRepo{}, factorNodes(milli(2000), milli(3000)), nil, &fakeXUIPool{}, &fakeDisabler{})
	svc.SetPSPClientRepo(psp)

	rows, err := svc.UserServerUsage(context.Background(), 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
	r := rows[0]
	if r.TrafficFactorMilli == nil || *r.TrafficFactorMilli != 2000 || r.LifetimeTotalBytes != 1000 || r.ChargedLifetimeBytes != 2000 ||
		r.PeriodTotalBytes != 400 || r.ChargedPeriodBytes != 800 {
		t.Fatalf("row = %+v, want factor 2000, raw 1000/400, charged 2000/800", r)
	}
	// A second line on the same server at another price: no single factor.
	psp.byUser[1] = append(psp.byUser[1], &domain.PSPClient{ID: 2, UserID: 1, PanelID: 10, NodeID: 5, LineID: "r1"})
	rows, _ = svc.UserServerUsage(context.Background(), 1)
	if rows[0].TrafficFactorMilli != nil {
		t.Fatalf("mixed lines reported factor %d", *rows[0].TrafficFactorMilli)
	}
}

func TestChargedShare(t *testing.T) {
	cases := []struct{ part, rawLife, chargedLife, want int64 }{
		{400, 1000, 1000, 400},
		{400, 1000, 2000, 800},
		{400, 1000, 0, 0},
		{0, 1000, 2000, 0},
		{1200, 1000, 2000, 2000}, // never more than the row was charged
		{400, 0, 2000, 0},
	}
	for _, c := range cases {
		if got := chargedShare(c.part, c.rawLife, c.chargedLife); got != c.want {
			t.Errorf("chargedShare(%d, %d, %d) = %d, want %d", c.part, c.rawLife, c.chargedLife, got, c.want)
		}
	}
}

// detachingRepo reports some rows as detached and records deletions.
type detachingRepo struct {
	*fakePSPClientRepo
	detached map[int64]bool
	deleted  []int64
}

func (r *detachingRepo) DetachedClientIDs(context.Context) (map[int64]bool, error) {
	return r.detached, nil
}
func (r *detachingRepo) DeleteIfDetached(_ context.Context, id int64) error {
	r.deleted = append(r.deleted, id)
	return nil
}

// A dropped line's row is booked one last time and then deleted; a row
// created moments ago, one whose server could not be read, and one whose
// panel-side client its server still reports are kept.
func TestPollOnce_DeletesDetachedRowAfterBookingIt(t *testing.T) {
	p := newSharedPoll(t, 0, factorNodes(milli(2000), nil))
	p.client.CreatedAt = time.Now().Add(-time.Hour)
	fresh := &domain.PSPClient{ID: 2, UserID: 1, PanelID: 10, NodeID: 5, LineID: "r1", Email: "fresh@x", CreatedAt: time.Now()}
	unread := &domain.PSPClient{ID: 3, UserID: 1, PanelID: 99, NodeID: 5, LineID: "r9", Email: "unread@x", CreatedAt: time.Now().Add(-time.Hour)}
	gone := &domain.PSPClient{ID: 4, UserID: 1, PanelID: 10, NodeID: 5, LineID: "r2", Email: "gone@x", CreatedAt: time.Now().Add(-time.Hour)}
	p.psp.byUser[1] = append(p.psp.byUser[1], fresh, unread, gone)
	repo := &detachingRepo{fakePSPClientRepo: p.psp, detached: map[int64]bool{1: true, 2: true, 3: true, 4: true}}
	p.svc.SetPSPClientRepo(repo)

	p.poll(t, 700, 500)
	if p.user().LifetimeTotalBytes != 2000 {
		t.Fatalf("final delta not booked before deletion: %d", p.user().LifetimeTotalBytes)
	}
	// Row 1's client is still reported by panel 10 (its removal upstream has
	// not happened): it stays and keeps being booked. Row 4's is gone.
	if len(repo.deleted) != 1 || repo.deleted[0] != 4 {
		t.Fatalf("deleted = %v, want only row 4, whose client the panel no longer reports", repo.deleted)
	}
}
