package user

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type suspendMail struct {
	uid    int64
	reason string
	detail string
}

// fakeServiceMailer captures the service-suspend / -restore notifications so the
// tests can assert the reason is carried through.
type fakeServiceMailer struct {
	suspended []suspendMail
	restored  []int64
}

func (f *fakeServiceMailer) SendServiceSuspendedToUser(_ context.Context, uid int64, reason, detail string) error {
	f.suspended = append(f.suspended, suspendMail{uid, reason, detail})
	return nil
}
func (f *fakeServiceMailer) SendServiceRestoredToUser(_ context.Context, uid int64) error {
	f.restored = append(f.restored, uid)
	return nil
}

// SetServiceSuspendedAndSync must email the user that their SERVICE is suspended,
// carrying the reason + detail — so every suspend path (blocked-client, manual,
// quota, expiry) notifies uniformly from this one chokepoint.
func TestSetServiceSuspendedAndSync_EmailsReason(t *testing.T) {
	u := &domain.User{ID: 7, Enabled: true}
	svc := &Service{
		users:     &memoryUserRepo{byID: map[int64]*domain.User{7: u}},
		ownership: emptyOwnershipRepo{},
		settings:  bfSettings{},
	}
	mail := &fakeServiceMailer{}
	svc.SetMailNotifier(mail)
	// Run the async notify synchronously so the assertion is deterministic.
	svc.SetBackgroundRunner(func(_ string, fn func(ctx context.Context)) { fn(context.Background()) })

	if err := svc.SetServiceSuspendedAndSync(context.Background(), 7, domain.DisabledBlockedClient, "too many clients"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if len(mail.suspended) != 1 {
		t.Fatalf("want exactly 1 suspend email, got %d", len(mail.suspended))
	}
	got := mail.suspended[0]
	if got.uid != 7 || got.reason != string(domain.DisabledBlockedClient) || got.detail != "too many clients" {
		t.Fatalf("suspend email = %+v, want {7, blocked_client, too many clients}", got)
	}
	if len(mail.restored) != 0 {
		t.Fatalf("suspend must not send a restored email: %v", mail.restored)
	}
}

// ResumeServiceAndSync must email the user that their service is back.
func TestResumeServiceAndSync_EmailsRestored(t *testing.T) {
	u := &domain.User{ID: 8, Enabled: true, ServiceDisabledReason: domain.DisabledBlockedClient}
	svc := &Service{
		users:     &memoryUserRepo{byID: map[int64]*domain.User{8: u}},
		ownership: emptyOwnershipRepo{},
		settings:  bfSettings{},
	}
	mail := &fakeServiceMailer{}
	svc.SetMailNotifier(mail)
	svc.SetBackgroundRunner(func(_ string, fn func(ctx context.Context)) { fn(context.Background()) })

	if err := svc.ResumeServiceAndSync(context.Background(), 8); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(mail.restored) != 1 || mail.restored[0] != 8 {
		t.Fatalf("want exactly one restored email for user 8, got %v", mail.restored)
	}
	if len(mail.suspended) != 0 {
		t.Fatalf("resume must not send a suspend email: %v", mail.suspended)
	}
}

// A nil mailer must be tolerated (suspend still works, just no email).
func TestSetServiceSuspendedAndSync_NilMailerOK(t *testing.T) {
	u := &domain.User{ID: 9, Enabled: true}
	svc := &Service{
		users:     &memoryUserRepo{byID: map[int64]*domain.User{9: u}},
		ownership: emptyOwnershipRepo{},
		settings:  bfSettings{},
	}
	if err := svc.SetServiceSuspendedAndSync(context.Background(), 9, domain.DisabledServiceManual, "admin paused"); err != nil {
		t.Fatalf("suspend with nil mailer must succeed: %v", err)
	}
}

// The poll's in-memory guard reads a snapshot taken at the top of the cycle,
// so a hold written after that snapshot (an admin pause, the detector's own
// suspension) is invisible to it. The chokepoint re-checks the FRESH row: the
// quota reason never replaces a hard hold, and nothing downstream happens —
// no row write, no "your quota is used up" mail, no push.
func TestSetServiceSuspendedAndSync_QuotaNeverReplacesAHardHold(t *testing.T) {
	for _, hold := range []domain.AutoDisabledReason{domain.DisabledGeoAutoSuspend, domain.DisabledServiceManual} {
		t.Run(string(hold), func(t *testing.T) {
			u := &domain.User{ID: 7, Enabled: true, ServiceDisabledReason: hold, ServiceDisableDetail: "held"}
			repo := &memoryUserRepo{byID: map[int64]*domain.User{7: u}}
			svc := &Service{users: repo, ownership: emptyOwnershipRepo{}, settings: bfSettings{}}
			mail := &fakeServiceMailer{}
			svc.SetMailNotifier(mail)
			svc.SetBackgroundRunner(func(_ string, fn func(ctx context.Context)) { fn(context.Background()) })
			life := &fakeSharedLife{}
			svc.SetSharedLifecycleSyncer(life)

			err := svc.SetServiceSuspendedAndSync(context.Background(), 7, domain.DisabledTrafficExceeded, "traffic limit exceeded")
			if !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("err = %v, want ErrConflict", err)
			}
			if got := repo.byID[7]; got.ServiceDisabledReason != hold || got.ServiceDisableDetail != "held" {
				t.Fatalf("row = %q/%q, want %q/held unchanged", got.ServiceDisabledReason, got.ServiceDisableDetail, hold)
			}
			if len(mail.suspended) != 0 {
				t.Fatalf("suspend mail sent: %+v", mail.suspended)
			}
			if len(life.calls) != 0 {
				t.Fatalf("config pushed: %+v", life.calls)
			}
		})
	}
}

// Only the quota reason is refused: an admin, or the blocked-client policy,
// may still replace one hold with another. That is a person's (or a stricter
// policy's) newer decision, not machinery overwriting one.
func TestSetServiceSuspendedAndSync_OtherReasonsMayReplaceAHold(t *testing.T) {
	u := &domain.User{ID: 7, Enabled: true, ServiceDisabledReason: domain.DisabledGeoAutoSuspend}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{7: u}}
	svc := &Service{users: repo, ownership: emptyOwnershipRepo{}, settings: bfSettings{}}

	if err := svc.SetServiceSuspendedAndSync(context.Background(), 7, domain.DisabledGeoAnomaly, "confirmed by an admin"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if got := repo.byID[7].ServiceDisabledReason; got != domain.DisabledGeoAnomaly {
		t.Fatalf("reason = %q, want %q", got, domain.DisabledGeoAnomaly)
	}
}
