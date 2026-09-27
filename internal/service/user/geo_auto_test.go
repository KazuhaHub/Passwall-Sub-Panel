package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// geoAutoHarness is the service-mail tests' harness (service_mail_test.go),
// plus a shared-lifecycle fake so a push is observable and an auth-cache hook
// so the invalidation is too. Background work runs inline, so a mail is
// either sent before the call returns or not at all.
type geoAutoHarness struct {
	svc         *Service
	repo        *memoryUserRepo
	mail        *fakeServiceMailer
	life        *fakeSharedLife
	invalidated []int64
}

func newGeoAutoHarness(users ...*domain.User) *geoAutoHarness {
	h := &geoAutoHarness{
		repo: &memoryUserRepo{byID: map[int64]*domain.User{}},
		mail: &fakeServiceMailer{},
		life: &fakeSharedLife{},
	}
	for _, u := range users {
		h.repo.byID[u.ID] = u
	}
	h.svc = &Service{users: h.repo, ownership: emptyOwnershipRepo{}, settings: bfSettings{}}
	h.svc.SetMailNotifier(h.mail)
	h.svc.SetBackgroundRunner(func(_ string, fn func(ctx context.Context)) { fn(context.Background()) })
	h.svc.SetSharedLifecycleSyncer(h.life)
	h.svc.SetAuthInvalidator(func(uid int64) { h.invalidated = append(h.invalidated, uid) })
	return h
}

// geoAutoCounter reads one outcome of psp_geo_auto_suspension_total from the
// metrics snapshot, which also proves the family is registered.
func geoAutoCounter(outcome string) int64 {
	name := "psp_geo_auto_suspension_total{outcome=" + outcome + "}"
	for _, c := range metrics.Take().Counters {
		if c.Name == name {
			return c.Value
		}
	}
	return 0
}

// D3: the detector never overwrites another reason. Every non-empty reason
// is someone else's decision or someone else's fact — an admin pause, the
// blocked-client policy, a human geo suspension, the quota, the expiry — and
// the row, the user's inbox and the upstream client all stay as they are.
// A clear user in the same service IS suspended, so a function that never
// writes cannot pass this.
func TestSuspendServiceIfClear_NeverOverwritesAnotherReason(t *testing.T) {
	for _, held := range []domain.AutoDisabledReason{
		domain.DisabledServiceManual, domain.DisabledGeoAnomaly, domain.DisabledBlockedClient,
		domain.DisabledTrafficExceeded, domain.DisabledExpired, domain.DisabledGeoAutoSuspend,
	} {
		t.Run(string(held), func(t *testing.T) {
			heldAt := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
			h := newGeoAutoHarness(
				&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: held, ServiceDisableDetail: "theirs", ServiceDisabledAt: &heldAt},
				&domain.User{ID: 8, Enabled: true},
			)

			applied, err := h.svc.SuspendServiceIfClear(context.Background(), 7, domain.DisabledGeoAutoSuspend, "ours")
			if err != nil || applied {
				t.Fatalf("held row: applied=%v err=%v, want false, nil", applied, err)
			}
			if got := h.repo.byID[7]; got.ServiceDisabledReason != held || got.ServiceDisableDetail != "theirs" || !got.ServiceDisabledAt.Equal(heldAt) {
				t.Fatalf("held row = %q/%q/%v, want %q/theirs/%v unchanged",
					got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt, held, heldAt)
			}

			applied, err = h.svc.SuspendServiceIfClear(context.Background(), 8, domain.DisabledGeoAutoSuspend, "ours")
			if err != nil || !applied {
				t.Fatalf("clear row (control): applied=%v err=%v, want true, nil", applied, err)
			}
			for _, m := range h.mail.suspended {
				if m.uid == 7 {
					t.Fatalf("the held user was mailed: %+v", m)
				}
			}
			for _, c := range h.life.calls {
				if c.userID == 7 {
					t.Fatalf("the held user's config was pushed: %+v", c)
				}
			}
		})
	}
}

// When the CAS wins: the auth cache is dropped, one suspension mail carries
// the reason and the detail, and the push reads the FRESH row, so the
// upstream client goes out disabled. When it loses, none of that happens.
func TestSuspendServiceIfClear_EmailsAndPushesOnlyWhenApplied(t *testing.T) {
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true})

	applied, err := h.svc.SuspendServiceIfClear(context.Background(), 7, domain.DisabledGeoAutoSuspend, "about 60 minutes")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v, want true, nil", applied, err)
	}
	got := h.repo.byID[7]
	if got.ServiceDisabledReason != domain.DisabledGeoAutoSuspend || got.ServiceDisableDetail != "about 60 minutes" || got.ServiceDisabledAt == nil {
		t.Fatalf("row = %q/%q/%v, want geo_auto with detail and a timestamp",
			got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt)
	}
	if len(h.mail.suspended) != 1 || h.mail.suspended[0] != (suspendMail{7, string(domain.DisabledGeoAutoSuspend), "about 60 minutes"}) {
		t.Fatalf("suspension mail = %+v, want exactly one {7, geo_auto, about 60 minutes}", h.mail.suspended)
	}
	if len(h.life.calls) != 1 || h.life.calls[0].userID != 7 || h.life.calls[0].want.Enable {
		t.Fatalf("push = %+v, want one push for user 7 with Enable=false", h.life.calls)
	}
	if len(h.invalidated) != 1 || h.invalidated[0] != 7 {
		t.Fatalf("auth invalidations = %v, want [7]", h.invalidated)
	}

	applied, err = h.svc.SuspendServiceIfClear(context.Background(), 7, domain.DisabledGeoAutoSuspend, "again")
	if err != nil || applied {
		t.Fatalf("second call: applied=%v err=%v, want false, nil", applied, err)
	}
	if len(h.mail.suspended) != 1 || len(h.life.calls) != 1 || len(h.invalidated) != 1 {
		t.Fatalf("a lost CAS mailed/pushed/invalidated: mail=%d push=%d invalidate=%d, want 1/1/1",
			len(h.mail.suspended), len(h.life.calls), len(h.invalidated))
	}
}

// The suspension is committed before the push, so a push that fails is
// queued for the retry loop exactly like SetServiceSuspendedAndSync does,
// and the call still reports applied: the caller audits a suspension that
// is real in PSP and on its way upstream.
func TestSuspendServiceIfClear_PushFailureIsQueued(t *testing.T) {
	u := &domain.User{ID: 42, UPN: "u@example.com", Role: domain.RoleUser, Enabled: true}
	tasks := &recordingTaskRepo{}
	life := &failingSharedLife{fail: true}
	svc := migratedSvc(u, life, tasks)

	applied, err := svc.SuspendServiceIfClear(context.Background(), 42, domain.DisabledGeoAutoSuspend, "d")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v, want true, nil (the failure is queued, not returned)", applied, err)
	}
	if life.calls != 1 {
		t.Fatalf("push attempts = %d, want 1", life.calls)
	}
	if got := pushConfigTasks(tasks); len(got) != 1 || got[0].TargetID != 42 {
		t.Fatalf("queued push tasks = %+v, want one for user 42", got)
	}
}

// UseEmergencyAccess reads the service reason and writes its own under
// emergencyMu. If the geo CAS ran outside that lock, a grant could read an
// empty reason and then overwrite a geo_auto written in between with
// traffic_exceeded — which the rollover would later lift. So the CAS waits for
// the lock.
func TestSuspendServiceIfClear_WaitsForTheEmergencyLock(t *testing.T) {
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true})
	done := make(chan bool, 1)

	h.svc.WithEmergencyLock(func() {
		go func() {
			applied, _ := h.svc.SuspendServiceIfClear(context.Background(), 7, domain.DisabledGeoAutoSuspend, "d")
			done <- applied
		}()
		time.Sleep(50 * time.Millisecond)
		if got := h.repo.byID[7].ServiceDisabledReason; got != domain.DisabledNone {
			t.Errorf("reason = %q while the emergency lock was held, want '' (the CAS must wait)", got)
		}
	})

	select {
	case applied := <-done:
		if !applied {
			t.Fatal("applied = false after the lock was released, want true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SuspendServiceIfClear never returned after the lock was released")
	}
	if got := h.repo.byID[7].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("reason = %q after the lock was released, want geo_auto", got)
	}
}

// Both geo transitions hold lockUser, the per-user lock ResyncMembership
// holds, across the write AND the push. Without it a resync that read the
// user as active before the suspension can push enable=true after the
// suspension pushed enable=false, and the upstream client keeps serving a row
// that says geo_auto until the next push or heal. The emergency lock above
// has its own test; this is the other one. The lock is released before any
// assertion can end the test, so a failure cannot strand the call.
func TestSuspendServiceIfClear_WaitsForTheUserLock(t *testing.T) {
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true})
	done := make(chan bool, 1)

	unlock := h.svc.lockUser(7)
	go func() {
		applied, _ := h.svc.SuspendServiceIfClear(context.Background(), 7, domain.DisabledGeoAutoSuspend, "d")
		done <- applied
	}()
	time.Sleep(50 * time.Millisecond)
	held, pushed := h.repo.byID[7].ServiceDisabledReason, len(h.life.calls)
	unlock()
	if held != domain.DisabledNone || pushed != 0 {
		t.Errorf("reason = %q, pushes = %d while the user lock was held, want '' and 0 (the write must wait)", held, pushed)
	}

	select {
	case applied := <-done:
		if !applied {
			t.Fatal("applied = false after the lock was released, want true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SuspendServiceIfClear never returned after the lock was released")
	}
	if got := h.repo.byID[7].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("reason = %q after the lock was released, want geo_auto", got)
	}
}

// The lift's side of the same lock: its fresh read, clear and push wait for a
// resync in flight, so the resync's lifecycle push cannot land after the
// lift's and disable a client the row says is serving again.
func TestLiftServiceIfHeldSince_WaitsForTheUserLock(t *testing.T) {
	at := time.Now().Add(-2 * time.Hour)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at})
	done := make(chan bool, 1)

	unlock := h.svc.lockUser(7)
	go func() {
		lifted, _ := h.svc.LiftServiceIfHeldSince(context.Background(), 7, domain.DisabledGeoAutoSuspend, time.Now())
		done <- lifted
	}()
	time.Sleep(50 * time.Millisecond)
	held, pushed := h.repo.byID[7].ServiceDisabledReason, len(h.life.calls)
	unlock()
	if held != domain.DisabledGeoAutoSuspend || pushed != 0 {
		t.Errorf("reason = %q, pushes = %d while the user lock was held, want geo_auto and 0 (the lift must wait)", held, pushed)
	}

	select {
	case lifted := <-done:
		if !lifted {
			t.Fatal("lifted = false after the lock was released, want true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LiftServiceIfHeldSince never returned after the lock was released")
	}
	if got := h.repo.byID[7].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("reason = %q after the lock was released, want cleared", got)
	}
}

// adminPausesAfterReadRepo is the user repository at the one moment a
// synchronous test cannot otherwise reach: the lift's fresh read returns the
// due geo_auto row, and an admin's pause lands right after it, before the
// clear. SetServiceSuspendedAndSync takes no per-user lock, so lockUser does
// not keep it out; only the clear's own predicate does. One-shot, so any
// later read sees the row as it now is.
type adminPausesAfterReadRepo struct {
	*memoryUserRepo
	pausedAt time.Time
	paused   bool
}

func (r *adminPausesAfterReadRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	u, err := r.memoryUserRepo.GetByID(ctx, id)
	if err == nil && !r.paused {
		r.paused = true
		if perr := r.memoryUserRepo.UpdateServiceState(ctx, id, domain.DisabledServiceManual, "admin pause", &r.pausedAt); perr != nil {
			return nil, perr
		}
	}
	return u, err
}

// An admin's pause that lands between the lift's fresh read and its clear is
// a newer, deliberate decision, and the lift must not wipe it: the clear is
// conditional on the row still carrying geo_auto. An unconditional clear here
// would resume, upstream too, a user an admin had just paused.
func TestLiftServiceIfHeldSince_LeavesAPauseThatLandedAfterTheRead(t *testing.T) {
	at := time.Now().Add(-2 * time.Hour)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisableDetail: "auto", ServiceDisabledAt: &at})
	pausedAt := time.Now().Add(-time.Minute)
	h.svc.users = &adminPausesAfterReadRepo{memoryUserRepo: h.repo, pausedAt: pausedAt}

	lifted, err := h.svc.LiftServiceIfHeldSince(context.Background(), 7, domain.DisabledGeoAutoSuspend, time.Now())
	if err != nil || lifted {
		t.Fatalf("lifted=%v err=%v, want false, nil — the row no longer carries geo_auto", lifted, err)
	}
	got := h.repo.byID[7]
	if got.ServiceDisabledReason != domain.DisabledServiceManual || got.ServiceDisableDetail != "admin pause" ||
		got.ServiceDisabledAt == nil || !got.ServiceDisabledAt.Equal(pausedAt) {
		t.Fatalf("row = %q/%q/%v, want the admin's service_manual/admin pause/%v untouched",
			got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt, pausedAt)
	}
	if len(h.life.calls) != 0 || len(h.invalidated) != 0 {
		t.Fatalf("pushes = %+v, invalidations = %v; a lift that cleared nothing must not push or invalidate", h.life.calls, h.invalidated)
	}
}

// The lift clears only its own reason. geo_anomaly is a person's decision
// and is lifted by a person; the geo_auto row next to it is the control.
func TestLiftServiceIfHeldSince_OnlyClearsItsOwnReason(t *testing.T) {
	at := time.Now().Add(-2 * time.Hour)
	h := newGeoAutoHarness(
		&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAnomaly, ServiceDisableDetail: "human", ServiceDisabledAt: &at},
		&domain.User{ID: 8, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisableDetail: "auto", ServiceDisabledAt: &at},
	)

	lifted, err := h.svc.LiftServiceIfHeldSince(context.Background(), 7, domain.DisabledGeoAutoSuspend, time.Now())
	if err != nil || lifted {
		t.Fatalf("geo_anomaly row: lifted=%v err=%v, want false, nil", lifted, err)
	}
	if got := h.repo.byID[7]; got.ServiceDisabledReason != domain.DisabledGeoAnomaly || got.ServiceDisableDetail != "human" {
		t.Fatalf("geo_anomaly row = %q/%q, want geo_anomaly/human unchanged", got.ServiceDisabledReason, got.ServiceDisableDetail)
	}

	lifted, err = h.svc.LiftServiceIfHeldSince(context.Background(), 8, domain.DisabledGeoAutoSuspend, time.Now())
	if err != nil || !lifted {
		t.Fatalf("geo_auto row (control): lifted=%v err=%v, want true, nil", lifted, err)
	}
	if got := h.repo.byID[8]; got.ServiceDisabledReason != domain.DisabledNone || got.ServiceDisableDetail != "" || got.ServiceDisabledAt != nil {
		t.Fatalf("geo_auto row = %q/%q/%v, want cleared", got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt)
	}
	for _, c := range h.life.calls {
		if c.userID == 7 {
			t.Fatalf("the geo_anomaly user's config was pushed: %+v", c)
		}
	}
}

// The due check runs on the FRESH row. A poll whose snapshot predates an
// admin resume and a new suspension must not lift the new one: its cutoff
// (now - duration) is older than the row's timestamp.
func TestLiftServiceIfHeldSince_RefusesASuspensionNewerThanTheCutoff(t *testing.T) {
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at})

	lifted, err := h.svc.LiftServiceIfHeldSince(context.Background(), 7, domain.DisabledGeoAutoSuspend, at.Add(-time.Second))
	if err != nil || lifted {
		t.Fatalf("cutoff before the suspension: lifted=%v err=%v, want false, nil", lifted, err)
	}
	if got := h.repo.byID[7].ServiceDisabledReason; got != domain.DisabledGeoAutoSuspend {
		t.Fatalf("reason = %q, want geo_auto still", got)
	}

	lifted, err = h.svc.LiftServiceIfHeldSince(context.Background(), 7, domain.DisabledGeoAutoSuspend, at)
	if err != nil || !lifted {
		t.Fatalf("cutoff at the suspension: lifted=%v err=%v, want true, nil", lifted, err)
	}
}

// The suspension mail already said the service comes back by itself, so the
// automatic lift sends nothing. It still invalidates the auth cache and
// pushes, with the upstream client re-enabled.
func TestLiftServiceIfHeldSince_SendsNoRestoreMail(t *testing.T) {
	at := time.Now().Add(-2 * time.Hour)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at})

	lifted, err := h.svc.LiftServiceIfHeldSince(context.Background(), 7, domain.DisabledGeoAutoSuspend, time.Now())
	if err != nil || !lifted {
		t.Fatalf("lifted=%v err=%v, want true, nil", lifted, err)
	}
	if len(h.mail.restored) != 0 || len(h.mail.suspended) != 0 {
		t.Fatalf("mail sent: restored=%v suspended=%v, want none", h.mail.restored, h.mail.suspended)
	}
	if len(h.life.calls) != 1 || !h.life.calls[0].want.Enable {
		t.Fatalf("push = %+v, want one push with Enable=true", h.life.calls)
	}
	if len(h.invalidated) != 1 || h.invalidated[0] != 7 {
		t.Fatalf("auth invalidations = %v, want [7]", h.invalidated)
	}
}

// Two lifts of one suspension (the scheduled and the manual poll) must lift
// it once: the second finds nothing of its own to clear.
func TestLiftServiceIfHeldSince_IsIdempotent(t *testing.T) {
	at := time.Now().Add(-2 * time.Hour)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at})

	first, err := h.svc.LiftServiceIfHeldSince(context.Background(), 7, domain.DisabledGeoAutoSuspend, time.Now())
	if err != nil || !first {
		t.Fatalf("first: lifted=%v err=%v, want true, nil", first, err)
	}
	second, err := h.svc.LiftServiceIfHeldSince(context.Background(), 7, domain.DisabledGeoAutoSuspend, time.Now())
	if err != nil || second {
		t.Fatalf("second: lifted=%v err=%v, want false, nil", second, err)
	}
	if len(h.life.calls) != 1 {
		t.Fatalf("pushes = %d, want 1", len(h.life.calls))
	}
}

// An admin resuming a geo_auto suspension is the false-positive signal (the
// detector suspended someone a person decided should not be). It is counted
// where it happens, in the one resume path; resuming any other reason is not.
func TestResumeServiceAndSync_CountsAnAdminLiftOfGeoAuto(t *testing.T) {
	metrics.Reset()
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(
		&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at},
		&domain.User{ID: 8, Enabled: true, ServiceDisabledReason: domain.DisabledServiceManual, ServiceDisabledAt: &at},
	)

	if err := h.svc.ResumeServiceAndSync(context.Background(), 7); err != nil {
		t.Fatalf("resume geo_auto: %v", err)
	}
	if err := h.svc.ResumeServiceAndSync(context.Background(), 8); err != nil {
		t.Fatalf("resume service_manual: %v", err)
	}
	if got := geoAutoCounter("lifted_admin"); got != 1 {
		t.Fatalf("lifted_admin = %d, want 1 (only the geo_auto resume counts)", got)
	}
}

// Invalid reasons are refused before any lock or write: an empty reason
// would make the CAS a no-op, and a non-service reason would lock nobody out.
func TestSuspendServiceIfClear_RejectsANonServiceReason(t *testing.T) {
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true})
	for _, r := range []domain.AutoDisabledReason{domain.DisabledNone, domain.DisabledManual} {
		if _, err := h.svc.SuspendServiceIfClear(context.Background(), 7, r, "d"); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("reason %q: err = %v, want ErrValidation", r, err)
		}
	}
	if _, err := h.svc.LiftServiceIfHeldSince(context.Background(), 7, domain.DisabledNone, time.Now()); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("lift with an empty reason: err = %v, want ErrValidation", err)
	}
	if got := h.repo.byID[7].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("reason = %q, want untouched", got)
	}
}

// ---------------------------------------------------------------------------
// A caller cancelled after the write has committed.
// ---------------------------------------------------------------------------

// abortingUserRepo is the user repository the way a database-backed one
// behaves under a cancelled context — every call fails with ctx.Err() — plus
// the moment that matters here: the conditional write commits and THEN the
// caller's context is cancelled, as when an admin closes the "Poll now" tab or
// the app shuts down while the poll is in Phase 4.
type abortingUserRepo struct {
	*memoryUserRepo
	cancel context.CancelFunc
}

func (r *abortingUserRepo) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.memoryUserRepo.GetByID(ctx, id)
}

func (r *abortingUserRepo) SetServiceStateIfClear(ctx context.Context, userID int64, reason domain.AutoDisabledReason, detail string, at time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	applied, err := r.memoryUserRepo.SetServiceStateIfClear(ctx, userID, reason, detail, at)
	if applied {
		r.cancel()
	}
	return applied, err
}

func (r *abortingUserRepo) ClearServiceStateIfReason(ctx context.Context, userID int64, reason domain.AutoDisabledReason) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	lifted, err := r.memoryUserRepo.ClearServiceStateIfReason(ctx, userID, reason)
	if lifted {
		r.cancel()
	}
	return lifted, err
}

// ctxSharedLife is the shared-client push the way the panel adapter behaves:
// a cancelled context fails the request before it leaves. down makes the
// panel itself refuse, so only the queued retry can converge it. bounded
// records, per call that got through, whether the context carried a deadline.
type ctxSharedLife struct {
	down    bool
	calls   []sharedLifeCall
	bounded []bool
}

func (f *ctxSharedLife) SyncUserLifecycle(ctx context.Context, userID int64, want domain.UserLifecycle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, ok := ctx.Deadline()
	f.bounded = append(f.bounded, ok)
	if f.down {
		return errors.New("panel unreachable")
	}
	f.calls = append(f.calls, sharedLifeCall{userID, want})
	return nil
}

// ctxTaskRepo is the sync-task queue the way the database-backed one behaves
// under a cancelled context.
type ctxTaskRepo struct {
	recordingTaskRepo
	bounded []bool
}

func (r *ctxTaskRepo) GetActiveByTarget(ctx context.Context, typ domain.SyncTaskType, target string, id int64) (*domain.SyncTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.recordingTaskRepo.GetActiveByTarget(ctx, typ, target, id)
}

func (r *ctxTaskRepo) Create(ctx context.Context, t *domain.SyncTask) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, ok := ctx.Deadline()
	r.bounded = append(r.bounded, ok)
	return r.recordingTaskRepo.Create(ctx, t)
}

// abortAfterWrite wires a Service whose caller context is cancelled the
// moment the conditional write commits.
func abortAfterWrite(u *domain.User, down bool) (*Service, *ctxSharedLife, *ctxTaskRepo, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := &abortingUserRepo{memoryUserRepo: &memoryUserRepo{byID: map[int64]*domain.User{u.ID: u}}, cancel: cancel}
	life := &ctxSharedLife{down: down}
	tasks := &ctxTaskRepo{}
	svc := &Service{users: repo, ownership: emptyOwnershipRepo{}, tasks: tasks, settings: bfSettings{}}
	svc.SetSharedLifecycleSyncer(life)
	return svc, life, tasks, ctx
}

// Once the lift has cleared the row, PSP says the user is served again. The
// upstream client is still disabled until the push lands, so a caller that
// goes away right after the write must not take the push (or, when the panel
// is down, its queued retry) with it: that left an account every PSP surface
// called active cut off for up to an hour, with nothing queued to fix it.
// The follow-up is still bounded: it runs on a context with a deadline.
func TestLiftServiceIfHeldSince_FinishesThePushWhenTheCallerIsCancelledAfterTheWrite(t *testing.T) {
	at := time.Now().Add(-2 * time.Hour)
	held := func() *domain.User {
		return &domain.User{ID: 7, UPN: "u@example.com", Enabled: true,
			ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at}
	}

	t.Run("panel up: pushed", func(t *testing.T) {
		svc, life, tasks, ctx := abortAfterWrite(held(), false)

		lifted, err := svc.LiftServiceIfHeldSince(ctx, 7, domain.DisabledGeoAutoSuspend, time.Now())

		if err != nil || !lifted {
			t.Fatalf("lifted=%v err=%v, want true, nil (the caller went away after the write, not before it)", lifted, err)
		}
		if len(life.calls) != 1 || life.calls[0].userID != 7 || !life.calls[0].want.Enable {
			t.Fatalf("push = %+v, want one push for user 7 with Enable=true", life.calls)
		}
		if len(life.bounded) != 1 || !life.bounded[0] {
			t.Fatalf("push context bounded = %v, want [true] (detached, but never unbounded)", life.bounded)
		}
		if got := pushConfigTasks(&tasks.recordingTaskRepo); len(got) != 0 {
			t.Fatalf("queued push tasks = %+v, want none (the push landed)", got)
		}
	})

	t.Run("panel down: queued", func(t *testing.T) {
		svc, _, tasks, ctx := abortAfterWrite(held(), true)

		lifted, err := svc.LiftServiceIfHeldSince(ctx, 7, domain.DisabledGeoAutoSuspend, time.Now())

		if err != nil || !lifted {
			t.Fatalf("lifted=%v err=%v, want true, nil (the failed push is queued, not returned)", lifted, err)
		}
		if got := pushConfigTasks(&tasks.recordingTaskRepo); len(got) != 1 || got[0].TargetID != 7 {
			t.Fatalf("queued push tasks = %+v, want one for user 7", got)
		}
		if len(tasks.bounded) != 1 || !tasks.bounded[0] {
			t.Fatalf("queue context bounded = %v, want [true]", tasks.bounded)
		}
	})
}

// The same on the suspension side, where it fails OPEN: the row says
// geo_auto, the user reads a suspension mail, and the upstream client keeps
// serving them. The fresh re-read the push works from is part of the
// follow-up too, so it must not ride the cancelled context either.
func TestSuspendServiceIfClear_FinishesThePushWhenTheCallerIsCancelledAfterTheWrite(t *testing.T) {
	clear := func() *domain.User { return &domain.User{ID: 7, UPN: "u@example.com", Enabled: true} }

	t.Run("panel up: pushed", func(t *testing.T) {
		svc, life, tasks, ctx := abortAfterWrite(clear(), false)

		applied, err := svc.SuspendServiceIfClear(ctx, 7, domain.DisabledGeoAutoSuspend, "d")

		if err != nil || !applied {
			t.Fatalf("applied=%v err=%v, want true, nil (the caller went away after the write, not before it)", applied, err)
		}
		if len(life.calls) != 1 || life.calls[0].userID != 7 || life.calls[0].want.Enable {
			t.Fatalf("push = %+v, want one push for user 7 with Enable=false", life.calls)
		}
		if len(life.bounded) != 1 || !life.bounded[0] {
			t.Fatalf("push context bounded = %v, want [true] (detached, but never unbounded)", life.bounded)
		}
		if got := pushConfigTasks(&tasks.recordingTaskRepo); len(got) != 0 {
			t.Fatalf("queued push tasks = %+v, want none (the push landed)", got)
		}
	})

	t.Run("panel down: queued", func(t *testing.T) {
		svc, _, tasks, ctx := abortAfterWrite(clear(), true)

		applied, err := svc.SuspendServiceIfClear(ctx, 7, domain.DisabledGeoAutoSuspend, "d")

		if err != nil || !applied {
			t.Fatalf("applied=%v err=%v, want true, nil (the failed push is queued, not returned)", applied, err)
		}
		if got := pushConfigTasks(&tasks.recordingTaskRepo); len(got) != 1 || got[0].TargetID != 7 {
			t.Fatalf("queued push tasks = %+v, want one for user 7", got)
		}
		if len(tasks.bounded) != 1 || !tasks.bounded[0] {
			t.Fatalf("queue context bounded = %v, want [true]", tasks.bounded)
		}
	})
}
