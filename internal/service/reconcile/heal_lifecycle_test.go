package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// capturingSyncer records the lifecycle each heal path hands to the writer, so
// a test can assert on the INTENT that would reach the panel rather than on a
// call count.
type capturingSyncer struct {
	added []domain.UserLifecycle
}

func (s *capturingSyncer) AddClientToInbound(_ context.Context, _ int64, _ int64, _ int,
	_ domain.Protocol, _, _, _, _ string, want domain.UserLifecycle, _ int64) error {
	s.added = append(s.added, want)
	return nil
}

func (s *capturingSyncer) SetOwnedClientEnable(context.Context, int64, int, string,
	domain.Protocol, string, string, string, domain.UserLifecycle, int64) error {
	return nil
}

func (s *capturingSyncer) RotateClientUUID(context.Context, int64, int, string,
	domain.Protocol, string, string, string, string, domain.UserLifecycle, int64) error {
	return nil
}

// A client that has gone missing is recreated with the user's REAL enable
// state, not a hardcoded true.
//
// It used to be hardcoded. checkOne's caller — the entries loop — has no
// EffectiveEnabled gate of its own (unlike checkMissingOwnerships, which gates
// at the top), so nothing else held that line: a suspended or expired account
// whose client vanished came back SERVING, and stayed that way until Check 3
// noticed on a later pass. The reconcile cron defaults to 15 minutes.
func TestCheckOneRecreatesAMissingClientWithTheUsersRealEnableState(t *testing.T) {
	cases := []struct {
		name string
		user *domain.User
		want bool
	}{
		{
			name: "admin-disabled user",
			user: &domain.User{ID: 1, UUID: "u-1", Enabled: false},
			want: false,
		},
		{
			name: "expired user",
			user: &domain.User{ID: 2, UUID: "u-2", Enabled: true, ExpireAt: past()},
			want: false,
		},
		{
			name: "healthy user still comes back enabled",
			user: &domain.User{ID: 3, UUID: "u-3", Enabled: true},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			syncer := &capturingSyncer{}
			// pool is only consulted to name the panel in the Issue.
			s := &Service{syncer: syncer, pool: recPool{}}
			ce := &inboundCacheEntry{
				inbound: &ports.Inbound{ID: 7, Protocol: "vless"},
				clients: nil, // FindClient returns nil → Check 1 fires
			}
			e := &domain.XUIClientEntry{PanelID: 10, InboundID: 7, ClientEmail: "u@psp.local"}

			s.checkOne(context.Background(), tc.user,
				tc.user.Lifecycle(time.Now(), 0), e, ce, nil, LevelFull)
			if len(syncer.added) != 1 {
				t.Fatalf("expected exactly one recreate, got %d", len(syncer.added))
			}
			if got := syncer.added[0].Enable; got != tc.want {
				t.Fatalf("recreated with Enable=%v, want %v — a disabled user must not come back serving traffic", got, tc.want)
			}
		})
	}
}

func past() *time.Time {
	t := time.Now().Add(-24 * time.Hour)
	return &t
}
