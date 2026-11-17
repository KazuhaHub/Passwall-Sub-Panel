package sqlstore

import (
	"fmt"

	"gorm.io/gorm"
)

// The 4.3 release line's data migrations, kept together.
//
// This file holds every migration that exists only to carry a database written
// by a release before 4.3.0 over to the 4.3 shape. Today that is one step: the
// per-server traffic multiplier's ledger backfill. One dispatcher, migrateV4_3,
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
// whose charged ledger still reads 0 at that point would lose its history.

// trafficFactorBackfillMigrationID marks the ledger backfill as done.
const trafficFactorBackfillMigrationID = "traffic_factor_ledgers_v4_3"

// migrateV4_3 runs the 4.3 line's migrations in order. Each step is
// independently repeatable.
func migrateV4_3(db *gorm.DB) error {
	for _, step := range []func(*gorm.DB) error{
		backfillRawLifetime,
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
