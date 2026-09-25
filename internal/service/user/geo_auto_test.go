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
