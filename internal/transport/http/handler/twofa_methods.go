package handler

import (
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

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
// challenge, reflecting what the account ACTUALLY has — so a passkey-only account
// (no TOTP) isn't offered a TOTP box it can't use, and a TOTP account that burned
// all its recovery codes isn't offered "recovery". A passkey is its own second
// factor (enrolling one is opting in), gated only on the passkey master switch +
// the first factor being a password (a passwordless passkey login can't satisfy
// 2FA with the same factor twice). Email stays an admin opt-in weaker fallback.
func availableTwoFAMethods(u *domain.User, firstFactor string, s ports.UISettings, hasPasskey, hasRecovery bool) []string {
	methods := []string{}
	if u.TOTPEnabled {
		methods = append(methods, twoFAMethodTOTP)
	}
	if hasRecovery {
		methods = append(methods, twoFAMethodRecovery)
	}
	if s.PasskeyEnabled && firstFactor == jwtutil.FirstFactorPassword && hasPasskey {
		methods = append(methods, twoFAMethodPasskey)
	}
	if s.TwoFAAllowEmail && strings.TrimSpace(u.Email) != "" {
		methods = append(methods, twoFAMethodEmail)
	}
	return methods
}
