package handler

import (
	"context"
	"slices"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type stubLister struct{ n int }

func (s stubLister) List(context.Context, int64) ([]*domain.PasskeyCredential, error) {
	out := make([]*domain.PasskeyCredential, s.n)
	return out, nil
}

func TestAvailableTwoFAMethods(t *testing.T) {
	withPK := stubLister{n: 2}
	noPK := stubLister{n: 0}
	u := &domain.User{ID: 1, Email: "a@b.c"}
	noEmail := &domain.User{ID: 1}

	cases := []struct {
		name        string
		u           *domain.User
		firstFactor string
		s           ports.UISettings
		pk          passkeyLister
		want        []string
	}{
		{
			name: "baseline is totp+recovery only",
			u:    u, firstFactor: jwtutil.FirstFactorPassword,
			s:    ports.UISettings{},
			pk:   withPK,
			want: []string{"totp", "recovery"},
		},
		{
			name: "passkey offered when allowed + enrolled + password first factor",
			u:    u, firstFactor: jwtutil.FirstFactorPassword,
			s:    ports.UISettings{TwoFAAllowPasskey: true, PasskeyEnabled: true},
			pk:   withPK,
			want: []string{"totp", "recovery", "passkey"},
		},
		{
			name: "passkey NOT offered when first factor was a passkey (no same-factor twice)",
			u:    u, firstFactor: jwtutil.FirstFactorPasskey,
			s:    ports.UISettings{TwoFAAllowPasskey: true, PasskeyEnabled: true},
			pk:   withPK,
			want: []string{"totp", "recovery"},
		},
		{
			name: "passkey NOT offered when the user has none enrolled",
			u:    u, firstFactor: jwtutil.FirstFactorPassword,
			s:    ports.UISettings{TwoFAAllowPasskey: true, PasskeyEnabled: true},
			pk:   noPK,
			want: []string{"totp", "recovery"},
		},
		{
			name: "passkey NOT offered when the feature master switch is off",
			u:    u, firstFactor: jwtutil.FirstFactorPassword,
			s:    ports.UISettings{TwoFAAllowPasskey: true, PasskeyEnabled: false},
			pk:   withPK,
			want: []string{"totp", "recovery"},
		},
		{
			name: "email offered when allowed + user has an email",
			u:    u, firstFactor: jwtutil.FirstFactorPassword,
			s:    ports.UISettings{TwoFAAllowEmail: true},
			pk:   noPK,
			want: []string{"totp", "recovery", "email"},
		},
		{
			name: "email NOT offered when the user has no email",
			u:    noEmail, firstFactor: jwtutil.FirstFactorPassword,
			s:    ports.UISettings{TwoFAAllowEmail: true},
			pk:   noPK,
			want: []string{"totp", "recovery"},
		},
	}
	for _, tc := range cases {
		got := availableTwoFAMethods(context.Background(), tc.u, tc.firstFactor, tc.s, tc.pk)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
