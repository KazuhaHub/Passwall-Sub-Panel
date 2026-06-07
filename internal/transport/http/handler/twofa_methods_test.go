package handler

import (
	"slices"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestAvailableTwoFAMethods(t *testing.T) {
	totp := &domain.User{ID: 1, Email: "a@b.c", TOTPEnabled: true}
	passkeyOnly := &domain.User{ID: 1, Email: "a@b.c"} // no TOTP
	noEmail := &domain.User{ID: 1, TOTPEnabled: true}

	cases := []struct {
		name        string
		u           *domain.User
		firstFactor string
		s           ports.UISettings
		hasPasskey  bool
		hasRecovery bool
		want        []string
	}{
		{
			name: "totp account with recovery codes",
			u:    totp, firstFactor: jwtutil.FirstFactorPassword,
			s:           ports.UISettings{},
			hasRecovery: true,
			want:        []string{"totp", "recovery"},
		},
		{
			name: "totp account that burned all recovery codes",
			u:    totp, firstFactor: jwtutil.FirstFactorPassword,
			s:           ports.UISettings{},
			hasRecovery: false,
			want:        []string{"totp"},
		},
		{
			name: "passkey-only account offers passkey + recovery, NOT totp",
			u:    passkeyOnly, firstFactor: jwtutil.FirstFactorPassword,
			s:          ports.UISettings{PasskeyEnabled: true},
			hasPasskey: true, hasRecovery: true,
			want: []string{"recovery", "passkey"},
		},
		{
			name: "passkey offered when enrolled + password first factor (no separate allow toggle)",
			u:    totp, firstFactor: jwtutil.FirstFactorPassword,
			s:          ports.UISettings{PasskeyEnabled: true},
			hasPasskey: true, hasRecovery: true,
			want: []string{"totp", "recovery", "passkey"},
		},
		{
			name: "passkey NOT offered when first factor was a passkey (no same-factor twice)",
			u:    totp, firstFactor: jwtutil.FirstFactorPasskey,
			s:          ports.UISettings{PasskeyEnabled: true},
			hasPasskey: true, hasRecovery: true,
			want: []string{"totp", "recovery"},
		},
		{
			name: "passkey NOT offered when the feature master switch is off",
			u:    totp, firstFactor: jwtutil.FirstFactorPassword,
			s:          ports.UISettings{PasskeyEnabled: false},
			hasPasskey: true, hasRecovery: true,
			want: []string{"totp", "recovery"},
		},
		{
			name: "email offered when allowed + user has an email",
			u:    totp, firstFactor: jwtutil.FirstFactorPassword,
			s:           ports.UISettings{TwoFAAllowEmail: true},
			hasRecovery: true,
			want:        []string{"totp", "recovery", "email"},
		},
		{
			name: "email NOT offered when the user has no email",
			u:    noEmail, firstFactor: jwtutil.FirstFactorPassword,
			s:           ports.UISettings{TwoFAAllowEmail: true},
			hasRecovery: true,
			want:        []string{"totp", "recovery"},
		},
	}
	for _, tc := range cases {
		got := availableTwoFAMethods(tc.u, tc.firstFactor, tc.s, tc.hasPasskey, tc.hasRecovery)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
