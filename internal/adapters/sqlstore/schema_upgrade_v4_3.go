package sqlstore

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The 4.3 release line's data migrations, kept together.
//
// This file holds every migration that exists only to carry a database written
// by a release before 4.3.0 over to the 4.3 shape: the per-line traffic
// multiplier's ledger backfill, the removal of the stored state of the retired
// VLESS route lines, and the conversion of the shared clients to one client
// per line (ADR 0043). One dispatcher, migrateV4_3,
// runs them in order, and EnsureSchema calls only that, right after
// migrateV4_2. A later release line gets its own file and dispatcher beside
// this one instead of adding another call to EnsureSchema.
//
// Delete this file, its test file and the migrateV4_3 call once upgrades from
// releases before 4.3.0 are no longer supported. Nothing outside this file may
// depend on a helper defined here, so that deletion stays a deletion.
//
// WHERE IT RUNS. After autoMigrateCurrentSchema has added the new columns, and
// before the application starts its first traffic poll (EnsureSchema returns
// before any service is wired). The order matters for the backfill: the first
// weighted poll adds charged deltas to the per-row charged ledger, so a row
// whose charged ledger still reads 0 at that point would lose its history. The
// client conversion only queues resync tasks; the sync-task worker carries
// them out once the application runs.

// trafficFactorBackfillMigrationID marks the ledger backfill as done.
const trafficFactorBackfillMigrationID = "traffic_factor_ledgers_v4_3"

// migrateV4_3 runs the 4.3 line's migrations in order. Each step is
// independently repeatable.
func migrateV4_3(db *gorm.DB) error {
	for _, step := range []func(*gorm.DB) error{
		backfillRawLifetime,
		dropVLESSRouteState,
		convertClientsToLines,
	} {
		if err := step(db); err != nil {
			return err
		}
	}
	return nil
}

// backfillRawLifetime seeds the multiplier's new ledgers from the old ones.
// Before 4.3 every byte was charged one-for-one, so the history a ledger holds
// is both the raw and the charged amount:
//
//   - users: raw_lifetime_{up,down,total}_bytes = lifetime_{up,down,total}_bytes
//     (the lifetime columns become the charged ledger and keep their value);
//   - psp_clients and, where the retired table still exists, user_xui_clients:
//     charged_lifetime_bytes = lifetime_total_bytes (the per-row lifetime
//     columns stay raw).
//
// It runs once, recorded in schema_migrations in the same transaction. The
// marker is what makes it safe to repeat: after 4.3 a per-row charged ledger
// of 0 beside a non-zero raw lifetime is a legitimate state (a free server),
// and copying raw over it on a later boot would charge that history. The WHERE
// clauses keep a single run from touching a row 4.3 already wrote, for a
// database a 4.3 binary started on before this marker existed.
func backfillRawLifetime(db *gorm.DB) error {
	// user_xui_clients is retired: a fresh install never has it and an upgrade
	// keeps it only until the shared-client migration drops it. AutoMigrate does
	// not manage it, so its new column is added here — outside the transaction
	// below, because MySQL commits implicitly around DDL.
	const legacy = "user_xui_clients"
	hasLegacy := db.Migrator().HasTable(legacy)
	if hasLegacy && !db.Migrator().HasColumn(&ownershipRow{}, "charged_lifetime_bytes") {
		if err := db.Migrator().AddColumn(&ownershipRow{}, "ChargedLifetimeBytes"); err != nil {
			return fmt.Errorf("user_xui_clients charged column: %w", err)
		}
	}
	return applyOnce(db, trafficFactorBackfillMigrationID, func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE users SET
	raw_lifetime_up_bytes = lifetime_up_bytes,
	raw_lifetime_down_bytes = lifetime_down_bytes,
	raw_lifetime_total_bytes = lifetime_total_bytes
WHERE raw_lifetime_total_bytes = 0 AND lifetime_total_bytes > 0`).Error; err != nil {
			return fmt.Errorf("users raw lifetime: %w", err)
		}
		if err := tx.Exec(`UPDATE psp_clients SET charged_lifetime_bytes = lifetime_total_bytes
WHERE charged_lifetime_bytes = 0 AND lifetime_total_bytes > 0`).Error; err != nil {
			return fmt.Errorf("psp_clients charged lifetime: %w", err)
		}
		if !hasLegacy {
			return nil
		}
		if err := tx.Exec(`UPDATE user_xui_clients SET charged_lifetime_bytes = lifetime_total_bytes
WHERE charged_lifetime_bytes = 0 AND lifetime_total_bytes > 0`).Error; err != nil {
			return fmt.Errorf("user_xui_clients charged lifetime: %w", err)
		}
		return nil
	})
}

// retiredRouteColumns names the two dropped nodes columns for the Migrator,
// which needs a parsed model for column operations on SQLite. It is not part of
// the schema and is never migrated.
type retiredRouteColumns struct {
	ID            int64  `gorm:"primaryKey"`
	RouteState    string `gorm:"column:route_state"`
	RouteRevision int64  `gorm:"column:route_revision"`
}

func (retiredRouteColumns) TableName() string { return "nodes" }

// dropVLESSRouteState removes what the retired VLESS route line feature stored:
// nodes.route_state, nodes.route_revision and the vless_route_leases table. The
// feature is gone in 4.3 and its data is deliberately not carried over: nothing
// is migrated and nothing is cleaned up on 3X-UI. The schema models no longer
// declare any of it, so AutoMigrate never re-adds it. Each object is dropped
// only if present, which makes the step repeatable and a no-op on a database
// that never had it (a fresh 4.3 install).
func dropVLESSRouteState(db *gorm.DB) error {
	m := db.Migrator()
	for _, column := range []string{"route_state", "route_revision"} {
		if !m.HasColumn(&retiredRouteColumns{}, column) {
			continue
		}
		if err := m.DropColumn(&retiredRouteColumns{}, column); err != nil {
			return fmt.Errorf("drop nodes.%s: %w", column, err)
		}
		// gorm's SQLite DropColumn rewrites the CREATE TABLE text with a regular
		// expression and silently does nothing for a column that ALTER TABLE
		// appended last. Check, and fall back to the statement every supported
		// engine understands (SQLite 3.35+, MySQL, PostgreSQL).
		if m.HasColumn(&retiredRouteColumns{}, column) {
			if err := db.Exec("ALTER TABLE nodes DROP COLUMN " + column).Error; err != nil {
				return fmt.Errorf("drop nodes.%s: %w", column, err)
			}
		}
	}
	if m.HasTable("vless_route_leases") {
		if err := m.DropTable("vless_route_leases"); err != nil {
			return fmt.Errorf("drop vless_route_leases: %w", err)
		}
	}
	return nil
}

// perLineClientsMigrationID marks the per-line client conversion as done.
const perLineClientsMigrationID = "per_line_clients_v4_3"

// convertClientsToLines carries the shared clients of 4.2 over to one client
// per line (ADR 0043), automatically: the owner confirmed nothing on 4.2 is
// priced, so there is no per-server state to preserve and no admin action.
//
//   - Every row gets its uuid_key (the masked UUID that keeps a derived line
//     identity unique on its listener).
//   - A legacy shared row becomes the direct line of the lowest-ID node it is
//     attached to, keeping its ID, email and counters. One user's legacy rows
//     on a panel are attached to disjoint nodes, so no two of them can claim
//     the same line. A row attached nowhere stays legacy; the planner adopts it
//     when the user next has a line on that panel.
//   - Every user who holds a client gets a durable user_resync task. The task
//     runs the normal planner, which narrows the adopted row to its own line,
//     creates the other direct lines with the user's UUID and the relay lines
//     with derived identities, and lets the lifecycle create and detach what
//     3X-UI / S-UI need.
//
// Recorded in schema_migrations in the same transaction: run once.
func convertClientsToLines(db *gorm.DB) error {
	return applyOnce(db, perLineClientsMigrationID, func(tx *gorm.DB) error {
		var rows []struct {
			ID     int64
			UUID   string
			NodeID *int64
		}
		if err := tx.Table("psp_clients").Select("id, uuid, node_id").Order("id").Find(&rows).Error; err != nil {
			return fmt.Errorf("list clients: %w", err)
		}
		for _, row := range rows {
			updates := map[string]any{"uuid_key": domain.LineUUIDKey(row.UUID)}
			if row.NodeID == nil {
				var lowest *int64
				if err := tx.Table("psp_client_inbounds").Select("MIN(node_id)").
					Where("client_id = ?", row.ID).Scan(&lowest).Error; err != nil {
					return fmt.Errorf("lowest node of client %d: %w", row.ID, err)
				}
				if lowest != nil && *lowest > 0 {
					updates["node_id"] = *lowest
					updates["line_id"] = domain.DirectLineID
				}
			}
			if err := tx.Table("psp_clients").Where("id = ?", row.ID).Updates(updates).Error; err != nil {
				return fmt.Errorf("convert client %d: %w", row.ID, err)
			}
		}
		var userIDs []int64
		if err := tx.Table("psp_clients").Distinct("user_id").Order("user_id").Pluck("user_id", &userIDs).Error; err != nil {
			return fmt.Errorf("list client owners: %w", err)
		}
		now := time.Now().UTC()
		for _, uid := range userIDs {
			var active int64
			if err := tx.Model(&syncTaskRow{}).Where("type = ? AND target_type = ? AND target_id = ? AND status IN ?",
				string(domain.SyncTaskUserResync), "user", uid,
				[]string{string(domain.SyncTaskPending), string(domain.SyncTaskRunning)}).Count(&active).Error; err != nil {
				return fmt.Errorf("check resync task for user %d: %w", uid, err)
			}
			if active > 0 {
				continue
			}
			if err := tx.Create(&syncTaskRow{
				Type: string(domain.SyncTaskUserResync), Status: string(domain.SyncTaskPending),
				TargetType: "user", TargetID: uid, Summary: "4.3: one client per line",
				NextRunAt: now, CreatedAt: now, UpdatedAt: now,
			}).Error; err != nil {
				return fmt.Errorf("enqueue resync for user %d: %w", uid, err)
			}
		}
		return nil
	})
}
