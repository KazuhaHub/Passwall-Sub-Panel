package twofa

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type memStore struct {
	user         *domain.User
	secret       string
	enabled      bool
	recovery     []string
	setCalls     int
	cleared      bool
	codesWrites  [][]string
	consumeFails bool // simulate losing the atomic CAS race
}

func (m *memStore) SetTOTP(_ context.Context, _ int64, secret string, enabled bool, codes []string) error {
	m.setCalls++
	m.secret, m.enabled, m.recovery = secret, enabled, codes
	return nil
}
func (m *memStore) GetTOTP(context.Context, int64) (string, bool, []string, error) {
	return m.secret, m.enabled, m.recovery, nil
}
func (m *memStore) SetRecoveryCodes(_ context.Context, _ int64, codes []string) error {
	m.recovery = codes
	m.codesWrites = append(m.codesWrites, codes)
	return nil
}
func (m *memStore) ConsumeRecoveryCode(_ context.Context, _ int64, _, next []string) (bool, error) {
	if m.consumeFails {
		return false, nil // another concurrent request already consumed this code
	}
	m.recovery = next
	m.codesWrites = append(m.codesWrites, next)
	return true, nil
}
func (m *memStore) ClearTOTP(context.Context, int64) error { m.cleared = true; return nil }
func (m *memStore) GetByID(context.Context, int64) (*domain.User, error) {
	return m.user, nil
}

type stubSettings struct{ on bool }

func (s stubSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	// SiteTitle is the full brand; AppTitle is the short product name. The TOTP
	// issuer must prefer SiteTitle (BrandName) so authenticator apps show the
	// site name, not "Passwall".
	return ports.UISettings{TOTPEnabled: s.on, SiteTitle: "Kazuha Hub Passwall", AppTitle: "Passwall"}, nil
}

func newSvc(store *memStore, on bool, validCode string) *Service {
	return New(Deps{
		Users:    store,
		Settings: stubSettings{on: on},
		Validate: func(code, secret string) bool { return code == validCode && secret != "" },
		GenSecret: func(_, _ string) (string, string, error) {
			return "SECRET32", "otpauth://totp/Passwall:u@x?secret=SECRET32", nil
		},
		GenRecovery: func() ([]string, error) { return []string{"AAAA-BBBB", "CCCC-DDDD"}, nil },
	})
}

func TestBegin_Gated(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1, UPN: "u@x", PasswordHash: "bcrypt"}}
	if _, _, err := newSvc(st, false, "111111").Begin(context.Background(), 1); err == nil {
		t.Fatal("Begin must error when the 2FA setting is off")
	}
}

func TestBegin_AlreadyEnabled(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1, UPN: "u@x", PasswordHash: "bcrypt"}, enabled: true, secret: "S"}
	if _, _, err := newSvc(st, true, "111111").Begin(context.Background(), 1); err == nil {
		t.Fatal("Begin must error when 2FA is already enabled")
	}
}

func TestBegin_IssuerUsesBrandName(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1, UPN: "u@x", PasswordHash: "bcrypt"}}
	var gotIssuer string
	svc := New(Deps{
		Users:    st,
		Settings: stubSettings{on: true},
		Validate: func(string, string) bool { return true },
		GenSecret: func(issuer, _ string) (string, string, error) {
			gotIssuer = issuer
			return "SECRET32", "otpauth://x", nil
		},
		GenRecovery: func() ([]string, error) { return nil, nil },
	})
	if _, _, err := svc.Begin(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if gotIssuer != "Kazuha Hub Passwall" {
		t.Fatalf("issuer = %q, want the SiteTitle-derived brand name", gotIssuer)
	}
}

func TestBegin_StoresPendingSecret(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1, UPN: "u@x", PasswordHash: "bcrypt"}}
	url, secret, err := newSvc(st, true, "111111").Begin(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if secret != "SECRET32" || url == "" {
		t.Fatalf("Begin should return secret+url, got %q %q", secret, url)
	}
	if st.secret != "SECRET32" || st.enabled {
		t.Fatalf("Begin must store the secret DISABLED, got secret=%q enabled=%v", st.secret, st.enabled)
	}
}

func TestEnable_InvalidCode(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1}, secret: "SECRET32", enabled: false}
	if _, err := newSvc(st, true, "111111").Enable(context.Background(), 1, "999999"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Enable with wrong code must be ErrUnauthorized, got %v", err)
	}
	if st.enabled {
		t.Fatal("wrong code must not enable")
	}
}

func TestEnable_ValidCodeReturnsRecovery(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1}, secret: "SECRET32", enabled: false}
	codes, err := newSvc(st, true, "111111").Enable(context.Background(), 1, "111111")
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 2 || codes[0] != "AAAA-BBBB" {
		t.Fatalf("Enable should return plaintext recovery codes, got %v", codes)
	}
	if !st.enabled || len(st.recovery) != 2 {
		t.Fatalf("Enable must store enabled + hashed recovery codes, enabled=%v codes=%d", st.enabled, len(st.recovery))
	}
	// Stored codes must be HASHES, not the plaintext.
	if st.recovery[0] == "AAAA-BBBB" {
		t.Fatal("recovery codes must be stored hashed, not plaintext")
	}
}

func TestVerifyLogin_TOTP(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1}, secret: "SECRET32", enabled: true}
	ok, err := newSvc(st, true, "111111").VerifyLogin(context.Background(), 1, "111111")
	if err != nil || !ok {
		t.Fatalf("valid TOTP must verify: ok=%v err=%v", ok, err)
	}
	if bad, _ := newSvc(st, true, "111111").VerifyLogin(context.Background(), 1, "000000"); bad {
		t.Fatal("wrong TOTP must not verify")
	}
}

func TestVerifyLogin_RecoveryCodeConsumed(t *testing.T) {
	// Stored hash of "AAAA-BBBB" (normalized).
	st := &memStore{user: &domain.User{ID: 1}, secret: "SECRET32", enabled: true,
		recovery: []string{hashRecovery("AAAA-BBBB"), hashRecovery("CCCC-DDDD")}}
	svc := newSvc(st, true, "111111")
	ok, err := svc.VerifyLogin(context.Background(), 1, "aaaa-bbbb") // case/format-insensitive
	if err != nil || !ok {
		t.Fatalf("valid recovery code must verify: ok=%v err=%v", ok, err)
	}
	// Must be consumed: the remaining set has only the other code.
	if len(st.recovery) != 1 || st.recovery[0] != hashRecovery("CCCC-DDDD") {
		t.Fatalf("recovery code must be consumed, remaining=%v", st.recovery)
	}
	// Replay of the consumed code fails.
	if again, _ := svc.VerifyLogin(context.Background(), 1, "AAAA-BBBB"); again {
		t.Fatal("a consumed recovery code must not verify again")
	}
}

func TestVerifyLogin_RecoveryRaceLoserRejected(t *testing.T) {
	// The code matches, but the atomic consume loses the race (another concurrent
	// request consumed it first). Verify must NOT succeed — otherwise one
	// single-use recovery code mints two sessions (the double-spend the review found).
	st := &memStore{user: &domain.User{ID: 1}, secret: "SECRET32", enabled: true,
		recovery: []string{hashRecovery("AAAA-BBBB")}, consumeFails: true}
	ok, err := newSvc(st, true, "111111").VerifyLogin(context.Background(), 1, "AAAA-BBBB")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a recovery code whose atomic consume lost the race must not verify")
	}
}

func TestDisable_RequiresValidCode(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1}, secret: "SECRET32", enabled: true}
	if err := newSvc(st, true, "111111").Disable(context.Background(), 1, "000000"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("disable with wrong code must be ErrUnauthorized, got %v", err)
	}
	if st.cleared {
		t.Fatal("wrong code must not clear")
	}
	if err := newSvc(st, true, "111111").Disable(context.Background(), 1, "111111"); err != nil {
		t.Fatal(err)
	}
	if !st.cleared {
		t.Fatal("valid code must clear TOTP")
	}
}

func TestAdminReset_NoCode(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1}, secret: "S", enabled: true}
	if err := newSvc(st, true, "111111").AdminReset(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if !st.cleared {
		t.Fatal("admin reset must clear TOTP unconditionally")
	}
}

func TestRegenerateRecovery_RequiresEnabled(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1, UPN: "u@x"}, enabled: false, secret: "S"}
	if _, err := newSvc(st, true, "111111").RegenerateRecovery(context.Background(), 1, "111111"); err == nil {
		t.Fatal("RegenerateRecovery must error when 2FA is not enabled")
	}
}

func TestRegenerateRecovery_BadProof(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1, UPN: "u@x"}, enabled: true, secret: "S",
		recovery: []string{hashRecovery("OLD1-OLD1")}}
	before := st.setCalls
	if _, err := newSvc(st, true, "111111").RegenerateRecovery(context.Background(), 1, "wrong"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("bad proof must be ErrUnauthorized, got %v", err)
	}
	if st.setCalls != before {
		t.Fatal("bad proof must not rewrite recovery codes")
	}
}

func TestRegenerateRecovery_SuccessWithTOTP(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1, UPN: "u@x"}, enabled: true, secret: "S"}
	codes, err := newSvc(st, true, "111111").RegenerateRecovery(context.Background(), 1, "111111")
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 2 {
		t.Fatalf("want 2 fresh codes, got %d", len(codes))
	}
	if !st.enabled || st.secret != "S" {
		t.Fatal("regeneration must preserve secret + enabled")
	}
	if len(st.recovery) != 2 || st.recovery[0] != hashRecovery(codes[0]) {
		t.Fatal("store must hold hashes of the new plaintext codes")
	}
}

func TestRegenerateRecovery_SuccessWithRecoveryCode(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1, UPN: "u@x"}, enabled: true, secret: "S",
		recovery: []string{hashRecovery("ZZZZ-YYYY")}}
	codes, err := newSvc(st, true, "111111").RegenerateRecovery(context.Background(), 1, "zzzz-yyyy")
	if err != nil {
		t.Fatalf("a valid recovery code must prove possession: %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("want 2 fresh codes, got %d", len(codes))
	}
}

func TestAdminRegenerateRecovery(t *testing.T) {
	st := &memStore{user: &domain.User{ID: 1, UPN: "u@x"}, enabled: true, secret: "S"}
	codes, err := newSvc(st, true, "111111").AdminRegenerateRecovery(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 2 || !st.enabled || st.secret != "S" {
		t.Fatal("admin regenerate must return fresh codes and keep 2FA on")
	}
	st2 := &memStore{user: &domain.User{ID: 2, UPN: "v@x"}, enabled: false}
	if _, err := newSvc(st2, true, "111111").AdminRegenerateRecovery(context.Background(), 2); err == nil {
		t.Fatal("admin regenerate must error when the user has no 2FA")
	}
}
