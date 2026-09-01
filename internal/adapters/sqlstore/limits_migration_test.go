package sqlstore

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

// legacyUsersTable creates the users table as it looked BEFORE the third
// state: the three entitlement columns NOT NULL, defaulting to 0, where 0 was
// the only way to say "unlimited". Rows are inserted through raw SQL so the
// current (nullable) model cannot launder them.
func seedLegacyUsers(t *testing.T, db *gorm.DB) {
	t.Helper()
	rows := []struct {
		upn        string
		traffic    int64
		ip, device int
	}{
		{"all-zero", 0, 0, 0}, // the common case: no limits anywhere
		{"quota-only", 100 << 30, 0, 0},
		{"caps-only", 0, 3, 2},
		{"everything", 50 << 30, 5, 4},
	}
	for _, r := range rows {
		if err := db.Exec(`INSERT INTO users
			(upn, sub_token, uuid, role, group_id, enabled, traffic_limit_bytes, ip_limit, device_limit)
			VALUES (?, ?, ?, 'user', 0, TRUE, ?, ?, ?)`,
			r.upn, r.upn+"-tok", r.upn+"-uuid", r.traffic, r.ip, r.device).Error; err != nil {
			t.Fatalf("seed %s: %v", r.upn, err)
		}
	}
}

type limitCols struct {
	Traffic *int64 `gorm:"column:traffic_limit_bytes"`
	IP      *int   `gorm:"column:ip_limit"`
	Device  *int   `gorm:"column:device_limit"`
}

func readLimits(t *testing.T, db *gorm.DB, upn string) limitCols {
	t.Helper()
	var got limitCols
	if err := db.Raw(
		`SELECT traffic_limit_bytes, ip_limit, device_limit FROM users WHERE upn = ?`, upn,
	).Scan(&got).Error; err != nil {
		t.Fatalf("read %s: %v", upn, err)
	}
	return got
}

// The migration reinterprets a stored 0 as "inherit". It is correct exactly
// once, so this pins both halves: that it converts, and that a second run
// leaves a deliberately-entered 0 alone.
func TestMigrateLimitsToTriState(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Skipf("no test DB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	// EnsureSchema already recorded the migration on this fresh database, so
	// roll it back to simulate an install upgrading INTO it.
	if err := db.Exec(`DELETE FROM schema_migrations WHERE id = ?`, limitsTriStateMigrationID).Error; err != nil {
		t.Fatal(err)
	}
	seedLegacyUsers(t, db)

	if err := migrateLimitsToTriState(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Every 0 becomes NULL = inherit. Nothing is lost: before the group layer
	// existed, a 0 could not carry "unlimited in contrast to my group".
	if got := readLimits(t, db, "all-zero"); got.Traffic != nil || got.IP != nil || got.Device != nil {
		t.Errorf("all-zero: %+v, want every column NULL (inherit)", got)
	}
	// Non-zero values are real decisions and stay explicit.
	got := readLimits(t, db, "everything")
	if got.Traffic == nil || *got.Traffic != 50<<30 || got.IP == nil || *got.IP != 5 || got.Device == nil || *got.Device != 4 {
		t.Errorf("everything: %+v, want the explicit values preserved", got)
	}
	// Columns convert independently.
	got = readLimits(t, db, "quota-only")
	if got.Traffic == nil || *got.Traffic != 100<<30 {
		t.Errorf("quota-only traffic = %+v, want it preserved", got.Traffic)
	}
	if got.IP != nil || got.Device != nil {
		t.Errorf("quota-only caps = %+v, want NULL", got)
	}
	got = readLimits(t, db, "caps-only")
	if got.Traffic != nil {
		t.Errorf("caps-only traffic = %+v, want NULL", got.Traffic)
	}
	if got.IP == nil || *got.IP != 3 || got.Device == nil || *got.Device != 2 {
		t.Errorf("caps-only caps = %+v, want preserved", got)
	}

	// THE important half. An operator now sets a user to an explicit 0 —
	// "unlimited, whatever my group says". Re-running the migration must not
	// silently convert that back into inheritance.
	if err := db.Exec(`UPDATE users SET ip_limit = 0 WHERE upn = 'everything'`).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateLimitsToTriState(db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := readLimits(t, db, "everything"); got.IP == nil {
		t.Error("a second run reinterpreted a deliberately-entered 0; the marker did not hold")
	}
}

// Behaviour must be identical the instant the migration lands: groups start
// stating nothing, so every converted user still resolves to unlimited.
func TestMigrationPreservesEffectiveLimits(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Skipf("no test DB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE id = ?`, limitsTriStateMigrationID).Error; err != nil {
		t.Fatal(err)
	}
	seedLegacyUsers(t, db)
	if err := migrateLimitsToTriState(db); err != nil {
		t.Fatal(err)
	}

	ur, _ := reposFor(t, db)
	u, err := ur.GetByUPN(context.Background(), "all-zero")
	if err != nil {
		t.Fatal(err)
	}
	if u.TrafficLimitBytes != 0 || u.IPLimit != 0 || u.DeviceLimit != 0 {
		t.Fatalf("resolved = %d/%d/%d, want all-unlimited exactly as before the migration",
			u.TrafficLimitBytes, u.IPLimit, u.DeviceLimit)
	}
	u, err = ur.GetByUPN(context.Background(), "everything")
	if err != nil {
		t.Fatal(err)
	}
	if u.TrafficLimitBytes != 50<<30 || u.IPLimit != 5 || u.DeviceLimit != 4 {
		t.Fatalf("resolved = %d/%d/%d, want the explicit values unchanged",
			u.TrafficLimitBytes, u.IPLimit, u.DeviceLimit)
	}
}
