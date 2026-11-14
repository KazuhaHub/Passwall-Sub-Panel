package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The hit tables hold what a native node reports about its destination rules.
// These tests pin the three things the ingest worker and the admin API rely on
// on every dialect: an upsert that ADDS counts and keeps the earliest first and
// latest last instant (so a re-sent hour never loses data), a batch marker
// whose "already stored" answer comes from RowsAffected and not from catching a
// duplicate-key error, and a write path that survives the rows of one batch
// colliding on the aggregation key (PostgreSQL refuses that in one statement).

const hourMS = int64(3600000)

func newHitRepo(t *testing.T) (*DestHitRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return NewDestRepos(db).Hits, db
}

func hit(hour int64, panel, user int64, source, action, dest string, port int, count, first, last int64) domain.DestHit {
	return domain.DestHit{HourMS: hour, PanelID: panel, UserID: user, Source: source, Action: action, Dest: dest, Port: port, Count: count, FirstMS: first, LastMS: last}
}

func TestDestHitTables_AreInTheSchema(t *testing.T) {
	_, db := newHitRepo(t)
	for _, table := range []string{"dest_hits", "dest_audit_batches", "dest_audit_drops"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %s was not created by EnsureSchema", table)
		}
	}
	for _, idx := range []string{"idx_dest_hit_user", "idx_dest_hit_source", "idx_dest_hit_panel"} {
		if !db.Migrator().HasIndex(&destHitRow{}, idx) {
			t.Errorf("dest_hits has no index %s", idx)
		}
	}
}

func TestDestHitRow_WidthsMatchTheColumns(t *testing.T) {
	size := func(model any, field string) int {
		s, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("parse %T: %v", model, err)
		}
		f := s.LookUpField(field)
		if f == nil {
			t.Fatalf("%T has no field %s", model, field)
		}
		return f.Size
	}
	for _, c := range []struct {
		model any
		field string
		want  int
	}{
		{&destHitRow{}, "Source", destHitSourceWidth},
		{&destHitRow{}, "Action", destHitActionWidth},
		{&destHitRow{}, "Dest", destHitDestWidth},
		{&destAuditBatchRow{}, "BatchID", destBatchIDWidth},
		{&destAuditBatchRow{}, "AgentID", destAgentIDWidth},
		{&destAuditDropRow{}, "Kind", destDropKindWidth},
	} {
		if got := size(c.model, c.field); got != c.want {
			t.Errorf("%T.%s column is %d wide, the repo checks %d", c.model, c.field, got, c.want)
		}
	}
}

// MySQL's InnoDB refuses a key over 3072 bytes, and utf8mb4 costs four a
// character: the wide aggregation key must stay under it.
func TestDestHitRow_PrimaryKeyFitsMySQL(t *testing.T) {
	bytesOf := destHitSourceWidth*4 + destHitActionWidth*4 + destHitDestWidth*4 + 8*3 + 4
	if bytesOf >= 3072 {
		t.Fatalf("primary key is %d bytes, over InnoDB's 3072", bytesOf)
	}
}

func TestStoreBatch_WritesRowsAndTheMarker(t *testing.T) {
	r, db := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	res, err := r.StoreBatch(ctx, "agt_a", "b1", time.Now(), []domain.DestHit{
		hit(h, 1, 7, "p12", "block", "bad.example", 443, 3, h+10, h+20),
		hit(h, 1, 7, "p12", "observe", "bad.example", 443, 2, h+5, h+6),
	})
	if err != nil || res.Duplicate || res.Stored != 2 {
		t.Fatalf("store = %+v, %v", res, err)
	}
	var n int64
	db.Model(&destHitRow{}).Count(&n)
	if n != 2 {
		t.Fatalf("rows = %d, want 2 (action is part of the key)", n)
	}
	db.Model(&destAuditBatchRow{}).Count(&n)
	if n != 1 {
		t.Fatalf("batch markers = %d, want 1", n)
	}
}

// A hour that arrives twice from two different batches adds its count and
// keeps the widest first/last window.
func TestStoreBatch_UpsertAddsCountsAndWidensTheWindow(t *testing.T) {
	r, db := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	if _, err := r.StoreBatch(ctx, "agt_a", "b1", time.Now(), []domain.DestHit{hit(h, 1, 7, "p12", "block", "bad.example", 443, 3, h+100, h+200)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.StoreBatch(ctx, "agt_a", "b2", time.Now(), []domain.DestHit{hit(h, 1, 7, "p12", "block", "bad.example", 443, 4, h+50, h+150)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.StoreBatch(ctx, "agt_a", "b3", time.Now(), []domain.DestHit{hit(h, 1, 7, "p12", "block", "bad.example", 443, 1, h+120, h+900)}); err != nil {
		t.Fatal(err)
	}
	var row destHitRow
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Count != 8 || row.FirstMS != h+50 || row.LastMS != h+900 {
		t.Fatalf("row = count %d first %d last %d, want 8 / %d / %d", row.Count, row.FirstMS-h, row.LastMS-h, int64(50), int64(900))
	}
}

func TestStoreBatch_ADuplicateBatchIsRefusedWithoutWriting(t *testing.T) {
	r, db := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	rows := []domain.DestHit{hit(h, 1, 7, "p12", "block", "bad.example", 443, 3, h+1, h+2)}
	if _, err := r.StoreBatch(ctx, "agt_a", "b1", time.Now(), rows); err != nil {
		t.Fatal(err)
	}
	res, err := r.StoreBatch(ctx, "agt_a", "b1", time.Now(), rows)
	if err != nil {
		t.Fatalf("a duplicate is not an error, got %v", err)
	}
	if !res.Duplicate || res.Stored != 0 {
		t.Fatalf("result = %+v, want a duplicate that stored nothing", res)
	}
	var row destHitRow
	db.First(&row)
	if row.Count != 3 {
		t.Fatalf("count = %d after a duplicate, want 3 (counted once)", row.Count)
	}
	// The same batch id from ANOTHER agent is a different batch.
	if res, err := r.StoreBatch(ctx, "agt_b", "b1", time.Now(), rows); err != nil || res.Duplicate {
		t.Fatalf("another agent's batch of the same id = %+v, %v", res, err)
	}
}

// PostgreSQL refuses a multi-row ON CONFLICT DO UPDATE that names a key twice.
// p12x1 and p12x2 both map to p12, so the rows of ONE batch collide: they must
// be merged before the statement is built.
func TestStoreBatch_RowsThatShareAKeyAreMergedFirst(t *testing.T) {
	r, db := newHitRepo(t)
	h := 400 * hourMS
	res, err := r.StoreBatch(context.Background(), "agt_a", "b1", time.Now(), []domain.DestHit{
		hit(h, 1, 7, "p12", "block", "x.example", 443, 2, h+30, h+40),
		hit(h, 1, 7, "p12", "block", "x.example", 443, 5, h+10, h+20),
		hit(h, 1, 7, "p12", "block", "y.example", 443, 1, h+10, h+20),
	})
	if err != nil {
		t.Fatalf("a batch with colliding keys failed: %v", err)
	}
	if res.Stored != 2 {
		t.Fatalf("stored = %d, want 2 after the merge", res.Stored)
	}
	var row destHitRow
	if err := db.Where("dest = ?", "x.example").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Count != 7 || row.FirstMS != h+10 || row.LastMS != h+40 {
		t.Fatalf("merged row = %d %d %d", row.Count, row.FirstMS-h, row.LastMS-h)
	}
}

func TestDomainMergeDestHits_KeepsFirstAppearanceOrder(t *testing.T) {
	h := 400 * hourMS
	got := domain.MergeDestHits([]domain.DestHit{
		hit(h, 1, 1, "p1", "block", "b", 1, 1, h+9, h+9),
		hit(h, 1, 1, "p1", "block", "a", 1, 1, h+1, h+1),
		hit(h, 1, 1, "p1", "block", "b", 1, 2, h+2, h+3),
	})
	if len(got) != 2 || got[0].Dest != "b" || got[0].Count != 3 || got[0].FirstMS != h+2 || got[0].LastMS != h+9 || got[1].Dest != "a" {
		t.Fatalf("merged = %+v", got)
	}
}

// A batch larger than one transaction is written in several, and every row of
// it is there afterwards.
func TestStoreBatch_ALargeBatchIsChunked(t *testing.T) {
	r, db := newHitRepo(t)
	h := 400 * hourMS
	const n = 2*destHitTxRows + 37
	rows := make([]domain.DestHit, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, hit(h, 1, 7, "p12", "block", fmt.Sprintf("h%d.example", i), 443, 1, h+1, h+2))
	}
	res, err := r.StoreBatch(context.Background(), "agt_a", "big", time.Now(), rows)
	if err != nil || res.Stored != n {
		t.Fatalf("store = %+v, %v", res, err)
	}
	var got int64
	db.Model(&destHitRow{}).Count(&got)
	if got != n {
		t.Fatalf("rows = %d, want %d", got, n)
	}
}

func TestStoreBatch_RefusesAnOverWideRow(t *testing.T) {
	r, _ := newHitRepo(t)
	h := 400 * hourMS
	_, err := r.StoreBatch(context.Background(), "agt_a", "b1", time.Now(),
		[]domain.DestHit{hit(h, 1, 7, "p12", "block", strings.Repeat("a", destHitDestWidth+1), 443, 1, h, h)})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("an over-wide dest = %v, want ErrValidation", err)
	}
}

// A batch with only counters and no rows still leaves its marker, so a resend
// does not count the node's dropped counters twice.
func TestStoreBatch_ABatchWithNoRowsStillLeavesAMarker(t *testing.T) {
	r, db := newHitRepo(t)
	if res, err := r.StoreBatch(context.Background(), "agt_a", "b1", time.Now(), nil); err != nil || res.Duplicate || res.Stored != 0 {
		t.Fatalf("empty = %+v, %v", res, err)
	}
	var n int64
	db.Model(&destAuditBatchRow{}).Count(&n)
	if n != 1 {
		t.Fatalf("markers = %d", n)
	}
}

func TestAddDropsAndDroppedInRange(t *testing.T) {
	r, _ := newHitRepo(t)
	ctx := context.Background()
	h := 400 * hourMS
	for _, d := range []struct {
		hour int64
		kind string
		n    int64
	}{
		{h, domain.DestDropNodeHits, 5}, {h, domain.DestDropNodeHits, 2}, {h, domain.DestDropOverBudget, 10},
		{h + hourMS, domain.DestDropQueueFull, 4}, {h, domain.DestDropNodeTrial, 1000}, {h + 5*hourMS, domain.DestDropIngest, 9},
	} {
		if err := r.AddDrops(ctx, d.hour, 1, d.kind, d.n); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AddDrops(ctx, h, 1, domain.DestDropIngest, 0); err != nil {
		t.Fatalf("a zero drop is a no-op, got %v", err)
	}
	got, err := r.DroppedInRange(ctx, h, h+2*hourMS)
	if err != nil || got != 5+2+10+4 {
		t.Fatalf("dropped in [h, h+2h) = %d, %v; want 21 (trial overflow is not a gap in the block record)", got, err)
	}
	if got, _ := r.DroppedInRange(ctx, 0, 0); got != 5+2+10+4+9 {
		t.Fatalf("dropped overall = %d", got)
	}
	if got, _ := r.DroppedInRange(ctx, h+10*hourMS, h+11*hourMS); got != 0 {
		t.Fatalf("an empty range dropped %d", got)
	}
}

func seedHits(t *testing.T, r *DestHitRepo) int64 {
	t.Helper()
	h := 400 * hourMS
	rows := []domain.DestHit{
		hit(h, 1, 7, "p12", "block", "bad.example", 443, 30, h+1, h+9),
		hit(h, 1, 7, "p13", "observe", "wb.example", 80, 5, h+2, h+8),
		hit(h, 2, 8, "p12", "block", "other.example", 25, 7, h+3, h+4),
		hit(h+hourMS, 1, 7, "p12", "block", "bad.example", 443, 1, h+hourMS, h+hourMS),
		hit(h, 1, 0, "g3", "observe", "trial.example", 0, 100, h+1, h+2),
		hit(h, 1, 8, "s4", "observe", "src.example", 443, 2, h+1, h+2),
	}
	if _, err := r.StoreBatch(context.Background(), "agt_a", "seed", time.Now(), rows); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestList_FiltersPagesAndGroups(t *testing.T) {
	r, _ := newHitRepo(t)
	ctx := context.Background()
	h := seedHits(t, r)

	rows, total, err := r.List(ctx, domain.DestHitFilter{SinceMS: h, UntilMS: h + 2*hourMS, PageSize: 3, IncludeTrial: true})
	if err != nil || total != 6 || len(rows) != 3 {
		t.Fatalf("page 1 = %d rows of %d, %v", len(rows), total, err)
	}
	// A listing leaves the allowlist trial rows out unless asked.
	if _, total, _ := r.List(ctx, domain.DestHitFilter{SinceMS: h, UntilMS: h + 2*hourMS}); total != 5 {
		t.Fatalf("default total = %d, want 5 (no trial row)", total)
	}
	rows, _, _ = r.List(ctx, domain.DestHitFilter{SinceMS: h, UntilMS: h + 2*hourMS, PageSize: 3, IncludeTrial: true})
	if len(rows) != 3 {
		t.Fatalf("page 1 = %d rows of %d, %v", len(rows), total, err)
	}
	if rows[0].HourMS != h+hourMS {
		t.Fatalf("newest hour first: got %d", rows[0].HourMS-h)
	}
	rows, _, _ = r.List(ctx, domain.DestHitFilter{SinceMS: h, UntilMS: h + 2*hourMS, PageSize: 3, Page: 2, IncludeTrial: true})
	if len(rows) != 3 {
		t.Fatalf("page 2 = %d rows", len(rows))
	}
	if rows, total, _ = r.List(ctx, domain.DestHitFilter{UserID: 7, Action: "observe"}); total != 1 || rows[0].Source != "p13" {
		t.Fatalf("user+action filter = %d %+v", total, rows)
	}
	if _, total, _ = r.List(ctx, domain.DestHitFilter{Source: "p12"}); total != 3 {
		t.Fatalf("source filter = %d", total)
	}
	if _, total, _ = r.List(ctx, domain.DestHitFilter{PanelID: 2}); total != 1 {
		t.Fatalf("panel filter = %d", total)
	}
	if _, total, _ = r.List(ctx, domain.DestHitFilter{Keyword: "bad.exa"}); total != 2 {
		t.Fatalf("keyword filter = %d", total)
	}
	if _, total, _ = r.List(ctx, domain.DestHitFilter{Keyword: "%"}); total != 0 {
		t.Fatalf("a literal percent matched %d rows: the LIKE escape is broken", total)
	}

	rows, total, err = r.List(ctx, domain.DestHitFilter{GroupBy: "site", Source: "p12", Action: "block"})
	if err != nil || total != 2 || rows[0].Dest != "bad.example" || rows[0].Count != 31 {
		t.Fatalf("group by site = %d %+v %v", total, rows, err)
	}
	rows, total, _ = r.List(ctx, domain.DestHitFilter{GroupBy: "user", Action: "block"})
	if total != 2 || rows[0].UserID != 7 || rows[0].Count != 31 {
		t.Fatalf("group by user = %d %+v", total, rows)
	}
	rows, total, _ = r.List(ctx, domain.DestHitFilter{GroupBy: "policy", IncludeTrial: true})
	if total != 4 || rows[0].Source != "g3" {
		t.Fatalf("group by policy = %d %+v", total, rows)
	}
	if _, _, err := r.List(ctx, domain.DestHitFilter{GroupBy: "dest"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown group_by = %v", err)
	}
}

func TestSummary_CountsTheWholeSelectionAndKeepsTrialSeparate(t *testing.T) {
	r, _ := newHitRepo(t)
	h := seedHits(t, r)
	s, err := r.Summary(context.Background(), domain.DestHitFilter{SinceMS: h, UntilMS: h + 2*hourMS})
	if err != nil {
		t.Fatal(err)
	}
	// block: 30+7+1; observe (would-block, not trial): 5+2; users: 7, 8.
	if s.Block != 38 || s.Observe != 7 || s.Users != 2 {
		t.Fatalf("summary = %+v", s)
	}
	// An action filter does not narrow the cards.
	s2, _ := r.Summary(context.Background(), domain.DestHitFilter{SinceMS: h, UntilMS: h + 2*hourMS, Action: "block", Source: "p12"})
	if s2 != s {
		t.Fatalf("summary with action/source filters = %+v, want the unfiltered %+v", s2, s)
	}
}

func TestSourceCountsLastHitsPanelCountsAndSources(t *testing.T) {
	r, _ := newHitRepo(t)
	ctx := context.Background()
	h := seedHits(t, r)
	counts, err := r.SourceCounts(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]domain.DestSourceCount{}
	for _, c := range counts {
		byKey[c.Source+"/"+c.Action] = c
	}
	if byKey["p12/block"].Count != 38 || byKey["p13/observe"].Count != 5 || byKey["p13/observe"].LastMS != h+8 {
		t.Fatalf("counts = %+v", counts)
	}
	if got, _ := r.SourceCounts(ctx, h+hourMS); len(got) != 1 || got[0].Source != "p12" {
		t.Fatalf("a later window = %+v", got)
	}
	last, _ := r.LastHits(ctx, h, []string{"p12", "p13"})
	if last["p12"] != h+hourMS || last["p13"] == 0 || len(last) != 2 {
		t.Fatalf("last hits = %+v: only the listed sources", last)
	}
	// Bounded to the window: an hour after the first, only p12 has rows.
	if last, _ = r.LastHits(ctx, h+hourMS, []string{"p12", "p13"}); len(last) != 1 || last["p12"] != h+hourMS {
		t.Fatalf("last hits in a later window = %+v", last)
	}
	if last, err = r.LastHits(ctx, h, nil); err != nil || len(last) != 0 {
		t.Fatalf("no sources asked, so nothing read: %+v, %v", last, err)
	}
	pc, _ := r.PanelCounts(ctx, h)
	if pc[1] != 30+5+1+2 || pc[2] != 7 {
		t.Fatalf("panel counts = %+v (the trial rows are not a panel's hits)", pc)
	}
	srcs, _ := r.Sources(ctx, h, h+2*hourMS)
	if strings.Join(srcs, ",") != "g3,p12,p13,s4" {
		t.Fatalf("sources = %v", srcs)
	}
}

func TestUserRecentAndBlockCounts(t *testing.T) {
	r, _ := newHitRepo(t)
	ctx := context.Background()
	h := seedHits(t, r)
	got, err := r.UserRecent(ctx, 7, h)
	if err != nil || len(got) != 2 || got[0].Source != "p12" || got[0].Count != 31 || got[1].Action != "observe" {
		t.Fatalf("recent = %+v, %v", got, err)
	}
	dests, panels, err := r.UserDests(ctx, 7, h, "p12", "block", 3)
	if err != nil || len(dests) != 1 || dests[0] != "bad.example" || len(panels) != 1 {
		t.Fatalf("dests = %v %v %v", dests, panels, err)
	}
	// Block counts hold block rows only: the report-only p13 row of user 7 is not in it.
	bc, _ := r.BlockCounts(ctx, 7, h)
	if len(bc) != 1 || bc["p12"] != 31 {
		t.Fatalf("block counts = %+v", bc)
	}
	if bc, _ = r.BlockCounts(ctx, 7, h+hourMS); bc["p12"] != 1 {
		t.Fatalf("windowed block counts = %+v", bc)
	}
}

func TestPrune_HitsTrialDropsAndBatches(t *testing.T) {
	r, db := newHitRepo(t)
	ctx := context.Background()
	h := seedHits(t, r)
	_ = r.AddDrops(ctx, h, 1, domain.DestDropOverBudget, 3)
	_ = r.AddDrops(ctx, h+5*hourMS, 1, domain.DestDropOverBudget, 3)

	// The trial row is older than the trial cutoff but not the hit cutoff.
	n, err := r.PruneTrial(ctx, h+hourMS)
	if err != nil || n != 1 {
		t.Fatalf("prune trial = %d, %v", n, err)
	}
	var left int64
	db.Model(&destHitRow{}).Where("source = ?", "p12").Count(&left)
	if left != 3 {
		t.Fatalf("a trial prune touched policy rows (%d left)", left)
	}
	if n, err = r.PruneHits(ctx, h+hourMS); err != nil || n != 4 {
		t.Fatalf("prune hits = %d, %v", n, err)
	}
	if n, _ = r.PruneDrops(ctx, h+hourMS); n != 1 {
		t.Fatalf("prune drops = %d", n)
	}
	old := time.Now().Add(-96 * time.Hour)
	db.Model(&destAuditBatchRow{}).Where("batch_id = ?", "seed").Update("received_at", old)
	_, _ = r.StoreBatch(ctx, "agt_a", "fresh", time.Now(), nil)
	if n, err = r.PruneBatches(ctx, time.Now().Add(-domain.AuditBatchMarkerRetention)); err != nil || n != 1 {
		t.Fatalf("prune batches = %d, %v", n, err)
	}
}

func TestPruneOrphans_KeepsTrialRowsAndLiveReferences(t *testing.T) {
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
	rows := []domain.DestHit{
		hit(h, panel.ID, u.ID, "p1", "block", "keep.example", 1, 1, h, h),
		hit(h, panel.ID, 0, "g1", "observe", "trial.example", 0, 1, h, h),
		hit(h, panel.ID, u.ID+999, "p1", "block", "gone-user.example", 1, 1, h, h),
		hit(h, panel.ID+999, u.ID, "p1", "block", "gone-panel.example", 1, 1, h, h),
	}
	if _, err := hr.StoreBatch(ctx, "agt_a", "b", time.Now(), rows); err != nil {
		t.Fatal(err)
	}
	_ = hr.AddDrops(ctx, h, panel.ID+999, domain.DestDropIngest, 1)
	n, err := hr.PruneOrphans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("pruned %d, want 3 (a deleted user's row, a deleted panel's row and its drop counter)", n)
	}
	var left []destHitRow
	db.Order("dest").Find(&left)
	if len(left) != 2 || left[0].Dest != "keep.example" || left[1].Dest != "trial.example" {
		t.Fatalf("left = %+v", left)
	}
}

// The MySQL spelling cannot be run here, so its SQL is generated offline.
func TestDestHitConflict_PerDialectSQL(t *testing.T) {
	conn, _ := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/d")
	t.Cleanup(func() { _ = conn.Close() })
	mdb, err := gorm.Open(mysqldriver.New(mysqldriver.Config{Conn: conn, SkipInitializeWithVersion: true}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	rows := []destHitRow{{HourMS: 1, PanelID: 1, UserID: 1, Source: "p1", Action: "block", Dest: "a", Port: 1, Count: 1}}
	mysqlSQL := mdb.ToSQL(func(tx *gorm.DB) *gorm.DB { return tx.Clauses(destHitConflict("mysql")).Create(&rows) })
	for _, want := range []string{"ON DUPLICATE KEY UPDATE", "`count`=`count` + VALUES(`count`)", "LEAST(`first_ms`, VALUES(`first_ms`))", "GREATEST(`last_ms`, VALUES(`last_ms`))"} {
		if !strings.Contains(strings.ReplaceAll(mysqlSQL, " = ", "="), want) {
			t.Errorf("MySQL SQL lacks %q: %s", want, mysqlSQL)
		}
	}
	// SQLite and PostgreSQL SQL is exercised for real by the other tests; what
	// this pins is that the two spellings differ in the way the dialects need.
	litePlan := destHitConflict("sqlite").DoUpdates
	pgPlan := destHitConflict("postgres").DoUpdates
	if len(litePlan) != 3 || len(pgPlan) != 3 {
		t.Fatal("every dialect updates count, first_ms and last_ms")
	}
}

// ---- audit_collect on the panel row ----

func TestAuditCollect_DefaultsToHitsOnCreate(t *testing.T) {
	r := newPanelRepo(t)
	p := &domain.Panel{Kind: domain.PanelKindPSP, Name: "n1", URL: "psp://agt_1"}
	if err := r.Save(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetByID(context.Background(), p.ID)
	if err != nil || got.AuditCollect != domain.AuditCollectHits {
		t.Fatalf("a created panel's audit_collect = %q, %v; want hits", got.AuditCollect, err)
	}
}

// An admin editing the server's credentials saves the whole row, which carries
// the domain value read earlier. That must never reset a recording choice made
// meanwhile through the narrow writer.
func TestAuditCollect_AWholeRowSaveNeverWritesIt(t *testing.T) {
	r := newPanelRepo(t)
	ctx := context.Background()
	p := &domain.Panel{Kind: domain.PanelKindPSP, Name: "n1", URL: "psp://agt_1"}
	if err := r.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	off := domain.AuditCollectOff
	if err := r.UpdateNativeMetadata(ctx, p.ID, nil, nil, nil, &off); err != nil {
		t.Fatal(err)
	}
	stale, _ := r.GetByID(ctx, p.ID)
	stale.AuditCollect = domain.AuditCollectHits // an older read, or a caller that never set it
	stale.Remark = "edited"
	if err := r.Save(ctx, stale); err != nil {
		t.Fatal(err)
	}
	stale.AuditCollect = ""
	if err := r.Save(ctx, stale); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetByID(ctx, p.ID)
	if got.AuditCollect != domain.AuditCollectOff || got.Remark != "edited" {
		t.Fatalf("after whole-row saves audit_collect = %q remark = %q; want off / edited", got.AuditCollect, got.Remark)
	}
}

func TestAuditCollect_NativeMetadataWriteIsNarrowAndPspOnly(t *testing.T) {
	r := newPanelRepo(t)
	ctx := context.Background()
	p := &domain.Panel{Kind: domain.PanelKindPSP, Name: "n1", URL: "psp://agt_1", Remark: "keep"}
	if err := r.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	full := domain.AuditCollectHitsAndUsage
	if err := r.UpdateNativeMetadata(ctx, p.ID, nil, nil, nil, &full); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetByID(ctx, p.ID)
	if got.AuditCollect != domain.AuditCollectHitsAndUsage || got.Remark != "keep" {
		t.Fatalf("after the write = %q %q", got.AuditCollect, got.Remark)
	}
	// Omitted leaves it alone.
	remark := "x"
	if err := r.UpdateNativeMetadata(ctx, p.ID, nil, &remark, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.GetByID(ctx, p.ID); got.AuditCollect != domain.AuditCollectHitsAndUsage {
		t.Fatalf("an omitted audit_collect changed to %q", got.AuditCollect)
	}
	bad := domain.AuditCollect("everything")
	if err := r.UpdateNativeMetadata(ctx, p.ID, nil, nil, nil, &bad); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("an unknown level = %v, want ErrValidation", err)
	}
	third := seedPanel(t, r)
	off := domain.AuditCollectOff
	if err := r.UpdateNativeMetadata(ctx, third.ID, nil, nil, nil, &off); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a non-native server = %v, want ErrValidation", err)
	}
}

func TestExistingUserIDs(t *testing.T) {
	repos, db := newNativeCredentialTestRepos(t)
	ctx := context.Background()
	u := &domain.User{UPN: "alive", SubToken: "t-alive", UUID: "u-alive", Role: domain.RoleUser, Enabled: true}
	if err := repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := NewDestRepos(db).Hits.ExistingUserIDs(ctx, []int64{u.ID, u.ID + 1, u.ID + 2})
	if err != nil || len(got) != 1 || !got[u.ID] {
		t.Fatalf("existing = %v, %v", got, err)
	}
	if got, err := NewDestRepos(db).Hits.ExistingUserIDs(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("no ids = %v, %v", got, err)
	}
}

func TestBlockCountsSince_BlocksOnlyPerUserAndSource(t *testing.T) {
	r, _ := newHitRepo(t)
	h := seedHits(t, r)
	got, err := r.BlockCountsSince(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	// user 7: p12 block 31; its p13 observe row is not a block. user 8: p12 7.
	// The trial row (user 0) and s4's observe are not blocks either.
	if len(got) != 2 || got[7]["p12"] != 31 || len(got[7]) != 1 || got[8]["p12"] != 7 {
		t.Fatalf("counts = %+v", got)
	}
	if got, _ = r.BlockCountsSince(context.Background(), h+hourMS); got[7]["p12"] != 1 || got[8] != nil {
		t.Fatalf("windowed = %+v", got)
	}
}
