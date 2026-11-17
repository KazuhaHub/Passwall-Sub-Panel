package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The 4.2 release line's data migrations, kept together.
//
// This file holds every migration that exists only to carry a database written
// by a release before 4.2.0 over to the 4.2 shape: the relay-line ID and label
// backfills, and the rebuild that gives dest_hits its exit-server key column.
// One dispatcher, migrateV4_2, runs them in order, and EnsureSchema calls only
// that. A later release line gets its own file and dispatcher beside this one
// instead of adding another call to EnsureSchema.
//
// Delete this file, its test file and the migrateV4_2 call once upgrades from
// releases before 4.2.0 are no longer supported. Nothing outside this file may
// depend on a helper defined here, so that deletion stays a deletion.
//
// WHERE IT RUNS. In the place the two backfills have always run: after
// autoMigrateCurrentSchema, the V3 -> V4 bridge and the traffic-counter repair,
// before the built-in roles. They cannot run earlier (a column they read may not
// exist until AutoMigrate adds it), but the dest_hits rebuild has to happen
// before AutoMigrate could touch dest_hits: handed a table whose primary key
// lacks a column the model declares, GORM's SQLite migrator tries a table
// rebuild of its own and fails on the key columns. So AutoMigrate is told to
// leave a dest_hits that still awaits the rebuild alone
// (modelsWithoutPendingV4_2Rebuilds, called from autoMigrateCurrentSchema), and
// the rebuild, running later in this dispatcher, creates the table in its final
// shape, indexes included. Deleting this file means deleting that call too.

// migrateV4_2 runs the 4.2 line's migrations in the order the backfills ran
// before they were collected here, with the dest_hits rebuild last. Each step
// is independently repeatable.
func migrateV4_2(db *gorm.DB) error {
	// Assign labels before the ID migration serializes missing labels as empty keys.
	for _, step := range []func(*gorm.DB) error{
		backfillLineLabels,
		backfillRelayLineIDs,
		rebuildDestHitsWithEgressKey,
	} {
		if err := step(db); err != nil {
			return err
		}
	}
	return nil
}

// backfillRelayLineIDs is idempotent and only changes the relay JSON column.
// The compare-and-swap also prevents concurrent starting replicas from
// publishing different IDs for the same historical entry.
func backfillRelayLineIDs(db *gorm.DB) error {
	var rows []struct {
		ID     int64
		Relays string
	}
	if err := db.Table("nodes").Select("id, relays").Where("relays IS NOT NULL AND relays <> '' AND relays <> '[]'").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var lines jsonRelays
		if err := lines.Scan(row.Relays); err != nil {
			return fmt.Errorf("relay IDs for node %d: %w", row.ID, err)
		}
		changed := domain.AssignRelayIDs([]domain.RelayLine(lines))
		if err := domain.ValidateRelayIDs([]domain.RelayLine(lines)); err != nil {
			return fmt.Errorf("relay IDs for node %d: %w", row.ID, err)
		}
		if !changed {
			continue
		}
		result := db.Table("nodes").Where("id = ? AND relays = ?", row.ID, row.Relays).Update("relays", lines)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("relay IDs for node %d changed concurrently; retry startup", row.ID)
		}
	}
	return nil
}

// backfillLineLabels gives relay and VLESS route lines written before ADR 0040
// the node's Region and Tags. It decides per line by key presence, not by a
// completion marker: a row an older binary rewrote after a downgrade loses the
// keys again and is completed on the next start, while a key present with an
// empty value is an explicit clear and is left alone. Copying the node's
// values verbatim keeps every rendered subscription byte-identical. Each
// column is written with compare-and-swap against the exact text read, so a
// concurrently starting replica makes this start fail and retry.
func backfillLineLabels(db *gorm.DB) error {
	var rows []struct {
		ID         int64
		Region     string
		Tags       jsonStrings
		Relays     *string
		RouteState *string
	}
	if err := db.Table("nodes").Select("id, region, tags, relays, route_state").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if row.Relays != nil {
			if relays, changed, err := relaysWithLabels(*row.Relays, row.Region, row.Tags); err != nil {
				return fmt.Errorf("line labels for node %d: %w", row.ID, err)
			} else if changed {
				if err := casNodeColumn(db, row.ID, "relays", *row.Relays, relays); err != nil {
					return err
				}
			}
		}
		if row.RouteState != nil {
			if state, changed, err := routeStateWithLabels(*row.RouteState, row.Region, row.Tags); err != nil {
				return fmt.Errorf("line labels for node %d: %w", row.ID, err)
			} else if changed {
				if err := casNodeColumn(db, row.ID, "route_state", *row.RouteState, state); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func casNodeColumn(db *gorm.DB, nodeID int64, column, previous string, next any) error {
	result := db.Table("nodes").Where("id = ? AND "+column+" = ?", nodeID, previous).Update(column, next)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("line labels for node %d changed concurrently; retry startup", nodeID)
	}
	return nil
}

// missingLabelKeys reports, per line, whether the stored JSON lacks the
// region/tags keys (under the given key spellings).
func missingLabelKeys(raw []byte, regionKey, tagsKey string) ([][2]bool, error) {
	var lines []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, err
	}
	out := make([][2]bool, len(lines))
	for i, line := range lines {
		_, hasRegion := line[regionKey]
		_, hasTags := line[tagsKey]
		out[i] = [2]bool{!hasRegion, !hasTags}
	}
	return out, nil
}

func relaysWithLabels(raw, region string, tags []string) (jsonRelays, bool, error) {
	if raw == "" || raw == "[]" {
		return nil, false, nil
	}
	missing, err := missingLabelKeys([]byte(raw), "Region", "Tags")
	if err != nil {
		return nil, false, err
	}
	var lines jsonRelays
	if err := lines.Scan(raw); err != nil {
		return nil, false, err
	}
	if len(lines) != len(missing) {
		return nil, false, fmt.Errorf("relay line count mismatch")
	}
	changed := false
	for i := range lines {
		if missing[i][0] {
			lines[i].Region, changed = region, true
		}
		if missing[i][1] {
			lines[i].Tags, changed = domain.CopyLineTags(tags), true
		}
	}
	return lines, changed, nil
}

func routeStateWithLabels(raw, region string, tags []string) (jsonRouteState, bool, error) {
	if raw == "" {
		return jsonRouteState{}, false, nil
	}
	var envelope struct {
		Lines json.RawMessage `json:"lines"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return jsonRouteState{}, false, err
	}
	if len(envelope.Lines) == 0 || string(envelope.Lines) == "null" {
		return jsonRouteState{}, false, nil
	}
	missing, err := missingLabelKeys(envelope.Lines, "region", "tags")
	if err != nil {
		return jsonRouteState{}, false, err
	}
	var state jsonRouteState
	if err := state.Scan(raw); err != nil {
		return jsonRouteState{}, false, err
	}
	if len(state.Lines) != len(missing) {
		return jsonRouteState{}, false, fmt.Errorf("route line count mismatch")
	}
	changed := false
	for i := range state.Lines {
		if missing[i][0] {
			state.Lines[i].Region, changed = region, true
		}
		if missing[i][1] {
			state.Lines[i].Tags, changed = domain.CopyLineTags(tags), true
		}
	}
	return state, changed, nil
}

// ---- dest_hits: the exit server joins the primary key ----

const (
	destHitsTable        = "dest_hits"
	destHitsRebuildTable = "dest_hits_v42"
	destHitsRetiredTable = "dest_hits_v42_old"
)

// destHitsRebuildRow is dest_hits as the rebuild creates it: the same columns
// and primary key as destHitRow, under a temporary name and with no secondary
// index (an index name is global on PostgreSQL and SQLite, so the real names
// are created only once the old table is gone).
type destHitsRebuildRow struct {
	HourMS        int64  `gorm:"column:hour_ms;primaryKey;autoIncrement:false"`
	PanelID       int64  `gorm:"column:panel_id;primaryKey;autoIncrement:false"`
	EgressPanelID int64  `gorm:"column:egress_panel_id;primaryKey;autoIncrement:false;not null;default:0"`
	UserID        int64  `gorm:"column:user_id;primaryKey;autoIncrement:false"`
	Source        string `gorm:"column:source;primaryKey;size:24"`
	Action        string `gorm:"column:action;primaryKey;size:8"`
	Dest          string `gorm:"column:dest;primaryKey;size:253"`
	Port          int    `gorm:"column:port;primaryKey;autoIncrement:false"`
	Count         int64  `gorm:"column:count;not null;default:0"`
	FirstMS       int64  `gorm:"column:first_ms;not null;default:0"`
	LastMS        int64  `gorm:"column:last_ms;not null;default:0"`
}

func (destHitsRebuildRow) TableName() string { return destHitsRebuildTable }

// destHitsIndexes are dest_hits' secondary indexes, as destHitRow declares them.
var destHitsIndexes = []struct {
	name    string
	columns []string
}{
	{"idx_dest_hit_user", []string{"user_id", "hour_ms"}},
	{"idx_dest_hit_source", []string{"source", "hour_ms"}},
	{"idx_dest_hit_panel", []string{"panel_id", "hour_ms"}},
	{"idx_dest_hit_egress", []string{"egress_panel_id", "hour_ms"}},
}

var destHitsColumns = []string{"hour_ms", "panel_id", "egress_panel_id", "user_id", "source", "action", "dest", "port", "count", "first_ms", "last_ms"}

// rebuildDestHitsWithEgressKey puts egress_panel_id into dest_hits' primary
// key. Every row that exists keeps its data and gets exit 0 ("left from the
// reporting server itself"), which is exactly what it was.
//
// A key column cannot be added in place on all three dialects, so the table is
// rebuilt: a new table with the final key is created and filled from the old
// one, then swapped in. DONE IS DECIDED FROM THE DATABASE, never from a marker:
// the table is done when its primary key holds the column. Anything else
// (including a crash at any point of an earlier attempt) starts again from the
// top, which first drops whatever temporary table that attempt left.
//
//   - PostgreSQL and SQLite run all of it in one transaction (their DDL is
//     transactional), so there is no state in between.
//   - MySQL's DDL commits as it goes. The copy happens into the temporary
//     table, the indexes are built on it, and the swap is ONE atomic RENAME
//     TABLE of both names; dest_hits therefore always exists, with either the
//     old key or the new. What a crash can leave is the temporary table (before
//     the swap) or the retired old table (after it); the next start drops both.
//
// Replicas of THIS release starting together are serialized on MySQL by a named
// lock (lockDestHitsRebuild). A replica of an OLDER release that keeps writing
// while this runs can still lose the rows it writes between the copy and the
// swap on MySQL.
//
// On PostgreSQL and SQLite the rebuild is one transaction, so a crash leaves
// nothing behind, but there is no lock between replicas: a second replica that
// starts while the first one rebuilds fails once on the duplicate CREATE TABLE
// (or on the table the first one dropped) and succeeds when it is started
// again, with no data lost.
func rebuildDestHitsWithEgressKey(db *gorm.DB) error {
	return rebuildDestHitsLocked(db, lockDestHitsRebuild)
}

// destHitsRebuildLockWait is how long a starting replica waits for another one
// that is rebuilding dest_hits (MySQL only). A rebuild is a copy of the hit
// table, so this is generous; past it the start fails rather than guess.
var destHitsRebuildLockWait = 10 * time.Minute

// destHitsLocker runs fn while no other replica rebuilds dest_hits.
type destHitsLocker func(db *gorm.DB, fn func() error) error

// rebuildDestHitsLocked decides, takes the lock, and DECIDES AGAIN under it: a
// replica that waited for the lock finds the other one's finished table and
// must neither copy it (its rows would get exit 0) nor drop it.
func rebuildDestHitsLocked(db *gorm.DB, lock destHitsLocker) error {
	done, err := destHitsKeyHasEgress(db)
	if err != nil {
		return err
	}
	if done {
		return dropDestHitsLeftovers(db)
	}
	return lock(db, func() error {
		done, err := destHitsKeyHasEgress(db)
		if err != nil {
			return err
		}
		if done {
			return dropDestHitsLeftovers(db)
		}
		if db.Dialector.Name() == "mysql" {
			return rebuildDestHits(db, true)
		}
		return db.Transaction(func(tx *gorm.DB) error { return rebuildDestHits(tx, false) })
	})
}

// destHitsLockKeepAlive is how often the lock's session is touched while the
// rebuild runs, so an idle-connection timeout (wait_timeout, a proxy) does not
// end the session and silently drop the lock during a long copy.
var destHitsLockKeepAlive = time.Minute

// lockDestHitsRebuild serializes the MySQL rebuild across replicas with a named
// lock. GET_LOCK belongs to one SESSION, and the pool may hand every statement
// a different connection, so the lock is taken, held and released on one
// dedicated connection; the rebuild itself uses the pool. PostgreSQL and SQLite
// have no such lock (see rebuildDestHitsWithEgressKey).
func lockDestHitsRebuild(db *gorm.DB, fn func() error) error {
	if db.Dialector.Name() != "mysql" {
		return fn()
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("lock %s rebuild: %w", destHitsTable, err)
	}
	// Closing a sql.Conn only returns the session to the pool: the lock would
	// stay held by a pooled session. Every path that is not a confirmed
	// RELEASE_LOCK therefore marks the session bad, so database/sql closes it
	// for real (the server drops the lock with the session).
	discard := func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	defer conn.Close()
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK('psp_dest_hits_v42', ?)", int(destHitsRebuildLockWait.Seconds())).Scan(&got); err != nil {
		discard()
		return fmt.Errorf("lock %s rebuild: %w", destHitsTable, err)
	}
	switch {
	case !got.Valid:
		// NULL: the server could not take the lock at all (for instance the
		// session was killed), which is not the same as another replica holding it.
		discard()
		return fmt.Errorf("lock %s rebuild: GET_LOCK failed (returned NULL; the lock session was probably killed or the server ran out of memory)", destHitsTable)
	case got.Int64 != 1:
		discard()
		return fmt.Errorf("another replica has been rebuilding %s for more than %s (lock psp_dest_hits_v42 timed out); start this one again once it has finished", destHitsTable, destHitsRebuildLockWait)
	}

	// Keep the session busy while the rebuild uses other connections.
	stop, stopped := make(chan struct{}), make(chan struct{})
	var lost error
	go func() {
		defer close(stopped)
		tick := time.NewTicker(destHitsLockKeepAlive)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if _, err := conn.ExecContext(ctx, "SELECT 1"); err != nil {
					lost = err
					return
				}
			}
		}
	}()
	runErr := fn()
	close(stop)
	<-stopped

	var released sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK('psp_dest_hits_v42')").Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
		discard()
		if runErr == nil && lost == nil {
			// The rebuild finished; only the bookkeeping failed, and the session
			// has been closed, which releases the lock on the server.
			return nil
		}
	}
	if runErr != nil {
		return runErr
	}
	if lost != nil {
		return fmt.Errorf("lock %s rebuild: the lock session was lost during the rebuild (%v); the rebuild finished, start again to verify", destHitsTable, lost)
	}
	return nil
}

// destHitsKeyHasEgress reports whether egress_panel_id is part of dest_hits'
// primary key. A table the driver cannot describe is an error, not "not done".
func destHitsKeyHasEgress(db *gorm.DB) (bool, error) {
	columns, err := db.Migrator().ColumnTypes(destHitsTable)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", destHitsTable, err)
	}
	for _, column := range columns {
		if column.Name() != "egress_panel_id" {
			continue
		}
		primary, known := column.PrimaryKey()
		if !known {
			return false, fmt.Errorf("inspect %s: the driver does not report primary keys", destHitsTable)
		}
		return primary, nil
	}
	return false, nil
}

func dropDestHitsLeftovers(db *gorm.DB) error {
	for _, name := range []string{destHitsRebuildTable, destHitsRetiredTable} {
		if !db.Migrator().HasTable(name) {
			continue
		}
		if err := db.Exec("DROP TABLE IF EXISTS " + quoteName(db, name)).Error; err != nil {
			return fmt.Errorf("drop leftover %s: %w", name, err)
		}
	}
	return nil
}

func quoteName(db *gorm.DB, name string) string {
	var b strings.Builder
	db.Dialector.QuoteTo(&b, name)
	return b.String()
}

func rebuildDestHits(db *gorm.DB, mysql bool) error {
	if err := dropDestHitsLeftovers(db); err != nil {
		return err
	}
	hasEgress, err := migrationHasColumn(db, destHitsTable, "egress_panel_id")
	if err != nil {
		return err
	}
	if err := db.Migrator().CreateTable(&destHitsRebuildRow{}); err != nil {
		return fmt.Errorf("create %s: %w", destHitsRebuildTable, err)
	}
	q := func(name string) string { return quoteName(db, name) }
	quoted := make([]string, len(destHitsColumns))
	selected := make([]string, len(destHitsColumns))
	for i, c := range destHitsColumns {
		quoted[i], selected[i] = q(c), q(c)
		if c == "egress_panel_id" && !hasEgress {
			selected[i] = "0"
		}
	}
	if err := db.Exec("INSERT INTO " + q(destHitsRebuildTable) + " (" + strings.Join(quoted, ", ") + ") SELECT " +
		strings.Join(selected, ", ") + " FROM " + q(destHitsTable)).Error; err != nil {
		return fmt.Errorf("copy %s: %w", destHitsTable, err)
	}
	createIndexes := func(table string) error {
		for _, index := range destHitsIndexes {
			cols := make([]string, len(index.columns))
			for i, c := range index.columns {
				cols[i] = q(c)
			}
			if err := db.Exec("CREATE INDEX " + q(index.name) + " ON " + q(table) + " (" + strings.Join(cols, ", ") + ")").Error; err != nil {
				return fmt.Errorf("create index %s: %w", index.name, err)
			}
		}
		return nil
	}
	if mysql {
		// Index names are per table here, so they can be built before the swap.
		if err := createIndexes(destHitsRebuildTable); err != nil {
			return err
		}
		if err := db.Exec("RENAME TABLE " + q(destHitsTable) + " TO " + q(destHitsRetiredTable) + ", " +
			q(destHitsRebuildTable) + " TO " + q(destHitsTable)).Error; err != nil {
			return fmt.Errorf("swap %s: %w", destHitsTable, err)
		}
		return dropDestHitsLeftovers(db)
	}
	// Index and constraint names are global: the old table, with its indexes,
	// goes first, so the final names are free.
	if err := db.Exec("DROP TABLE " + q(destHitsTable)).Error; err != nil {
		return fmt.Errorf("drop old %s: %w", destHitsTable, err)
	}
	if err := db.Exec("ALTER TABLE " + q(destHitsRebuildTable) + " RENAME TO " + q(destHitsTable)).Error; err != nil {
		return fmt.Errorf("rename %s: %w", destHitsRebuildTable, err)
	}
	if db.Dialector.Name() == "postgres" {
		if err := db.Exec("ALTER TABLE " + q(destHitsTable) + " RENAME CONSTRAINT " + q(destHitsRebuildTable+"_pkey") + " TO " + q(destHitsTable+"_pkey")).Error; err != nil {
			return fmt.Errorf("rename %s key: %w", destHitsTable, err)
		}
	}
	return createIndexes(destHitsTable)
}

// modelsWithoutPendingV4_2Rebuilds is models without the ones a 4.2 migration
// will rebuild, so AutoMigrate leaves their tables as they are. A table that
// does not exist yet is not pending: AutoMigrate creates it in its final shape.
func modelsWithoutPendingV4_2Rebuilds(db *gorm.DB, models []any) ([]any, error) {
	if !db.Migrator().HasTable(destHitsTable) {
		return models, nil
	}
	done, err := destHitsKeyHasEgress(db)
	if err != nil || done {
		return models, err
	}
	out := make([]any, 0, len(models))
	for _, m := range models {
		if _, pending := m.(*destHitRow); !pending {
			out = append(out, m)
		}
	}
	return out, nil
}
