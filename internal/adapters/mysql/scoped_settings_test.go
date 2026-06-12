package mysql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func newScopedTestRepos(t *testing.T) (ports.SettingsRepo, *kvScopeSettingsRepo, ports.ScopedSettings) {
	t.Helper()
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
	global := newKVSettingsRepo(db)
	scope := newKVScopeSettingsRepo(db)
	return global, scope, NewScopedSettings(global, scope)
}

// TestScopedSettings_NoOverridesEqualsGlobal: with an empty scope_settings table
// every group resolves to the exact global value — the zero-migration guarantee
// (and the regression baseline for migrating consumers from Load → LoadForUser).
func TestScopedSettings_NoOverridesEqualsGlobal(t *testing.T) {
	global, _, resolver := newScopedTestRepos(t)
	ctx := context.Background()

	gl, _ := global.Load(ctx, ports.UISettings{})
	g, err := resolver.LoadForGroup(ctx, 1, ports.UISettings{})
	if err != nil {
		t.Fatalf("LoadForGroup: %v", err)
	}
	if g.Require2FAForStaff != gl.Require2FAForStaff ||
		g.LockoutThreshold != gl.LockoutThreshold ||
		g.SubUpdateIntervalHours != gl.SubUpdateIntervalHours ||
		g.JWTIssuer != gl.JWTIssuer {
		t.Errorf("no-override group must equal global:\n group=%+v\nglobal=%+v", g, gl)
	}
}

// TestScopedSettings_GroupOverrideWins: a group override changes only that
// group's effective value; the global value (and other groups) are unaffected.
func TestScopedSettings_GroupOverrideWins(t *testing.T) {
	global, scope, resolver := newScopedTestRepos(t)
	ctx := context.Background()

	base, _ := global.Load(ctx, ports.UISettings{})
	base.Require2FAForStaff = false
	if err := global.Save(ctx, base); err != nil {
		t.Fatalf("save global: %v", err)
	}
	if err := scope.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "security", Name: "require_2fa_for_staff", Value: "1"}); err != nil {
		t.Fatalf("set override: %v", err)
	}

	g1, _ := resolver.LoadForGroup(ctx, 1, ports.UISettings{})
	if !g1.Require2FAForStaff {
		t.Error("group 1 should see the override require_2fa_for_staff=true")
	}
	gl, _ := resolver.Load(ctx, ports.UISettings{})
	if gl.Require2FAForStaff {
		t.Error("global value must be unaffected by a group override")
	}
	g2, _ := resolver.LoadForGroup(ctx, 2, ports.UISettings{})
	if g2.Require2FAForStaff {
		t.Error("group 2 (no override) must inherit the global false")
	}
}

// TestScopedSettings_GroupIDZeroIsPureGlobal: GroupID 0 resolves to the global
// value without consulting the override table (authpolicy's existing fail-safe).
func TestScopedSettings_GroupIDZeroIsPureGlobal(t *testing.T) {
	global, _, resolver := newScopedTestRepos(t)
	ctx := context.Background()

	gl, _ := global.Load(ctx, ports.UISettings{})
	g0, err := resolver.LoadForGroup(ctx, 0, ports.UISettings{})
	if err != nil {
		t.Fatalf("LoadForGroup(0): %v", err)
	}
	if g0.Require2FAForStaff != gl.Require2FAForStaff || g0.LockoutThreshold != gl.LockoutThreshold {
		t.Errorf("GroupID 0 must be pure global; got %+v vs %+v", g0, gl)
	}
}

// TestScopedSettings_OverrideBeatsDefaultedBase: the override is applied ON TOP
// of the already-defaulted global base and is NOT re-floored — a group may set a
// value below the default (here lockout_threshold 5 vs the default 10).
func TestScopedSettings_OverrideBeatsDefaultedBase(t *testing.T) {
	global, scope, resolver := newScopedTestRepos(t)
	ctx := context.Background()

	gl, _ := global.Load(ctx, ports.UISettings{})
	if gl.LockoutThreshold != 10 {
		t.Fatalf("precondition: default lockout_threshold = %d, want 10", gl.LockoutThreshold)
	}
	if err := scope.SetOverride(ctx, "group", 1, ports.ScopeOverride{Type: "security", Name: "lockout_threshold", Value: "5"}); err != nil {
		t.Fatalf("set override: %v", err)
	}
	g, _ := resolver.LoadForGroup(ctx, 1, ports.UISettings{})
	if g.LockoutThreshold != 5 {
		t.Errorf("group override lockout_threshold = %d, want 5 (not re-floored to default 10)", g.LockoutThreshold)
	}
}

// TestScopedSettings_LoadForUser: routes through the user's GroupID; GroupID 0
// and a nil user resolve to pure global.
func TestScopedSettings_LoadForUser(t *testing.T) {
	global, scope, resolver := newScopedTestRepos(t)
	ctx := context.Background()

	base, _ := global.Load(ctx, ports.UISettings{})
	base.Require2FAForStaff = false
	_ = global.Save(ctx, base)
	_ = scope.SetOverride(ctx, "group", 3, ports.ScopeOverride{Type: "security", Name: "require_2fa_for_staff", Value: "1"})

	inGroup, _ := resolver.LoadForUser(ctx, &domain.User{GroupID: 3}, ports.UISettings{})
	if !inGroup.Require2FAForStaff {
		t.Error("user in group 3 must see the override")
	}
	noGroup, _ := resolver.LoadForUser(ctx, &domain.User{GroupID: 0}, ports.UISettings{})
	if noGroup.Require2FAForStaff {
		t.Error("user with GroupID 0 must resolve to pure global (false)")
	}
	nilUser, _ := resolver.LoadForUser(ctx, nil, ports.UISettings{})
	if nilUser.Require2FAForStaff {
		t.Error("nil user must resolve to global (false)")
	}
}
