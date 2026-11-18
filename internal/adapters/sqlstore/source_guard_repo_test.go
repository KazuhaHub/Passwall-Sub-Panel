package sqlstore

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestSourceGuardRepoCloudflareRoundTrip(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).SourceGuard
	ctx := context.Background()
	if _, ok, err := repo.LoadCloudflare(ctx); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	at := time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC)
	for _, cidrs := range [][]string{{"173.245.48.0/20", "2400:cb00::/32"}, {"104.16.0.0/13"}} {
		if err := repo.SaveCloudflare(ctx, domain.CloudflareRanges{CIDRs: cidrs, FetchedAt: at}); err != nil {
			t.Fatal(err)
		}
		got, ok, err := repo.LoadCloudflare(ctx)
		if err != nil || !ok || !reflect.DeepEqual(got.CIDRs, cidrs) || !got.FetchedAt.Equal(at) {
			t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
		}
	}
}

func TestSourceGuardRepoHitsAreReplacedPrunedAndCleared(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).SourceGuard
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	hit := func(ip string, count int64, age time.Duration) domain.SourceGuardHit {
		return domain.SourceGuardHit{PanelID: 1, Set: "src_5_aaaaaaaaaa", ListenerNodeID: 5, IP: ip, Count: count, LastSeen: now.Add(-age)}
	}
	cutoff := now.Add(-30 * 24 * time.Hour)
	if err := repo.RecordHits(ctx, 1, []domain.SourceGuardHit{hit("198.51.100.1", 3, time.Hour), hit("198.51.100.2", 1, 2*time.Hour)}, cutoff, 1000); err != nil {
		t.Fatal(err)
	}
	// The same source reported again replaces its row.
	if err := repo.RecordHits(ctx, 1, []domain.SourceGuardHit{hit("198.51.100.1", 9, time.Minute)}, cutoff, 1000); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListHits(ctx, []int64{1})
	if err != nil || len(got) != 2 || got[0].IP != "198.51.100.1" || got[0].Count != 9 {
		t.Fatalf("hits = %+v err=%v", got, err)
	}
	// Old rows go, and the newest `limit` stay.
	if err := repo.RecordHits(ctx, 1, []domain.SourceGuardHit{hit("198.51.100.3", 1, 40*24*time.Hour)}, cutoff, 1000); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.ListHits(ctx, []int64{1}); len(got) != 2 {
		t.Fatalf("stale row kept: %+v", got)
	}
	if err := repo.RecordHits(ctx, 1, nil, cutoff, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.ListHits(ctx, []int64{1}); len(got) != 1 || got[0].IP != "198.51.100.1" {
		t.Fatalf("limit not applied: %+v", got)
	}
	if n, err := repo.ClearHits(ctx, "src_5_aaaaaaaaaa", "198.51.100.1"); err != nil || n != 1 {
		t.Fatalf("clear = %d, %v", n, err)
	}
	if got, _ = repo.ListHits(ctx, []int64{1}); len(got) != 0 {
		t.Fatalf("not cleared: %+v", got)
	}
}

// A landing publishes a relay line only when the source guard allows it: a
// landing that cannot guard keeps only lines that cost what the direct line
// does, a landing that can guard needs the addresses of a priced class to be
// known (a line that costs what the direct line costs needs none).
func TestRelayLinesAreWithheldBySourceGuardState(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	agents := &nodeAgentRepo{db: nodes.db}
	two, three := 2000, 3000
	mk := func(panel int64, inbound int, lines ...domain.RelayLine) *domain.Node {
		n := &domain.Node{PanelID: panel, InboundID: inbound, DisplayName: "n", ServerAddress: "land.example.com",
			DesiredProtocol: "vless", DesiredPort: 443, Region: "JP", Enabled: true, Relays: lines}
		if err := nodes.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		got, err := nodes.GetByID(ctx, n.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	ext := domain.RelayLine{ID: "ext", Name: "e", Address: "e.example", Port: 1, Enabled: true, Tags: []string{}, TrafficFactorMilli: &two}
	same := domain.RelayLine{ID: "same", Name: "s", Address: "s.example", Port: 2, Enabled: true, Tags: []string{}}
	extWith := ext
	extWith.ID, extWith.SourceCIDRs = "ext2", []string{"192.0.2.0/24"}
	fwd := domain.RelayLine{ID: "fwd", Name: "f", Address: "f.example", Port: 3, Enabled: true, Tags: []string{}, EntryPanelID: 21, TrafficFactorMilli: &three}

	// No agent: it cannot guard.
	got := mk(20, 1, ext, same)
	if got.Relays[0].SourceWithheld != domain.SourceWithheldFactor || got.Relays[0].Publishable() || got.Relays[1].SourceWithheld != "" {
		t.Fatalf("cannot guard: %+v", got.Relays)
	}

	// A capable landing: the external line needs addresses, the managed line an entry.
	if err := agents.Create(ctx, &domain.NodeAgent{AgentID: "agt_land", PanelID: 30, Epoch: 1, CredentialSHA256: strings.Repeat("a", 64), DesiredCoreEngine: domain.NodeCoreXray}); err != nil {
		t.Fatal(err)
	}
	if err := agents.UpdateProtocolObservation(ctx, "agt_land", 1, []string{"transit.source_guard.v1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := agents.Create(ctx, &domain.NodeAgent{AgentID: "agt_entry", PanelID: 21, Epoch: 1, CredentialSHA256: strings.Repeat("b", 64), DesiredCoreEngine: domain.NodeCoreXray}); err != nil {
		t.Fatal(err)
	}
	got = mk(30, 2, ext, extWith, same, fwd)
	reasons := map[string]string{}
	for _, line := range got.Relays {
		reasons[line.ID] = line.SourceWithheld
	}
	want := map[string]string{"ext": domain.SourceWithheldUnknown, "ext2": "", "same": "", "fwd": domain.SourceWithheldUnknown}
	if !reflect.DeepEqual(reasons, want) {
		t.Fatalf("capable, entry unknown: %v", reasons)
	}
	if err := agents.RecordObservedAddress(ctx, "agt_entry", "93.184.216.34"); err != nil {
		t.Fatal(err)
	}
	again, _ := nodes.GetByID(ctx, got.ID)
	if again.Relays[3].SourceWithheld != "" {
		t.Fatalf("the entry's address is known now: %+v", again.Relays[3])
	}
}

// A managed forward at the direct factor needs no entry address: it is in the
// direct class, which is reachable from anywhere.
func TestDirectClassManagedLineIsNotWithheldWhenItsEntryIsUnknown(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	agents := &nodeAgentRepo{db: nodes.db}
	if err := agents.Create(ctx, &domain.NodeAgent{AgentID: "agt_l2", PanelID: 40, Epoch: 1, CredentialSHA256: strings.Repeat("c", 64), DesiredCoreEngine: domain.NodeCoreXray}); err != nil {
		t.Fatal(err)
	}
	if err := agents.UpdateProtocolObservation(ctx, "agt_l2", 1, []string{"transit.source_guard.v1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	n := &domain.Node{PanelID: 40, InboundID: 1, DisplayName: "n", ServerAddress: "land.example.com",
		DesiredProtocol: "vless", DesiredPort: 443, Region: "JP", Enabled: true,
		Relays: []domain.RelayLine{{ID: "fwd", Name: "f", Address: "f.example", Port: 3, Enabled: true, Tags: []string{}, EntryPanelID: 99}}}
	if err := nodes.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	got, err := nodes.GetByID(ctx, n.ID)
	if err != nil || got.Relays[0].SourceWithheld != "" {
		t.Fatalf("withheld = %+v err=%v", got.Relays, err)
	}
}
