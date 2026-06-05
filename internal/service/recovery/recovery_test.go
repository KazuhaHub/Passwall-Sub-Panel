package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- stubs ----

type stubUsers struct{ byUPN map[string]*domain.User }

func (s stubUsers) GetByUPN(_ context.Context, upn string) (*domain.User, error) {
	if u, ok := s.byUPN[strings.TrimSpace(upn)]; ok {
		return u, nil
	}
	return nil, domain.ErrNotFound
}

type memTokens struct {
	created      []*domain.AuthToken
	consume      *domain.AuthToken
	consumeErr   error
	deletedUPs   [][2]any
	consumedHash string
}

func (m *memTokens) Create(_ context.Context, t *domain.AuthToken) error {
	m.created = append(m.created, t)
	return nil
}
func (m *memTokens) ConsumeByTokenHash(_ context.Context, _ , tokenHash string, _ time.Time) (*domain.AuthToken, error) {
	m.consumedHash = tokenHash
	return m.consume, m.consumeErr
}
func (m *memTokens) ConsumeByUserCode(_ context.Context, _ string, userID int64, _ string, _ time.Time) (*domain.AuthToken, error) {
	if m.consume != nil {
		m.consume.UserID = userID
	}
	return m.consume, m.consumeErr
}
func (m *memTokens) DeleteByUserPurpose(_ context.Context, userID int64, purpose string) error {
	m.deletedUPs = append(m.deletedUPs, [2]any{userID, purpose})
	return nil
}
func (m *memTokens) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }

type sentMail struct {
	to, name, link, code string
	expireMin            int
}
type stubMail struct{ sent []sentMail }

func (s *stubMail) SendPasswordReset(_ context.Context, to, name, link, code string, expireMin int) error {
	s.sent = append(s.sent, sentMail{to, name, link, code, expireMin})
	return nil
}

type stubSettings struct{ s ports.UISettings }

func (s stubSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return s.s, nil
}
func (s stubSettings) Save(context.Context, ports.UISettings) error { return nil }

type setPwdRec struct {
	called bool
	userID int64
	pwd    string
}

func newSvc(d Deps) (*Service, *setPwdRec) {
	rec := &setPwdRec{}
	d.SetPassword = func(_ context.Context, userID int64, pwd string) error {
		rec.called, rec.userID, rec.pwd = true, userID, pwd
		return nil
	}
	// Synchronous dispatch so the email send runs inline and tests are
	// deterministic (production runs it on a goroutine).
	d.Dispatch = func(f func()) { f() }
	if d.Now == nil {
		d.Now = func() time.Time { return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC) }
	}
	if d.NewToken == nil {
		d.NewToken = func() (string, error) { return "rawlink", nil }
	}
	if d.NewCode == nil {
		d.NewCode = func() (string, error) { return "123456", nil }
	}
	return New(d), rec
}

func userWithPw(id int64, upn, email string) *domain.User {
	return &domain.User{ID: id, UPN: upn, Email: email, PasswordHash: "bcrypt", Enabled: true}
}

func TestRequestReset_FeatureOff(t *testing.T) {
	tk := &memTokens{}
	ml := &stubMail{}
	svc, _ := newSvc(Deps{
		Users:    stubUsers{byUPN: map[string]*domain.User{"a": userWithPw(1, "a", "a@x")}},
		Tokens:   tk, Mail: ml,
		Settings: stubSettings{s: ports.UISettings{PasswordRecoveryEnabled: false}},
	})
	if err := svc.RequestReset(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if len(tk.created) != 0 || len(ml.sent) != 0 {
		t.Fatal("feature off must not create tokens or send email")
	}
}

func TestRequestReset_LinkDelivery(t *testing.T) {
	tk := &memTokens{}
	ml := &stubMail{}
	svc, _ := newSvc(Deps{
		Users:    stubUsers{byUPN: map[string]*domain.User{"a": userWithPw(1, "a", "a@x")}},
		Tokens:   tk, Mail: ml,
		Settings: stubSettings{s: ports.UISettings{PasswordRecoveryEnabled: true, PasswordRecoveryDelivery: "link", SubBaseURL: "https://panel.example"}},
	})
	if err := svc.RequestReset(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if len(tk.created) != 1 || tk.created[0].TokenHash == "" || tk.created[0].CodeHash != "" {
		t.Fatalf("link delivery must store a token_hash only: %+v", tk.created)
	}
	if tk.created[0].UserID != 1 || tk.created[0].Email != "a@x" {
		t.Fatalf("token wrong: %+v", tk.created[0])
	}
	if len(ml.sent) != 1 || ml.sent[0].link != "https://panel.example/reset-password?token=rawlink" || ml.sent[0].code != "" {
		t.Fatalf("link email wrong: %+v", ml.sent)
	}
	// prior tokens invalidated
	if len(tk.deletedUPs) != 1 {
		t.Fatalf("must invalidate prior tokens, got %v", tk.deletedUPs)
	}
}

func TestRequestReset_OTPDelivery(t *testing.T) {
	tk := &memTokens{}
	ml := &stubMail{}
	svc, _ := newSvc(Deps{
		Users:    stubUsers{byUPN: map[string]*domain.User{"a": userWithPw(1, "a", "a@x")}},
		Tokens:   tk, Mail: ml,
		Settings: stubSettings{s: ports.UISettings{PasswordRecoveryEnabled: true, PasswordRecoveryDelivery: "otp"}},
	})
	_ = svc.RequestReset(context.Background(), "a")
	if len(tk.created) != 1 || tk.created[0].CodeHash == "" || tk.created[0].TokenHash != "" {
		t.Fatalf("otp delivery must store a code_hash only: %+v", tk.created)
	}
	if len(ml.sent) != 1 || ml.sent[0].code != "123456" || ml.sent[0].link != "" {
		t.Fatalf("otp email wrong: %+v", ml.sent)
	}
}

func TestRequestReset_NoEnumeration(t *testing.T) {
	for _, tc := range []struct {
		name string
		u    *domain.User
		key  string
	}{
		{"unknown", nil, "ghost"},
		{"no-email", &domain.User{ID: 1, UPN: "a", PasswordHash: "bcrypt"}, "a"},
		{"no-localpw", &domain.User{ID: 1, UPN: "a", Email: "a@x"}, "a"},
	} {
		tk := &memTokens{}
		ml := &stubMail{}
		byUPN := map[string]*domain.User{}
		if tc.u != nil {
			byUPN["a"] = tc.u
		}
		svc, _ := newSvc(Deps{
			Users:    stubUsers{byUPN: byUPN},
			Tokens:   tk, Mail: ml,
			Settings: stubSettings{s: ports.UISettings{PasswordRecoveryEnabled: true, PasswordRecoveryDelivery: "link", SubBaseURL: "https://p"}},
		})
		if err := svc.RequestReset(context.Background(), tc.key); err != nil {
			t.Fatalf("%s: must return nil (no enumeration), got %v", tc.name, err)
		}
		if len(tk.created) != 0 || len(ml.sent) != 0 {
			t.Fatalf("%s: must not create token / send email", tc.name)
		}
	}
}

func TestRequestReset_DisabledAccountSkipped(t *testing.T) {
	tk := &memTokens{}
	ml := &stubMail{}
	u := userWithPw(1, "a", "a@x")
	u.Enabled = false
	u.AutoDisabledReason = domain.DisabledManual // not a self-service reason
	svc, _ := newSvc(Deps{
		Users:    stubUsers{byUPN: map[string]*domain.User{"a": u}},
		Tokens:   tk, Mail: ml,
		Settings: stubSettings{s: ports.UISettings{PasswordRecoveryEnabled: true, PasswordRecoveryDelivery: "link", SubBaseURL: "https://p"}},
	})
	_ = svc.RequestReset(context.Background(), "a")
	if len(tk.created) != 0 || len(ml.sent) != 0 {
		t.Fatal("manually-disabled accounts must not get reset emails")
	}
}

func TestRequestReset_LinkWithoutBaseURLRefuses(t *testing.T) {
	tk := &memTokens{}
	ml := &stubMail{}
	svc, _ := newSvc(Deps{
		Users:    stubUsers{byUPN: map[string]*domain.User{"a": userWithPw(1, "a", "a@x")}},
		Tokens:   tk, Mail: ml,
		// link delivery + empty SubBaseURL → refuse (no Host-header poisoning).
		Settings: stubSettings{s: ports.UISettings{PasswordRecoveryEnabled: true, PasswordRecoveryDelivery: "link", SubBaseURL: ""}},
	})
	_ = svc.RequestReset(context.Background(), "a")
	if len(tk.created) != 0 || len(ml.sent) != 0 {
		t.Fatal("link delivery without a configured base URL must refuse rather than send a poisonable link")
	}
}

func TestReset_LinkValid(t *testing.T) {
	tk := &memTokens{consume: &domain.AuthToken{UserID: 42}}
	svc, rec := newSvc(Deps{
		Users:    stubUsers{byUPN: map[string]*domain.User{}},
		Tokens:   tk, Mail: &stubMail{},
		Settings: stubSettings{},
	})
	if err := svc.Reset(context.Background(), ResetInput{Token: "rawlink", NewPassword: "GoodPass1"}); err != nil {
		t.Fatalf("valid reset: %v", err)
	}
	if !rec.called || rec.userID != 42 || rec.pwd != "GoodPass1" {
		t.Fatalf("SetPassword not called correctly: %+v", rec)
	}
}

func TestReset_InvalidToken(t *testing.T) {
	tk := &memTokens{consume: nil, consumeErr: domain.ErrNotFound}
	svc, rec := newSvc(Deps{Users: stubUsers{}, Tokens: tk, Mail: &stubMail{}, Settings: stubSettings{}})
	err := svc.Reset(context.Background(), ResetInput{Token: "bad", NewPassword: "GoodPass1"})
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("invalid token must be ErrUnauthorized, got %v", err)
	}
	if rec.called {
		t.Fatal("SetPassword must not be called on invalid token")
	}
}

func TestReset_WeakPasswordNotConsumed(t *testing.T) {
	tk := &memTokens{consume: &domain.AuthToken{UserID: 1}}
	svc, rec := newSvc(Deps{Users: stubUsers{}, Tokens: tk, Mail: &stubMail{}, Settings: stubSettings{}})
	err := svc.Reset(context.Background(), ResetInput{Token: "rawlink", NewPassword: "weak"})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("weak password must be ErrValidation, got %v", err)
	}
	if rec.called {
		t.Fatal("SetPassword must not run on a weak password")
	}
	if tk.consumedHash != "" {
		t.Fatal("token must NOT be consumed when the password is rejected (don't burn it)")
	}
}
