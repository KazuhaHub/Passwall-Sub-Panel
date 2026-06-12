package login2fa

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// stubTokens implements ports.AuthTokenRepo, recording the created token and
// answering ConsumeByUserCode against it (matching hash + not yet consumed).
type stubTokens struct {
	created       *domain.AuthToken
	deletedPurp   string
	consumeReturn bool // when true, ConsumeByUserCode returns the stored token on hash match
}

func (s *stubTokens) Create(_ context.Context, t *domain.AuthToken) error { s.created = t; return nil }
func (s *stubTokens) ConsumeByTokenHash(context.Context, string, string, time.Time) (*domain.AuthToken, error) {
	return nil, domain.ErrNotFound
}
func (s *stubTokens) ConsumeByUserCode(_ context.Context, purpose string, userID int64, codeHash string, _ time.Time) (*domain.AuthToken, error) {
	if s.created != nil && s.created.UserID == userID && s.created.Purpose == purpose && s.created.CodeHash == codeHash && s.consumeReturn {
		s.consumeReturn = false // single-use
		return s.created, nil
	}
	return nil, domain.ErrNotFound
}
func (s *stubTokens) DeleteByUserPurpose(_ context.Context, _ int64, purpose string) error {
	s.deletedPurp = purpose
	return nil
}
func (s *stubTokens) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }

type stubSettings struct{ allowEmail bool }

func (s stubSettings) LoadForUser(_ context.Context, _ *domain.User, d ports.UISettings) (ports.UISettings, error) {
	d.TwoFAAllowEmail = s.allowEmail
	return d, nil
}

// stubPerGroupSettings enables email-2FA only for users in allowGroupID — proves
// SendCode threads the user through LoadForUser so a group override takes effect.
type stubPerGroupSettings struct{ allowGroupID int64 }

func (s stubPerGroupSettings) LoadForUser(_ context.Context, u *domain.User, d ports.UISettings) (ports.UISettings, error) {
	d.TwoFAAllowEmail = u != nil && u.GroupID == s.allowGroupID
	return d, nil
}

type stubSender struct {
	to, code string
	sent     bool
}

func (s *stubSender) SendLogin2FACode(_ context.Context, to, _, code string, _ int) error {
	s.to, s.code, s.sent = to, code, true
	return nil
}

func newSvc(tok *stubTokens, sender *stubSender, allowEmail bool) *Service {
	return New(Deps{
		Tokens:   tok,
		Mail:     sender,
		Settings: stubSettings{allowEmail: allowEmail},
		NewCode:  func() (string, error) { return "424242", nil },
		Dispatch: func(f func()) { f() }, // synchronous for the test
	})
}

func TestSendCode_Gated(t *testing.T) {
	tok, sender := &stubTokens{}, &stubSender{}
	u := &domain.User{ID: 7, Email: "a@b.c"}
	if err := newSvc(tok, sender, false).SendCode(context.Background(), u); err == nil {
		t.Fatal("SendCode must error when email-2FA is disabled")
	}
	if sender.sent {
		t.Fatal("no email may be sent when disabled")
	}
}

func TestSendCode_RequiresEmail(t *testing.T) {
	tok, sender := &stubTokens{}, &stubSender{}
	u := &domain.User{ID: 7, Email: "   "}
	if err := newSvc(tok, sender, true).SendCode(context.Background(), u); err == nil {
		t.Fatal("SendCode must error when the user has no email")
	}
}

func TestSendCode_StoresHashAndSends(t *testing.T) {
	tok, sender := &stubTokens{}, &stubSender{}
	u := &domain.User{ID: 7, Email: "a@b.c", UPN: "u@x"}
	if err := newSvc(tok, sender, true).SendCode(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if tok.created == nil || tok.created.Purpose != domain.AuthTokenPurposeLogin2FA {
		t.Fatal("must create a login_2fa token")
	}
	if tok.created.CodeHash == "424242" || tok.created.CodeHash == "" {
		t.Fatal("the code must be stored HASHED, never plaintext")
	}
	if tok.deletedPurp != domain.AuthTokenPurposeLogin2FA {
		t.Fatal("a new code must invalidate earlier outstanding ones")
	}
	if !sender.sent || sender.to != "a@b.c" || sender.code != "424242" {
		t.Fatalf("email must be sent with the plaintext code, got sent=%v to=%q code=%q", sender.sent, sender.to, sender.code)
	}
}

func TestSendCode_PerGroupAllowEmail(t *testing.T) {
	svc := New(Deps{
		Tokens: &stubTokens{}, Mail: &stubSender{},
		Settings: stubPerGroupSettings{allowGroupID: 7},
		NewCode:  func() (string, error) { return "424242", nil },
		Dispatch: func(f func()) { f() },
	})
	// A user in the enabled group can be sent an email code…
	if err := svc.SendCode(context.Background(), &domain.User{ID: 1, GroupID: 7, Email: "a@b.c"}); err != nil {
		t.Fatalf("group 7 should have email-2FA enabled via override: %v", err)
	}
	// …while a user in another group inherits the global (off) and stays gated.
	if err := svc.SendCode(context.Background(), &domain.User{ID: 2, GroupID: 9, Email: "c@d.e"}); err == nil {
		t.Fatal("group 9 inherits the global (off) → SendCode must be gated")
	}
}

func TestSendCode_ResendCooldown(t *testing.T) {
	tok, sender := &stubTokens{}, &stubSender{}
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	cur := now
	svc := New(Deps{
		Tokens:   tok,
		Mail:     sender,
		Settings: stubSettings{allowEmail: true},
		NewCode:  func() (string, error) { return "424242", nil },
		Dispatch: func(f func()) { f() },
		Now:      func() time.Time { return cur },
	})
	u := &domain.User{ID: 7, Email: "a@b.c", UPN: "u@x"}
	if err := svc.SendCode(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if !sender.sent {
		t.Fatal("first send must go out")
	}
	// A re-send within the cooldown window must be suppressed (no new token/email).
	sender.sent, tok.created = false, nil
	if err := svc.SendCode(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if sender.sent || tok.created != nil {
		t.Fatal("a re-send within the cooldown must be suppressed")
	}
	// Once the cooldown elapses, a fresh send is allowed again.
	cur = now.Add(2 * time.Minute)
	if err := svc.SendCode(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if !sender.sent || tok.created == nil {
		t.Fatal("a re-send after the cooldown must go out")
	}
}

func TestVerifyCode(t *testing.T) {
	tok, sender := &stubTokens{}, &stubSender{}
	svc := newSvc(tok, sender, true)
	u := &domain.User{ID: 7, Email: "a@b.c", UPN: "u@x"}
	if err := svc.SendCode(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	tok.consumeReturn = true
	// Wrong code → false.
	if ok, _ := svc.VerifyCode(context.Background(), 7, "000000"); ok {
		t.Fatal("a wrong code must not verify")
	}
	// Right code → true, then single-use (second attempt fails).
	if ok, _ := svc.VerifyCode(context.Background(), 7, "424242"); !ok {
		t.Fatal("the correct code must verify")
	}
	if ok, _ := svc.VerifyCode(context.Background(), 7, "424242"); ok {
		t.Fatal("a consumed code must not verify again")
	}
}
