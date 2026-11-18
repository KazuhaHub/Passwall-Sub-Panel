package sqlstore

import (
	"context"
	"reflect"
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
