// Package authpolicy decides whether an account must enroll a second factor
// before it can use the panel — the "require 2FA" enforcement (per-user,
// per-group, and panel-wide-for-staff). It only gates local-password accounts;
// SSO sign-ins are the IdP's responsibility.
package authpolicy

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type GroupGetter interface {
	GetByID(ctx context.Context, id int64) (*domain.Group, error)
}

type PasskeyLister interface {
	FindByUserID(ctx context.Context, userID int64) ([]*domain.PasskeyCredential, error)
}

// SettingsLoader yields the EFFECTIVE settings for a user (global ⊕ the user's
// group overrides). Wired to the ScopedSettings resolver; with no overrides this
// is byte-identical to the global Settings.Load, so the enforcement decision is
// unchanged until an admin sets a per-group override.
type SettingsLoader interface {
	LoadForUser(ctx context.Context, u *domain.User, defaults ports.UISettings) (ports.UISettings, error)
}

type Deps struct {
	Groups   GroupGetter
	Passkeys PasskeyLister
	Settings SettingsLoader
}

type Service struct{ d Deps }

func New(d Deps) *Service { return &Service{d: d} }

// MustEnroll reports whether u is REQUIRED to set up a second factor but hasn't.
// Required = the panel-wide "require for staff" setting (admins/operators) OR the
// user's group flag — both read from the EFFECTIVE (group-scoped) settings, so a
// group override of require_2fa_for_staff bites only that group. Satisfied = an
// enrolled TOTP or at least one passkey. Only local-password accounts are gated.
// (There is no per-user override: v3.8.0 dropped User.Require2FA — enforcement is
// staff-wide ∨ per-group.)
func (s *Service) MustEnroll(ctx context.Context, u *domain.User) (bool, error) {
	if u == nil || !u.HasLocalPassword() {
		return false, nil
	}
	set, err := s.d.Settings.LoadForUser(ctx, u, ports.UISettings{})
	if err != nil {
		return false, err
	}
	// Fail-safe: if no second factor can be enrolled panel-wide, a requirement is
	// unsatisfiable — never gate (it would be a lockout with no way out). Turning
	// off every enrollment method cleanly disables enforcement.
	if !set.TOTPEnabled && !set.PasskeyEnabled {
		return false, nil
	}
	required := (u.Role == domain.RoleAdmin || u.Role == domain.RoleOperator) && set.Require2FAForStaff
	if !required && u.GroupID != 0 && s.d.Groups != nil {
		g, err := s.d.Groups.GetByID(ctx, u.GroupID)
		if err == nil && g != nil && g.Require2FA {
			required = true
		}
		// A missing/error group is treated as "no group requirement" — never
		// fail open into gating someone the group doesn't actually require.
	}
	if !required {
		return false, nil
	}
	// Satisfied by a TOTP enrollment, or — only while passkeys are enabled
	// panel-wide — any registered passkey. A passkey can't be used at login once
	// the admin disables passkeys panel-wide (the assertion ceremony is blocked),
	// so it must NOT count as a satisfying factor then: instead of silently
	// dropping a passkey-only account to single-factor password login, treat the
	// requirement as unmet so the user is guided to enroll an available method
	// (TOTP). The earlier fail-safe already covers "no method enrollable at all".
	if u.TOTPEnabled {
		return false, nil
	}
	if set.PasskeyEnabled {
		creds, err := s.d.Passkeys.FindByUserID(ctx, u.ID)
		if err != nil {
			return false, err
		}
		if len(creds) > 0 {
			return false, nil
		}
	}
	return true, nil
}
