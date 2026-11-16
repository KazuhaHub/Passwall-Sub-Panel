package sqlstore

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func managedLanding(t *testing.T, repo *nodeRepo) *domain.Node {
	t.Helper()
	n := &domain.Node{
		PanelID: 1, InboundID: 2, DisplayName: "JP", ServerAddress: "Land.Example.com.",
		DesiredProtocol: "vless", DesiredPort: 443, Region: "JP", Enabled: true,
		Relays: []domain.RelayLine{
			{ID: "managed", Name: "via A", Address: "a.example.com", Port: 20443, Enabled: true, EntryPanelID: 9, Tags: []string{}},
			{ID: "external", Name: "CDN", Address: "cdn.example.com", Port: 0, Enabled: true, Tags: []string{}},
		},
	}
	if err := repo.Create(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTransitRepoEnsureIsStableAndReadinessFollowsTheDesiredForward(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	transit := &transitRepo{db: nodes.db}
	landing := managedLanding(t, nodes)

	first, err := transit.Ensure(ctx, landing.ID, "managed")
	if err != nil {
		t.Fatal(err)
	}
	again, err := transit.Ensure(ctx, landing.ID, "managed")
	if err != nil || again.ID != first.ID {
		t.Fatalf("Ensure is not stable: %d then %d (%v)", first.ID, again.ID, err)
	}

	// Never confirmed: the managed line is withheld, the external one is not.
	got, err := nodes.GetByID(ctx, landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Relays[0].Publishable() || !got.Relays[1].Publishable() {
		t.Fatalf("publishable = %v / %v", got.Relays[0].Publishable(), got.Relays[1].Publishable())
	}

	desired, ok := domain.DesiredManagedForward(got, got.Relays[0])
	if !ok || desired.Target != "land.example.com" || desired.TargetPort != 443 || desired.Port != 20443 || desired.EntryPanelID != 9 {
		t.Fatalf("desired = %+v, %v", desired, ok)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	if changed, err := transit.SetApplied(ctx, first.ID, desired, now); err != nil || !changed {
		t.Fatalf("SetApplied = %v, %v", changed, err)
	}
	if changed, err := transit.SetApplied(ctx, first.ID, desired, now.Add(time.Minute)); err != nil || changed {
		t.Fatalf("a replayed confirmation reported a change: %v, %v", changed, err)
	}
	listed, err := nodes.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !listed[0].Relays[0].Publishable() {
		t.Fatal("a confirmed managed line is not publishable")
	}

	// The line asks for another port: the old confirmation no longer counts.
	listed[0].Relays[0].Port = 20444
	if err := nodes.UpdateMetadata(ctx, listed[0]); err != nil {
		t.Fatal(err)
	}
	got, err = nodes.GetByID(ctx, landing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Relays[0].Publishable() {
		t.Fatal("a confirmation of a different forward made the line publishable")
	}

	if changed, err := transit.ClearApplied(ctx, first.ID); err != nil || !changed {
		t.Fatalf("ClearApplied = %v, %v", changed, err)
	}
	if changed, err := transit.ClearApplied(ctx, first.ID); err != nil || changed {
		t.Fatalf("a second clear reported a change: %v, %v", changed, err)
	}
}

func TestTransitRepoCountersCarryAcrossResets(t *testing.T) {
	nodes, ctx := newNodeTestRepo(t)
	transit := &transitRepo{db: nodes.db}
	landing := managedLanding(t, nodes)
	row, err := transit.Ensure(ctx, landing.ID, "managed")
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		up, down int64
		epoch    uint64
	}{
		{100, 1000, 1}, // first sample in an epoch counts whole
		{150, 1500, 1}, // +50 / +500
		{150, 1500, 1}, // replay: nothing
		{20, 30, 2},    // core restarted: whole sample
		{10, 10, 2},    // went backwards within an epoch: a reset, whole sample
	} {
		if err := transit.RecordCounters(ctx, row.ID, sample.up, sample.down, sample.epoch); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := transit.ListByNodes(ctx, []int64{landing.ID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	if rows[0].UpBytes != 100+50+20+10 || rows[0].DownBytes != 1000+500+30+10 {
		t.Fatalf("totals = %d / %d", rows[0].UpBytes, rows[0].DownBytes)
	}
	if err := transit.RecordCounters(ctx, row.ID, -1, 0, 2); err == nil {
		t.Fatal("negative counters accepted")
	}
}
