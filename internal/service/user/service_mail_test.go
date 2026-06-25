package user

import (
	"context"
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
