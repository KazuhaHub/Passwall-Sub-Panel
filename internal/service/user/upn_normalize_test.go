package user

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The service-layer half of the upn identity contract.
//
// internal/adapters/sqlstore owns the cross-dialect half (it is the only package
// that reads PSP_TEST_DB_KIND, so it is where "all three backends agree" gets
// pinned). What CANNOT be asserted there is that the service normalizes BEFORE
// it writes — the repo deliberately stores what it is handed. That is this file.
//
// These run against memoryUserRepo, whose Create now rejects a byte-exact
// duplicate upn exactly like the real UNIQUE index. Without that, every
// assertion below would pass vacuously.

// S1 — writes are canonical. An admin typing mixed case must not create a
// non-canonical row, because non-canonical rows are what the whole problem is
// made of: they are invisible to a normalized guard and they accumulate.
func TestCreateLocalStoresNormalizedUPN(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: &bfGroupRepo{g: &domain.Group{ID: 0}}}

	res, err := svc.CreateLocal(context.Background(), CreateLocalInput{
		UPN:   "  Alice@Corp.Com  ",
		Email: "alice@corp.com",
	})
	if err != nil {
		t.Fatalf("CreateLocal: %v", err)
	}
	if got := res.User.UPN; got != "alice@corp.com" {
		t.Fatalf("stored upn = %q, want %q (write paths must canonicalize, or the DB "+
			"unique index cannot enforce one identity per name)", got, "alice@corp.com")
	}
}

// S2 — the collision guard sees through case. This is the admin-facing symptom:
// creating "Alice@corp.com" when "alice@corp.com" exists must be refused with
// ErrAlreadyExists (which the handler turns into a 409), not silently accepted
// as a second identity for the same human.
func TestCreateLocalRejectsCaseVariantOfExistingUPN(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{
		1: {ID: 1, UPN: "alice@corp.com", Role: domain.RoleUser, Enabled: true},
	}}
	svc := &Service{users: repo, groups: &bfGroupRepo{g: &domain.Group{ID: 0}}}

	_, err := svc.CreateLocal(context.Background(), CreateLocalInput{
		UPN:   "Alice@Corp.Com",
		Email: "alice@corp.com",
	})
	if err == nil {
		t.Fatal("CreateLocal accepted a case variant of an existing upn, want domain.ErrAlreadyExists")
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("CreateLocal = %v, want it to wrap domain.ErrAlreadyExists", err)
	}
	if len(repo.byID) != 1 {
		t.Fatalf("%d rows exist, want 1 — a second identity was created for one human", len(repo.byID))
	}
}

// S3 — the sharpest case, and the one that motivated this work.
//
// An admin pre-provisions alice@corp.com with a group and a quota. Alice logs in
// through SSO for the first time; the IdP asserts "Alice@corp.com" (Entra emits
// directory casing). EnsureSSO's pass-2 guard looks the upn up to LINK the
// assertion to the existing row — and if that guard does not fold, it misses,
// and the account is FORKED: a second row with group 0 and quota 0, while the
// admin's carefully configured row sits there unused and the admin is never told.
//
// Note the direction of the damage: the fork is fail-safe (privileges are lost,
// never gained), which is why this is an integrity/availability defect and not a
// privilege-escalation vulnerability.
func TestEnsureSSOLinksCaseVariantToPreProvisionedAccount(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{
		1: {
			ID:          1,
			UPN:         "alice@corp.com",
			Role:        domain.RoleUser,
			SSOProvider: domain.SSOProviderLocal,
			SSOSubject:  "alice@corp.com",
			GroupID:     3,
			Enabled:     true,
		},
	}}
	svc := &Service{users: repo, groups: &bfGroupRepo{g: &domain.Group{ID: 0}}}

	u, err := svc.EnsureSSO(context.Background(), EnsureSSOInput{
		Provider: domain.SSOProviderSAML,
		Subject:  "idp-subject-abc",
		UPN:      "Alice@Corp.Com",
		Email:    "alice@corp.com",
	})
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if u.ID != 1 {
		t.Fatalf("EnsureSSO resolved id=%d, want id=1 — the pre-provisioned account was FORKED "+
			"into a second row instead of linked", u.ID)
	}
	if u.GroupID != 3 {
		t.Fatalf("GroupID = %d, want 3 (the admin's configuration was lost)", u.GroupID)
	}
	if len(repo.byID) != 1 {
		t.Fatalf("%d rows exist, want 1", len(repo.byID))
	}
}

// S4 — SSO JIT provisioning writes canonical too. EnsureSSO stored the IdP's
// value verbatim, not even trimmed, so a non-conforming IdP could persist a upn
// with padding that no local-credential path (all of which trim) could ever
// match again — and upn has no update path, so the row was unrepairable.
func TestEnsureSSOProvisionsNormalizedUPN(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{}}
	svc := &Service{users: repo, groups: &bfGroupRepo{g: &domain.Group{ID: 0}}}

	u, err := svc.EnsureSSO(context.Background(), EnsureSSOInput{
		Provider:        domain.SSOProviderSAML,
		Subject:         "idp-subject-new",
		UPN:             " Bob@Corp.Com ",
		Email:           "bob@corp.com",
		AllowAutoCreate: true,
	})
	if err != nil {
		t.Fatalf("EnsureSSO: %v", err)
	}
	if u.UPN != "bob@corp.com" {
		t.Fatalf("provisioned upn = %q, want %q", u.UPN, "bob@corp.com")
	}
}

// S5 — the guard must also see a LEGACY non-canonical row. This is why the
// guard is handed the raw input rather than the normalized one: GetByUPN probes
// exact first, so `Alice@Corp.Com` still collides with a row written that way
// before normalization existed. Pre-folding the guard's argument would discard
// that probe and let an admin mint a second identity beside the legacy row.
func TestCreateLocalRejectsExactLegacyNonCanonicalUPN(t *testing.T) {
	repo := &memoryUserRepo{byID: map[int64]*domain.User{
		1: {ID: 1, UPN: "Alice@Corp.Com", Role: domain.RoleUser, Enabled: true},
	}}
	svc := &Service{users: repo, groups: &bfGroupRepo{g: &domain.Group{ID: 0}}}

	_, err := svc.CreateLocal(context.Background(), CreateLocalInput{
		UPN: "Alice@Corp.Com", Email: "alice@corp.com",
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("CreateLocal = %v, want domain.ErrAlreadyExists against the legacy row", err)
	}
	if len(repo.byID) != 1 {
		t.Fatalf("%d rows exist, want 1", len(repo.byID))
	}
}

// A disabled account must not be distinguishable from an enabled one WITHOUT
// the correct password.
//
// AccountLoginAllowed used to run before bcrypt.CompareHashAndPassword, so any
// unauthenticated caller could tell "this account exists and is disabled/pending"
// apart from every other outcome by sending a junk password — and because the
// login-attempt counter only records invalid_credentials, those probes never fed
// the lockout. Proving you own the account has to come first; only then is its
// state yours to learn.
func TestVerifyLocalPasswordHidesDisabledStateWithoutCorrectPassword(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	repo := &memoryUserRepo{byID: map[int64]*domain.User{
		1: {
			ID: 1, UPN: "disabled@corp.com", Role: domain.RoleUser,
			PasswordHash: string(hash),
			Enabled:      false, AutoDisabledReason: domain.DisabledPendingEmailVerify,
		},
	}}
	svc := &Service{users: repo}
	ctx := context.Background()

	// Wrong password against a disabled account must look exactly like wrong
	// password against any other account: ErrUnauthorized, never ErrForbidden.
	if _, err := svc.VerifyLocalPassword(ctx, "disabled@corp.com", "wrong-password"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("wrong password on a disabled account = %v, want domain.ErrUnauthorized "+
			"(returning ErrForbidden leaks the account's state to an unauthenticated prober)", err)
	}

	// With the CORRECT password the caller has proven ownership, so the
	// disabled state is theirs to see — the handler needs it for its 403 reason.
	if _, err := svc.VerifyLocalPassword(ctx, "disabled@corp.com", "correct-horse"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("correct password on a disabled account = %v, want domain.ErrForbidden", err)
	}
}
