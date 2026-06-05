package mysql

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestAuthTokenRepo(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
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
	repo := NewRepos(db).AuthToken
	if repo == nil {
		t.Fatal("AuthToken repo not wired in NewRepos")
	}
	ctx := context.Background()
	now := time.Now()

	mk := func(tokenHash, codeHash string, userID int64, exp time.Time) *domain.AuthToken {
		return &domain.AuthToken{
			UserID: userID, Purpose: domain.AuthTokenPurposePasswordReset,
			TokenHash: tokenHash, CodeHash: codeHash, Email: "u@x", ExpiresAt: exp,
		}
	}

	// --- link token: create → consume once → second consume fails ---
	if err := repo.Create(ctx, mk("hash-link", "", 7, now.Add(15*time.Minute))); err != nil {
		t.Fatalf("create link: %v", err)
	}
	got, err := repo.ConsumeByTokenHash(ctx, domain.AuthTokenPurposePasswordReset, "hash-link", now)
	if err != nil || got == nil || got.UserID != 7 {
		t.Fatalf("consume link = (%+v, %v), want user 7", got, err)
	}
	if got.UsedAt == nil {
		t.Fatal("consumed token must have UsedAt stamped")
	}
	if _, err := repo.ConsumeByTokenHash(ctx, domain.AuthTokenPurposePasswordReset, "hash-link", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second consume must be ErrNotFound, got %v", err)
	}

	// --- expired link token rejected ---
	if err := repo.Create(ctx, mk("hash-expired", "", 8, now.Add(-1*time.Minute))); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	if _, err := repo.ConsumeByTokenHash(ctx, domain.AuthTokenPurposePasswordReset, "hash-expired", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired token must be ErrNotFound, got %v", err)
	}

	// --- wrong purpose / wrong hash rejected ---
	if err := repo.Create(ctx, mk("hash-x", "", 9, now.Add(15*time.Minute))); err != nil {
		t.Fatalf("create x: %v", err)
	}
	if _, err := repo.ConsumeByTokenHash(ctx, domain.AuthTokenPurposeEmailVerify, "hash-x", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("wrong purpose must be ErrNotFound, got %v", err)
	}
	if _, err := repo.ConsumeByTokenHash(ctx, domain.AuthTokenPurposePasswordReset, "nope", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("wrong hash must be ErrNotFound, got %v", err)
	}

	// --- OTP code scoped to a user ---
	if err := repo.Create(ctx, mk("", "code-hash", 10, now.Add(10*time.Minute))); err != nil {
		t.Fatalf("create otp: %v", err)
	}
	// wrong user → not found
	if _, err := repo.ConsumeByUserCode(ctx, domain.AuthTokenPurposePasswordReset, 999, "code-hash", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otp wrong user must be ErrNotFound, got %v", err)
	}
	otp, err := repo.ConsumeByUserCode(ctx, domain.AuthTokenPurposePasswordReset, 10, "code-hash", now)
	if err != nil || otp == nil || otp.UserID != 10 {
		t.Fatalf("otp consume = (%+v, %v), want user 10", otp, err)
	}
	if _, err := repo.ConsumeByUserCode(ctx, domain.AuthTokenPurposePasswordReset, 10, "code-hash", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otp replay must be ErrNotFound, got %v", err)
	}

	// --- OTP per-token attempt cap: 5 wrong guesses burn the token ---
	if err := repo.Create(ctx, mk("", "right-code", 20, now.Add(10*time.Minute))); err != nil {
		t.Fatalf("create otp-cap: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := repo.ConsumeByUserCode(ctx, domain.AuthTokenPurposePasswordReset, 20, "wrong-code", now); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("wrong guess %d should be ErrNotFound, got %v", i, err)
		}
	}
	// 6th attempt (even with the CORRECT code) must fail — token already burned.
	if _, err := repo.ConsumeByUserCode(ctx, domain.AuthTokenPurposePasswordReset, 20, "right-code", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("correct code after 5 wrong guesses must fail (token burned), got %v", err)
	}

	// --- DeleteByUserPurpose invalidates outstanding tokens ---
	if err := repo.Create(ctx, mk("old1", "", 11, now.Add(15*time.Minute))); err != nil {
		t.Fatalf("create old1: %v", err)
	}
	if err := repo.Create(ctx, mk("old2", "", 11, now.Add(15*time.Minute))); err != nil {
		t.Fatalf("create old2: %v", err)
	}
	if err := repo.DeleteByUserPurpose(ctx, 11, domain.AuthTokenPurposePasswordReset); err != nil {
		t.Fatalf("delete by user purpose: %v", err)
	}
	if _, err := repo.ConsumeByTokenHash(ctx, domain.AuthTokenPurposePasswordReset, "old1", now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old1 should be gone, got %v", err)
	}

	// --- DeleteExpired prunes ---
	n, err := repo.DeleteExpired(ctx, now)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if n < 1 {
		t.Fatalf("DeleteExpired should prune the expired/used rows, got %d", n)
	}
}
