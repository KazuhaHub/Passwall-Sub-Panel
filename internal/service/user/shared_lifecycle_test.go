package user

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type fakeSharedLife struct {
	mu    sync.Mutex // HealSharedClients pushes lifecycle from concurrent workers
	calls []sharedLifeCall
}

type sharedLifeCall struct {
	userID int64
	want   domain.UserLifecycle
}

func (f *fakeSharedLife) SyncUserLifecycle(_ context.Context, userID int64, want domain.UserLifecycle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sharedLifeCall{userID, want})
	return nil
}

// The change-driven paths push the user's EFFECTIVE enable state onto the shared
// client. A disabled user → enable=false (HOLE #1: their shared client is cut off).
func TestSyncSharedLifecycle_PushesEffectiveDisable(t *testing.T) {
	fake := &fakeSharedLife{}
	svc := &Service{}
	svc.SetSharedLifecycleSyncer(fake)

	svc.syncSharedLifecycle(context.Background(), &domain.User{ID: 7, Enabled: false})
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fake.calls))
	}
	if c := fake.calls[0]; c.userID != 7 || c.want.Enable {
		t.Fatalf("disabled user must push enable=false for uid 7: %+v", c)
	}
}

func TestSyncSharedLifecycle_PushesEffectiveEnable(t *testing.T) {
	fake := &fakeSharedLife{}
	svc := &Service{}
	svc.SetSharedLifecycleSyncer(fake)

	// Enabled, no expiry → EffectiveEnabled true.
	svc.syncSharedLifecycle(context.Background(), &domain.User{ID: 8, Enabled: true})
	if len(fake.calls) != 1 || !fake.calls[0].want.Enable {
		t.Fatalf("enabled user must push enable=true: %+v", fake.calls)
	}
}

// nil syncer (before wiring / in most tests) is a no-op, never a panic.
func TestSyncSharedLifecycle_NilSyncerNoop(t *testing.T) {
	(&Service{}).syncSharedLifecycle(context.Background(), &domain.User{ID: 1, Enabled: true})
}

// A negative connection cap must be rejected at the service boundary. The
// panel reads any value <= 0 as "no cap", so a stored -1 would render in PSP's
// UI as a tightened limit while actually disabling enforcement — a control
// that means the opposite of what it shows.
func TestConnLimitValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		v       int
		wantErr bool
	}{
		{"zero is the unlimited encoding", 0, false},
		{"a real cap", 5, false},
		{"negative is rejected", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConnLimit("ip_limit", tc.v)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want a validation error")
				}
				if !errors.Is(err, domain.ErrValidation) {
					t.Fatalf("error = %v, want it to wrap domain.ErrValidation so the handler maps it to 400", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
