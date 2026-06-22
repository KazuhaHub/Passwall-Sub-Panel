package user

import (
	"context"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type fakeSharedLife struct {
	mu    sync.Mutex // HealSharedClients pushes lifecycle from concurrent workers
	calls []sharedLifeCall
}

type sharedLifeCall struct {
	userID        int64
	enable        bool
	expiry, totGB int64
}

func (f *fakeSharedLife) SyncUserLifecycle(_ context.Context, userID int64, enable bool, expiry, totalGB int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sharedLifeCall{userID, enable, expiry, totalGB})
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
	if c := fake.calls[0]; c.userID != 7 || c.enable {
		t.Fatalf("disabled user must push enable=false for uid 7: %+v", c)
	}
}

func TestSyncSharedLifecycle_PushesEffectiveEnable(t *testing.T) {
	fake := &fakeSharedLife{}
	svc := &Service{}
	svc.SetSharedLifecycleSyncer(fake)

	// Enabled, no expiry → EffectiveEnabled true.
	svc.syncSharedLifecycle(context.Background(), &domain.User{ID: 8, Enabled: true})
	if len(fake.calls) != 1 || !fake.calls[0].enable {
		t.Fatalf("enabled user must push enable=true: %+v", fake.calls)
	}
}

// nil syncer (before wiring / in most tests) is a no-op, never a panic.
func TestSyncSharedLifecycle_NilSyncerNoop(t *testing.T) {
	(&Service{}).syncSharedLifecycle(context.Background(), &domain.User{ID: 1, Enabled: true})
}
