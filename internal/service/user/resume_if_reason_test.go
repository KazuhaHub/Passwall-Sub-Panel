package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ResumeServiceIfReason is the resume a stale screen cannot misfire: the
// risk center's drawer names the hold it showed, and the service lifts only
// that one. Each row holds one reason on the account and asks to lift one;
// every side effect ResumeServiceAndSync has must happen exactly when the
// lift does, and none of them when it does not.
func TestResumeServiceIfReason_LiftsOnlyThatReason(t *testing.T) {
	at := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	violationAt := at.Add(-time.Hour)
	cases := []struct {
		name       string
		held       domain.AutoDisabledReason
		ask        domain.AutoDisabledReason
		wantLifted bool
		wantRecord bool // an auto_lifted_admin record + a lifted_admin count
	}{
		{name: "geo_auto", held: domain.DisabledGeoAutoSuspend, ask: domain.DisabledGeoAutoSuspend, wantLifted: true, wantRecord: true},
		{name: "geo_anomaly", held: domain.DisabledGeoAnomaly, ask: domain.DisabledGeoAnomaly, wantLifted: true},
		{name: "service_manual", held: domain.DisabledServiceManual, ask: domain.DisabledServiceManual, wantLifted: true},
		{name: "blocked_client", held: domain.DisabledBlockedClient, ask: domain.DisabledBlockedClient, wantLifted: true},
		{name: "another reason holds it", held: domain.DisabledServiceManual, ask: domain.DisabledGeoAutoSuspend},
		{name: "another reason over a block", held: domain.DisabledBlockedClient, ask: domain.DisabledServiceManual},
		{name: "nothing holds it", held: domain.DisabledNone, ask: domain.DisabledGeoAutoSuspend},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &domain.User{ID: 7, UPN: "u@example.com", Enabled: true, BlockViolationCount: 3, LastBlockViolationAt: &violationAt}
			if tc.held != domain.DisabledNone {
				u.ServiceDisabledReason, u.ServiceDisableDetail, u.ServiceDisabledAt = tc.held, "theirs", &at
			}
			h := newGeoAutoHarness(u)
			flags := &fakeFlagHistory{}
			h.svc.SetFlagRecorder(flags)
			countBefore := geoAutoCounter("lifted_admin")

			before := time.Now()
			lifted, err := h.svc.ResumeServiceIfReason(context.Background(), 7, tc.ask)
			after := time.Now()

			if err != nil || lifted != tc.wantLifted {
				t.Fatalf("lifted=%v err=%v, want %v, nil", lifted, err, tc.wantLifted)
			}
			got := h.repo.byID[7]
			if !tc.wantLifted {
				if got.ServiceDisabledReason != tc.held {
					t.Fatalf("reason = %q, want %q untouched", got.ServiceDisabledReason, tc.held)
				}
				if tc.held != domain.DisabledNone && (got.ServiceDisableDetail != "theirs" || got.ServiceDisabledAt == nil || !got.ServiceDisabledAt.Equal(at)) {
					t.Fatalf("row = %q/%v, want theirs/%v untouched", got.ServiceDisableDetail, got.ServiceDisabledAt, at)
				}
				if got.BlockViolationCount != 3 {
					t.Fatalf("violation count = %d, want 3 untouched (nothing was lifted)", got.BlockViolationCount)
				}
				if len(flags.calls) != 0 || len(h.life.calls) != 0 || len(h.mail.restored) != 0 || len(h.invalidated) != 0 {
					t.Fatalf("a refused resume had effects: records=%v pushes=%+v mail=%v invalidations=%v",
						flags.calls, h.life.calls, h.mail.restored, h.invalidated)
				}
				if n := geoAutoCounter("lifted_admin") - countBefore; n != 0 {
					t.Fatalf("lifted_admin moved by %d, want 0", n)
				}
				return
			}

			if got.ServiceDisabledReason != domain.DisabledNone || got.ServiceDisableDetail != "" || got.ServiceDisabledAt != nil {
				t.Fatalf("row = %q/%q/%v, want cleared", got.ServiceDisabledReason, got.ServiceDisableDetail, got.ServiceDisabledAt)
			}
			if len(h.life.calls) != 1 || h.life.calls[0].userID != 7 || !h.life.calls[0].want.Enable {
				t.Fatalf("push = %+v, want one push for user 7 with Enable=true", h.life.calls)
			}
			if len(h.invalidated) != 1 || h.invalidated[0] != 7 {
				t.Fatalf("auth invalidations = %v, want [7]", h.invalidated)
			}
			wantViolations := 3
			if tc.held == domain.DisabledBlockedClient {
				// The next allowed fetch must not re-suspend at once.
				wantViolations = 0
			}
			if got.BlockViolationCount != wantViolations {
				t.Fatalf("violation count = %d, want %d", got.BlockViolationCount, wantViolations)
			}
			recs := flags.records()
			wantCount := int64(0)
			if tc.wantRecord {
				wantCount = 1
				if len(recs) != 1 {
					t.Fatalf("records = %+v, want one auto_lifted_admin", recs)
				}
				requireGeoAutoRecord(t, recs[0], 7, domain.FlagAutoLiftedAdmin, "admin_resume", before, after)
			} else if len(recs) != 0 {
				t.Fatalf("records = %+v, want none: only the detector's own hold has a geo_auto history", recs)
			}
			if n := geoAutoCounter("lifted_admin") - countBefore; n != wantCount {
				t.Fatalf("lifted_admin moved by %d, want %d", n, wantCount)
			}
		})
	}
}

// A lift the user gets told about, as with ResumeServiceAndSync: every
// suspension mail has its restore mail. A refused lift sends nothing.
func TestResumeServiceIfReason_SendsTheRestoreMail(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(
		&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at},
		&domain.User{ID: 8, Enabled: true, ServiceDisabledReason: domain.DisabledServiceManual, ServiceDisabledAt: &at},
	)

	if lifted, err := h.svc.ResumeServiceIfReason(context.Background(), 7, domain.DisabledGeoAutoSuspend); err != nil || !lifted {
		t.Fatalf("user 7: lifted=%v err=%v, want true, nil", lifted, err)
	}
	if lifted, err := h.svc.ResumeServiceIfReason(context.Background(), 8, domain.DisabledGeoAutoSuspend); err != nil || lifted {
		t.Fatalf("user 8: lifted=%v err=%v, want false, nil", lifted, err)
	}
	if len(h.mail.restored) != 1 || h.mail.restored[0] != 7 {
		t.Fatalf("restore mails = %v, want exactly [7]", h.mail.restored)
	}
	if len(h.mail.suspended) != 0 {
		t.Fatalf("suspension mails = %+v, want none", h.mail.suspended)
	}
}

// The read, the clear and the push wait for lockUser, the lock
// ResyncMembership and the detector's own transitions hold, so a resync in
// flight cannot push the older lifecycle over the resume. The lock is
// released before any assertion can end the test.
func TestResumeServiceIfReason_TakesTheUserLock(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAnomaly, ServiceDisabledAt: &at})
	done := make(chan bool, 1)

	unlock := h.svc.lockUser(7)
	go func() {
		lifted, _ := h.svc.ResumeServiceIfReason(context.Background(), 7, domain.DisabledGeoAnomaly)
		done <- lifted
	}()
	time.Sleep(50 * time.Millisecond)
	held, pushed := h.repo.byID[7].ServiceDisabledReason, len(h.life.calls)
	unlock()
	if held != domain.DisabledGeoAnomaly || pushed != 0 {
		t.Errorf("reason = %q, pushes = %d while the user lock was held, want geo_anomaly and 0 (the resume must wait)", held, pushed)
	}

	select {
	case lifted := <-done:
		if !lifted {
			t.Fatal("lifted = false after the lock was released, want true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ResumeServiceIfReason never returned after the lock was released")
	}
	if got := h.repo.byID[7].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("reason = %q after the lock was released, want cleared", got)
	}
}

// The fresh read is not the last word: a hold another admin writes between
// it and the clear (SetServiceSuspendedAndSync takes no per-user lock) is
// newer than what the drawer showed, and the conditional clear leaves it.
func TestResumeServiceIfReason_LeavesAHoldWrittenAfterTheRead(t *testing.T) {
	at := time.Now().Add(-2 * time.Hour)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisableDetail: "auto", ServiceDisabledAt: &at})
	pausedAt := time.Now().Add(-time.Minute)
	h.svc.users = &adminPausesAfterReadRepo{memoryUserRepo: h.repo, pausedAt: pausedAt}
	flags := &fakeFlagHistory{}
	h.svc.SetFlagRecorder(flags)

	lifted, err := h.svc.ResumeServiceIfReason(context.Background(), 7, domain.DisabledGeoAutoSuspend)
	if err != nil || lifted {
		t.Fatalf("lifted=%v err=%v, want false, nil — the row no longer carries geo_auto", lifted, err)
	}
	if got := h.repo.byID[7]; got.ServiceDisabledReason != domain.DisabledServiceManual || got.ServiceDisableDetail != "admin pause" {
		t.Fatalf("row = %q/%q, want the admin's service_manual/admin pause untouched", got.ServiceDisabledReason, got.ServiceDisableDetail)
	}
	if len(flags.calls) != 0 || len(h.life.calls) != 0 || len(h.mail.restored) != 0 || len(h.invalidated) != 0 {
		t.Fatalf("a clear that cleared nothing had effects: records=%v pushes=%+v mail=%v invalidations=%v",
			flags.calls, h.life.calls, h.mail.restored, h.invalidated)
	}
}

// Once the clear commits, PSP calls the user served again, so the push (or
// its queued retry) must outlive a request that goes away right after the
// write, as in LiftServiceIfHeldSince; and a push that fails is queued, not
// returned.
func TestResumeServiceIfReason_FinishesThePushWhenTheCallerIsCancelledAfterTheWrite(t *testing.T) {
	at := time.Now().Add(-2 * time.Hour)
	held := func() *domain.User {
		return &domain.User{ID: 7, UPN: "u@example.com", Enabled: true,
			ServiceDisabledReason: domain.DisabledServiceManual, ServiceDisabledAt: &at}
	}

	t.Run("panel up: pushed", func(t *testing.T) {
		svc, life, tasks, ctx := abortAfterWrite(held(), false)

		lifted, err := svc.ResumeServiceIfReason(ctx, 7, domain.DisabledServiceManual)

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

		lifted, err := svc.ResumeServiceIfReason(ctx, 7, domain.DisabledServiceManual)

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

// Only a service reason names a hold this can lift; an empty one or an
// account-axis reason is a caller's mistake, refused before any lock or read.
func TestResumeServiceIfReason_RejectsANonServiceReason(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledServiceManual, ServiceDisabledAt: &at})
	for _, r := range []domain.AutoDisabledReason{domain.DisabledNone, domain.DisabledManual, "bogus"} {
		if _, err := h.svc.ResumeServiceIfReason(context.Background(), 7, r); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("reason %q: err = %v, want ErrValidation", r, err)
		}
	}
	if got := h.repo.byID[7].ServiceDisabledReason; got != domain.DisabledServiceManual {
		t.Fatalf("reason = %q, want service_manual untouched", got)
	}
}

// ResumeGeoAutoIfHeld is the geo_auto case of ResumeServiceIfReason and
// nothing else: the detector's own hold is lifted, with its record, and a
// person's hold — even a geo one — is left alone.
func TestResumeGeoAutoIfHeld_IsTheGeoAutoCase(t *testing.T) {
	at := time.Now().Add(-10 * time.Minute)
	h := newGeoAutoHarness(
		&domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend, ServiceDisabledAt: &at},
		&domain.User{ID: 8, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAnomaly, ServiceDisabledAt: &at},
		&domain.User{ID: 9, Enabled: true},
	)
	flags := &fakeFlagHistory{}
	h.svc.SetFlagRecorder(flags)

	if lifted, err := h.svc.ResumeGeoAutoIfHeld(context.Background(), 7); err != nil || !lifted {
		t.Fatalf("geo_auto: lifted=%v err=%v, want true, nil", lifted, err)
	}
	for _, uid := range []int64{8, 9} {
		if lifted, err := h.svc.ResumeGeoAutoIfHeld(context.Background(), uid); err != nil || lifted {
			t.Fatalf("user %d: lifted=%v err=%v, want false, nil", uid, lifted, err)
		}
	}
	if got := h.repo.byID[7].ServiceDisabledReason; got != domain.DisabledNone {
		t.Fatalf("user 7 reason = %q, want cleared", got)
	}
	if got := h.repo.byID[8].ServiceDisabledReason; got != domain.DisabledGeoAnomaly {
		t.Fatalf("user 8 reason = %q, want geo_anomaly untouched", got)
	}
	if recs := flags.records(); len(recs) != 1 || recs[0].UserID != 7 || recs[0].Event != domain.FlagAutoLiftedAdmin {
		t.Fatalf("records = %+v, want user 7's auto_lifted_admin only", recs)
	}
}
