package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestAuthEventRepo(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	// Close the SQLite handle before t.TempDir cleanup, else Windows can't
	// unlink the still-open panel.db.
	t.Cleanup(func() {
		if sqlDB, derr := db.DB(); derr == nil {
			_ = sqlDB.Close()
		}
	})
	repo := NewRepos(db).AuthEvent
	ctx := context.Background()
	now := time.Now()

	seed := []*domain.AuthEvent{
		{UserID: 1, UPN: "alice@x", Method: domain.AuthMethodLocal, Outcome: domain.AuthOutcomeSuccess, IP: "1.1.1.1", At: now.Add(-1 * time.Hour)},
		{UserID: 0, UPN: "alice@x", Method: domain.AuthMethodLocal, Outcome: domain.AuthOutcomeFailure, Reason: "invalid_credentials", IP: "2.2.2.2", At: now.Add(-2 * time.Hour)},
		{UserID: 2, UPN: "bob@x", Method: domain.AuthMethodSAML, Outcome: domain.AuthOutcomeSuccess, IP: "3.3.3.3", At: now.Add(-3 * time.Hour)},
		{UserID: 3, UPN: "old@x", Method: domain.AuthMethodOIDC, Outcome: domain.AuthOutcomeSuccess, IP: "4.4.4.4", At: now.AddDate(0, 0, -100)}, // older than 90d
	}
	for _, e := range seed {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if e.ID == 0 {
			t.Fatal("insert did not set ID")
		}
	}

	page := ports.Pagination{Page: 1, PageSize: 50}
	total := func(f ports.AuthEventFilter) int64 {
		_, tot, err := repo.List(ctx, f)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		return tot
	}

	if got := total(ports.AuthEventFilter{Pagination: page}); got != 4 {
		t.Fatalf("list all total = %d, want 4", got)
	}
	// Default order is at DESC → newest (alice success) first.
	if items, _, _ := repo.List(ctx, ports.AuthEventFilter{Pagination: page}); items[0].UPN != "alice@x" || items[0].Outcome != domain.AuthOutcomeSuccess {
		t.Fatalf("newest-first ordering broken: got %+v", items[0])
	}
	if got := total(ports.AuthEventFilter{Pagination: page, Outcome: "failure"}); got != 1 {
		t.Fatalf("outcome=failure total = %d, want 1", got)
	}
	if got := total(ports.AuthEventFilter{Pagination: page, Method: "saml"}); got != 1 {
		t.Fatalf("method=saml total = %d, want 1", got)
	}
	uid := int64(1)
	if got := total(ports.AuthEventFilter{Pagination: page, UserID: &uid}); got != 1 {
		t.Fatalf("user_id=1 total = %d, want 1", got)
	}
	if got := total(ports.AuthEventFilter{Pagination: page, Search: "bob"}); got != 1 {
		t.Fatalf("search upn=bob total = %d, want 1", got)
	}
	if got := total(ports.AuthEventFilter{Pagination: page, Search: "invalid_credentials"}); got != 1 {
		t.Fatalf("search reason total = %d, want 1", got)
	}

	// Retention: prune everything older than 90d → drops the 100-day-old row.
	deleted, err := repo.DeleteBefore(ctx, now.AddDate(0, 0, -90))
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteBefore = %d (err %v), want 1", deleted, err)
	}
	if got := total(ports.AuthEventFilter{Pagination: page}); got != 3 {
		t.Fatalf("after prune total = %d, want 3", got)
	}
}

// TestAuthEventRecentFailures pins the contract the login guard (captcha +
// lockout) relies on: count genuine credential failures for a scope within a
// window, plus the timestamp of the most recent one. Only
// reason=invalid_credentials counts — disabled / locked_out / server-error
// failures must NOT inflate the count (so the lock window can't slide).
func TestAuthEventRecentFailures(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, derr := db.DB(); derr == nil {
			_ = sqlDB.Close()
		}
	})
	repo := NewRepos(db).AuthEvent
	ctx := context.Background()
	now := time.Now()

	fail := func(ip, upn, reason string, at time.Time) *domain.AuthEvent {
		return &domain.AuthEvent{
			UserID: 0, UPN: upn, Method: domain.AuthMethodLocal,
			Outcome: domain.AuthOutcomeFailure, Reason: reason, IP: ip, At: at,
		}
	}
	seed := []*domain.AuthEvent{
		fail("1.1.1.1", "alice@x", domain.AuthReasonInvalidCredentials, now.Add(-2*time.Minute)),
		fail("1.1.1.1", "alice@x", domain.AuthReasonInvalidCredentials, now.Add(-1*time.Minute)),
		// success from same scope — never counts
		{UserID: 1, UPN: "alice@x", Method: domain.AuthMethodLocal, Outcome: domain.AuthOutcomeSuccess, IP: "1.1.1.1", At: now.Add(-30 * time.Second)},
		// a locked_out rejection from same scope — must NOT count (else the lock slides)
		fail("1.1.1.1", "alice@x", domain.AuthReasonLockedOut, now.Add(-10*time.Second)),
		// disabled-account failure from same scope — must NOT count
		fail("1.1.1.1", "alice@x", "disabled:manual", now.Add(-20*time.Second)),
		// same IP, different user
		fail("1.1.1.1", "bob@x", domain.AuthReasonInvalidCredentials, now.Add(-1*time.Minute)),
		// same user, different IP
		fail("2.2.2.2", "alice@x", domain.AuthReasonInvalidCredentials, now.Add(-1*time.Minute)),
		// genuine failure but outside the window
		fail("1.1.1.1", "alice@x", domain.AuthReasonInvalidCredentials, now.Add(-2*time.Hour)),
	}
	for _, e := range seed {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	since := now.Add(-30 * time.Minute)

	// ip_upn scope (both non-empty): only the two recent invalid_credentials.
	count, lastAt, err := repo.RecentAuthFailures(ctx, "1.1.1.1", "alice@x", since)
	if err != nil {
		t.Fatalf("RecentAuthFailures ip_upn: %v", err)
	}
	if count != 2 {
		t.Fatalf("ip_upn count = %d, want 2", count)
	}
	if want := now.Add(-1 * time.Minute); lastAt.Sub(want).Abs() > time.Second {
		t.Fatalf("ip_upn lastAt = %v, want ~%v", lastAt, want)
	}

	// ip-only scope (upn empty): alice×2 + bob×1 from that IP.
	if count, _, err = repo.RecentAuthFailures(ctx, "1.1.1.1", "", since); err != nil || count != 3 {
		t.Fatalf("ip-only count = %d (err %v), want 3", count, err)
	}

	// upn-only scope (ip empty): 1.1.1.1×2 + 2.2.2.2×1 for alice.
	if count, _, err = repo.RecentAuthFailures(ctx, "", "alice@x", since); err != nil || count != 3 {
		t.Fatalf("upn-only count = %d (err %v), want 3", count, err)
	}

	// no match → count 0, zero lastAt.
	count, lastAt, err = repo.RecentAuthFailures(ctx, "9.9.9.9", "alice@x", since)
	if err != nil || count != 0 || !lastAt.IsZero() {
		t.Fatalf("no-match = (%d, %v, %v), want (0, zero, nil)", count, lastAt, err)
	}
}

// TestAuthEventCountByReasonSince pins the query the notification center's
// login_security alert relies on: how many events with a given reason landed
// since a cutoff (e.g. recent locked_out rejections = an active brute-force).
func TestAuthEventCountByReasonSince(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, derr := db.DB(); derr == nil {
			_ = sqlDB.Close()
		}
	})
	repo := NewRepos(db).AuthEvent
	ctx := context.Background()
	now := time.Now()

	fail := func(reason string, at time.Time) *domain.AuthEvent {
		return &domain.AuthEvent{
			UPN: "x@x", Method: domain.AuthMethodLocal,
			Outcome: domain.AuthOutcomeFailure, Reason: reason, IP: "1.1.1.1", At: at,
		}
	}
	for _, e := range []*domain.AuthEvent{
		fail(domain.AuthReasonLockedOut, now.Add(-5*time.Minute)),
		fail(domain.AuthReasonLockedOut, now.Add(-1*time.Minute)),
		fail(domain.AuthReasonLockedOut, now.Add(-2*time.Hour)),            // outside window
		fail(domain.AuthReasonInvalidCredentials, now.Add(-1*time.Minute)), // other reason
	} {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	since := now.Add(-15 * time.Minute)
	if got, err := repo.CountByReasonSince(ctx, domain.AuthReasonLockedOut, since); err != nil || got != 2 {
		t.Fatalf("locked_out since = %d (err %v), want 2", got, err)
	}
	if got, err := repo.CountByReasonSince(ctx, domain.AuthReasonInvalidCredentials, since); err != nil || got != 1 {
		t.Fatalf("invalid_credentials since = %d (err %v), want 1", got, err)
	}
	if got, err := repo.CountByReasonSince(ctx, domain.AuthReasonLockedOut, now.Add(-3*time.Hour)); err != nil || got != 3 {
		t.Fatalf("locked_out wide window = %d (err %v), want 3", got, err)
	}
}
