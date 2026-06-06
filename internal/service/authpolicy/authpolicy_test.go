package authpolicy

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type stubGroups struct{ require bool }

func (s stubGroups) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	return &domain.Group{ID: id, Require2FA: s.require}, nil
}

type stubPasskeys struct{ n int }

func (s stubPasskeys) FindByUserID(_ context.Context, _ int64) ([]*domain.PasskeyCredential, error) {
	return make([]*domain.PasskeyCredential, s.n), nil
}

type stubSettings struct {
	staff     bool
	canEnroll bool // drives TOTPEnabled so a requirement is satisfiable
}

func (s stubSettings) Load(_ context.Context, d ports.UISettings) (ports.UISettings, error) {
	d.Require2FAForStaff = s.staff
	d.TOTPEnabled = s.canEnroll
	return d, nil
}

func svc(groupRequire, staffRequire bool, passkeys int) *Service {
	return New(Deps{
		Groups:   stubGroups{require: groupRequire},
		Passkeys: stubPasskeys{n: passkeys},
		Settings: stubSettings{staff: staffRequire, canEnroll: true},
	})
}

func localUser(role domain.Role) *domain.User {
	return &domain.User{ID: 1, Role: role, GroupID: 5, PasswordHash: "bcrypt"}
}

func TestMustEnroll(t *testing.T) {
	ctx := context.Background()

	t.Run("sso-only account is never gated", func(t *testing.T) {
		u := &domain.User{ID: 1, Role: domain.RoleUser, Require2FA: true} // no password
		if got, _ := svc(true, true, 0).MustEnroll(ctx, u); got {
			t.Fatal("an account without a local password must not be gated")
		}
	})

	t.Run("not required anywhere", func(t *testing.T) {
		if got, _ := svc(false, false, 0).MustEnroll(ctx, localUser(domain.RoleUser)); got {
			t.Fatal("no requirement → not gated")
		}
	})

	t.Run("per-user require, no 2FA → gated", func(t *testing.T) {
		u := localUser(domain.RoleUser)
		u.Require2FA = true
		if got, _ := svc(false, false, 0).MustEnroll(ctx, u); !got {
			t.Fatal("required + no factor → gated")
		}
	})

	t.Run("per-user require but has TOTP → satisfied", func(t *testing.T) {
		u := localUser(domain.RoleUser)
		u.Require2FA = true
		u.TOTPEnabled = true
		if got, _ := svc(false, false, 0).MustEnroll(ctx, u); got {
			t.Fatal("TOTP enrolled satisfies the requirement")
		}
	})

	t.Run("per-user require but has a passkey → satisfied", func(t *testing.T) {
		u := localUser(domain.RoleUser)
		u.Require2FA = true
		if got, _ := svc(false, false, 1).MustEnroll(ctx, u); got {
			t.Fatal("a passkey satisfies the requirement")
		}
	})

	t.Run("group require → gated", func(t *testing.T) {
		if got, _ := svc(true, false, 0).MustEnroll(ctx, localUser(domain.RoleUser)); !got {
			t.Fatal("group flag → gated")
		}
	})

	t.Run("staff-wide require gates an admin", func(t *testing.T) {
		if got, _ := svc(false, true, 0).MustEnroll(ctx, localUser(domain.RoleAdmin)); !got {
			t.Fatal("require-for-staff gates an admin")
		}
	})

	t.Run("staff-wide require gates an operator", func(t *testing.T) {
		if got, _ := svc(false, true, 0).MustEnroll(ctx, localUser(domain.RoleOperator)); !got {
			t.Fatal("require-for-staff gates an operator (operators must be able to reach enrollment)")
		}
	})

	t.Run("staff-wide require does not gate a regular user", func(t *testing.T) {
		if got, _ := svc(false, true, 0).MustEnroll(ctx, localUser(domain.RoleUser)); got {
			t.Fatal("require-for-staff must not gate a non-staff user")
		}
	})

	t.Run("fail-safe: no enrollment method available → never gate (no lockout)", func(t *testing.T) {
		// Required by the per-user flag, but TOTP + passkey are both off
		// panel-wide → the requirement is unsatisfiable, so gating would lock the
		// user out. Must NOT gate.
		s := New(Deps{
			Groups:   stubGroups{require: false},
			Passkeys: stubPasskeys{n: 0},
			Settings: stubSettings{staff: false, canEnroll: false},
		})
		u := localUser(domain.RoleUser)
		u.Require2FA = true
		if got, _ := s.MustEnroll(ctx, u); got {
			t.Fatal("must not gate when no second factor can be enrolled")
		}
	})
}
