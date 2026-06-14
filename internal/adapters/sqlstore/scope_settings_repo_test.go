package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func newScopeRepo(t *testing.T) *kvScopeSettingsRepo {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return newKVScopeSettingsRepo(db)
}

// TestScopeSettings_SetListDelete walks the sparse-override lifecycle: a fresh
// scope has no rows (full inheritance), SetOverride adds exactly one, and
// DeleteOverride restores inheritance.
func TestScopeSettings_SetListDelete(t *testing.T) {
	repo := newScopeRepo(t)
	ctx := context.Background()

	if got, _ := repo.ListOverrides(ctx, "group", 1); len(got) != 0 {
		t.Fatalf("fresh scope should be empty (full inheritance), got %d rows", len(got))
	}

	if err := repo.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "security", Name: "require_2fa_for_staff", Value: "1"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := repo.ListOverrides(ctx, "group", 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Name != "require_2fa_for_staff" || got[0].Value != "1" {
		t.Fatalf("override not stored: %+v", got)
	}

	if err := repo.DeleteOverride(ctx, "group", 1, "security", "require_2fa_for_staff"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := repo.ListOverrides(ctx, "group", 1); len(got) != 0 {
		t.Fatalf("after delete the scope should inherit again, got %d rows", len(got))
	}
}

// TestScopeSettings_UpsertSameKey pins that re-setting a key updates in place
// (one row, latest value) rather than stacking duplicates.
func TestScopeSettings_UpsertSameKey(t *testing.T) {
	repo := newScopeRepo(t)
	ctx := context.Background()

	if err := repo.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "security", Name: "lockout_threshold", Value: "5"}); err != nil {
		t.Fatalf("set 5: %v", err)
	}
	if err := repo.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "security", Name: "lockout_threshold", Value: "20"}); err != nil {
		t.Fatalf("set 20: %v", err)
	}
	got, _ := repo.ListOverrides(ctx, "group", 1)
	if len(got) != 1 {
		t.Fatalf("upsert should keep one row, got %d", len(got))
	}
	if got[0].Value != "20" {
		t.Errorf("upsert lost latest value: got %q, want 20", got[0].Value)
	}
}

// TestScopeSettings_RejectsUnknownKey: a (type,name) not in settingDescriptors is
// refused so a value can't be stranded on a dead key.
func TestScopeSettings_RejectsUnknownKey(t *testing.T) {
	repo := newScopeRepo(t)
	ctx := context.Background()

	if err := repo.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "security", Name: "does_not_exist", Value: "x"}); err == nil {
		t.Fatal("expected error setting an unknown key, got nil")
	}
	if got, _ := repo.ListOverrides(ctx, "group", 1); len(got) != 0 {
		t.Errorf("rejected key must not be written, got %d rows", len(got))
	}
}

// TestScopeSettings_RejectsEncryptedKey: encrypted-at-rest settings are
// global-only (§10-3). Both a key that is intrinsically encrypted AND a
// caller-supplied Encrypted=true must be refused.
func TestScopeSettings_RejectsEncryptedKey(t *testing.T) {
	repo := newScopeRepo(t)
	ctx := context.Background()

	// captcha_secret_key / geo_ip_update_token are encStrField (encrypted).
	if err := repo.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "security", Name: "captcha_secret_key", Value: "s3cret"}); err == nil {
		t.Error("expected error overriding an encrypted key, got nil")
	}
	// A non-encrypted key flagged Encrypted=true by the caller is also refused.
	if err := repo.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "security", Name: "require_2fa_for_staff", Value: "1", Encrypted: true}); err == nil {
		t.Error("expected error for caller-set Encrypted=true, got nil")
	}
	if got, _ := repo.ListOverrides(ctx, "group", 1); len(got) != 0 {
		t.Errorf("rejected encrypted overrides must not be written, got %d rows", len(got))
	}
}

// TestScopeSettings_DeleteScope removes every override for a scope in one call
// (the group-delete cleanup path).
func TestScopeSettings_DeleteScope(t *testing.T) {
	repo := newScopeRepo(t)
	ctx := context.Background()

	_ = repo.SetOverride(ctx, "group", 7, ports.ScopeOverride{Type: "security", Name: "require_2fa_for_staff", Value: "1"})
	_ = repo.SetOverride(ctx, "group", 7, ports.ScopeOverride{Type: "sub", Name: "sub_update_interval_hours", Value: "12"})
	if got, _ := repo.ListOverrides(ctx, "group", 7); len(got) != 2 {
		t.Fatalf("setup: want 2 overrides, got %d", len(got))
	}
	if err := repo.DeleteScope(ctx, "group", 7); err != nil {
		t.Fatalf("delete scope: %v", err)
	}
	if got, _ := repo.ListOverrides(ctx, "group", 7); len(got) != 0 {
		t.Errorf("DeleteScope should clear all rows, got %d", len(got))
	}
}

// TestScopeSettings_ScopeIsolation: one group's overrides never bleed into
// another's.
func TestScopeSettings_ScopeIsolation(t *testing.T) {
	repo := newScopeRepo(t)
	ctx := context.Background()

	_ = repo.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "security", Name: "lockout_threshold", Value: "3"})
	_ = repo.SetOverride(ctx, "group", 2, ports.ScopeOverride{Type: "security", Name: "lockout_threshold", Value: "99"})

	g1, _ := repo.ListOverrides(ctx, "group", 1)
	g2, _ := repo.ListOverrides(ctx, "group", 2)
	if len(g1) != 1 || g1[0].Value != "3" {
		t.Errorf("group 1 override wrong: %+v", g1)
	}
	if len(g2) != 1 || g2[0].Value != "99" {
		t.Errorf("group 2 override wrong: %+v", g2)
	}
}
