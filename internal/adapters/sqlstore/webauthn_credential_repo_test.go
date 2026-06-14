package sqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestWebAuthnCredentialRepo(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	repo := NewRepos(db).WebAuthn
	if repo == nil {
		t.Fatal("WebAuthn repo not wired in NewRepos")
	}
	ctx := context.Background()

	// --- save + round-trip ---
	c := &domain.PasskeyCredential{
		UserID: 7, CredentialID: "credAAAA", Credential: []byte(`{"id":"AAAA","pk":"x"}`),
		SignCount: 3, Name: "YubiKey",
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if c.ID == 0 || c.CreatedAt.IsZero() {
		t.Fatal("Save must set ID + CreatedAt")
	}
	got, err := repo.FindByUserID(ctx, 7)
	if err != nil || len(got) != 1 {
		t.Fatalf("FindByUserID = (%d, %v), want 1", len(got), err)
	}
	if got[0].CredentialID != "credAAAA" || string(got[0].Credential) != `{"id":"AAAA","pk":"x"}` || got[0].SignCount != 3 || got[0].Name != "YubiKey" {
		t.Fatalf("round-trip mismatch: %+v", got[0])
	}

	// --- global lookup by credential id (discoverable login resolver) ---
	byCred, err := repo.FindByCredentialID(ctx, "credAAAA")
	if err != nil || byCred == nil || byCred.UserID != 7 {
		t.Fatalf("FindByCredentialID = (%+v, %v), want user 7", byCred, err)
	}
	if _, err := repo.FindByCredentialID(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown credential id must be ErrNotFound, got %v", err)
	}

	// --- credential_id is GLOBALLY unique (one passkey = one account) ---
	dupe := &domain.PasskeyCredential{UserID: 9, CredentialID: "credAAAA", Credential: []byte(`{}`), SignCount: 0}
	if err := repo.Save(ctx, dupe); err == nil {
		t.Fatal("a second credential with the same credential_id must be rejected (global unique)")
	}

	// --- sign-count anti-rollback gate ---
	adv, err := repo.UpdateAfterLogin(ctx, c.ID, []byte(`{"id":"AAAA","pk":"x2"}`), 5, time.Now())
	if err != nil || !adv {
		t.Fatalf("advancing sign count should win: adv=%v err=%v", adv, err)
	}
	// A regressing (<= stored) count signals a clone — must NOT update.
	regress, err := repo.UpdateAfterLogin(ctx, c.ID, []byte(`{"forged":true}`), 4, time.Now())
	if err != nil {
		t.Fatalf("regress err: %v", err)
	}
	if regress {
		t.Fatal("a regressing sign count must be refused (anti-clone gate)")
	}
	after, _ := repo.FindByCredentialID(ctx, "credAAAA")
	if after.SignCount != 5 || string(after.Credential) != `{"id":"AAAA","pk":"x2"}` {
		t.Fatalf("stored record must reflect only the winning update, got count=%d cred=%s", after.SignCount, after.Credential)
	}

	// --- rename / delete are user-scoped ---
	if err := repo.Rename(ctx, c.ID, 999, "hacker"); err != nil {
		t.Fatalf("rename wrong-user err: %v", err)
	}
	reloaded, _ := repo.FindByCredentialID(ctx, "credAAAA")
	if reloaded.Name == "hacker" {
		t.Fatal("rename with a wrong user_id must be a no-op (no cross-user mutation)")
	}
	if err := repo.Rename(ctx, c.ID, 7, "Phone"); err != nil {
		t.Fatalf("rename own err: %v", err)
	}
	if reloaded, _ = repo.FindByCredentialID(ctx, "credAAAA"); reloaded.Name != "Phone" {
		t.Fatalf("owner rename should apply, got %q", reloaded.Name)
	}
	if err := repo.Delete(ctx, c.ID, 999); err != nil {
		t.Fatalf("delete wrong-user err: %v", err)
	}
	if list, _ := repo.FindByUserID(ctx, 7); len(list) != 1 {
		t.Fatal("delete with a wrong user_id must be a no-op")
	}
	if err := repo.Delete(ctx, c.ID, 7); err != nil {
		t.Fatalf("delete own err: %v", err)
	}
	if list, _ := repo.FindByUserID(ctx, 7); len(list) != 0 {
		t.Fatal("owner delete should remove the credential")
	}
}

// TestWebAuthnCredentialRepo_DeleteAllByUserID covers the admin "revoke all
// passkeys" break-glass: it must drop every credential of the target user, leave
// other users untouched, return the deleted count, and be idempotent.
func TestWebAuthnCredentialRepo_DeleteAllByUserID(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	repo := NewRepos(db).WebAuthn
	ctx := context.Background()

	mk := func(uid int64, cred string) {
		if err := repo.Save(ctx, &domain.PasskeyCredential{UserID: uid, CredentialID: cred, Credential: []byte(`{}`)}); err != nil {
			t.Fatalf("save %s: %v", cred, err)
		}
	}
	mk(7, "a")
	mk(7, "b")
	mk(7, "c")
	mk(9, "d")

	n, err := repo.DeleteAllByUserID(ctx, 7)
	if err != nil {
		t.Fatalf("DeleteAllByUserID: %v", err)
	}
	if n != 3 {
		t.Fatalf("DeleteAllByUserID returned %d, want 3", n)
	}
	if list, _ := repo.FindByUserID(ctx, 7); len(list) != 0 {
		t.Fatalf("user 7 should have no passkeys left, got %d", len(list))
	}
	if list, _ := repo.FindByUserID(ctx, 9); len(list) != 1 {
		t.Fatalf("another user's passkey must survive, got %d", len(list))
	}
	// Idempotent: a second sweep removes nothing and doesn't error.
	if n, err := repo.DeleteAllByUserID(ctx, 7); err != nil || n != 0 {
		t.Fatalf("second DeleteAllByUserID = (%d, %v), want (0, nil)", n, err)
	}
}
