package sqlstore

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ledgerRow reads the multiplier's ledgers straight off the tables.
type ledgerRow struct {
	LifetimeUpBytes       int64
	LifetimeDownBytes     int64
	LifetimeTotalBytes    int64
	RawLifetimeUpBytes    int64
	RawLifetimeDownBytes  int64
	RawLifetimeTotalBytes int64
	ChargedLifetimeBytes  int64
}

func readLedger(t *testing.T, db *gorm.DB, table string, id int64, cols string) ledgerRow {
	t.Helper()
	var row ledgerRow
	if err := db.Table(table).Select(cols).Where("id = ?", id).Scan(&row).Error; err != nil {
		t.Fatalf("read %s %d: %v", table, id, err)
	}
	return row
}

const userLedgerCols = "lifetime_up_bytes, lifetime_down_bytes, lifetime_total_bytes, raw_lifetime_up_bytes, raw_lifetime_down_bytes, raw_lifetime_total_bytes"

// The 4.3 backfill seeds the raw user ledger and the charged per-row ledgers
// from the pre-4.3 one-for-one history, once, on every supported database.
func TestMigrateV4_3BackfillsLedgersOnce(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ctx := context.Background()
	ur, gr := reposFor(t, db)
	g := seedGroup(t, gr, "ledger", domain.GroupLimits{})
	pre := seedUser(t, ur, "pre43", g.ID, domain.LimitOverrides{})
	idle := seedUser(t, ur, "idle", g.ID, domain.LimitOverrides{})
	already := seedUser(t, ur, "already", g.ID, domain.LimitOverrides{})
	// A database as a pre-4.3 binary left it: lifetime only.
	if err := db.Table("users").Where("id = ?", pre.ID).Updates(map[string]any{
		"lifetime_up_bytes": 40, "lifetime_down_bytes": 60, "lifetime_total_bytes": 100,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// A row a 4.3 binary already wrote must keep its own raw figure.
	if err := db.Table("users").Where("id = ?", already.ID).Updates(map[string]any{
		"lifetime_total_bytes": 300, "raw_lifetime_total_bytes": 200,
	}).Error; err != nil {
		t.Fatal(err)
	}
	clients := NewRepos(db).PSPClient
	clientID, err := clients.Create(ctx, &domain.PSPClient{UserID: pre.ID, PanelID: 1, Email: "pre@psp.local", UUID: "u-pre", Password: "p",
		LifetimeUpBytes: 40, LifetimeDownBytes: 60, LifetimeTotalBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	// The retired per-node table, as an upgrade from before the shared-client
	// migration still has it: without the new column.
	if err := db.Migrator().CreateTable(&ownershipRow{}); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Migrator().DropColumn(&ownershipRow{}, "charged_lifetime_bytes"); err != nil {
		t.Fatalf("drop new column: %v", err)
	}
	if err := db.Exec(`INSERT INTO user_xui_clients (user_id, panel_id, inbound_id, client_email, client_uuid, lifetime_total_bytes)
VALUES (?, 1, 1, 'legacy@x', 'legacy-uuid', 300)`, pre.ID).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	// EnsureSchema above already recorded the step on this fresh database;
	// forget it, as a database written before 4.3 never had it.
	if err := db.Where("id = ?", trafficFactorBackfillMigrationID).Delete(&schemaMigrationRow{}).Error; err != nil {
		t.Fatal(err)
	}

	if err := migrateV4_3(db); err != nil {
		t.Fatalf("migrateV4_3: %v", err)
	}
	if got := readLedger(t, db, "users", pre.ID, userLedgerCols); got.RawLifetimeUpBytes != 40 ||
		got.RawLifetimeDownBytes != 60 || got.RawLifetimeTotalBytes != 100 || got.LifetimeTotalBytes != 100 {
		t.Fatalf("pre-4.3 user = %+v, want raw = lifetime = 40/60/100", got)
	}
	if got := readLedger(t, db, "users", idle.ID, userLedgerCols); got.RawLifetimeTotalBytes != 0 {
		t.Fatalf("idle user raw = %d, want 0", got.RawLifetimeTotalBytes)
	}
	if got := readLedger(t, db, "users", already.ID, userLedgerCols); got.RawLifetimeTotalBytes != 200 || got.LifetimeTotalBytes != 300 {
		t.Fatalf("4.3 user = %+v, want raw 200 / charged 300 untouched", got)
	}
	if got := readLedger(t, db, "psp_clients", clientID, "lifetime_total_bytes, charged_lifetime_bytes"); got.ChargedLifetimeBytes != 100 || got.LifetimeTotalBytes != 100 {
		t.Fatalf("psp client = %+v, want charged = raw = 100", got)
	}
	var legacyCharged int64
	if err := db.Table("user_xui_clients").Select("charged_lifetime_bytes").Where("client_email = ?", "legacy@x").Scan(&legacyCharged).Error; err != nil {
		t.Fatalf("legacy charged column: %v", err)
	}
	if legacyCharged != 300 {
		t.Fatalf("legacy charged = %d, want 300", legacyCharged)
	}

	// After 4.3 has run, a charged ledger of 0 beside a raw lifetime is a free
	// server's history. A later boot must not copy raw over it.
	if err := db.Table("psp_clients").Where("id = ?", clientID).Updates(map[string]any{
		"lifetime_total_bytes": 500, "charged_lifetime_bytes": 0,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("second EnsureSchema: %v", err)
	}
	if got := readLedger(t, db, "psp_clients", clientID, "lifetime_total_bytes, charged_lifetime_bytes"); got.ChargedLifetimeBytes != 0 {
		t.Fatalf("rerun charged a free server's history: %+v", got)
	}
	if got := readLedger(t, db, "users", pre.ID, userLedgerCols); got.RawLifetimeTotalBytes != 100 {
		t.Fatalf("rerun changed the raw ledger: %+v", got)
	}
}

// The factor has one writer. A whole-row Save of a stale panel must not move
// it, a create may set it (0 included), and the bulk read sees every server.
func TestTrafficFactorColumnScopedWriter(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ctx := context.Background()
	repo := &xuiPanelRepo{db: db}

	plain := &domain.XUIPanel{Name: "plain", URL: "https://plain.example"}
	if err := repo.Save(ctx, plain); err != nil {
		t.Fatal(err)
	}
	zero := 0
	free := &domain.XUIPanel{Name: "free", URL: "https://free.example", TrafficFactorMilli: &zero}
	if err := repo.Save(ctx, free); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, plain.ID)
	if err != nil || got.EffectiveTrafficFactor() != domain.TrafficFactorDefault {
		t.Fatalf("default factor = %v, err %v", got, err)
	}
	if got, _ := repo.GetByID(ctx, free.ID); got.EffectiveTrafficFactor() != 0 {
		t.Fatalf("created free server reads %d, want 0", got.EffectiveTrafficFactor())
	}

	stale, _ := repo.GetByID(ctx, plain.ID)
	if err := repo.UpdateTrafficFactor(ctx, plain.ID, 1500); err != nil {
		t.Fatal(err)
	}
	// Same value again: MySQL reports no changed rows, which is not a miss.
	if err := repo.UpdateTrafficFactor(ctx, plain.ID, 1500); err != nil {
		t.Fatalf("no-op factor write: %v", err)
	}
	stale.Remark = "edited elsewhere"
	if err := repo.Save(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetByID(ctx, plain.ID); got.EffectiveTrafficFactor() != 1500 || got.Remark != "edited elsewhere" {
		t.Fatalf("after stale save: factor %d remark %q, want 1500 kept", got.EffectiveTrafficFactor(), got.Remark)
	}

	factors, err := repo.TrafficFactors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if factors[plain.ID] != 1500 || factors[free.ID] != 0 || len(factors) != 2 {
		t.Fatalf("factors = %v", factors)
	}

	for _, bad := range []int{-1, domain.TrafficFactorMax + 1} {
		if err := repo.UpdateTrafficFactor(ctx, plain.ID, bad); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("UpdateTrafficFactor(%d) = %v, want validation error", bad, err)
		}
	}
	if err := repo.UpdateTrafficFactor(ctx, 999999, 1000); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing server = %v, want not found", err)
	}
}

// The per-row charged ledgers and the user's raw ledger round-trip through
// the poll's narrow counter writers.
func TestMultiplierLedgersPersistThroughCounterWriters(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ctx := context.Background()
	ur, gr := reposFor(t, db)
	g := seedGroup(t, gr, "persist", domain.GroupLimits{})
	u := seedUser(t, ur, "persist", g.ID, domain.LimitOverrides{})
	u.LifetimeTotalBytes, u.RawLifetimeUpBytes, u.RawLifetimeDownBytes, u.RawLifetimeTotalBytes = 1500, 600, 400, 1000
	if err := ur.BatchUpdateTrafficState(ctx, []*domain.User{u}); err != nil {
		t.Fatal(err)
	}
	if got, _ := ur.GetByID(ctx, u.ID); got.RawLifetimeTotalBytes != 1000 || got.RawLifetimeUpBytes != 600 || got.LifetimeTotalBytes != 1500 {
		t.Fatalf("user = raw %d/%d charged %d", got.RawLifetimeUpBytes, got.RawLifetimeTotalBytes, got.LifetimeTotalBytes)
	}

	clients := NewRepos(db).PSPClient
	id, err := clients.Create(ctx, &domain.PSPClient{UserID: u.ID, PanelID: 1, Email: "persist@psp.local", UUID: "u-p", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := clients.GetByID(ctx, id)
	c.LifetimeTotalBytes, c.ChargedLifetimeBytes = 1000, 1500
	if err := clients.BatchUpdateCounters(ctx, []*domain.PSPClient{c}); err != nil {
		t.Fatal(err)
	}
	if got, _ := clients.GetByID(ctx, id); got.ChargedLifetimeBytes != 1500 || got.LifetimeTotalBytes != 1000 {
		t.Fatalf("client = raw %d charged %d", got.LifetimeTotalBytes, got.ChargedLifetimeBytes)
	}
}
