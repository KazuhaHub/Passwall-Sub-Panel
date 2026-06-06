package handler

import (
	"context"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// passkeyLister is the slice of the passkey service availableTwoFAMethods needs
// (a *passkey.Service satisfies it). Narrow so the logic is unit-testable.
type passkeyLister interface {
	List(ctx context.Context, userID int64) ([]*domain.PasskeyCredential, error)
}

// Method identifiers returned in the 2fa_required response and accepted by the
// alternative-verification endpoints. The frontend renders the "use another
// method" picker from this list.
const (
	twoFAMethodTOTP     = "totp"
	twoFAMethodRecovery = "recovery"
	twoFAMethodPasskey  = "passkey"
	twoFAMethodEmail    = "email"
)

// availableTwoFAMethods lists the verification methods offered at the login 2FA
// challenge for u, given the first factor already satisfied. TOTP + recovery
// codes are always available (recovery is the can't-disable fallback). Passkey
// and email are admin opt-in; passkey is additionally gated on the first factor
// being a password (so a passwordless passkey login can't satisfy 2FA with the
// same factor) and on the user actually having a passkey enrolled.
func availableTwoFAMethods(ctx context.Context, u *domain.User, firstFactor string, s ports.UISettings, pk passkeyLister) []string {
	methods := []string{twoFAMethodTOTP, twoFAMethodRecovery}
	if s.TwoFAAllowPasskey && s.PasskeyEnabled && firstFactor == jwtutil.FirstFactorPassword && pk != nil {
		if creds, err := pk.List(ctx, u.ID); err == nil && len(creds) > 0 {
			methods = append(methods, twoFAMethodPasskey)
		}
	}
	if s.TwoFAAllowEmail && strings.TrimSpace(u.Email) != "" {
		methods = append(methods, twoFAMethodEmail)
	}
	return methods
}
