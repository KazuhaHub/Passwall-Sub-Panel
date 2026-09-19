package sqlstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func newSAMLRequestRepo(t *testing.T) *samlRequestRepo {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, derr := db.DB(); derr == nil {
			_ = sqlDB.Close()
		}
	})
	return &samlRequestRepo{db: db}
}

// sample builds a well-formed request row. TokenHash is the primary key, so
// cases that create more than one row must vary it.
func sample(tokenHash string, expiresAt time.Time) *domain.SAMLLoginRequest {
	return &domain.SAMLLoginRequest{
		TokenHash:    tokenHash,
		BrowserHash:  "b000000000000000000000000000000000000000000000000000000000000000",
		RequestID:    "_req-abc123",
		ConfigDigest: "c000000000000000000000000000000000000000000000000000000000000000",
		ReturnTo:     "/user/me",
		CreatedAt:    time.Now().UTC(),
		ExpiresAt:    expiresAt.UTC(),
	}
}

// tokenHash produces a distinct 64-hex key per index.
func tokenHash(i int) string {
	const hexDigits = "0123456789abcdef"
	b := make([]byte, 64)
	for j := range b {
		b[j] = '0'
	}
	b[62] = hexDigits[(i/16)%16]
	b[63] = hexDigits[i%16]
	return string(b)
}

func TestSAMLRequestRepo_CreateThenConsumeReturnsTheRecord(t *testing.T) {
	r := newSAMLRequestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	req := sample(tokenHash(1), now.Add(5*time.Minute))

	if err := r.Create(ctx, req); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.Consume(ctx, req.TokenHash, req.BrowserHash, req.ConfigDigest, now)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	// The record's payload is what the ACS needs: the request ID it may accept
	// and the already-sanitised return path it may bounce to.
	if got.RequestID != req.RequestID {
		t.Fatalf("request_id = %q, want %q", got.RequestID, req.RequestID)
	}
	if got.ReturnTo != req.ReturnTo {
		t.Fatalf("return_to = %q, want %q", got.ReturnTo, req.ReturnTo)
	}
}

// The request is single-use: the whole point is that a replayed ACS POST cannot
// ride the same server-side state.
func TestSAMLRequestRepo_ConsumeIsSingleUse(t *testing.T) {
	r := newSAMLRequestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	req := sample(tokenHash(2), now.Add(5*time.Minute))

	if err := r.Create(ctx, req); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := r.Consume(ctx, req.TokenHash, req.BrowserHash, req.ConfigDigest, now); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := r.Consume(ctx, req.TokenHash, req.BrowserHash, req.ConfigDigest, now.Add(time.Second)); !errors.Is(err, domain.ErrSAMLRequestInvalid) {
		t.Fatalf("second consume = %v, want ErrSAMLRequestInvalid", err)
	}
}

func TestSAMLRequestRepo_UnknownTokenIsRefused(t *testing.T) {
	r := newSAMLRequestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()

	_, err := r.Consume(ctx, tokenHash(3), "b0", "c0", now)
	if !errors.Is(err, domain.ErrSAMLRequestInvalid) {
		t.Fatalf("unknown token = %v, want ErrSAMLRequestInvalid", err)
	}
}

// A binding mismatch must not be reported distinctly (that would hand an
// attacker a free oracle for which of the three bindings it guessed wrong) and
// must not burn the request, so the legitimate browser can still complete it.
func TestSAMLRequestRepo_BindingMismatchIsRefusedWithoutConsuming(t *testing.T) {
	r := newSAMLRequestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	req := sample(tokenHash(4), now.Add(5*time.Minute))
	if err := r.Create(ctx, req); err != nil {
		t.Fatalf("create: %v", err)
	}

	cases := map[string][3]string{
		"wrong browser hash":  {req.TokenHash, "b000000000000000000000000000000000000000000000000000000000000001", req.ConfigDigest},
		"wrong config digest": {req.TokenHash, req.BrowserHash, "c000000000000000000000000000000000000000000000000000000000000001"},
		"wrong token hash":    {tokenHash(99), req.BrowserHash, req.ConfigDigest},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Consume(ctx, c[0], c[1], c[2], now); !errors.Is(err, domain.ErrSAMLRequestInvalid) {
				t.Fatalf("consume = %v, want ErrSAMLRequestInvalid", err)
			}
		})
	}

	// The rejected attempts must not have claimed the row.
	if _, err := r.Consume(ctx, req.TokenHash, req.BrowserHash, req.ConfigDigest, now); err != nil {
		t.Fatalf("a legitimate consume after refused attempts failed: %v", err)
	}
}

func TestSAMLRequestRepo_ExpiredRequestIsRefused(t *testing.T) {
	r := newSAMLRequestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	req := sample(tokenHash(5), now.Add(-time.Second)) // already closed

	if err := r.Create(ctx, req); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := r.Consume(ctx, req.TokenHash, req.BrowserHash, req.ConfigDigest, now); !errors.Is(err, domain.ErrSAMLRequestInvalid) {
		t.Fatalf("expired consume = %v, want ErrSAMLRequestInvalid", err)
	}
}

// Concurrent ACS posts carrying the same token: exactly one may claim it, which
// is what stops a replayed POST from racing a legitimate one.
func TestSAMLRequestRepo_ConcurrentConsumeAdmitsExactlyOne(t *testing.T) {
	r := newSAMLRequestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	req := sample(tokenHash(6), now.Add(5*time.Minute))
	if err := r.Create(ctx, req); err != nil {
		t.Fatalf("create: %v", err)
	}

	const racers = 8
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		claims int
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Consume(ctx, req.TokenHash, req.BrowserHash, req.ConfigDigest, now); err == nil {
				mu.Lock()
				claims++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if claims != 1 {
		t.Fatalf("claimed %d times, want exactly 1", claims)
	}
}

// Cleanup drops only closed windows. A consumed-but-still-live row must survive,
// because keeping it is what makes a replayed POST land on "already used"
// instead of "unknown token".
func TestSAMLRequestRepo_DeleteExpiredOnlyRemovesClosedWindows(t *testing.T) {
	r := newSAMLRequestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()

	expired := sample(tokenHash(7), now.Add(-time.Minute))
	consumed := sample(tokenHash(8), now.Add(10*time.Minute))
	live := sample(tokenHash(9), now.Add(10*time.Minute))
	for _, req := range []*domain.SAMLLoginRequest{expired, consumed, live} {
		if err := r.Create(ctx, req); err != nil {
			t.Fatalf("create %s: %v", req.TokenHash, err)
		}
	}
	if _, err := r.Consume(ctx, consumed.TokenHash, consumed.BrowserHash, consumed.ConfigDigest, now); err != nil {
		t.Fatalf("consume: %v", err)
	}

	deleted, err := r.DeleteExpired(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d rows, want 1 (only the closed window)", deleted)
	}
	if _, err := r.Consume(ctx, live.TokenHash, live.BrowserHash, live.ConfigDigest, now); err != nil {
		t.Fatalf("a live request did not survive cleanup: %v", err)
	}
	if _, err := r.Consume(ctx, consumed.TokenHash, consumed.BrowserHash, consumed.ConfigDigest, now); !errors.Is(err, domain.ErrSAMLRequestInvalid) {
		t.Fatalf("the consumed row was removed rather than kept: %v", err)
	}
}

// A duplicate token hash is impossible in practice (32 random bytes) but the
// primary key is what makes that guarantee, so it is asserted rather than
// assumed.
func TestSAMLRequestRepo_DuplicateTokenHashIsRefused(t *testing.T) {
	r := newSAMLRequestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	req := sample(tokenHash(10), now.Add(5*time.Minute))

	if err := r.Create(ctx, req); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := r.Create(ctx, sample(tokenHash(10), now.Add(5*time.Minute))); err == nil {
		t.Fatal("a duplicate token_hash was accepted; the primary key is not enforcing uniqueness")
	}
}

// The payload the ACS trusts must survive the round trip byte for byte: the
// request ID is compared against the Response's InResponseTo, and the return
// path is used to build a redirect.
func TestSAMLRequestRepo_PayloadRoundTripsByteForByte(t *testing.T) {
	r := newSAMLRequestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	req := sample(tokenHash(11), now.Add(5*time.Minute))
	req.RequestID = "_1f2e3d4c5b6a7988" // an ID shape a real IdP emits
	req.ReturnTo = "/user/me?tab=subscription&page=2"

	if err := r.Create(ctx, req); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.Consume(ctx, req.TokenHash, req.BrowserHash, req.ConfigDigest, now)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got.RequestID != req.RequestID || got.ReturnTo != req.ReturnTo {
		t.Fatalf("round trip changed the payload: got %q / %q", got.RequestID, got.ReturnTo)
	}
	if got.ConsumedAt == nil {
		t.Fatal("a claimed request must report a consumed_at, or callers cannot tell it was spent")
	}
}
