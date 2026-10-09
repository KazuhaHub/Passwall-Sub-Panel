package user

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// emergencyRaceRepo is memoryUserRepo made safe for the concurrent grant,
// reset and push these tests run. It also reports each committed emergency
// write (the grant's GrantEmergencyAccess, the reset's ResetEmergencyAccess)
// on committed, and runs onCommit right after it, so a test can act at
// exactly the point where the write is durable and the push has not run.
type emergencyRaceRepo struct {
	*memoryUserRepo
	mu        sync.Mutex
	committed chan struct{}
	onCommit  func()
}

func newEmergencyRaceRepo(u *domain.User) *emergencyRaceRepo {
	return &emergencyRaceRepo{
		memoryUserRepo: &memoryUserRepo{byID: map[int64]*domain.User{u.ID: u}},
		committed:      make(chan struct{}, 8),
	}
}

func (r *emergencyRaceRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.memoryUserRepo.GetByID(ctx, id)
}

func (r *emergencyRaceRepo) Update(ctx context.Context, u *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.memoryUserRepo.Update(ctx, u)
}

func (r *emergencyRaceRepo) UpdateServiceState(ctx context.Context, userID int64, reason domain.AutoDisabledReason, detail string, disabledAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.memoryUserRepo.UpdateServiceState(ctx, userID, reason, detail, disabledAt)
}

func (r *emergencyRaceRepo) GrantEmergencyAccess(ctx context.Context, userID int64, until time.Time, usedCount int, baselineBytes int64) error {
	r.mu.Lock()
	err := r.memoryUserRepo.GrantEmergencyAccess(ctx, userID, until, usedCount, baselineBytes)
	r.mu.Unlock()
	return r.commit(err)
}

func (r *emergencyRaceRepo) ResetEmergencyAccess(ctx context.Context, userID int64) error {
	r.mu.Lock()
	err := r.memoryUserRepo.ResetEmergencyAccess(ctx, userID)
	r.mu.Unlock()
	return r.commit(err)
}

func (r *emergencyRaceRepo) commit(err error) error {
	if err == nil {
		if r.onCommit != nil {
			r.onCommit()
		}
		r.committed <- struct{}{}
	}
	return err
}

// heldLifecycle holds the FIRST lifecycle push until release is closed, the
// way a slow panel holds an updateClient, and records each push when it
// lands, in landing order.
type heldLifecycle struct {
	mu      sync.Mutex
	pushes  int
	entered chan struct{}
	release chan struct{}
	landed  []domain.UserLifecycle
}

func newHeldLifecycle() *heldLifecycle {
	return &heldLifecycle{entered: make(chan struct{}), release: make(chan struct{})}
}

func (f *heldLifecycle) SyncUserLifecycle(_ context.Context, _ int64, want domain.UserLifecycle) error {
	f.mu.Lock()
	f.pushes++
	first := f.pushes == 1
	f.mu.Unlock()
	if first {
		close(f.entered)
		<-f.release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.landed = append(f.landed, want)
	return nil
}

func (f *heldLifecycle) landedSoFar() []domain.UserLifecycle {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.UserLifecycle(nil), f.landed...)
}

// awaitSignal fails the test if ch yields nothing within a few seconds, so
// a regression shows up as a named failure, not a hung test.
func awaitSignal[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func emergencyOps() (reset, grant func(context.Context, *Service) error) {
	reset = func(ctx context.Context, svc *Service) error { return svc.ResetEmergencyUsage(ctx, 7) }
	grant = func(ctx context.Context, svc *Service) error {
		_, err := svc.UseEmergencyAccess(ctx, 7, true)
		return err
	}
	return reset, grant
}

// emergencyRaceUsers are the two starting rows: a user who has spent their
// only use (the window ended, but its timestamp is still recorded, so a reset
// pushes), and an over-quota user who has not used it yet.
func emergencyRaceUsers() (outOfUses, overQuota domain.User) {
	ended := time.Now().Add(-time.Hour)
	base := domain.User{ID: 7, UPN: "race@example.test", Role: domain.RoleUser, Enabled: true,
		ServiceDisabledReason: domain.DisabledTrafficExceeded, ServiceDisableDetail: "traffic limit exceeded"}
	outOfUses, overQuota = base, base
	outOfUses.EmergencyUsedCount, outOfUses.EmergencyUntil, outOfUses.EmergencyBaselineBytes = 1, &ended, 1<<30
	return outOfUses, overQuota
}

// TestEmergencyPushes_LastPushCarriesTheLatestWindow pins the ordering of the
// grant's and the reset's panel pushes. Both commit under emergencyMu but push
// after releasing it, and the reset used to push the snapshot it had read
// under the lock. So an admin reset whose push was held up by a slow panel
// landed "enable off, no expiry" AFTER the grant the user made in the meantime
// had pushed its window. PSP showed an active window and a spent use, while
// the panel kept the client disabled for the whole window, and nothing
// re-pushed it. The poll skips an active window and an already-suspended user,
// and reconcile replays what the stale push minted. A grant's slow push
// landing after a reset brought the revoked window back the same way. Now
// both push from a fresh read under lockUser, so the push that lands last
// must be the stored row's lifecycle.
func TestEmergencyPushes_LastPushCarriesTheLatestWindow(t *testing.T) {
	reset, grant := emergencyOps()
	outOfUses, overQuota := emergencyRaceUsers()
	for _, tc := range []struct {
		name          string
		user          domain.User
		first, second func(context.Context, *Service) error
		wantWindow    bool
	}{
		{"slow reset push then a grant", outOfUses, reset, grant, true},
		{"slow grant push then a reset", overQuota, grant, reset, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := tc.user
			repo := newEmergencyRaceRepo(&u)
			life := newHeldLifecycle()
			release := sync.OnceFunc(func() { close(life.release) })
			t.Cleanup(release) // a failed assertion must not strand the held push
			svc := &Service{users: repo, ownership: emptyOwnershipRepo{}, tasks: &recordingTaskRepo{},
				settings: &fakeFloorSettingsRepo{cfg: emSettings()}}
			svc.SetSharedLifecycleSyncer(life)

			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			go func() { firstDone <- tc.first(context.Background(), svc) }()
			awaitSignal(t, repo.committed, "the first write")
			awaitSignal(t, life.entered, "the first push to reach the panel")

			go func() { secondDone <- tc.second(context.Background(), svc) }()
			awaitSignal(t, repo.committed, "the second write")
			release()
			if err := awaitSignal(t, firstDone, "the first call"); err != nil {
				t.Fatalf("first call: %v", err)
			}
			if err := awaitSignal(t, secondDone, "the second call"); err != nil {
				t.Fatalf("second call: %v", err)
			}

			stored, err := repo.GetByID(context.Background(), 7)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if stored.EmergencyActive(now) != tc.wantWindow {
				t.Fatalf("stored window active = %v (until %v), want %v: the scenario did not run as written",
					stored.EmergencyActive(now), stored.EmergencyUntil, tc.wantWindow)
			}
			landed := life.landedSoFar()
			if len(landed) != 2 {
				t.Fatalf("pushes landed = %d (%+v), want 2", len(landed), landed)
			}
			if want, last := stored.Lifecycle(now, 0), landed[len(landed)-1]; last != want {
				t.Fatalf("the panel was left on %+v, but the stored row (window until %v, used %d) says %+v; pushes landed in order: %+v",
					last, stored.EmergencyUntil, stored.EmergencyUsedCount, want, landed)
			}
		})
	}
}

// TestEmergencyPushes_ReadTheRowTheyPush pins the other half of that fix:
// each push reads the row under lockUser instead of pushing what its caller
// held. The order in which two pushes take the lock need not match the order
// of their commits. A membership resync holding the user's lock lets the
// grant and the reset both commit and then queue for it. If a push carried
// its own operation's snapshot, the one that ran second could still be the
// older state. Here every push runs after both commits, so every push must
// carry the stored row. Neither may reach the panel while the resync holds
// the lock.
func TestEmergencyPushes_ReadTheRowTheyPush(t *testing.T) {
	reset, grant := emergencyOps()
	outOfUses, overQuota := emergencyRaceUsers()
	for _, tc := range []struct {
		name          string
		user          domain.User
		first, second func(context.Context, *Service) error
	}{
		{"reset then grant", outOfUses, reset, grant},
		{"grant then reset", overQuota, grant, reset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := tc.user
			repo := newEmergencyRaceRepo(&u)
			life := &fakeSharedLife{}
			svc := &Service{users: repo, ownership: emptyOwnershipRepo{}, tasks: &recordingTaskRepo{},
				settings: &fakeFloorSettingsRepo{cfg: emSettings()}}
			svc.SetSharedLifecycleSyncer(life)

			unlock := svc.lockUser(7) // a membership resync in flight for this user
			t.Cleanup(unlock)
			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			go func() { firstDone <- tc.first(context.Background(), svc) }()
			awaitSignal(t, repo.committed, "the first write")
			go func() { secondDone <- tc.second(context.Background(), svc) }()
			awaitSignal(t, repo.committed, "the second write")
			time.Sleep(50 * time.Millisecond) // let a push that ignores the lock land
			life.mu.Lock()
			early := len(life.calls)
			life.mu.Unlock()
			unlock()
			if early != 0 {
				t.Fatalf("%d push(es) reached the panel while the user lock was held; they must wait for it", early)
			}
			if err := awaitSignal(t, firstDone, "the first call"); err != nil {
				t.Fatalf("first call: %v", err)
			}
			if err := awaitSignal(t, secondDone, "the second call"); err != nil {
				t.Fatalf("second call: %v", err)
			}

			stored, err := repo.GetByID(context.Background(), 7)
			if err != nil {
				t.Fatal(err)
			}
			want := stored.Lifecycle(time.Now(), 0)
			if len(life.calls) != 2 {
				t.Fatalf("pushes = %d (%+v), want 2", len(life.calls), life.calls)
			}
			for i, c := range life.calls {
				if c.want != want {
					t.Fatalf("push %d carried %+v after both writes had committed; the stored row says %+v", i+1, c.want, want)
				}
			}
		})
	}
}

// TestEmergencyPushes_OutliveTheRequest pins that the panel follow-up of a
// committed grant or reset runs detached from the request, as the service
// transitions' does (detachedFollowUp), and still bounded. Both used to push
// and enqueue on the request context. An admin who closed the tab mid-batch,
// or a user who left the portal, then cancelled the push AND the enqueue
// meant to catch it. The row had the new window state, the panel kept the
// old one, and nothing was queued. The poll never repairs that, because it
// skips an active window and an already-suspended user.
func TestEmergencyPushes_OutliveTheRequest(t *testing.T) {
	reset, grant := emergencyOps()
	window := time.Now().Add(time.Hour)
	inWindow := domain.User{ID: 7, UPN: "gone@example.test", Role: domain.RoleUser, Enabled: true,
		ServiceDisabledReason: domain.DisabledTrafficExceeded, ServiceDisableDetail: "emergency access active",
		EmergencyUsedCount: 1, EmergencyUntil: &window, EmergencyBaselineBytes: 1 << 30}
	overQuota := domain.User{ID: 7, UPN: "gone@example.test", Role: domain.RoleUser, Enabled: true,
		ServiceDisabledReason: domain.DisabledTrafficExceeded, ServiceDisableDetail: "traffic limit exceeded"}
	for _, tc := range []struct {
		name      string
		user      domain.User
		op        func(context.Context, *Service) error
		panelDown bool
	}{
		{"reset, panel up: pushed", inWindow, reset, false},
		{"reset, panel down: queued", inWindow, reset, true},
		{"grant, panel up: pushed", overQuota, grant, false},
		{"grant, panel down: queued", overQuota, grant, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			u := tc.user
			repo := newEmergencyRaceRepo(&u)
			repo.onCommit = cancel // the request goes away the moment the write commits
			life := &ctxSharedLife{down: tc.panelDown}
			tasks := &ctxTaskRepo{}
			svc := &Service{users: repo, ownership: emptyOwnershipRepo{}, tasks: tasks,
				settings: &fakeFloorSettingsRepo{cfg: emSettings()}}
			svc.SetSharedLifecycleSyncer(life)

			if err := tc.op(ctx, svc); err != nil {
				t.Fatalf("err = %v, want nil: the request went away after the write, not before it", err)
			}
			queued := pushConfigTasks(&tasks.recordingTaskRepo)
			if tc.panelDown {
				if len(queued) != 1 || queued[0].TargetID != 7 {
					t.Fatalf("queued push tasks = %+v, want one for user 7", queued)
				}
				if len(tasks.bounded) != 1 || !tasks.bounded[0] {
					t.Fatalf("queue context bounded = %v, want [true]", tasks.bounded)
				}
				return
			}
			if len(life.calls) != 1 || life.calls[0].userID != 7 {
				t.Fatalf("push = %+v, want one push for user 7", life.calls)
			}
			if len(life.bounded) != 1 || !life.bounded[0] {
				t.Fatalf("push context bounded = %v, want [true] (detached, but never unbounded)", life.bounded)
			}
			if len(queued) != 0 {
				t.Fatalf("queued push tasks = %+v, want none (the push landed)", queued)
			}
		})
	}
}
