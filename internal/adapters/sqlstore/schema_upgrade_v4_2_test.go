package sqlstore

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestHistoricalRelayIDsArePersistedAndIdempotent(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "legacy", Region: "HK", Tags: []string{"edge"}}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	legacy := `[{"Name":"relay","Address":"relay.example","Port":443,"Enabled":true}]`
	if err := r.db.Table("nodes").Where("id = ?", n.ID).Update("relays", legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(r.db); err != nil {
		t.Fatal(err)
	}
	first, err := r.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Relays) != 1 || first.Relays[0].ID == "" || first.Relays[0].Address != "relay.example" ||
		first.Relays[0].Region != "HK" || !reflect.DeepEqual(first.Relays[0].Tags, []string{"edge"}) {
		t.Fatalf("bad migration: %+v", first.Relays)
	}
	if err := EnsureSchema(r.db); err != nil {
		t.Fatal(err)
	}
	second, err := r.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Relays[0].ID != second.Relays[0].ID {
		t.Fatal("migration changed a published ID")
	}
}

// ADR 0040 §3: lines written before line labels existed take the node's
// values; a key present with an empty value is an explicit clear and stays.
func TestLineLabelBackfillByKeyPresence(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "legacy", Region: "HK", Tags: []string{"edge", "edge", " raw "}}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	relays := `[{"ID":"a","Name":"old","Address":"relay.example","Enabled":true},` +
		`{"ID":"b","Address":"cleared.example","Enabled":true,"Region":"","Tags":[]},` +
		`{"ID":"c","Address":"half.example","Enabled":true,"Region":"JP"}]`
	if err := r.db.Table("nodes").Where("id = ?", n.ID).Updates(map[string]any{"relays": relays}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLineLabels(r.db); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	nodeTags := []string{"edge", "edge", " raw "} // copied verbatim: output must not change
	if a := got.Relays[0]; a.Region != "HK" || !reflect.DeepEqual(a.Tags, nodeTags) {
		t.Fatalf("legacy relay not backfilled: %+v", a)
	}
	if b := got.Relays[1]; b.Region != "" || len(b.Tags) != 0 {
		t.Fatalf("explicit clear overwritten: %+v", b)
	}
	if c := got.Relays[2]; c.Region != "JP" || !reflect.DeepEqual(c.Tags, nodeTags) {
		t.Fatalf("only the missing key may be filled: %+v", c)
	}
	var stored struct{ Relays string }
	if err := r.db.Table("nodes").Select("relays").Where("id = ?", n.ID).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	// Idempotent: the second start changes nothing, even after the node moves on.
	if err := r.db.Table("nodes").Where("id = ?", n.ID).Update("region", "US").Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLineLabels(r.db); err != nil {
		t.Fatal(err)
	}
	var again struct{ Relays string }
	if err := r.db.Table("nodes").Select("relays").Where("id = ?", n.ID).Scan(&again).Error; err != nil {
		t.Fatal(err)
	}
	if again != stored {
		t.Fatal("a second backfill rewrote completed lines")
	}
}

// A row an older binary rewrote after a downgrade lost the keys again; the
// next start completes it with the node's value at that time.
func TestLineLabelBackfillCompletesRowsRewrittenAfterDowngrade(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "n", Region: "HK", Relays: []domain.RelayLine{{Address: "relay.example", Enabled: true, Region: "JP", Tags: []string{"x"}}}}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	downgraded := `[{"ID":"` + n.Relays[0].ID + `","Address":"relay.example","Enabled":true}]`
	if err := r.db.Table("nodes").Where("id = ?", n.ID).Updates(map[string]any{"relays": downgraded, "region": "SG"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLineLabels(r.db); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetByID(ctx, n.ID)
	if got.Relays[0].Region != "SG" || got.Relays[0].Tags == nil {
		t.Fatalf("downgraded row not completed: %+v", got.Relays[0])
	}
}

func TestLineLabelBackfillCompareAndSwapDetectsConcurrentWriter(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "n", Region: "HK"}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := casNodeColumn(r.db, n.ID, "relays", `[{"stale":true}]`, jsonRelays{}); err == nil {
		t.Fatal("a CAS against a changed column succeeded")
	}
}

// Storage refuses a line whose omitted fields were never resolved, so a
// forgotten resolution step fails instead of silently clearing labels.
func TestStorageRejectsUnresolvedLineLabels(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "n", Relays: []domain.RelayLine{{Address: "relay.example", RegionUnresolved: true}}}
	if err := r.Create(ctx, n); !errors.Is(err, domain.ErrUnresolvedLineLabels) {
		t.Fatalf("unresolved relay stored: %v", err)
	}
	n.Relays = nil
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	stored, err := jsonRelays{{ID: "a", Address: "x"}}.Value()
	if err != nil || !strings.Contains(stored.(string), `"Region":""`) || !strings.Contains(stored.(string), `"Tags":[]`) {
		t.Fatalf("new writes must always carry both keys: %v %v", stored, err)
	}
}

// ---- dest_hits: the exit server joins the primary key ----

// legacyDestHits is dest_hits as releases before 4.2.0 created it: no
// egress_panel_id, the old key, the three old indexes. Plain DDL every
// dialect accepts.
func legacyDestHits(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, stmt := range []string{
		"DROP TABLE dest_hits",
		`CREATE TABLE dest_hits (hour_ms BIGINT NOT NULL, panel_id BIGINT NOT NULL, user_id BIGINT NOT NULL,
			source VARCHAR(24) NOT NULL, action VARCHAR(8) NOT NULL, dest VARCHAR(253) NOT NULL, port BIGINT NOT NULL,
			count BIGINT NOT NULL DEFAULT 0, first_ms BIGINT NOT NULL DEFAULT 0, last_ms BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (hour_ms, panel_id, user_id, source, action, dest, port))`,
		"CREATE INDEX idx_dest_hit_user ON dest_hits (user_id, hour_ms)",
		"CREATE INDEX idx_dest_hit_source ON dest_hits (source, hour_ms)",
		"CREATE INDEX idx_dest_hit_panel ON dest_hits (panel_id, hour_ms)",
		"INSERT INTO dest_hits (hour_ms, panel_id, user_id, source, action, dest, port, count, first_ms, last_ms) VALUES (3600000, 1, 7, 'p1', 'block', 'a.example', 443, 5, 3600001, 3600009)",
		"INSERT INTO dest_hits (hour_ms, panel_id, user_id, source, action, dest, port, count, first_ms, last_ms) VALUES (7200000, 2, 0, 'g3', 'observe', 'b.example', 80, 2, 7200001, 7200002)",
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func destHitsShapeIsCurrent(t *testing.T, db *gorm.DB) {
	t.Helper()
	done, err := destHitsKeyHasEgress(db)
	if err != nil || !done {
		t.Fatalf("egress_panel_id in the primary key = %v, %v", done, err)
	}
	var kept []destHitRow
	if err := db.Order("hour_ms").Find(&kept).Error; err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 || kept[0].EgressPanelID != 0 || kept[0].Count != 5 || kept[0].FirstMS != 3600001 || kept[0].LastMS != 3600009 ||
		kept[1].EgressPanelID != 0 || kept[1].Source != "g3" || kept[1].Count != 2 {
		t.Fatalf("rows after the rebuild = %+v", kept)
	}
	// The key now lets one row exist per exit.
	if err := db.Create(&destHitRow{HourMS: 3600000, PanelID: 1, EgressPanelID: 4, UserID: 7, Source: "p1", Action: "block", Dest: "a.example", Port: 443, Count: 1}).Error; err != nil {
		t.Fatalf("a second exit for the same row: %v", err)
	}
	for _, index := range destHitsIndexes {
		if !db.Migrator().HasIndex(&destHitRow{}, index.name) {
			t.Errorf("index %s missing", index.name)
		}
	}
	for _, leftover := range []string{destHitsRebuildTable, destHitsRetiredTable} {
		if db.Migrator().HasTable(leftover) {
			t.Errorf("leftover table %s", leftover)
		}
	}
	db.Delete(&destHitRow{}, "egress_panel_id = ?", 4)
}

func TestDestHitsRebuild_CarriesOldRowsToExitZero(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	legacyDestHits(t, db)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	destHitsShapeIsCurrent(t, db)
	// A second start finds nothing to do.
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("second start: %v", err)
	}
	destHitsShapeIsCurrent(t, db)
}

// A crash at any point of an earlier attempt leaves a temporary table beside the
// old one (before the swap), or the retired old table beside the new one (after
// it, on MySQL); the next start finishes the job.
func TestDestHitsRebuild_ResumesFromLeftovers(t *testing.T) {
	open := func(t *testing.T) *gorm.DB {
		db, err := openIsolatedTestDB(t)
		if err != nil {
			t.Fatal(err)
		}
		if err := EnsureSchema(db); err != nil {
			t.Fatal(err)
		}
		legacyDestHits(t, db)
		return db
	}
	t.Run("temporary table beside the old one", func(t *testing.T) {
		db := open(t)
		if err := db.Migrator().CreateTable(&destHitsRebuildRow{}); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO " + destHitsRebuildTable + " (hour_ms, panel_id, egress_panel_id, user_id, source, action, dest, port) VALUES (1, 1, 9, 1, 'p9', 'block', 'junk.example', 1)").Error; err != nil {
			t.Fatal(err)
		}
		if err := EnsureSchema(db); err != nil {
			t.Fatalf("resume: %v", err)
		}
		destHitsShapeIsCurrent(t, db)
	})
	t.Run("leftover tables beside the new one", func(t *testing.T) {
		db := open(t)
		if err := EnsureSchema(db); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{destHitsRebuildTable, destHitsRetiredTable} {
			if err := db.Exec("CREATE TABLE " + name + " (x BIGINT NOT NULL)").Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := EnsureSchema(db); err != nil {
			t.Fatalf("resume: %v", err)
		}
		destHitsShapeIsCurrent(t, db)
	})
}

// A replica that waited for the rebuild lock finds the other replica's finished
// table: it must leave it alone (its rows, exit ids included) and not copy it
// over with the exit forced to 0.
func TestDestHitsRebuild_ReChecksAfterTheLock(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	legacyDestHits(t, db)
	locked := false
	other := func(db *gorm.DB, fn func() error) error {
		locked = true
		// While this replica waited, another one rebuilt and ingested a hit that
		// left through exit 7.
		if err := rebuildDestHitsLocked(db, func(_ *gorm.DB, f func() error) error { return f() }); err != nil {
			return err
		}
		if err := db.Create(&destHitRow{HourMS: 3600000, PanelID: 1, EgressPanelID: 7, UserID: 7, Source: "p1", Action: "block", Dest: "a.example", Port: 443, Count: 9}).Error; err != nil {
			return err
		}
		return fn()
	}
	if err := rebuildDestHitsLocked(db, other); err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("the lock was never taken for a table that needed the rebuild")
	}
	var row destHitRow
	if err := db.Where("egress_panel_id = ?", 7).First(&row).Error; err != nil || row.Count != 9 {
		t.Fatalf("the other replica's row = %+v, %v", row, err)
	}
	var n int64
	db.Model(&destHitRow{}).Count(&n)
	if n != 3 {
		t.Fatalf("rows = %d, want the 2 carried over and the 1 ingested", n)
	}
}

// A table that is already rebuilt never takes the lock.
func TestDestHitsRebuild_DoneTableTakesNoLock(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	err = rebuildDestHitsLocked(db, func(*gorm.DB, func() error) error {
		t.Fatal("lock taken for a finished table")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// MySQL only: the named lock excludes a second session while held, the
// keep-alive runs during the rebuild, and nothing lingers afterwards (the lock
// is free again, not parked on a pooled session).
func TestDestHitsRebuildLock_HeldDuringAndFreeAfter(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if db.Dialector.Name() != "mysql" {
		t.Skip("GET_LOCK is MySQL's")
	}
	old := destHitsLockKeepAlive
	destHitsLockKeepAlive = 20 * time.Millisecond
	t.Cleanup(func() { destHitsLockKeepAlive = old })
	probe := func() int64 {
		sqlDB, _ := db.DB()
		conn, err := sqlDB.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var got sql.NullInt64
		if err := conn.QueryRowContext(t.Context(), "SELECT GET_LOCK('psp_dest_hits_v42', 0)").Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got.Int64 == 1 {
			_, _ = conn.ExecContext(t.Context(), "SELECT RELEASE_LOCK('psp_dest_hits_v42')")
		}
		return got.Int64
	}
	err = lockDestHitsRebuild(db, func() error {
		time.Sleep(100 * time.Millisecond) // several keep-alive ticks
		if got := probe(); got != 0 {
			t.Errorf("another session got the lock while it was held (%d)", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := probe(); got != 1 {
		t.Fatalf("the lock lingers after the rebuild (%d)", got)
	}
}
