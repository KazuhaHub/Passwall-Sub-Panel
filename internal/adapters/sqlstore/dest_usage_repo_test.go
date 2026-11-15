package sqlstore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// PER-SITE USAGE (tier B). dest_usage_hourly is an hourly aggregate of how many
// connections one account made to one site through one node. These tests hold
// the store to: the upsert ADDS on every dialect, the usage rows are written in
// the very transaction of the batch's marker and hits (so a re-sent batch counts
// neither twice), retention and the orphan purge cover the table, and the read
// side folds to a Top N plus the rest without losing a count.

func usage(hour, panel, user int64, site string, count int64) domain.DestUsage {
	return domain.DestUsage{HourMS: hour, PanelID: panel, UserID: user, Site: site, Count: count}
}

func TestDestUsageTable_IsInTheSchemaWithItsIndexes(t *testing.T) {
	_, db := newHitRepo(t)
	if !db.Migrator().HasTable("dest_usage_hourly") {
		t.Fatal("dest_usage_hourly was not created by EnsureSchema")
	}
	for _, idx := range []string{"idx_dest_usage_user", "idx_dest_usage_panel"} {
		if !db.Migrator().HasIndex(&destUsageRow{}, idx) {
			t.Errorf("dest_usage_hourly has no index %s", idx)
		}
	}
	s, err := schema.Parse(&destUsageRow{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if f := s.LookUpField("Site"); f == nil || f.Size != destUsageSiteWidth {
		t.Fatalf("site column width = %v, the repo checks %d", f, destUsageSiteWidth)
	}
	// Every drop kind the ingest writes fits the column that stores it.
	kinds := []string{domain.DestDropNodeHits, domain.DestDropNodeTrial, domain.DestDropOverBudget, domain.DestDropQueueFull,
		domain.DestDropIngest, domain.DestDropNodeUsage, domain.DestDropUsageOverBudget}
	for _, k := range kinds {
		if len(k) > destDropKindWidth {
			t.Errorf("drop kind %q is %d bytes, the column holds %d", k, len(k), destDropKindWidth)
		}
	}
}

func TestStoreBatchWithUsage_AddsCountsAcrossBatchesAndMergesWithinOne(t *testing.T) {
	r, db := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	res, err := r.StoreBatchWithUsage(ctx, "agt_a", "b1", time.Now(), nil, []domain.DestUsage{
		usage(h, 1, 7, "a.example", 3),
		usage(h, 1, 7, "a.example", 2), // collides inside the batch: PostgreSQL refuses it in one statement unless merged first
		usage(h, 1, 7, "(ip)", 1),
	})
	if err != nil || res.Duplicate || res.UsageStored != 2 {
		t.Fatalf("store = %+v, %v", res, err)
	}
	if _, err := r.StoreBatchWithUsage(ctx, "agt_a", "b2", time.Now(), nil, []domain.DestUsage{usage(h, 1, 7, "a.example", 4)}); err != nil {
		t.Fatal(err)
	}
	var rows []destUsageRow
	if err := db.Order("site").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Site != "(ip)" || rows[0].Count != 1 || rows[1].Site != "a.example" || rows[1].Count != 9 {
		t.Fatalf("rows = %+v, want (ip)=1 and a.example=3+2+4", rows)
	}
}

// The marker, the hits and the usage are one unit: a batch stored before is
// refused whole, so neither table counts it twice.
func TestStoreBatchWithUsage_ADuplicateBatchWritesNeitherHitsNorUsage(t *testing.T) {
	r, db := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	hits := []domain.DestHit{hit(h, 1, 7, "p1", "block", "bad.example", 443, 3, h+1, h+2)}
	us := []domain.DestUsage{usage(h, 1, 7, "a.example", 5)}
	if res, err := r.StoreBatchWithUsage(ctx, "agt_a", "b1", time.Now(), hits, us); err != nil || res.Stored != 1 || res.UsageStored != 1 {
		t.Fatalf("first = %+v, %v", res, err)
	}
	res, err := r.StoreBatchWithUsage(ctx, "agt_a", "b1", time.Now(), hits, us)
	if err != nil || !res.Duplicate || res.Stored != 0 || res.UsageStored != 0 {
		t.Fatalf("re-send = %+v, %v; want a duplicate that wrote nothing", res, err)
	}
	var uc destUsageRow
	if err := db.First(&uc).Error; err != nil || uc.Count != 5 {
		t.Fatalf("usage = %+v, %v; the re-send must not add", uc, err)
	}
	var h1 destHitRow
	if err := db.First(&h1).Error; err != nil || h1.Count != 3 {
		t.Fatalf("hits = %+v, %v; the re-send must not add", h1, err)
	}
}

// A batch of only usage (no hit row) is valid, and one over the transaction
// size is chunked with the hits sharing the chunks.
func TestStoreBatchWithUsage_ALargeBatchIsChunkedAndUsageOnlyIsFine(t *testing.T) {
	r, db := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	var us []domain.DestUsage
	for i := range 2500 {
		us = append(us, usage(h, 1, int64(i%50)+1, fmt.Sprintf("s%d.example", i), 1))
	}
	hits := []domain.DestHit{hit(h, 1, 1, "p1", "block", "x.example", 1, 1, h, h)}
	res, err := r.StoreBatchWithUsage(ctx, "agt_a", "big", time.Now(), hits, us)
	if err != nil || res.UsageStored != 2500 || res.Stored != 1 {
		t.Fatalf("store = %+v, %v", res, err)
	}
	var n int64
	db.Model(&destUsageRow{}).Count(&n)
	if n != 2500 {
		t.Fatalf("usage rows = %d, want 2500", n)
	}
	if res, err := r.StoreBatchWithUsage(ctx, "agt_a", "only", time.Now(), nil, []domain.DestUsage{usage(h, 1, 1, "only.example", 1)}); err != nil || res.UsageStored != 1 {
		t.Fatalf("usage-only = %+v, %v", res, err)
	}
}

func TestStoreBatchWithUsage_RefusesAnOverWideSiteBeforeWritingAnything(t *testing.T) {
	r, db := newHitRepo(t)
	h := 400 * hourMS
	_, err := r.StoreBatchWithUsage(context.Background(), "agt_a", "b1", time.Now(), nil,
		[]domain.DestUsage{usage(h, 1, 1, strings.Repeat("a", destUsageSiteWidth+1), 1)})
	if err == nil {
		t.Fatal("an over-wide site was accepted")
	}
	var n int64
	db.Model(&destAuditBatchRow{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d marker(s) written for a refused batch", n)
	}
}

func TestUserUsage_FoldsToTopNAndTheRestWithoutLosingACount(t *testing.T) {
	r, _ := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	rows := []domain.DestUsage{
		usage(h, 1, 7, "big.example", 50),
		usage(h+hourMS, 1, 7, "big.example", 25),
		usage(h, 2, 7, "big.example", 5), // another panel, same account
		usage(h, 1, 7, "mid.example", 10),
		usage(h, 1, 7, "(ip)", 4),
		usage(h, 1, 7, "tail1.example", 1),
		usage(h, 1, 7, "tail2.example", 1),
		usage(h, 1, 8, "other-user.example", 99), // another account: never in the answer
		usage(h-5*hourMS, 1, 7, "old.example", 7), // outside the range
	}
	if _, err := r.StoreBatchWithUsage(ctx, "agt_a", "b1", time.Now(), nil, rows); err != nil {
		t.Fatal(err)
	}
	got, err := r.UserUsage(ctx, 7, 0, h-hourMS, h+2*hourMS, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 96 || got.OtherSites != 2 || got.OtherCount != 2 {
		t.Fatalf("summary = %+v, want total 96 with the two tail sites (2 connections) folded into the rest", got)
	}
	if len(got.Items) != 3 || got.Items[0] != (domain.DestUsageSite{Site: "big.example", Count: 80}) ||
		got.Items[1] != (domain.DestUsageSite{Site: "mid.example", Count: 10}) || got.Items[2] != (domain.DestUsageSite{Site: "(ip)", Count: 4}) {
		t.Fatalf("items = %+v", got.Items)
	}
	// One panel only.
	got, err = r.UserUsage(ctx, 7, 2, h-hourMS, h+2*hourMS, 3)
	if err != nil || got.Total != 5 || len(got.Items) != 1 || got.OtherSites != 0 {
		t.Fatalf("panel 2 = %+v, %v", got, err)
	}
	// Nothing recorded: empty, not an error.
	got, err = r.UserUsage(ctx, 99, 0, 0, 0, 20)
	if err != nil || got.Total != 0 || len(got.Items) != 0 {
		t.Fatalf("empty = %+v, %v", got, err)
	}
}

// Usage losses are counted per hour and panel with their own kinds, and are NOT
// part of the hit gap count a hits listing reports.
func TestUsageDropped_IsSeparateFromTheHitGap(t *testing.T) {
	r, _ := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	for _, d := range []struct {
		panel int64
		kind  string
		n     int64
	}{
		{1, domain.DestDropNodeUsage, 4}, {1, domain.DestDropUsageOverBudget, 3}, {2, domain.DestDropNodeUsage, 10}, {1, domain.DestDropNodeHits, 2},
	} {
		if err := r.AddDrops(ctx, h, d.panel, d.kind, d.n); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := r.UsageDropped(ctx, 0, h+hourMS, 1); err != nil || n != 7 {
		t.Fatalf("panel 1 usage drops = %d, %v; want 7", n, err)
	}
	if n, err := r.UsageDropped(ctx, 0, h+hourMS, 0); err != nil || n != 17 {
		t.Fatalf("all usage drops = %d, %v; want 17", n, err)
	}
	if n, err := r.DroppedInRange(ctx, 0, h+hourMS); err != nil || n != 2 {
		t.Fatalf("hit gap = %d, %v; want only the hit-side drop (2)", n, err)
	}
}

func TestPruneUsage_AgesOutByHourAndTheOrphanPurgeCoversTheTable(t *testing.T) {
	repos, db := newNativeCredentialTestRepos(t)
	ctx := context.Background()
	hr := NewDestRepos(db).Hits
	panel := &domain.XUIPanel{Kind: domain.PanelKind3XUI, Name: "live", URL: "https://x.example"}
	if err := repos.XUIPanel.Save(ctx, panel); err != nil {
		t.Fatal(err)
	}
	u := &domain.User{UPN: "alive", SubToken: "t-alive", UUID: "u-alive", Role: domain.RoleUser, Enabled: true}
	if err := repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	h := 400 * hourMS
	if _, err := hr.StoreBatchWithUsage(ctx, "agt_a", "b", time.Now(), nil, []domain.DestUsage{
		usage(h, panel.ID, u.ID, "keep.example", 1),
		usage(h-10*hourMS, panel.ID, u.ID, "old.example", 1),
		usage(h, panel.ID, u.ID+999, "gone-user.example", 1),
		usage(h, panel.ID+999, u.ID, "gone-panel.example", 1),
	}); err != nil {
		t.Fatal(err)
	}
	n, err := hr.PruneUsage(ctx, h-5*hourMS)
	if err != nil || n != 1 {
		t.Fatalf("PruneUsage = %d, %v; want the one row older than the cutoff", n, err)
	}
	n, err = hr.PruneOrphans(ctx)
	if err != nil || n != 2 {
		t.Fatalf("PruneOrphans = %d, %v; want a deleted account's row and a deleted panel's", n, err)
	}
	var left []destUsageRow
	db.Find(&left)
	if len(left) != 1 || left[0].Site != "keep.example" {
		t.Fatalf("left = %+v", left)
	}
}

// The MySQL spelling cannot be run here, so its SQL is generated offline: the
// count is ADDED (VALUES(count)), never replaced.
func TestDestUsageConflict_MySQLAddsTheCount(t *testing.T) {
	mdb := dryRunMySQL(t)
	rows := []destUsageRow{{HourMS: 1, PanelID: 1, UserID: 1, Site: "a", Count: 1}}
	got := mdb.ToSQL(func(tx *gorm.DB) *gorm.DB { return tx.Clauses(destUsageConflict("mysql")).Create(&rows) })
	if !strings.Contains(strings.ReplaceAll(got, " = ", "="), "`count`=`count` + VALUES(`count`)") {
		t.Fatalf("MySQL SQL does not add: %s", got)
	}
}
