package registration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// ---- stubs ----

type stubUserStore struct {
	createLocalCalls []user.CreateLocalInput
	syncCalls        []user.CreateLocalInput
	createErr        error
	activated        []int64
	byUPN            map[string]*domain.User
	nextID           int64
	setPwd           []int64 // userIDs SetPassword was called for (resume path)
}

func (s *stubUserStore) CreateLocal(_ context.Context, in user.CreateLocalInput) (*user.CreateLocalResult, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	s.createLocalCalls = append(s.createLocalCalls, in)
	s.nextID++
	return &user.CreateLocalResult{User: &domain.User{ID: s.nextID, UPN: in.UPN, Email: in.Email}}, nil
}
func (s *stubUserStore) CreateLocalAndSync(_ context.Context, in user.CreateLocalInput) (*user.CreateLocalSyncedResult, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	s.syncCalls = append(s.syncCalls, in)
	s.nextID++
	return &user.CreateLocalSyncedResult{User: &domain.User{ID: s.nextID, UPN: in.UPN}}, nil
}
func (s *stubUserStore) ActivateAfterVerification(_ context.Context, userID int64) error {
	s.activated = append(s.activated, userID)
	return nil
}
func (s *stubUserStore) GetByUPN(_ context.Context, upn string) (*domain.User, error) {
	if u, ok := s.byUPN[upn]; ok {
		return u, nil
	}
	return nil, domain.ErrNotFound
}
func (s *stubUserStore) SetPassword(_ context.Context, userID int64, newPassword string) error {
	s.setPwd = append(s.setPwd, userID)
	return nil
}

type stubGroups struct{ groups []*domain.Group }

func (s stubGroups) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	for _, g := range s.groups {
		if g.ID == id {
			return g, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (s stubGroups) List(context.Context) ([]*domain.Group, error) { return s.groups, nil }

type memTokens struct {
	created    []*domain.AuthToken
	consume    *domain.AuthToken
	consumeErr error
}

func (m *memTokens) Create(_ context.Context, t *domain.AuthToken) error {
	m.created = append(m.created, t)
	return nil
}
func (m *memTokens) ConsumeByTokenHash(context.Context, string, string, time.Time) (*domain.AuthToken, error) {
	return m.consume, m.consumeErr
}
func (m *memTokens) ConsumeByUserCode(_ context.Context, _ string, userID int64, _ string, _ time.Time) (*domain.AuthToken, error) {
	if m.consume != nil {
		m.consume.UserID = userID
	}
	return m.consume, m.consumeErr
}
func (m *memTokens) DeleteByUserPurpose(context.Context, int64, string) error { return nil }
func (m *memTokens) DeleteExpired(context.Context, time.Time) (int64, error)   { return 0, nil }

type stubMail struct{ sent int }

func (s *stubMail) SendEmailVerification(context.Context, string, string, string, string, int) error {
	s.sent++
	return nil
}

type stubSettings struct{ s ports.UISettings }

func (s stubSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return s.s, nil
}
func (s stubSettings) Save(context.Context, ports.UISettings) error { return nil }

func newSvc(set ports.UISettings) (*Service, *stubUserStore, *memTokens, *stubMail) {
	us := &stubUserStore{byUPN: map[string]*domain.User{}}
	tk := &memTokens{}
	ml := &stubMail{}
	svc := New(Deps{
		Users: us, Groups: stubGroups{groups: []*domain.Group{{ID: 1, Slug: "default"}}},
		Tokens: tk, Mail: ml, Settings: stubSettings{s: set},
		Dispatch: func(f func()) { f() }, // synchronous for deterministic tests
		Now:      func() time.Time { return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC) },
		NewToken: func() (string, error) { return "rawtok", nil },
		NewCode:  func() (string, error) { return "123456", nil },
	})
	return svc, us, tk, ml
}

func enabledSet() ports.UISettings {
	return ports.UISettings{RegistrationEnabled: true, RegistrationDefaultGroupID: 1, RegistrationDelivery: "link", SubBaseURL: "https://p"}
}

func TestRegister_Disabled(t *testing.T) {
	svc, us, _, _ := newSvc(ports.UISettings{RegistrationEnabled: false})
	if _, err := svc.Register(context.Background(), RegisterInput{Email: "a@x.com", Password: "GoodPass1"}); err == nil {
		t.Fatal("registration disabled must error")
	}
	if len(us.createLocalCalls)+len(us.syncCalls) != 0 {
		t.Fatal("disabled registration must not create a user")
	}
}

func TestRegister_InvalidEmailAndWeakPassword(t *testing.T) {
	svc, _, _, _ := newSvc(enabledSet())
	if _, err := svc.Register(context.Background(), RegisterInput{Email: "not-an-email", Password: "GoodPass1"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid email must be ErrValidation, got %v", err)
	}
	if _, err := svc.Register(context.Background(), RegisterInput{Email: "a@x.com", Password: "weak"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("weak password must be ErrValidation, got %v", err)
	}
}

func TestRegister_DomainWhitelist(t *testing.T) {
	set := enabledSet()
	set.RegistrationEmailDomains = "example.com, corp.org"
	svc, us, _, _ := newSvc(set)
	if _, err := svc.Register(context.Background(), RegisterInput{Email: "a@gmail.com", Password: "GoodPass1"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("disallowed domain must be ErrValidation, got %v", err)
	}
	if _, err := svc.Register(context.Background(), RegisterInput{Email: "a@Example.com", Password: "GoodPass1"}); err != nil {
		t.Fatalf("allowed domain (case-insensitive) must pass, got %v", err)
	}
	if len(us.createLocalCalls) != 1 {
		t.Fatalf("allowed registration should create one pending user, got %d", len(us.createLocalCalls))
	}
}

func TestRegister_WithVerification(t *testing.T) {
	svc, us, tk, ml := newSvc(enabledSet()) // AllowUnverified=false → verification required
	res, err := svc.Register(context.Background(), RegisterInput{Email: "a@x.com", Password: "GoodPass1", DisplayName: "Al"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.RequiresVerification {
		t.Fatal("default should require verification")
	}
	if len(us.createLocalCalls) != 1 || !us.createLocalCalls[0].PendingEmailVerify {
		t.Fatalf("must CreateLocal a pending user: %+v", us.createLocalCalls)
	}
	if us.createLocalCalls[0].UPN != "a@x.com" || us.createLocalCalls[0].GroupID != 1 {
		t.Fatalf("UPN should be the email, group inherited: %+v", us.createLocalCalls[0])
	}
	if len(us.syncCalls) != 0 {
		t.Fatal("verification path must NOT provision (no CreateLocalAndSync)")
	}
	if len(tk.created) != 1 || tk.created[0].Purpose != domain.AuthTokenPurposeEmailVerify || tk.created[0].TokenHash == "" {
		t.Fatalf("must create an email_verify link token: %+v", tk.created)
	}
	if ml.sent != 1 {
		t.Fatalf("must send one verification email, got %d", ml.sent)
	}
}

func TestRegister_NoVerification(t *testing.T) {
	set := enabledSet()
	set.RegistrationAllowUnverified = true
	svc, us, tk, ml := newSvc(set)
	res, err := svc.Register(context.Background(), RegisterInput{Email: "a@x.com", Password: "GoodPass1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequiresVerification {
		t.Fatal("allow-unverified should not require verification")
	}
	if len(us.syncCalls) != 1 {
		t.Fatalf("no-verification path must CreateLocalAndSync (provision), got %d", len(us.syncCalls))
	}
	if len(us.createLocalCalls) != 0 || len(tk.created) != 0 || ml.sent != 0 {
		t.Fatal("no-verification path must not create a pending user / token / email")
	}
}

func TestRegister_EmailTaken(t *testing.T) {
	svc, us, _, _ := newSvc(enabledSet())
	us.createErr = domain.ErrAlreadyExists
	if _, err := svc.Register(context.Background(), RegisterInput{Email: "a@x.com", Password: "GoodPass1"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("taken email must surface ErrAlreadyExists, got %v", err)
	}
}

// TestRegister_ResumePending: re-registering an email that already owns a
// PENDING (unverified, disabled) account resumes the signup — refresh password,
// re-send the code — instead of dead-ending on "already registered".
func TestRegister_ResumePending(t *testing.T) {
	svc, us, tk, ml := newSvc(enabledSet())
	us.byUPN["a@x.com"] = &domain.User{ID: 42, UPN: "a@x.com", Email: "a@x.com",
		Enabled: false, AutoDisabledReason: domain.DisabledPendingEmailVerify}

	res, err := svc.Register(context.Background(), RegisterInput{Email: "a@x.com", Password: "GoodPass1"})
	if err != nil {
		t.Fatalf("resume must not error, got %v", err)
	}
	if !res.RequiresVerification {
		t.Fatal("resume must still require verification")
	}
	if len(us.createLocalCalls) != 0 {
		t.Fatal("resume must NOT create a second account")
	}
	if len(us.setPwd) != 1 || us.setPwd[0] != 42 {
		t.Fatalf("resume must refresh the pending account's password, got %v", us.setPwd)
	}
	if ml.sent != 1 || len(tk.created) != 1 {
		t.Fatalf("resume must re-send exactly one verification email/token, sent=%d tokens=%d", ml.sent, len(tk.created))
	}
}

// TestRegister_EnabledAccountRejected: an already-verified (enabled) account is
// never hijackable via re-registration — it must surface ErrAlreadyExists and
// send nothing.
func TestRegister_EnabledAccountRejected(t *testing.T) {
	svc, us, _, ml := newSvc(enabledSet())
	us.byUPN["a@x.com"] = &domain.User{ID: 7, UPN: "a@x.com", Email: "a@x.com", Enabled: true}
	if _, err := svc.Register(context.Background(), RegisterInput{Email: "a@x.com", Password: "GoodPass1"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("enabled account must surface ErrAlreadyExists, got %v", err)
	}
	if len(us.setPwd) != 0 || ml.sent != 0 {
		t.Fatal("a real account must not have its password reset or an email sent")
	}
}

// TestResendVerification_PendingOnly: resend emails only a pending account; an
// enabled or absent account is a silent no-op (enumeration-safe).
func TestResendVerification_PendingOnly(t *testing.T) {
	svc, us, _, ml := newSvc(enabledSet())
	us.byUPN["pending@x.com"] = &domain.User{ID: 1, UPN: "pending@x.com", Email: "pending@x.com",
		Enabled: false, AutoDisabledReason: domain.DisabledPendingEmailVerify}
	us.byUPN["live@x.com"] = &domain.User{ID: 2, UPN: "live@x.com", Email: "live@x.com", Enabled: true}

	svc.ResendVerification(context.Background(), "pending@x.com")
	svc.ResendVerification(context.Background(), "live@x.com")   // enabled → no-op
	svc.ResendVerification(context.Background(), "absent@x.com") // unknown → no-op
	if ml.sent != 1 {
		t.Fatalf("only the pending account should get a resend, sent=%d", ml.sent)
	}
}

// TestResendVerification_Cooldown: a second send for the same account within the
// cooldown window is suppressed (anti email-bomb). The fixed test clock means
// any two sends fall in the same window.
func TestResendVerification_Cooldown(t *testing.T) {
	svc, us, _, ml := newSvc(enabledSet())
	us.byUPN["a@x.com"] = &domain.User{ID: 5, UPN: "a@x.com", Email: "a@x.com",
		Enabled: false, AutoDisabledReason: domain.DisabledPendingEmailVerify}
	svc.ResendVerification(context.Background(), "a@x.com")
	svc.ResendVerification(context.Background(), "a@x.com")
	if ml.sent != 1 {
		t.Fatalf("second resend within cooldown must be suppressed, sent=%d", ml.sent)
	}
}

func TestVerify_LinkActivates(t *testing.T) {
	svc, us, tk, _ := newSvc(enabledSet())
	tk.consume = &domain.AuthToken{UserID: 77, Purpose: domain.AuthTokenPurposeEmailVerify}
	if err := svc.Verify(context.Background(), VerifyInput{Token: "rawtok"}); err != nil {
		t.Fatal(err)
	}
	if len(us.activated) != 1 || us.activated[0] != 77 {
		t.Fatalf("verify must activate user 77, got %v", us.activated)
	}
}

func TestVerify_InvalidToken(t *testing.T) {
	svc, us, tk, _ := newSvc(enabledSet())
	tk.consumeErr = domain.ErrNotFound
	if err := svc.Verify(context.Background(), VerifyInput{Token: "bad"}); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("invalid verify token must be ErrUnauthorized, got %v", err)
	}
	if len(us.activated) != 0 {
		t.Fatal("invalid token must not activate")
	}
}
