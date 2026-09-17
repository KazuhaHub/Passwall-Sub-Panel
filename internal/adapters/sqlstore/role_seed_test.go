package sqlstore

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestSeedBuiltinRoles asserts the three built-ins exist after EnsureSchema with
// the exact shapes that make the migration a zero-behavior-change one: the
// immutable Global Administrator keeps slug "admin" and holds the wildcard, and
// operator/user are seeded editable.
func TestSeedBuiltinRoles(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("openTestDB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	var count int64
	if err := db.Model(&roleRow{}).Count(&count).Error; err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if count != 3 {
		t.Fatalf("seeded %d roles, want 3 (admin/operator/user)", count)
	}

	var admin roleRow
	if err := db.Where("slug = ?", string(domain.RoleAdmin)).First(&admin).Error; err != nil {
		t.Fatalf("load admin role: %v", err)
	}
	if !admin.Builtin || !admin.Immutable {
		t.Errorf("admin role builtin=%v immutable=%v, want both true", admin.Builtin, admin.Immutable)
	}
	if got := []string(admin.Permissions); len(got) != 1 || got[0] != string(domain.PermAll) {
		t.Errorf("admin permissions = %v, want [%q]", got, domain.PermAll)
	}
	if admin.Name != "Global Administrator" {
		t.Errorf("admin display name = %q, want %q", admin.Name, "Global Administrator")
	}

	for _, slug := range []string{string(domain.RoleOperator), string(domain.RoleUser)} {
		var r roleRow
		if err := db.Where("slug = ?", slug).First(&r).Error; err != nil {
			t.Fatalf("load %q role: %v", slug, err)
		}
		if !r.Builtin {
			t.Errorf("%q role should be builtin", slug)
		}
		if r.Immutable {
			t.Errorf("%q role must be editable (immutable=false)", slug)
		}
	}
}

// TestSeedBuiltinRolesIdempotent asserts a second EnsureSchema is a no-op on the
// role set (boot re-run safety).
func TestSeedBuiltinRolesIdempotent(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("openTestDB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema #1: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema #2: %v", err)
	}
	var count int64
	if err := db.Model(&roleRow{}).Count(&count).Error; err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if count != 3 {
		t.Fatalf("after re-seed have %d roles, want 3", count)
	}
}

// TestSeedPreservesEditableRoleEdits asserts an admin's edit to an editable
// built-in (operator) survives a reboot — insert-if-absent, never overwrite.
func TestSeedPreservesEditableRoleEdits(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("openTestDB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema #1: %v", err)
	}
	// Admin narrows operator to a single permission.
	if err := db.Model(&roleRow{}).Where("slug = ?", string(domain.RoleOperator)).
		Update("permissions", jsonStrings{string(domain.PermUsersRead)}).Error; err != nil {
		t.Fatalf("edit operator perms: %v", err)
	}
	// Reboot.
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema #2: %v", err)
	}
	var op roleRow
	if err := db.Where("slug = ?", string(domain.RoleOperator)).First(&op).Error; err != nil {
		t.Fatalf("reload operator: %v", err)
	}
	if got := []string(op.Permissions); len(got) != 1 || got[0] != string(domain.PermUsersRead) {
		t.Errorf("operator perms after reboot = %v, want the admin edit [%q] to survive", got, domain.PermUsersRead)
	}
}

// TestSeedSelfHealsGlobalAdmin asserts that if the immutable admin row is ever
// corrupted (bad migration / manual DB edit) so it no longer holds the wildcard
// or lost its immutable flag, boot repairs it — no GA can silently lose power
// with no recovery role (invariant #1 / M4).
func TestSeedSelfHealsGlobalAdmin(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("openTestDB: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema #1: %v", err)
	}
	// Corrupt the admin row: strip the wildcard and clear immutability.
	if err := db.Model(&roleRow{}).Where("slug = ?", string(domain.RoleAdmin)).Updates(map[string]any{
		"permissions": jsonStrings{string(domain.PermUsersRead)},
		"immutable":   false,
	}).Error; err != nil {
		t.Fatalf("corrupt admin row: %v", err)
	}
	// Reboot must heal it.
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema #2: %v", err)
	}
	var admin roleRow
	if err := db.Where("slug = ?", string(domain.RoleAdmin)).First(&admin).Error; err != nil {
		t.Fatalf("reload admin: %v", err)
	}
	if got := []string(admin.Permissions); len(got) != 1 || got[0] != string(domain.PermAll) {
		t.Errorf("admin perms after heal = %v, want [%q]", got, domain.PermAll)
	}
	if !admin.Immutable {
		t.Errorf("admin immutable flag not restored on boot")
	}
}
