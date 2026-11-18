package sqlstore

import (
	"context"
	"sort"
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

// The VLESS route line feature is gone in 4.3: its columns and lease table are
// dropped from a database that has them, and the step is a no-op, repeatable,
// on one that never did.
func TestMigrateV4_3DropsVLESSRouteState(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	m := db.Migrator()
	if m.HasColumn("nodes", "route_state") || m.HasColumn("nodes", "route_revision") || m.HasTable("vless_route_leases") {
		t.Fatal("a fresh schema still declares route state")
	}
	// Never had it: a no-op, twice.
	for i := 0; i < 2; i++ {
		if err := migrateV4_3(db); err != nil {
			t.Fatalf("no-op run %d: %v", i, err)
		}
	}

	// A database as a pre-4.3 binary left it, with a node carrying route state.
	if err := db.Exec(`ALTER TABLE nodes ADD COLUMN route_state TEXT`).Error; err != nil {
		t.Fatalf("add route_state: %v", err)
	}
	if err := db.Exec(`ALTER TABLE nodes ADD COLUMN route_revision BIGINT NOT NULL DEFAULT 0`).Error; err != nil {
		t.Fatalf("add route_revision: %v", err)
	}
	// In a real 4.2 table the route columns sit in the middle, not last: a
	// column after them is what makes gorm's SQLite DropColumn rebuild the
	// table instead of doing nothing.
	if err := db.Exec(`ALTER TABLE nodes ADD COLUMN after_route BIGINT NOT NULL DEFAULT 0`).Error; err != nil {
		t.Fatalf("add a column after the route state: %v", err)
	}
	if err := db.Exec(`CREATE TABLE vless_route_leases (id BIGINT NOT NULL PRIMARY KEY, node_id BIGINT NOT NULL)`).Error; err != nil {
		t.Fatalf("create leases: %v", err)
	}
	ctx := context.Background()
	nodes := NewRepos(db).Node
	n := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "keep"}
	if err := nodes.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	gone := &domain.Node{PanelID: 1, InboundID: 2, DisplayName: "deleted"}
	if err := nodes.Create(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`DELETE FROM nodes WHERE id = ?`, gone.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE nodes SET route_state = '{"revision":2}', route_revision = 2 WHERE id = ?`, n.ID).Error; err != nil {
		t.Fatalf("seed route state: %v", err)
	}
	indexesBefore := nodeIndexNames(t, db)
	for i := 0; i < 2; i++ {
		if err := migrateV4_3(db); err != nil {
			t.Fatalf("drop run %d: %v", i, err)
		}
	}
	// Dropping the columns keeps every index of nodes, the unique
	// (panel_id, inbound_id) key above all, and keeps the id sequence: a
	// deleted node's id is never handed out again.
	if got := nodeIndexNames(t, db); !equalStrings(got, indexesBefore) {
		t.Fatalf("nodes indexes after the drop = %v, want %v", got, indexesBefore)
	}
	if err := db.Exec(`ALTER TABLE nodes DROP COLUMN after_route`).Error; err != nil {
		t.Fatalf("drop the test column: %v", err)
	}
	if err := nodes.Create(ctx, &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "duplicate"}); err == nil {
		t.Fatal("a second node on panel 1 / inbound 1 was accepted after the drop")
	}
	next := &domain.Node{PanelID: 1, InboundID: 3, DisplayName: "next"}
	if err := nodes.Create(ctx, next); err != nil {
		t.Fatal(err)
	}
	if next.ID <= gone.ID {
		t.Fatalf("new node id %d reuses the deleted node's id %d", next.ID, gone.ID)
	}
	for _, column := range []string{"route_state", "route_revision"} {
		if m.HasColumn("nodes", column) {
			t.Fatalf("nodes.%s survived the migration", column)
		}
	}
	if m.HasTable("vless_route_leases") {
		t.Fatal("vless_route_leases survived the migration")
	}
	got, err := nodes.GetByID(ctx, n.ID)
	if err != nil || got.DisplayName != "keep" {
		t.Fatalf("node lost by the drop: %+v %v", got, err)
	}
}

// nodeIndexNames lists the secondary indexes of nodes, sorted.
func nodeIndexNames(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	indexes, err := db.Migrator().GetIndexes("nodes")
	if err != nil {
		t.Fatalf("list nodes indexes: %v", err)
	}
	var names []string
	for _, index := range indexes {
		if primary, ok := index.PrimaryKey(); ok && primary {
			continue
		}
		names = append(names, index.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("nodes has no secondary index to compare")
	}
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The upgrade turns each shared client of 4.2 into the direct line of the
// lowest-ID node it is attached to, fills uuid_key, and queues one resync per
// user for the planner to create the other lines. Once.
func TestMigrateV4_3ConvertsClientsToLines(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ctx := context.Background()
	repos := NewRepos(db)
	ur, gr := reposFor(t, db)
	g := seedGroup(t, gr, "lines", domain.GroupLimits{})
	u := seedUser(t, ur, "lines", g.ID, domain.LimitOverrides{})
	idle := seedUser(t, ur, "idle-lines", g.ID, domain.LimitOverrides{})
	mk := func(panelID int64, email, uuid string, nodes ...int64) int64 {
		t.Helper()
		id, err := repos.PSPClient.Create(ctx, &domain.PSPClient{UserID: u.ID, PanelID: panelID, Email: email, UUID: uuid, Password: "p",
			LifetimeTotalBytes: 500})
		if err != nil {
			t.Fatal(err)
		}
		inbs := make([]domain.PSPClientInbound, 0, len(nodes))
		for _, n := range nodes {
			inbs = append(inbs, domain.PSPClientInbound{ClientID: id, NodeID: n})
		}
		if len(inbs) > 0 {
			if err := repos.PSPClient.SetInbounds(ctx, id, inbs); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	shared := mk(10, "lines@psp.local", "A265B1EC-CD81-43E7-8239-09F322EF22D6", 5, 3)
	other := mk(11, "lines-other@psp.local", "a265b1ec-cd81-43e7-8239-09f322ef22d6", 9)
	nowhere := mk(12, "lines-nowhere@psp.local", "a265b1ec-cd81-43e7-8239-09f322ef22d6")
	// Written as 4.2 left them: no line, no uuid_key.
	if err := db.Table("psp_clients").Where("id IN ?", []int64{shared, other, nowhere}).
		Updates(map[string]any{"node_id": nil, "line_id": nil, "uuid_key": ""}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", perLineClientsMigrationID).Delete(&schemaMigrationRow{}).Error; err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		if err := migrateV4_3(db); err != nil {
			t.Fatalf("migrateV4_3 run %d: %v", run, err)
		}
	}
	check := func(id int64, wantNode int64, wantLine string) {
		t.Helper()
		c, err := repos.PSPClient.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if c.NodeID != wantNode || c.LineID != wantLine || c.Email == "" || c.LifetimeTotalBytes != 500 {
			t.Fatalf("client %d = node %d line %q email %q lifetime %d, want node %d line %q",
				id, c.NodeID, c.LineID, c.Email, c.LifetimeTotalBytes, wantNode, wantLine)
		}
	}
	check(shared, 3, domain.DirectLineID)
	check(other, 9, domain.DirectLineID)
	check(nowhere, 0, "")
	var keys []string
	if err := db.Table("psp_clients").Where("id IN ?", []int64{shared, other, nowhere}).Pluck("uuid_key", &keys).Error; err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k != "a265b1eccd810000823909f322ef22d6" {
			t.Fatalf("uuid_key = %q", k)
		}
	}
	var tasks []syncTaskRow
	if err := db.Where("type = ?", string(domain.SyncTaskUserResync)).Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].TargetID != u.ID || tasks[0].Status != string(domain.SyncTaskPending) {
		t.Fatalf("resync tasks = %+v, want one pending for user %d (not for %d, who holds no client)", tasks, u.ID, idle.ID)
	}
	// The planner can now take over the converted row: its line is unique.
	if _, err := repos.PSPClient.Create(ctx, &domain.PSPClient{UserID: u.ID, PanelID: 10, NodeID: 3, LineID: domain.DirectLineID,
		Email: "dup@x", UUID: "u", Password: "p"}); err == nil {
		t.Fatal("a second row for the same line was accepted")
	}
}
