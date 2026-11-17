package linkprobe

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakeSettings struct{ s ports.UISettings }

func (f *fakeSettings) Load(_ context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	out := f.s
	out.NodePollSeconds, out.FullReportSeconds = defaults.NodePollSeconds, defaults.FullReportSeconds
	return out, nil
}
func (f *fakeSettings) LoadForGroup(context.Context, int64, ports.UISettings) (ports.UISettings, error) {
	return f.s, nil
}
func (f *fakeSettings) LoadForUser(context.Context, *domain.User, ports.UISettings) (ports.UISettings, error) {
	return f.s, nil
}

type fakeRepo struct {
	mu   sync.Mutex
	rows map[[3]string]domain.ProbeState
}

func key(panel int64, k, kind string) [3]string { return [3]string{string(rune(panel + '0')), k, kind} }

func (r *fakeRepo) Apply(_ context.Context, panelID int64, k, kind string,
	update func(prev *domain.ProbeState) (domain.ProbeState, bool, error)) (*domain.ProbeState, domain.ProbeState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rows == nil {
		r.rows = map[[3]string]domain.ProbeState{}
	}
	var prev *domain.ProbeState
	if row, ok := r.rows[key(panelID, k, kind)]; ok {
		prev = &row
	}
	next, write, err := update(prev)
	if err != nil {
		return nil, domain.ProbeState{}, err
	}
	if !write {
		if prev != nil {
			return prev, *prev, nil
		}
		return nil, domain.ProbeState{}, nil
	}
	next.PanelID, next.ObjectKey, next.Kind = panelID, k, kind
	r.rows[key(panelID, k, kind)] = next
	return prev, next, nil
}
func (r *fakeRepo) List(context.Context) ([]domain.ProbeState, error) {
	var out []domain.ProbeState
	for _, row := range r.rows {
		out = append(out, row)
	}
	return out, nil
}
func (r *fakeRepo) PruneExcept(_ context.Context, panelID int64, keep []string) (int64, error) {
	var n int64
	for k, row := range r.rows {
		if row.PanelID == panelID && !slices.Contains(keep, row.ObjectKey) {
			delete(r.rows, k)
			n++
		}
	}
	return n, nil
}
func (r *fakeRepo) ClearWithheld(context.Context) (int64, error) { return 0, nil }

var clock = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

func sentTransit() *nodeprotocol.Transit {
	return &nodeprotocol.Transit{
		Forwards: []nodeprotocol.Forward{{Key: "fwd_1", Port: 20443, Target: "203.0.113.9", TargetPort: 443, Network: nodeprotocol.ForwardNetworkTCPUDP}},
		LinksOut: []nodeprotocol.LinkOut{{Key: "lnk_2"}},
		LinksIn:  []nodeprotocol.LinkIn{{Key: "lnk_3"}},
		Probe: &nodeprotocol.ProbeSettings{IntervalSeconds: 60, TimeoutMS: 5000, LinkURL: "https://x.example/",
			Forwards: []nodeprotocol.ProbeTarget{{Key: "fwd_1", Method: "tcp"}}},
	}
}

func result(k, kind string, attempts, failures int) nodeprotocol.ProbeResult {
	r := nodeprotocol.ProbeResult{Key: k, Kind: kind, Attempts: attempts, Failures: failures}
	if failures < attempts {
		r.RTTMinMS, r.RTTMedianMS, r.RTTMaxMS = 10, 20, 30
	}
	return r
}

func TestAccepted(t *testing.T) {
	sent := sentTransit()
	all := []nodeprotocol.ProbeResult{
		result("lnk_2", "e2e", 20, 0),       // dialed link: kept
		result("lnk_2", "transport", 20, 0), // kept
		result("lnk_3", "e2e", 20, 0),       // the exit's half: dropped
		result("lnk_3", "transport", 20, 0),
		result("lnk_9", "e2e", 20, 0), // never sent
		result("fwd_1", "forward", 20, 0),
		result("fwd_2", "forward", 20, 0), // never sent
	}
	kept, dropped := Accepted(sent, all)
	var got []string
	for _, r := range kept {
		got = append(got, r.Key+"/"+r.Kind)
	}
	if want := []string{"lnk_2/e2e", "lnk_2/transport", "fwd_1/forward"}; !slices.Equal(got, want) || dropped != 4 {
		t.Fatalf("kept %v dropped %d, want %v and 4", got, dropped, want)
	}

	// Without a link URL no e2e result was asked for.
	noURL := sentTransit()
	noURL.Probe.LinkURL = ""
	kept, _ = Accepted(noURL, all)
	for _, r := range kept {
		if r.Kind == "e2e" {
			t.Fatalf("an e2e result was kept although no URL was sent: %+v", r)
		}
	}
	// A forward in the config but not named by the probe block is not probed.
	unnamed := sentTransit()
	unnamed.Probe.Forwards = nil
	if kept, _ := Accepted(unnamed, []nodeprotocol.ProbeResult{result("fwd_1", "forward", 20, 0)}); len(kept) != 0 {
		t.Fatalf("a forward the block did not name was kept: %+v", kept)
	}
	// No probe block, or no config, keeps nothing.
	none := sentTransit()
	none.Probe = nil
	if kept, dropped := Accepted(none, all); len(kept) != 0 || dropped != len(all) {
		t.Fatalf("results without a probe block were kept: %+v", kept)
	}
	if kept, _ := Accepted(nil, all); len(kept) != 0 {
		t.Fatal("results without a config were kept")
	}
}

func service(s ports.UISettings, repo *fakeRepo) *Service {
	return New(&fakeSettings{s: s}, repo, func() time.Time { return clock })
}

var caps = []string{nodeprotocol.CapabilityTransitProbe}

func TestConfigGate(t *testing.T) {
	transit := &nodeprotocol.Transit{
		Forwards: []nodeprotocol.Forward{{Key: "fwd_1", Port: 20443, Target: "203.0.113.9", TargetPort: 443, Network: nodeprotocol.ForwardNetworkTCPUDP}},
	}
	ctx := context.Background()
	svc := service(ports.UISettings{}, &fakeRepo{})

	if probe, _ := svc.Config(ctx, nil, transit, nil); probe != nil {
		t.Fatal("a block was built for an agent that did not declare the capability")
	}
	if probe, _ := svc.Config(ctx, []string{nodeprotocol.CapabilityTransitForward}, transit, nil); probe != nil {
		t.Fatal("a block was built for an agent that did not declare the capability")
	}
	if probe, _ := svc.Config(ctx, caps, &nodeprotocol.Transit{LinksIn: []nodeprotocol.LinkIn{{Key: "lnk_3"}}}, nil); probe != nil {
		t.Fatal("a block was built for a server with nothing to probe")
	}
	if probe, _ := svc.Config(ctx, caps, nil, nil); probe != nil {
		t.Fatal("a block was built without a transit body")
	}
	if probe, _ := service(ports.UISettings{TransitProbeOff: true}, &fakeRepo{}).Config(ctx, caps, transit, nil); probe != nil {
		t.Fatal("a block was built with probing off")
	}
	probe, err := svc.Config(ctx, caps, transit, map[nodeprotocol.ForwardKey]string{"fwd_1": nodeprotocol.ProbeMethodQUIC})
	if err != nil || probe == nil || probe.IntervalSeconds != 60 || probe.TimeoutMS != 5000 || probe.LinkURL != "" ||
		len(probe.Forwards) != 1 || probe.Forwards[0].Method != "quic" {
		t.Fatalf("probe = %+v, %v", probe, err)
	}
	// The method the contract would refuse (tls without a front) is withheld
	// rather than sent: it would fail the whole config.
	if probe, _ := svc.Config(ctx, caps, transit, map[nodeprotocol.ForwardKey]string{"fwd_1": nodeprotocol.ProbeMethodTLS}); probe != nil {
		t.Fatalf("a block the contract refuses was built: %+v", probe)
	}
}

// Without a link URL no e2e result is accepted: links are probed
// transport-only.
func TestTransportOnlyAcceptsNoE2E(t *testing.T) {
	transit := sentTransit()
	transit.Probe.LinkURL = ""
	if kept, _ := Accepted(transit, []nodeprotocol.ProbeResult{result("lnk_2", "e2e", 20, 0), result("lnk_2", "transport", 20, 0)}); len(kept) != 1 || kept[0].Kind != "transport" {
		t.Fatalf("kept %+v, want only the transport result", kept)
	}
}

func TestMethod(t *testing.T) {
	for _, tc := range []struct {
		udpOnly, front bool
		want           string
	}{{false, false, "tcp"}, {true, false, "quic"}, {false, true, "tls"}, {true, true, "tls"}} {
		if got := Method(tc.udpOnly, tc.front); got != tc.want {
			t.Errorf("Method(udpOnly=%v, front=%v) = %s, want %s", tc.udpOnly, tc.front, got, tc.want)
		}
	}
	if !UDPOnlyProtocol("hysteria2") || !UDPOnlyProtocol("tuic") || UDPOnlyProtocol("vless") {
		t.Fatal("UDPOnlyProtocol")
	}
}

// ingest books one report one minute after the previous call.
func ingest(t *testing.T, svc *Service, seq int64, results ...nodeprotocol.ProbeResult) bool {
	t.Helper()
	clock = clock.Add(time.Minute)
	return ingestNow(t, svc, seq, results...)
}

func ingestNow(t *testing.T, svc *Service, seq int64, results ...nodeprotocol.ProbeResult) bool {
	t.Helper()
	changed, err := svc.Ingest(context.Background(), 7, caps, sentTransit(), results, seq)
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

func downWith(success int64) nodeprotocol.ProbeResult {
	r := result("lnk_2", "e2e", 20, 20)
	r.LastSuccessMS = success
	return r
}

func TestIngestBooksStateAndHysteresis(t *testing.T) {
	repo := &fakeRepo{}
	svc := service(ports.UISettings{TransitProbeWithhold: true, TransitProbeWithholdAfter: 2, TransitProbeRestoreAfter: 2}, repo)
	row := func() domain.ProbeState { return repo.rows[key(7, "lnk_2", "e2e")] }

	if ingest(t, svc, 1, result("lnk_2", "e2e", 20, 20)) || row().Withheld {
		t.Fatal("one down window withheld")
	}
	if !ingest(t, svc, 2, result("lnk_2", "e2e", 20, 20)) || !row().Withheld || row().Consecutive != 2 {
		t.Fatalf("two down windows did not withhold: %+v", row())
	}
	ingest(t, svc, 3, result("lnk_2", "e2e", 20, 0))
	if !ingest(t, svc, 4, result("lnk_2", "e2e", 20, 0)) || row().Withheld {
		t.Fatalf("two ok windows did not restore: %+v", row())
	}
	if want := clock.Add(domain.ProbeStaleIntervals * 60 * time.Second); !row().ValidUntil.Equal(want) {
		t.Fatalf("valid until %v, want three intervals on: %v", row().ValidUntil, want)
	}
}

// Only an exact repeat (the same agent timestamp and the same window) is a
// replay. The agent's clock is never ordered, so one that steps back, or ran
// far ahead once, does not freeze the row.
func TestIngestReplayGuardIsAnExactRepeatOnly(t *testing.T) {
	repo := &fakeRepo{}
	svc := service(ports.UISettings{}, repo)
	row := func() domain.ProbeState { return repo.rows[key(7, "lnk_2", "e2e")] }

	ingest(t, svc, 5000, result("lnk_2", "e2e", 20, 20))
	ingest(t, svc, 5000, result("lnk_2", "e2e", 20, 20)) // the same report again
	if row().Consecutive != 1 || !row().ReportedAt.Before(clock) {
		t.Fatalf("a repeated report was taken: %+v", row())
	}
	// The agent clock steps back: a different window with an older timestamp counts.
	ingest(t, svc, 100, result("lnk_2", "e2e", 20, 0))
	if row().State != domain.ProbeOK || row().AgentReportedMS != 100 {
		t.Fatalf("a report from a clock that stepped back was refused: %+v", row())
	}
	// Far in the future once, then normal again.
	ingest(t, svc, 9_000_000_000_000, result("lnk_2", "e2e", 20, 20))
	ingest(t, svc, 6000, result("lnk_2", "e2e", 20, 0))
	if row().State != domain.ProbeOK || row().AgentReportedMS != 6000 {
		t.Fatalf("a far-future timestamp froze the row: %+v", row())
	}
}

// The run counts new windows. A report that repeats the stored window does not
// advance it until a probe interval has passed, because by then the agent has
// probed again (a link that stays down reports the same window each time).
func TestIngestCountsNewWindowsNotReports(t *testing.T) {
	repo := &fakeRepo{}
	svc := service(ports.UISettings{TransitProbeIntervalSeconds: 3600, TransitProbeWithhold: true, TransitProbeWithholdAfter: 3}, repo)
	row := func() domain.ProbeState { return repo.rows[key(7, "lnk_2", "e2e")] }

	for i := int64(1); i <= 30; i++ { // half an hour of 60 s reports, one window
		ingest(t, svc, i, result("lnk_2", "e2e", 20, 20))
	}
	if row().Consecutive != 1 || row().Withheld {
		t.Fatalf("identical windows advanced the run: %+v", row())
	}
	if row().ReportedAt != clock {
		t.Fatal("a repeated window did not refresh the row's freshness")
	}
	clock = clock.Add(time.Hour)
	ingestNow(t, svc, 31, result("lnk_2", "e2e", 20, 20))
	if row().Consecutive != 2 {
		t.Fatalf("a window an interval later did not count: %+v", row())
	}
	// A changed window counts at once, whatever the interval.
	ingest(t, svc, 32, downWith(1))
	if row().Consecutive != 3 || !row().Withheld {
		t.Fatalf("a new window did not count: %+v", row())
	}
	ingest(t, svc, 33, downWith(2))
	if row().Consecutive != 4 {
		t.Fatalf("a new window did not count: %+v", row())
	}
}

// After a gap the old run and flag are forgotten: the verdict is re-decided
// from fresh windows only.
func TestIngestForgetsARunAcrossAStaleGap(t *testing.T) {
	repo := &fakeRepo{}
	svc := service(ports.UISettings{TransitProbeWithhold: true, TransitProbeWithholdAfter: 2}, repo)
	row := func() domain.ProbeState { return repo.rows[key(7, "lnk_2", "e2e")] }
	ingest(t, svc, 1, downWith(1))
	ingest(t, svc, 2, downWith(2))
	if !row().Withheld {
		t.Fatalf("setup: %+v", row())
	}
	clock = clock.Add(2 * time.Hour) // the agent restarts and is silent
	ingestNow(t, svc, 3, downWith(3))
	if row().Withheld || row().Consecutive != 1 {
		t.Fatalf("a verdict from before the gap survived it: %+v", row())
	}
	ingest(t, svc, 4, downWith(4))
	if !row().Withheld {
		t.Fatal("two fresh down windows did not withhold")
	}
	// An empty window clears the flag.
	ingest(t, svc, 5, result("lnk_2", "e2e", 0, 0))
	if row().Withheld {
		t.Fatal("an unknown window left the flag set")
	}
}

func TestIngestUnknownNeverWithholds(t *testing.T) {
	repo := &fakeRepo{}
	svc := service(ports.UISettings{TransitProbeWithhold: true, TransitProbeWithholdAfter: 1}, repo)
	for i := int64(1); i <= 5; i++ {
		if ingest(t, svc, i, result("lnk_2", "e2e", 0, 0)) {
			t.Fatal("an empty window changed the withheld flag")
		}
	}
	if row := repo.rows[key(7, "lnk_2", "e2e")]; row.Withheld || row.State != domain.ProbeUnknown || row.WithheldAt(clock) {
		t.Fatalf("row = %+v", row)
	}
}

func TestIngestWithWithholdingOffNeverSetsTheFlag(t *testing.T) {
	repo := &fakeRepo{}
	svc := service(ports.UISettings{TransitProbeWithholdAfter: 1}, repo)
	for i := int64(1); i <= 5; i++ {
		ingest(t, svc, i, result("lnk_2", "e2e", 20, 20))
	}
	if row := repo.rows[key(7, "lnk_2", "e2e")]; row.Withheld || row.State != domain.ProbeDown || row.Consecutive != 5 {
		t.Fatalf("row = %+v: the state is tracked, the flag is not set", row)
	}
	// A flag a race left behind is cleared by the next report.
	k := key(7, "lnk_2", "e2e")
	left := repo.rows[k]
	left.Withheld = true
	repo.rows[k] = left
	ingest(t, svc, 6, result("lnk_2", "e2e", 20, 20))
	if repo.rows[k].Withheld {
		t.Fatal("a leftover flag survived a report while withholding is off")
	}
}

func TestIngestRefusalsAndPrune(t *testing.T) {
	repo := &fakeRepo{}
	svc := service(ports.UISettings{}, repo)
	ctx := context.Background()
	// No capability, no probing, no config: nothing is booked.
	if _, err := svc.Ingest(ctx, 7, nil, sentTransit(), []nodeprotocol.ProbeResult{result("lnk_2", "e2e", 20, 0)}, 1); err != nil || len(repo.rows) != 0 {
		t.Fatalf("rows = %v, %v", repo.rows, err)
	}
	if _, err := svc.Ingest(ctx, 7, caps, nil, []nodeprotocol.ProbeResult{result("lnk_2", "e2e", 20, 0)}, 1); err != nil || len(repo.rows) != 0 {
		t.Fatalf("rows = %v, %v", repo.rows, err)
	}
	off := service(ports.UISettings{TransitProbeOff: true}, repo)
	if _, err := off.Ingest(ctx, 7, caps, sentTransit(), []nodeprotocol.ProbeResult{result("lnk_2", "e2e", 20, 0)}, 1); err != nil || len(repo.rows) != 0 {
		t.Fatalf("rows = %v, %v", repo.rows, err)
	}
	// A row for an object that left the config is pruned by the next report.
	repo.rows = map[[3]string]domain.ProbeState{key(7, "lnk_99", "e2e"): {PanelID: 7, ObjectKey: "lnk_99", Kind: "e2e"}, key(8, "lnk_99", "e2e"): {PanelID: 8, ObjectKey: "lnk_99", Kind: "e2e"}}
	ingest(t, svc, 1, result("lnk_2", "e2e", 20, 0))
	if _, ok := repo.rows[key(7, "lnk_99", "e2e")]; ok {
		t.Fatal("a row of an object no longer in the config was kept")
	}
	if _, ok := repo.rows[key(8, "lnk_99", "e2e")]; !ok {
		t.Fatal("another server's row was pruned")
	}
}

func TestStaleAfter(t *testing.T) {
	eff := ports.TransitProbeEffectiveOf(ports.UISettings{})
	if got := StaleAfter(eff, 30); got != 3*time.Minute {
		t.Fatalf("StaleAfter = %v, want three probe intervals", got)
	}
	if got := StaleAfter(eff, 300); got != 15*time.Minute {
		t.Fatalf("StaleAfter = %v, want three report periods when reports are slower", got)
	}
}

// keepsRows is a repo whose prune does nothing, so that what Ingest stored is
// what its own filtering let through, and not what the prune cleaned up after.
type keepsRows struct{ *fakeRepo }

func (keepsRows) PruneExcept(context.Context, int64, []string) (int64, error) { return 0, nil }

func TestIngestStoresOnlyAcceptedResults(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(&fakeSettings{}, keepsRows{repo}, func() time.Time { return clock })
	_, err := svc.Ingest(context.Background(), 7, caps, sentTransit(), []nodeprotocol.ProbeResult{
		result("lnk_2", "e2e", 20, 0),
		result("lnk_2", "transport", 20, 0),
		result("lnk_3", "e2e", 20, 0),       // the exit's half
		result("lnk_9", "transport", 20, 0), // never sent
		result("fwd_1", "forward", 20, 0),
		result("fwd_2", "forward", 20, 0), // never sent
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range repo.rows {
		got = append(got, k[1]+"/"+k[2])
	}
	slices.Sort(got)
	if want := []string{"fwd_1/forward", "lnk_2/e2e", "lnk_2/transport"}; !slices.Equal(got, want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
}
